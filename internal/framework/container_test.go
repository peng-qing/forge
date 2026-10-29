package framework

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"forge/internal/core"
)

// These tests assert lifecycle contracts rather than preserving current bugs.
// No test sends an OS signal to the developer's process.
type testComponent struct {
	name    string
	startFn func() error
	closeFn func() error
}

func (c *testComponent) Name() string { return c.name }

func (c *testComponent) Start() error {
	if c.startFn != nil {
		return c.startFn()
	}
	return nil
}

func (c *testComponent) Close() error {
	if c.closeFn != nil {
		return c.closeFn()
	}
	return nil
}

type testDrainer struct {
	*testComponent
	drainFn func(context.Context) error
}

func (c *testDrainer) Drain(ctx context.Context) error { return c.drainFn(ctx) }

type testWaiter struct {
	*testComponent
	waitFn func() error
}

func (c *testWaiter) Wait() error { return c.waitFn() }

type testEvents struct {
	mu     sync.Mutex
	values []string
}

func (e *testEvents) add(value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.values = append(e.values, value)
}

func (e *testEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.values...)
}

func assertEvents(t *testing.T, events *testEvents, want []string) {
	t.Helper()
	if got := events.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestContainerRegistrationSkipsNil(t *testing.T) {
	c := NewContainer()
	component := &testComponent{name: "registered"}
	c.AddComponent(nil, component, nil)
	c.AddFinalizer(nil, func() error { return nil }, nil)
	if len(c.components) != 1 || c.components[0].component != component {
		t.Fatalf("unexpected registered components: %+v", c.components)
	}
	if len(c.finalizers) != 1 {
		t.Fatalf("finalizer count = %d, want 1", len(c.finalizers))
	}
}

func TestContainerStartFailureTracksSuccessfulPrefix(t *testing.T) {
	var events testEvents
	startErr := errors.New("startup failure")
	c := NewContainer()
	for _, name := range []string{"first", "failed", "never-started"} {
		name := name
		c.AddComponent(&testComponent{
			name: name,
			startFn: func() error {
				events.add("start:" + name)
				if name == "failed" {
					return startErr
				}
				return nil
			},
		})
	}
	if err := c.runComponents(); !errors.Is(err, startErr) {
		t.Fatalf("runComponents error = %v, want %v", err, startErr)
	}
	assertEvents(t, &events, []string{"start:first", "start:failed"})
	for index, want := range []bool{true, false, false} {
		if got := c.components[index].started; got != want {
			t.Errorf("component[%d].started = %v, want %v", index, got, want)
		}
	}
}

func TestContainerCloseUsesComponentCount(t *testing.T) {
	for _, finalizerCount := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("finalizers=%d", finalizerCount), func(t *testing.T) {
			defer func() {
				if cause := recover(); cause != nil {
					t.Errorf("closeComponents panicked: %v", cause)
				}
			}()
			var events testEvents
			c := NewContainer()
			for _, name := range []string{"first", "second"} {
				name := name
				c.AddComponent(&testComponent{
					name:    name,
					closeFn: func() error { events.add("close:" + name); return nil },
				})
				// Isolate close-loop bounds from startup bookkeeping.
				c.components[len(c.components)-1].started = true
			}
			for range finalizerCount {
				c.AddFinalizer(func() error { return nil })
			}
			if err := c.closeComponents(); err != nil {
				t.Errorf("closeComponents error = %v", err)
			}
			assertEvents(t, &events, []string{"close:second", "close:first"})
		})
	}
}

