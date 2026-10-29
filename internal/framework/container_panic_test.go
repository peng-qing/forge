package framework

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"forge/internal/core"
)

func TestContainerDrainPanicStillClosesAndFinalizes(t *testing.T) {
	var events testEvents
	c := NewContainer()
	for _, name := range []string{"panic-drain", "peer"} {
		name := name
		c.AddComponent(&testDrainer{
			testComponent: &testComponent{
				name:    name,
				closeFn: func() error { events.add("close:" + name); return nil },
			},
			drainFn: func(context.Context) error {
				events.add("drain:" + name)
				if name == "panic-drain" {
					panic("drain panic")
				}
				return nil
			},
		})
	}
	c.AddFinalizer(func() error { events.add("finalize"); return nil })
	runErr := errors.New("runtime trigger")
	c.exits <- componentExit{name: "trigger", err: runErr}
	err := c.Serve()
	var panicErr *core.PanicError
	if !errors.As(err, &panicErr) {
		t.Errorf("Serve error = %v, want *core.PanicError", err)
	} else if panicErr.Value != "drain panic" || len(panicErr.Stack) == 0 {
		t.Errorf("PanicError must preserve the panic value and stack: %+v", panicErr)
	}
	if !errors.Is(err, runErr) {
		t.Errorf("Serve lost runtime error: %v", err)
	}
	var cleanup []string
	drained := 0
	for _, event := range events.snapshot() {
		if strings.HasPrefix(event, "drain:") {
			drained++
		} else {
			if drained != 2 {
				t.Errorf("cleanup began before both drain attempts completed: %v", events.snapshot())
			}
			cleanup = append(cleanup, event)
		}
	}
	if want := []string{"close:peer", "close:panic-drain", "finalize"}; !reflect.DeepEqual(cleanup, want) {
		t.Errorf("cleanup events = %v, want %v", cleanup, want)
	}
}
