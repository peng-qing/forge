package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"forge/api/pb"
	"forge/internal/core"

	"github.com/peng-qing/go_tools/common/network"
)

func TestRouterDispatchesRegisteredCommand(t *testing.T) {
	const cmdID = pb.CommandID(2)
	var executed int
	var observed int
	router := NewRouter(1, 10, 0)
	router.observer = func(gotCmdID pb.CommandID, elapsed time.Duration, err error) {
		observed++
		if gotCmdID != cmdID {
			t.Errorf("observer cmdID = %d, want %d", gotCmdID, cmdID)
		}
		if elapsed < 0 {
			t.Errorf("observer elapsed = %s, want non-negative", elapsed)
		}
		if err != nil {
			t.Errorf("observer error = %v, want nil", err)
		}
	}
	router.onError = func(context.Context, network.Connection[*pb.Message], *pb.Message, error) {
		t.Error("onError called for successful command")
	}
	router.Register(cmdID, commandFunc(func(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message) error {
		executed++
		if ctx == nil {
			t.Error("command context is nil")
		}
		if msg.GetHeader().GetCmdId() != cmdID {
			t.Errorf("command cmdID = %d, want %d", msg.GetHeader().GetCmdId(), cmdID)
		}
		return nil
	}))

	router.OnMessage(context.Background(), nil, messageForCommand(cmdID))

	if executed != 1 {
		t.Fatalf("command executions = %d, want 1", executed)
	}
	if observed != 1 {
		t.Fatalf("observer calls = %d, want 1", observed)
	}
}

func TestRouterRegisterRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Router)
	}{
		{
			name: "nil command",
			register: func(router *Router) {
				router.Register(2, nil)
			},
		},
		{
			name: "command below range",
			register: func(router *Router) {
				router.Register(0, commandFunc(noopCommand))
			},
		},
		{
			name: "command above range",
			register: func(router *Router) {
				router.Register(11, commandFunc(noopCommand))
			},
		},
		{
			name: "duplicate command",
			register: func(router *Router) {
				router.Register(2, commandFunc(noopCommand))
				router.Register(2, commandFunc(noopCommand))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(1, 10, 0)
			defer func() {
				if recover() == nil {
					t.Fatal("Register() did not panic")
				}
			}()
			tt.register(router)
		})
	}
}

func TestNewRouterNormalizesCommandRange(t *testing.T) {
	router := NewRouter(10, 1, 0)
	router.Register(1, commandFunc(noopCommand))
	router.Register(10, commandFunc(noopCommand))
}

func TestRouterPauseAndRecover(t *testing.T) {
	const cmdID = pb.CommandID(3)
	var executed int
	var gotErr error
	router := NewRouter(1, 10, 0)
	router.onError = func(_ context.Context, _ network.Connection[*pb.Message], _ *pb.Message, err error) {
		gotErr = err
	}
	router.Register(cmdID, commandFunc(func(context.Context, network.Connection[*pb.Message], *pb.Message) error {
		executed++
		return nil
	}))

	if err := router.Pause(9); !errors.Is(err, ErrCommandMissing) {
		t.Fatalf("Pause(unregistered) error = %v, want %v", err, ErrCommandMissing)
	}
	if err := router.Recover(9); !errors.Is(err, ErrCommandMissing) {
		t.Fatalf("Recover(unregistered) error = %v, want %v", err, ErrCommandMissing)
	}
	if err := router.Pause(cmdID); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	router.OnMessage(context.Background(), nil, messageForCommand(cmdID))
	if !errors.Is(gotErr, ErrCommandPaused) {
		t.Fatalf("paused dispatch error = %v, want %v", gotErr, ErrCommandPaused)
	}
	if executed != 0 {
		t.Fatalf("paused command executions = %d, want 0", executed)
	}

	gotErr = nil
	if err := router.Recover(cmdID); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	router.OnMessage(context.Background(), nil, messageForCommand(cmdID))
	if gotErr != nil {
		t.Fatalf("recovered dispatch error = %v", gotErr)
	}
	if executed != 1 {
		t.Fatalf("recovered command executions = %d, want 1", executed)
	}
}