func TestContainerCloseSkipsUnstartedAndContinuesAfterErrors(t *testing.T) {
	var events testEvents
	firstErr := errors.New("first close failure")
	thirdErr := errors.New("third close failure")
	c := NewContainer()
	for index, name := range []string{"first", "unstarted", "third"} {
		index, name := index, name
		c.AddComponent(&testComponent{
			name: name,
			closeFn: func() error {
				events.add("close:" + name)
				if index == 0 {
					return firstErr
				}
				return thirdErr
			},
		})
		c.components[index].started = index != 1
		// Equal lengths isolate error handling from the incorrect loop bounds.
		c.AddFinalizer(func() error { return nil })
	}
	err := c.closeComponents()
	if !errors.Is(err, firstErr) || !errors.Is(err, thirdErr) {
		t.Errorf("close error = %v, want both close failures", err)
	}
	assertEvents(t, &events, []string{"close:third", "close:first"})
}

func TestContainerDrainSkipsFailedAndUnstartedComponents(t *testing.T) {
	var events testEvents
	c := NewContainer()
	for _, name := range []string{"started", "failed", "never-started"} {
		name := name
		c.AddComponent(&testDrainer{
			testComponent: &testComponent{name: name},
			drainFn:       func(context.Context) error { events.add("drain:" + name); return nil },
		})
	}
	c.components[0].started = true
	if err := c.drainComponents(); err != nil {
		t.Fatalf("drainComponents error = %v", err)
	}
	assertEvents(t, &events, []string{"drain:started"})
}

func TestContainerDrainAggregatesConcurrentErrors(t *testing.T) {
	const count = 32
	c := NewContainer()
	ready := make(chan struct{}, count)
	release := make(chan struct{})
	wantErrors := make([]error, count)
	for index := range count {
		wantErr := fmt.Errorf("drain failure %d", index)
		wantErrors[index] = wantErr
		c.AddComponent(&testDrainer{
			testComponent: &testComponent{name: fmt.Sprintf("drainer-%d", index)},
			drainFn: func(context.Context) error {
				ready <- struct{}{}
				<-release
				return wantErr
			},
		})
		c.components[index].started = true
	}
	result := make(chan error, 1)
	go func() { result <- c.drainComponents() }()
	// All drainers must enter before any is released: this also verifies concurrency.
	for range count {
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			close(release)
			<-result
			t.Fatal("drainers did not all enter concurrently")
		}
	}
	close(release)
	err := <-result
	for _, wantErr := range wantErrors {
		if !errors.Is(err, wantErr) {
			t.Errorf("aggregated error is missing %v", wantErr)
		}
	}
}

