package rex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func routineRecoveryFixture(t *testing.T) (*Store, *Routine, time.Time) {
	t.Helper()
	store := openTestStore(t)
	bot := Bot{Name: "Scheduled researcher"}
	if err := bot.Normalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBot(t.Context(), &bot); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	next := now.Add(24 * time.Hour)
	routine := &Routine{BotID: bot.ID, Title: "Daily research", Prompt: "Research", ScheduleKind: "daily", DailyTime: next.Local().Format("15:04"), Enabled: true, NextRunAt: &next}
	if err := store.SaveRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	return store, routine, now
}

func TestRoutineFailureCooldownResumesNextSlot(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new pause", true: "existing disabled pause"}[legacy], func(t *testing.T) {
			store, routine, now := routineRecoveryFixture(t)
			thread, err := store.ThreadForBot(t.Context(), Bot{ID: routine.BotID})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AttachRun(t.Context(), thread.ID, "busy"); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/runs/busy" {
					t.Errorf("unexpected runtime request %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(Run{ID: "busy", Status: "running"})
			}))
			defer server.Close()
			scheduler := NewScheduler(&Host{Store: store, Runtime: newTestRuntimeClient(t, server.URL, "")})
			scheduler.now = func() time.Time { return now }
			for i := 0; i < MaxConsecutiveFailed; i++ {
				scheduler.afterFailure(t.Context(), routine.ID, now.Add(-time.Hour), 1, "temporary provider failure")
			}
			paused, err := store.GetRoutine(t.Context(), routine.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !paused.Enabled || paused.PausedReason == "" || paused.RetryAt != nil {
				t.Fatalf("pause disabled schedule or retained retries: %+v", paused)
			}
			if legacy {
				if _, err := store.db.ExecContext(t.Context(), `UPDATE routines SET enabled=0 WHERE id=?`, routine.ID); err != nil {
					t.Fatal(err)
				}
			}
			scheduler.Tick(t.Context())
			runs, _ := store.RoutineRuns(t.Context(), routine.ID, 10)
			if len(runs) != 0 {
				t.Fatal("cooldown ran before next scheduled time")
			}
			// A new scheduler still sees the persisted pause and resumes at tomorrow's slot.
			scheduler = NewScheduler(scheduler.host)
			scheduler.now = func() time.Time { return *routine.NextRunAt }
			scheduler.Tick(t.Context())
			scheduler.Tick(t.Context())
			resumed, _ := store.GetRoutine(t.Context(), routine.ID)
			runs, _ = store.RoutineRuns(t.Context(), routine.ID, 10)
			// The bot is intentionally busy: reaching this failure proves the next slot fired.
			if !resumed.Enabled || resumed.PausedReason != "" || resumed.ConsecutiveFailures != 1 || resumed.RetryAt == nil || len(runs) != 1 || runs[0].Attempt != 1 {
				t.Fatalf("next slot did not get a fresh bounded attempt: routine=%+v runs=%+v", resumed, runs)
			}
		})
	}
}

func TestSuccessfulManualRoutineClearsFailurePause(t *testing.T) {
	for _, state := range []string{"cooldown", "legacy paused", "overdue pause", "explicitly off"} {
		t.Run(state, func(t *testing.T) {
			store, routine, now := routineRecoveryFixture(t)
			scheduler := NewScheduler(&Host{Store: store})
			scheduler.now = func() time.Time { return now }
			for i := 0; i < MaxConsecutiveFailed; i++ {
				scheduler.afterFailure(t.Context(), routine.ID, now, 1, "provider unavailable")
			}
			if state == "legacy paused" {
				// Matches the live bug: the previous success reset only the counter.
				if _, err := store.db.ExecContext(t.Context(), `UPDATE routines SET enabled=0,consecutive_failures=0 WHERE id=?`, routine.ID); err != nil {
					t.Fatal(err)
				}
			}
			if state == "overdue pause" {
				past := now.Add(-time.Minute)
				if _, err := store.db.ExecContext(t.Context(), `UPDATE routines SET next_run_at=? WHERE id=?`, past, routine.ID); err != nil {
					t.Fatal(err)
				}
			}
			if state == "explicitly off" {
				saved, _ := store.GetRoutine(t.Context(), routine.ID)
				saved.Enabled = false
				saved.NextRunAt = nil
				if err := store.SaveRoutine(t.Context(), saved); err != nil {
					t.Fatal(err)
				}
			}
			claim, err := store.ClaimRoutineRun(t.Context(), routine.ID, now, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetRoutineRunStarted(t.Context(), claim.ID, "manual"); err != nil {
				t.Fatal(err)
			}
			scheduler.RunSettled(t.Context(), &Run{ID: "manual", Status: "completed"})
			saved, _ := store.GetRoutine(t.Context(), routine.ID)
			if saved.Enabled != (state != "explicitly off") || saved.PausedReason != "" || saved.ConsecutiveFailures != 0 || saved.RetryAt != nil {
				t.Fatalf("manual success left stale failure state: %+v", saved)
			}
			if state != "explicitly off" && state != "overdue pause" && (saved.NextRunAt == nil || !saved.NextRunAt.Equal(*routine.NextRunAt)) {
				t.Fatal("manual success changed the future schedule")
			}
			if state == "overdue pause" {
				if saved.NextRunAt == nil || !saved.NextRunAt.After(now) {
					t.Fatal("overdue slot was not advanced after manual success")
				}
				scheduler.Tick(t.Context())
				runs, _ := store.RoutineRuns(t.Context(), routine.ID, 10)
				if len(runs) != 1 {
					t.Fatal("manual success caused a duplicate scheduled run")
				}
			}
		})
	}
}

func TestExplicitlyDisabledRoutineDoesNotResumeAtNextSlot(t *testing.T) {
	store, routine, now := routineRecoveryFixture(t)
	if err := store.PauseRoutine(t.Context(), routine.ID, "provider failure"); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.GetRoutine(t.Context(), routine.ID)
	saved.Enabled = false
	saved.NextRunAt = nil
	if err := store.SaveRoutine(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(&Host{Store: store})
	scheduler.now = func() time.Time { return now.Add(48 * time.Hour) }
	scheduler.Tick(t.Context())
	saved, _ = store.GetRoutine(t.Context(), routine.ID)
	runs, _ := store.RoutineRuns(t.Context(), routine.ID, 10)
	if saved.Enabled || len(runs) != 0 {
		t.Fatal("explicitly disabled routine resumed")
	}
}