func TestRouterReportsRejectedMessages(t *testing.T) {
	tests := []struct {
		name    string
		message *pb.Message
		wantErr error
	}{
		{name: "nil message", wantErr: ErrInvalidMessage},
		{name: "nil header", message: &pb.Message{}, wantErr: ErrInvalidMessage},
		{name: "unregistered command", message: messageForCommand(4), wantErr: ErrCommandMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotErr error
			var observed bool
			router := NewRouter(1, 10, 0)
			router.onError = func(_ context.Context, _ network.Connection[*pb.Message], _ *pb.Message, err error) {
				gotErr = err
			}
			router.observer = func(pb.CommandID, time.Duration, error) {
				observed = true
			}

			router.OnMessage(context.Background(), nil, tt.message)

			if !errors.Is(gotErr, tt.wantErr) {
				t.Fatalf("OnMessage() error = %v, want %v", gotErr, tt.wantErr)
			}
			if observed {
				t.Fatal("observer called for rejected message")
			}
		})
	}
}

func TestRouterReportsCommandError(t *testing.T) {
	const cmdID = pb.CommandID(5)
	wantErr := errors.New("command failed")
	var hookErr error
	var observerErr error
	router := NewRouter(1, 10, 0)
	router.onError = func(_ context.Context, _ network.Connection[*pb.Message], _ *pb.Message, err error) {
		hookErr = err
	}
	router.observer = func(_ pb.CommandID, _ time.Duration, err error) {
		observerErr = err
	}
	router.Register(cmdID, commandFunc(func(context.Context, network.Connection[*pb.Message], *pb.Message) error {
		return wantErr
	}))

	router.OnMessage(context.Background(), nil, messageForCommand(cmdID))

	if !errors.Is(hookErr, wantErr) {
		t.Fatalf("onError error = %v, want %v", hookErr, wantErr)
	}
	if !errors.Is(observerErr, wantErr) {
		t.Fatalf("observer error = %v, want %v", observerErr, wantErr)
	}
}

func TestRouterRecoversCommandPanic(t *testing.T) {
	const cmdID = pb.CommandID(6)
	var gotErr error
	router := NewRouter(1, 10, 0)
	router.onError = func(_ context.Context, _ network.Connection[*pb.Message], _ *pb.Message, err error) {
		gotErr = err
	}
	router.Register(cmdID, commandFunc(func(context.Context, network.Connection[*pb.Message], *pb.Message) error {
		panic("boom")
	}))

	router.OnMessage(context.Background(), nil, messageForCommand(cmdID))

	var panicErr *core.PanicError
	if !errors.As(gotErr, &panicErr) {
		t.Fatalf("panic error = %T %v, want *core.PanicError", gotErr, gotErr)
	}
	if panicErr.Key != "6" || panicErr.Value != "boom" || len(panicErr.Stack) == 0 {
		t.Fatalf("panic error = %#v, want command id, panic value and stack", panicErr)
	}
}

func TestRouterIgnoresCanceledContext(t *testing.T) {
	const cmdID = pb.CommandID(7)
	var executed bool
	var reported bool
	router := NewRouter(1, 10, 0)
	router.onError = func(context.Context, network.Connection[*pb.Message], *pb.Message, error) {
		reported = true
	}
	router.Register(cmdID, commandFunc(func(context.Context, network.Connection[*pb.Message], *pb.Message) error {
		executed = true
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	router.OnMessage(ctx, nil, messageForCommand(cmdID))

	if executed || reported {
		t.Fatalf("canceled dispatch executed = %v, reported = %v; want both false", executed, reported)
	}
}

type commandFunc func(context.Context, network.Connection[*pb.Message], *pb.Message) error

func (f commandFunc) Execute(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message) error {
	return f(ctx, conn, msg)
}

func noopCommand(context.Context, network.Connection[*pb.Message], *pb.Message) error {
	return nil
}

func messageForCommand(cmdID pb.CommandID) *pb.Message {
	return &pb.Message{Header: &pb.MessageHeader{CmdId: cmdID}}
}
