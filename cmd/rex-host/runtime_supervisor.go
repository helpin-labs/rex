package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Rex owns process recovery. A live PID alone does not establish readiness,
// especially after suspend/resume interrupts a local connection or startup.
type runtimeSupervisor struct {
	start    func(context.Context) (<-chan error, error)
	stop     func() error
	health   func(context.Context) error
	setReady func(context.Context, bool)

	checkInterval time.Duration
	readyTimeout  time.Duration
	retryDelay    time.Duration
}

func (s runtimeSupervisor) run(ctx context.Context, parentClosed, restart <-chan struct{}, serveErrors <-chan error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-parentClosed:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer s.setReady(ctx, false)

	check := time.NewTicker(min(200*time.Millisecond, s.checkInterval))
	defer check.Stop()
	retry := time.NewTimer(0)
	defer retry.Stop()
	var retryAt <-chan time.Time = retry.C
	var exited <-chan error
	var started, healthySince time.Time
	ready, failures, missedChecks := false, 0, 0

	recoverRuntime := func(err error) {
		s.setReady(ctx, false)
		ready, missedChecks = false, 0
		healthySince = time.Time{}
		exited = nil
		failures++
		delay := min(time.Duration(failures)*s.retryDelay, 30*time.Second)
		slog.Warn("agent runtime unavailable; retrying", "error", err, "attempt", failures, "delay", delay)
		retry.Reset(delay)
		retryAt = retry.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serveErrors:
			return fmt.Errorf("desktop API exited: %w", err)
		case <-restart:
			slog.Info("restarting agent runtime to apply settings")
			s.setReady(ctx, false)
			ready, failures, missedChecks = false, 0, 0
			healthySince = time.Time{}
			exited = nil
			retry.Reset(0)
			retryAt = retry.C
		case <-retryAt:
			retryAt = nil
			// Never start a second runtime until the owned predecessor is reaped.
			if err := s.stop(); err != nil {
				recoverRuntime(err)
				continue
			}
			var err error
			exited, err = s.start(ctx)
			if err != nil {
				recoverRuntime(err)
				continue
			}
			started = time.Now()
			check.Reset(min(200*time.Millisecond, s.checkInterval))
		case err := <-exited:
			recoverRuntime(fmt.Errorf("agent runtime exited: %v", err))
		case <-check.C:
			if retryAt != nil || exited == nil {
				continue
			}
			probeCtx, cancelProbe := context.WithTimeout(ctx, min(5*time.Second, s.readyTimeout))
			err := s.health(probeCtx)
			cancelProbe()
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				healthySince = time.Time{}
				if !ready {
					if time.Since(started) >= s.readyTimeout {
						recoverRuntime(fmt.Errorf("agent runtime did not become ready: %w", err))
					}
				} else {
					missedChecks++
					if missedChecks >= 3 {
						recoverRuntime(fmt.Errorf("agent runtime stopped responding: %w", err))
					}
				}
				continue
			}
			missedChecks = 0
			if healthySince.IsZero() {
				healthySince = time.Now()
			}
			if time.Since(healthySince) >= time.Minute {
				failures = 0
			}
			if !ready {
				ready = true
				s.setReady(ctx, true)
				check.Reset(s.checkInterval)
			}
		}
	}
}
