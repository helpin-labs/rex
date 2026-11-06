package main

import (
	"context"
	"errors"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func superviseForTest(t *testing.T, s runtimeSupervisor) (chan struct{}, chan bool, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s.checkInterval, s.readyTimeout, s.retryDelay = time.Millisecond, 20*time.Millisecond, time.Millisecond
	ready := make(chan bool, 100)
	s.setReady = func(_ context.Context, value bool) { ready <- value }
	if s.stop == nil {
		s.stop = func() error { return nil }
	}
	if s.health == nil {
		s.health = func(context.Context) error { return nil }
	}
	restart := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, nil, restart, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	return restart, ready, done
}

func waitRuntimeReady(t *testing.T, ready <-chan bool) {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case value := <-ready:
			if value {
				return
			}
		case <-timeout.C:
			t.Fatal("runtime did not recover")
		}
	}
}

func TestRuntimeSupervisorRetriesStartupAndSettingsRestart(t *testing.T) {
	var starts atomic.Int32
	restart, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) {
			n := starts.Add(1)
			if n == 1 || n == 3 {
				return nil, errors.New("temporarily unable to start")
			}
			return make(chan error), nil
		},
	})
	waitRuntimeReady(t, ready)
	if starts.Load() != 2 {
		t.Fatal("initial launch was not retried")
	}
	restart <- struct{}{}
	waitRuntimeReady(t, ready)
	if starts.Load() != 4 {
		t.Fatal("failed settings restart was not retried")
	}
}

func TestRuntimeSupervisorRecoversUnresponsiveLiveProcess(t *testing.T) {
	var starts atomic.Int32
	var unresponsive atomic.Bool
	var misses atomic.Int32
	_, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) {
			starts.Add(1)
			unresponsive.Store(false)
			return make(chan error), nil
		},
		health: func(context.Context) error {
			if unresponsive.Load() {
				misses.Add(1)
				return errors.New("connection unavailable after wake")
			}
			return nil
		},
	})
	waitRuntimeReady(t, ready)
	unresponsive.Store(true)
	waitRuntimeReady(t, ready)
	if starts.Load() != 2 || misses.Load() != 3 {
		t.Fatalf("starts=%d misses=%d", starts.Load(), misses.Load())
	}
}

func TestRuntimeSupervisorIgnoresIsolatedHealthFailures(t *testing.T) {
	var starts, checks atomic.Int32
	checked := make(chan struct{}, 1)
	_, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil },
		health: func(context.Context) error {
			n := checks.Add(1)
			if n >= 12 {
				select {
				case checked <- struct{}{}:
				default:
				}
			}
			if n > 1 && n%3 != 1 {
				return errors.New("transient probe error")
			}
			return nil
		},
	})
	waitRuntimeReady(t, ready)
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("health checks stopped")
	}
	if starts.Load() != 1 {
		t.Fatal("a temporary health error restarted a healthy process")
	}
}

func TestRuntimeSupervisorRetriesReadinessTimeout(t *testing.T) {
	var starts atomic.Int32
	_, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) { starts.Add(1); return make(chan error), nil },
		health: func(context.Context) error {
			if starts.Load() == 1 {
				return errors.New("startup stuck")
			}
			return nil
		},
	})
	waitRuntimeReady(t, ready)
	if starts.Load() != 2 {
		t.Fatal("live process that never became ready was not replaced")
	}
}

func TestRuntimeSupervisorReapsPredecessorBeforeReplacement(t *testing.T) {
	var starts, stopped atomic.Int32
	var active atomic.Bool
	restart, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) {
			if active.Swap(true) {
				return nil, errors.New("two runtimes started")
			}
			starts.Add(1)
			return make(chan error), nil
		},
		stop: func() error {
			if active.Load() && stopped.Add(1) <= 2 {
				return errors.New("previous runtime still stopping")
			}
			active.Store(false)
			return nil
		},
	})
	waitRuntimeReady(t, ready)
	restart <- struct{}{}
	waitRuntimeReady(t, ready)
	if starts.Load() != 2 || stopped.Load() != 3 {
		t.Fatalf("starts=%d stop checks=%d", starts.Load(), stopped.Load())
	}
}