func TestContainerDrainDeadline(t *testing.T) {
	c := NewContainer()
	c.AddComponent(&testDrainer{
		testComponent: &testComponent{name: "deadline-aware"},
		drainFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	c.components[0].started = true
	if err := c.drainComponents(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("drain error = %v, want DeadlineExceeded", err)
	}
}

func TestContainerWaiterPreservesExitBeforeWaitForExit(t *testing.T) {
	c := NewContainer()
	wantErr := errors.New("listener stopped")
	c.watchWaiterComponent("listener", &testWaiter{
		waitFn: func() error { return wantErr },
	})
	c.wg.Wait()
	if err := c.waitForExit(); !errors.Is(err, wantErr) {
		t.Errorf("waitForExit error = %v, want %v", err, wantErr)
	}
}

func TestContainerWaiterNilExitIsUnexpected(t *testing.T) {
	c := NewContainer()
	c.watchWaiterComponent("listener", &testWaiter{
		waitFn: func() error { return nil },
	})
	c.wg.Wait()
	if err := c.waitForExit(); !errors.Is(err, ErrUnexpectedComponentExit) {
		t.Errorf("Waiter nil exit error = %v, want ErrUnexpectedComponentExit", err)
	}
}

func TestContainerWaiterPanicReportsExit(t *testing.T) {
	c := NewContainer()
	c.watchWaiterComponent("panic-listener", &testWaiter{
		waitFn: func() error { panic("wait panic") },
	})
	c.wg.Wait()
	select {
	case result := <-c.exits:
		if result.name != "panic-listener" || result.err == nil {
			t.Errorf("panic exit result = %+v, want component name and error", result)
		}
		var panicErr *core.PanicError
		if !errors.As(result.err, &panicErr) {
			t.Errorf("panic exit error = %T, want *core.PanicError", result.err)
		} else {
			if panicErr.Key != "Container.panic-listener.Wait" || panicErr.Value != "wait panic" {
				t.Errorf("panic details = %+v, want original component and panic value", panicErr)
			}
			if len(panicErr.Stack) == 0 {
				t.Error("PanicError must preserve the panic stack")
			}
		}
	default:
		t.Error("Wait panic was swallowed without reporting an exit; Serve would keep waiting")
	}
}

func TestContainerWaitersDoNotBlockWhenExitBufferIsFull(t *testing.T) {
	c := NewContainer()
	c.exits <- componentExit{name: "first"}
	for range 16 {
		c.watchWaiterComponent("another", &testWaiter{waitFn: func() error { return nil }})
	}
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Release potentially blocked sends so a regression does not leak goroutines.
		go func() {
			for {
				select {
				case <-c.exits:
				case <-done:
					return
				}
			}
		}()
		<-done
		t.Fatal("supervisors blocked on a full exit buffer")
	}
}

func TestContainerFinalizersReverseOrderAndContinueAfterError(t *testing.T) {
	var events testEvents
	wantErr := errors.New("finalizer failure")
	c := NewContainer()
	c.AddFinalizer(
		func() error { events.add("first"); return nil },
		func() error { events.add("second"); return wantErr },
		func() error { events.add("third"); return nil },
	)
	if err := c.runFinalizer(); !errors.Is(err, wantErr) {
		t.Errorf("finalizer error = %v, want %v", err, wantErr)
	}
	assertEvents(t, &events, []string{"third", "second", "first"})
}

func TestContainerServeStartupFailureRollsBackAndFinalizes(t *testing.T) {
	var events testEvents
	startErr := errors.New("startup failure")
	closeErr := errors.New("close failure")
	finalizeErr := errors.New("finalizer failure")
	c := NewContainer()
	for _, name := range []string{"first", "failed", "never-started"} {
		name := name
		c.AddComponent(&testComponent{
			name: name,
			startFn: func() error {
				events.add("start:" + name)
				if name == "failed" {
					return startErr
				}
				return nil
			},
			closeFn: func() error { events.add("close:" + name); return closeErr },
		})
	}
	c.AddFinalizer(func() error { events.add("finalize"); return finalizeErr })
	err := c.Serve()
	for _, wantErr := range []error{startErr, closeErr, finalizeErr} {
		if !errors.Is(err, wantErr) {
			t.Errorf("Serve error is missing %v: %v", wantErr, err)
		}
	}
	assertEvents(t, &events, []string{"start:first", "start:failed", "close:first", "finalize"})
}

func TestContainerServeClosesWaitersBeforeFinalizers(t *testing.T) {
	var events testEvents
	c := NewContainer()
	var releases []func()
	for _, name := range []string{"first", "second"} {
		name := name
		done := make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(done) }) }
		releases = append(releases, release)
		c.AddComponent(&testWaiter{
			testComponent: &testComponent{
				name:    name,
				startFn: func() error { events.add("start:" + name); return nil },
				closeFn: func() error { events.add("close:" + name); release(); return nil },
			},
			waitFn: func() error { <-done; events.add("wait:" + name); return nil },
		})
	}
	c.AddFinalizer(func() error { events.add("finalize"); return nil })
	runErr := errors.New("runtime failure")
	c.exits <- componentExit{name: "trigger", err: runErr}
	result := make(chan error, 1)
	go func() { result <- c.Serve() }()
	var err error
	select {
	case err = <-result:
	case <-time.After(2 * time.Second):
		// Force-release the fakes to keep a broken Close implementation from leaking.
		for _, release := range releases {
			release()
		}
		err = <-result
		t.Error("Serve did not close its Waiters; it stalled before running Finalizers")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("Serve error = %v, want %v", err, runErr)
	}
	got := events.snapshot()
	var ordered []string
	waitCount := 0
	for _, event := range got {
		if strings.HasPrefix(event, "wait:") {
			waitCount++
			continue
		}
		if event == "finalize" && waitCount != 2 {
			t.Errorf("Finalizer ran before both Waiters ended: %v", got)
		}
		ordered = append(ordered, event)
	}
	want := []string{"start:first", "start:second", "close:second", "close:first", "finalize"}
	if !reflect.DeepEqual(ordered, want) {
		t.Errorf("lifecycle events = %v, want %v", got, want)
	}
}