func TestRuntimeSupervisorCrashBudgetDoesNotExpireAcrossResumes(t *testing.T) {
	processExit := make(chan error, 1)
	var starts atomic.Int32
	_, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) { starts.Add(1); return processExit, nil },
	})
	waitRuntimeReady(t, ready)
	for range 7 {
		processExit <- errors.New("runtime exited")
		waitRuntimeReady(t, ready)
	}
	if starts.Load() != 8 {
		t.Fatal("supervisor gave up after lifetime crash limit")
	}
}

func TestRuntimeSupervisorParentExitCancelsHealthProbe(t *testing.T) {
	parent := make(chan struct{})
	probing := make(chan struct{})
	s := runtimeSupervisor{
		start:         func(context.Context) (<-chan error, error) { return make(chan error), nil },
		stop:          func() error { return nil },
		health:        func(ctx context.Context) error { close(probing); <-ctx.Done(); return ctx.Err() },
		setReady:      func(context.Context, bool) {},
		checkInterval: time.Millisecond, readyTimeout: time.Minute, retryDelay: time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- s.run(t.Context(), parent, nil, nil) }()
	select {
	case <-probing:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	close(parent)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("parent exit did not cancel pending health request")
	}
}

func TestStopProcessAcceptsPreviouslyObservedExit(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestRuntimeExitedChild$")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	close(exited)
	<-exited
	if err := stopProcess(command, exited, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeExitedChild(t *testing.T) {}

// A new runtime waiting for its predecessor to release the store is alive:
// restarting it would only start another waiter, so the wait never counts
// toward the startup timeout.
func TestRuntimeSupervisorWaitsForStoreOwnerWithoutRestarting(t *testing.T) {
	var starts atomic.Int32
	var waitingUntil atomic.Int64
	waitingUntil.Store(time.Now().Add(200 * time.Millisecond).UnixNano())
	var lastOwner atomic.Value
	lastOwner.Store("unset")
	_, ready, _ := superviseForTest(t, runtimeSupervisor{
		start: func(context.Context) (<-chan error, error) {
			starts.Add(1)
			return make(chan error), nil
		},
		health: func(context.Context) error {
			if time.Now().UnixNano() < waitingUntil.Load() {
				return &runtimeWaitingError{owner: "pid 42"}
			}
			return nil
		},
		setWaiting: func(owner string) { lastOwner.Store(owner) },
	})
	time.Sleep(100 * time.Millisecond)
	if owner := lastOwner.Load(); owner != "pid 42" {
		t.Fatalf("waiting state not reported: %v", owner)
	}
	waitRuntimeReady(t, ready)
	if starts.Load() != 1 {
		t.Fatalf("runtime restarted %d times while waiting for the store owner", starts.Load()-1)
	}
	if owner := lastOwner.Load(); owner != "" {
		t.Fatalf("waiting state not cleared: %v", owner)
	}
}

func TestRuntimeHealthErrorRecognizesWaitingRuntime(t *testing.T) {
	waiting := runtimeHealthError(&sdk.HTTPStatusError{StatusCode: 503, Body: `{"status":"waiting_for_store_owner","owner":"pid 7 since x"}`})
	var target *runtimeWaitingError
	if !errors.As(waiting, &target) || target.owner != "pid 7 since x" {
		t.Fatalf("waiting = %v", waiting)
	}
	other := &sdk.HTTPStatusError{StatusCode: 503, Body: `{"status":"starting"}`}
	if errors.As(runtimeHealthError(other), &target) {
		t.Fatal("a starting runtime is not waiting for the store owner")
	}
}