// Verify the shutdown barrier and all four error sources through Serve itself.
func TestContainerServeDrainBarrierAndErrorAggregation(t *testing.T) {
	var events testEvents
	runErr := errors.New("runtime failure")
	drainErr := errors.New("drain failure")
	closeErr := errors.New("close failure")
	finalizeErr := errors.New("finalizer failure")
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDrains := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseDrains()

	c := NewContainer()
	for _, name := range []string{"first", "second"} {
		name := name
		c.AddComponent(&testDrainer{
			testComponent: &testComponent{
				name: name,
				closeFn: func() error {
					events.add("close:" + name)
					return closeErr
				},
			},
			drainFn: func(context.Context) error {
				events.add("drain-begin:" + name)
				ready <- struct{}{}
				<-release
				events.add("drain-end:" + name)
				return drainErr
			},
		})
	}
	c.AddFinalizer(func() error { events.add("finalize"); return finalizeErr })
	c.exits <- componentExit{name: "trigger", err: runErr}
	result := make(chan error, 1)
	go func() { result <- c.Serve() }()

	for range 2 {
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not start all Drainers")
		}
	}
	for _, event := range events.snapshot() {
		if strings.HasPrefix(event, "close:") || event == "finalize" {
			t.Errorf("cleanup began before draining was released: %s", event)
		}
	}
	releaseDrains()

	select {
	case err := <-result:
		for _, want := range []error{runErr, drainErr, closeErr, finalizeErr} {
			if !errors.Is(err, want) {
				t.Errorf("Serve error is missing %v: %v", want, err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not finish shutdown")
	}

	got := events.snapshot()
	var cleanup []string
	ended := 0
	for _, event := range got {
		if strings.HasPrefix(event, "drain-end:") {
			ended++
		}
		if strings.HasPrefix(event, "close:") || event == "finalize" {
			if ended != 2 {
				t.Errorf("cleanup ran before both Drainers completed: %v", got)
			}
			cleanup = append(cleanup, event)
		}
	}
	if want := []string{"close:second", "close:first", "finalize"}; !reflect.DeepEqual(cleanup, want) {
		t.Errorf("cleanup = %v, want %v", cleanup, want)
	}
}

func TestContainerServeStartupFailureClosesEarlierWaiter(t *testing.T) {
	var events testEvents
	done := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(done) }) }
	defer release()
	startErr := errors.New("later component failed to start")
	c := NewContainer()
	c.AddComponent(
		&testWaiter{
			testComponent: &testComponent{
				name:    "first",
				startFn: func() error { events.add("start:first"); return nil },
				closeFn: func() error { events.add("close:first"); release(); return nil },
			},
			waitFn: func() error { <-done; events.add("wait:first"); return nil },
		},
		&testComponent{
			name:    "failed",
			startFn: func() error { events.add("start:failed"); return startErr },
		},
	)
	c.AddFinalizer(func() error { events.add("finalize"); return nil })
	result := make(chan error, 1)
	go func() { result <- c.Serve() }()
	var err error
	select {
	case err = <-result:
	case <-time.After(2 * time.Second):
		release()
		err = <-result
		t.Error("startup rollback did not close the previously started Waiter")
	}
	if !errors.Is(err, startErr) {
		t.Errorf("Serve error = %v, want %v", err, startErr)
	}
	assertEvents(t, &events, []string{
		"start:first", "start:failed", "close:first", "wait:first", "finalize",
	})
}

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
