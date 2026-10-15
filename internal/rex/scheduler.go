package rex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Routine is a recurring task for one bot. Routines only run while the Mac
// is awake and Rex is running. A slot found less than MissedGrace late
// still runs; a later one is recorded as missed and the user decides
// whether to run it. However many slots passed, only the latest counts: a
// daily routine skipped for ten days asks once, not ten times.
type Routine struct {
	ID              string `json:"id"`
	BotID           string `json:"bot_id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	ScheduleKind    string `json:"schedule_kind"`
	IntervalMinutes int    `json:"interval_minutes"`
	DailyTime       string `json:"daily_time"`
	// Cron is a standard five-field expression (minute hour day month
	// weekday) in local time, for schedule_kind "cron".
	Cron                string     `json:"cron"`
	Enabled             bool       `json:"enabled"`
	ConversationID      string     `json:"conversation_id"`
	NextRunAt           *time.Time `json:"next_run_at,omitempty"`
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	PausedReason        string     `json:"paused_reason,omitempty"`
	RetryAt             *time.Time `json:"retry_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	retrySlot           *time.Time
	retryAttempt        int
}

type RoutineRun struct {
	ID           string     `json:"id"`
	RoutineID    string     `json:"routine_id"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Attempt      int        `json:"attempt"`
	Status       string     `json:"status"`
	RunID        string     `json:"run_id,omitempty"`
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

const (
	MinIntervalMinutes   = 15
	MaxAttempts          = 3
	RetryDelay           = 2 * time.Minute
	MissedGrace          = time.Hour
	MaxConsecutiveFailed = 5
	MaxEnabledRoutines   = 20
)

// Validate checks a routine and computes its first slot.
func (r *Routine) Validate(now time.Time) error {
	r.Title = strings.TrimSpace(r.Title)
	r.Prompt = strings.TrimSpace(r.Prompt)
	if r.Title == "" || r.Prompt == "" {
		return errors.New("a routine needs a title and instructions")
	}
	switch r.ScheduleKind {
	case "interval":
		if r.IntervalMinutes < MinIntervalMinutes {
			return fmt.Errorf("routines can run at most every %d minutes", MinIntervalMinutes)
		}
	case "daily":
		if _, err := time.Parse("15:04", r.DailyTime); err != nil {
			return errors.New("daily time must look like 08:30")
		}
	case "cron":
		r.Cron = strings.Join(strings.Fields(r.Cron), " ")
		schedule, err := cron.ParseStandard(r.Cron)
		if err != nil {
			return fmt.Errorf("the schedule %q is not a valid cron expression: %w", r.Cron, err)
		}
		// Look a week ahead for runs closer together than allowed.
		at := now
		for i := 0; i < 200; i++ {
			next := schedule.Next(at)
			if next.IsZero() {
				return errors.New("this schedule never runs")
			}
			if i > 0 && next.Sub(at) < MinIntervalMinutes*time.Minute {
				return fmt.Errorf("routines can run at most every %d minutes", MinIntervalMinutes)
			}
			if next.Sub(now) > 7*24*time.Hour {
				break
			}
			at = next
		}
	default:
		return errors.New("schedule must be interval, daily or cron")
	}
	return nil
}

// LatestDue is the latest scheduled time at or before now, starting from
// the routine's pending slot.
func (r Routine) LatestDue(slot, now time.Time) time.Time {
	latest := slot
	for i := 0; i < 100000; i++ {
		next := r.NextSlot(latest)
		if next.After(now) || !next.After(latest) {
			break
		}
		latest = next
	}
	return latest
}

// NextSlot returns the first scheduled time strictly after `after`.
func (r Routine) NextSlot(after time.Time) time.Time {
	if r.ScheduleKind == "cron" {
		schedule, err := cron.ParseStandard(r.Cron)
		if err != nil {
			return after.Add(24 * time.Hour).UTC()
		}
		return schedule.Next(after.Local()).UTC()
	}
	if r.ScheduleKind == "daily" {
		clock, _ := time.Parse("15:04", r.DailyTime)
		local := after.Local()
		slot := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, time.Local)
		if !slot.After(after) {
			slot = slot.AddDate(0, 0, 1)
		}
		return slot.UTC()
	}
	return after.Add(time.Duration(r.IntervalMinutes) * time.Minute).Truncate(time.Minute).UTC()
}

// Scheduler fires due routines. Each slot is claimed in SQLite before the
// run starts and carries the claim ID as the runtime host_run_id, so a
// restart can tell a started slot from one that never ran.
type Scheduler struct {
	host *Host
	now  func() time.Time
}

func NewScheduler(host *Host) *Scheduler { return &Scheduler{host: host, now: time.Now} }

func (s *Scheduler) Loop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.host.RuntimeReady() {
			s.Tick(ctx)
		}
	}
}

// Tick handles every routine whose slot or retry is due.
func (s *Scheduler) Tick(ctx context.Context) {
	routines, err := s.host.Store.ListRoutines(ctx)
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, routine := range routines {
		// Older versions disabled failure-paused routines but retained their next slot.
		if !routine.Enabled && !(routine.PausedReason != "" && routine.NextRunAt != nil) {
			continue
		}
		if routine.PausedReason != "" {
			if routine.NextRunAt == nil || routine.NextRunAt.After(now) {
				continue
			}
			if err := s.host.Store.ResetRoutineFailures(ctx, routine.ID); err != nil {
				continue
			}
			routine.Enabled, routine.PausedReason, routine.ConsecutiveFailures = true, "", 0
			routine.RetryAt, routine.retrySlot = nil, nil
		}
		if routine.RetryAt != nil && !routine.RetryAt.After(now) && routine.retrySlot != nil {
			s.fire(ctx, routine, *routine.retrySlot, routine.retryAttempt)
			continue
		}
		if routine.NextRunAt == nil || routine.NextRunAt.After(now) {
			continue
		}
		slot := *routine.NextRunAt
		next := routine.NextSlot(now)
		// After sleep only the latest passed slot matters.
		latest := routine.LatestDue(slot, now)
		if now.Sub(latest) > MissedGrace {
			// The Mac was asleep or Rex was closed. Ask the user instead
			// of running a stale task late, and move to the next slot.
			_ = s.host.Store.RecordMissedRoutine(ctx, routine.ID, slot, latest, next)
			continue
		}
		if err := s.host.Store.AdvanceRoutine(ctx, routine.ID, slot, next); err != nil {
			continue
		}
		s.fire(ctx, routine, latest, 1)
	}
}

// RunNow fires a routine immediately as its own slot.
func (s *Scheduler) RunNow(ctx context.Context, routineID string) (*RoutineRun, error) {
	routine, err := s.host.Store.GetRoutine(ctx, routineID)
	if err != nil {
		return nil, err
	}
	return s.fire(ctx, *routine, s.now().UTC().Truncate(time.Second), 1)
}

func (s *Scheduler) fire(ctx context.Context, routine Routine, slot time.Time, attempt int) (*RoutineRun, error) {
	claim, err := s.host.Store.ClaimRoutineRun(ctx, routine.ID, slot, attempt)
	if err != nil {
		// Already claimed by an earlier tick or before a restart.
		return nil, err
	}
	run, err := s.start(ctx, routine, claim)
	if err != nil {
		slog.Warn("routine failed to start", "routine", routine.ID, "error", err)
		_ = s.host.Store.FinishRoutineRun(ctx, claim.ID, "failed", err.Error())
		s.afterFailure(ctx, routine.ID, slot, attempt, err.Error())
		claim.Status, claim.Error = "failed", err.Error()
		return claim, err
	}
	_ = s.host.Store.SetRoutineRunStarted(ctx, claim.ID, run.ID)
	claim.Status, claim.RunID = "running", run.ID
	return claim, nil
}

func (s *Scheduler) start(ctx context.Context, routine Routine, claim *RoutineRun) (*Run, error) {
	bot, err := s.host.Store.GetBot(ctx, routine.BotID)
	if err != nil {
		return nil, err
	}
	// Routines post into the bot's conversation, marked with their title.
	conversation, err := s.host.Store.ThreadForBot(ctx, *bot)
	if err != nil {
		return nil, err
	}
	if routine.ConversationID != conversation.ID {
		_ = s.host.Store.SetRoutineConversation(ctx, routine.ID, conversation.ID)
	}
	if conversation.ActiveRunID != nil {
		if active, err := s.host.Runtime.GetRun(ctx, *conversation.ActiveRunID); err == nil && (active.Status == "running" || active.Status == "queued") {
			return nil, errors.New("the bot was busy in its conversation; the routine retries shortly")
		}
	}
	turn := &Turn{ConversationID: conversation.ID, BotID: bot.ID, UserText: routine.Prompt, MemoryStatus: "skipped", Section: "⏰ " + routine.Title}
	if err := s.host.Store.CreateTurn(ctx, turn); err != nil {
		return nil, err
	}
	instructions := fmt.Sprintf("This is a scheduled routine (%q), run at %s. Nobody is watching live, so finish the task without asking questions unless it is impossible, and save any output files under outputs/.\n\n%s",
		routine.Title, s.now().Local().Format("Mon 2 Jan 15:04"), routine.Prompt)
	run, err := s.host.startRun(ctx, *bot, conversation, instructions, false, "routine-"+claim.ID)
	if err != nil {
		_ = s.host.Store.DeleteTurn(ctx, turn.ID)
		return nil, err
	}
	_ = s.host.Store.SetTurnRun(ctx, turn.ID, run.ID)
	s.host.Watch(run.ID)
	return run, nil
}

// RunSettled records the outcome of a routine's run.
func (s *Scheduler) RunSettled(ctx context.Context, run *Run) {
	claim, err := s.host.Store.RoutineRunByRunID(ctx, run.ID)
	if err != nil || claim.Status != "running" {
		return
	}
	switch run.Status {
	case "completed", "paused":
		status := "succeeded"
		if run.Status == "paused" && run.PauseReason == "human_input" {
			status = "needs_input"
		}
		_ = s.host.Store.FinishRoutineRun(ctx, claim.ID, status, "")
		if routine, err := s.host.Store.GetRoutine(ctx, claim.RoutineID); err == nil && routine.PausedReason != "" && routine.NextRunAt != nil && !routine.NextRunAt.After(s.now()) {
			// A successful manual run covers the overdue slot; do not run it again.
			_ = s.host.Store.AdvanceRoutine(ctx, routine.ID, *routine.NextRunAt, routine.NextSlot(s.now()))
		}
		_ = s.host.Store.ResetRoutineFailures(ctx, claim.RoutineID)
	case "cancelled":
		_ = s.host.Store.FinishRoutineRun(ctx, claim.ID, "cancelled", "")
	default:
		message := firstNonEmpty(run.ErrorMessage, "the run failed")
		_ = s.host.Store.FinishRoutineRun(ctx, claim.ID, "failed", message)
		s.afterFailure(ctx, claim.RoutineID, claim.ScheduledFor, claim.Attempt, message)
	}
}

func (s *Scheduler) afterFailure(ctx context.Context, routineID string, slot time.Time, attempt int, message string) {
	failures, err := s.host.Store.IncrementRoutineFailures(ctx, routineID)
	if err != nil {
		return
	}
	if failures >= MaxConsecutiveFailed {
		_ = s.host.Store.PauseRoutine(ctx, routineID, fmt.Sprintf("Retries paused after %d failures in a row; scheduling resumes at the next scheduled time. Last error: %s", failures, message))
		return
	}
	if attempt < MaxAttempts {
		_ = s.host.Store.ScheduleRoutineRetry(ctx, routineID, slot, attempt+1, s.now().UTC().Add(RetryDelay))
	}
}

// Recover reconciles claims left behind by a crash or quit. A claim that
// never reached the runtime is marked interrupted and is not re-run.
func (s *Scheduler) Recover(ctx context.Context) {
	claims, err := s.host.Store.OpenRoutineRuns(ctx)
	if err != nil {
		return
	}
	var runs []Run
	if len(claims) > 0 {
		runs, _ = s.host.Runtime.ListRuns(ctx)
	}
	for _, claim := range claims {
		runID := claim.RunID
		if runID == "" {
			for _, run := range runs {
				if run.HostRunID == "routine-"+claim.ID {
					runID = run.ID
				}
			}
		}
		if runID == "" {
			_ = s.host.Store.FinishRoutineRun(ctx, claim.ID, "interrupted", "Rex stopped before this run started. It was not retried automatically.")
			continue
		}
		if claim.RunID == "" {
			_ = s.host.Store.SetRoutineRunStarted(ctx, claim.ID, runID)
		}
		s.host.Watch(runID)
	}
}

// --- store ---

const routineColumns = `id,bot_id,title,prompt,schedule_kind,interval_minutes,daily_time,enabled,conversation_id,next_run_at,last_run_at,consecutive_failures,paused_reason,retry_slot,retry_attempt,retry_at,created_at,updated_at,cron`

func scanRoutine(row interface{ Scan(...any) error }) (*Routine, error) {
	var item Routine
	var enabled int
	err := row.Scan(&item.ID, &item.BotID, &item.Title, &item.Prompt, &item.ScheduleKind, &item.IntervalMinutes, &item.DailyTime, &enabled, &item.ConversationID, &item.NextRunAt, &item.LastRunAt, &item.ConsecutiveFailures, &item.PausedReason, &item.retrySlot, &item.retryAttempt, &item.RetryAt, &item.CreatedAt, &item.UpdatedAt, &item.Cron)
	item.Enabled = enabled == 1
	return &item, err
}

func (s *Store) ListRoutines(ctx context.Context) ([]Routine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+routineColumns+` FROM routines ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Routine{}
	for rows.Next() {
		item, err := scanRoutine(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) GetRoutine(ctx context.Context, id string) (*Routine, error) {
	item, err := scanRoutine(s.db.QueryRowContext(ctx, `SELECT `+routineColumns+` FROM routines WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) CountEnabledRoutines(ctx context.Context, except string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM routines WHERE enabled=1 AND id<>?`, except).Scan(&count)
	return count, err
}

func (s *Store) SaveRoutine(ctx context.Context, routine *Routine) error {
	now := time.Now().UTC()
	if routine.ID == "" {
		routine.ID = uuid.NewString()
		routine.CreatedAt = now
	}
	routine.UpdatedAt = now
	enabled := 0
	if routine.Enabled {
		enabled = 1
		if routine.NextRunAt == nil {
			next := routine.NextSlot(now)
			routine.NextRunAt = &next
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO routines(`+routineColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET bot_id=excluded.bot_id,title=excluded.title,prompt=excluded.prompt,schedule_kind=excluded.schedule_kind,cron=excluded.cron,
		interval_minutes=excluded.interval_minutes,daily_time=excluded.daily_time,enabled=excluded.enabled,next_run_at=excluded.next_run_at,
		paused_reason=excluded.paused_reason,consecutive_failures=excluded.consecutive_failures,retry_slot=NULL,retry_attempt=0,retry_at=NULL,updated_at=excluded.updated_at`,
		routine.ID, routine.BotID, routine.Title, routine.Prompt, routine.ScheduleKind, routine.IntervalMinutes, routine.DailyTime, enabled, routine.ConversationID,
		routine.NextRunAt, routine.LastRunAt, routine.ConsecutiveFailures, routine.PausedReason, nil, 0, nil, routine.CreatedAt, routine.UpdatedAt, routine.Cron)
	return err
}

func (s *Store) DeleteRoutine(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM routines WHERE id=?`, id)
	return err
}

func (s *Store) SetRoutineConversation(ctx context.Context, id, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routines SET conversation_id=? WHERE id=?`, conversationID, id)
	return err
}

// AdvanceRoutine moves next_run_at forward only if it still equals slot, so
// two ticks cannot both take the same slot.
func (s *Store) AdvanceRoutine(ctx context.Context, id string, slot, next time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE routines SET next_run_at=?, last_run_at=?, updated_at=? WHERE id=? AND next_run_at=?`, next, slot, time.Now().UTC(), id, slot)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("slot already taken")
	}
	return nil
}

// RecordMissedRoutine moves a routine past its pending slot and records
// the latest missed slot for the user to run or skip. An earlier miss still
// waiting for an answer is skipped: only the latest one is offered.
func (s *Store) RecordMissedRoutine(ctx context.Context, id string, slot, missed, next time.Time) error {
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE routines SET next_run_at=?, updated_at=? WHERE id=? AND next_run_at=?`, next, time.Now().UTC(), id, slot)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return nil
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE routine_runs SET status='skipped' WHERE routine_id=? AND status='missed'`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO routine_runs(id,routine_id,scheduled_for,attempt,status,error,created_at,finished_at) VALUES(?,?,?,?,?,?,?,?)`,
			uuid.NewString(), id, missed, 1, "missed", "Rex was not running or the Mac was asleep at the scheduled time.", now, now)
		return err
	})
}

// ResolveMissed records the user's answer to a missed run: "caught_up"
// when they ran it, "skipped" when they did not.
func (s *Store) ResolveMissed(ctx context.Context, runID, status string) (*RoutineRun, error) {
	runs, err := s.routineRuns(ctx, `WHERE id=? AND status='missed'`, runID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `UPDATE routine_runs SET status=? WHERE id=?`, status, runID)
	return &runs[0], err
}

// ClaimRoutineRun inserts the claim row. The unique (routine, slot, attempt)
// constraint guarantees at most one run per slot attempt.
func (s *Store) ClaimRoutineRun(ctx context.Context, routineID string, slot time.Time, attempt int) (*RoutineRun, error) {
	item := &RoutineRun{ID: uuid.NewString(), RoutineID: routineID, ScheduledFor: slot, Attempt: attempt, Status: "claimed", CreatedAt: time.Now().UTC()}
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO routine_runs(id,routine_id,scheduled_for,attempt,status,created_at) VALUES(?,?,?,?,?,?)`, item.ID, item.RoutineID, slot, attempt, item.Status, item.CreatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE routines SET retry_slot=NULL, retry_attempt=0, retry_at=NULL WHERE id=?`, routineID)
		return err
	})
	return item, err
}

func (s *Store) SetRoutineRunStarted(ctx context.Context, id, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routine_runs SET status='running', run_id=? WHERE id=?`, runID, id)
	return err
}

func (s *Store) FinishRoutineRun(ctx context.Context, id, status, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routine_runs SET status=?, error=?, finished_at=? WHERE id=?`, status, message, time.Now().UTC(), id)
	return err
}

func (s *Store) RoutineRunByRunID(ctx context.Context, runID string) (*RoutineRun, error) {
	runs, err := s.routineRuns(ctx, `WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, ErrNotFound
	}
	return &runs[0], nil
}

func (s *Store) OpenRoutineRuns(ctx context.Context) ([]RoutineRun, error) {
	return s.routineRuns(ctx, `WHERE status IN ('claimed','running')`)
}

func (s *Store) RoutineRuns(ctx context.Context, routineID string, limit int) ([]RoutineRun, error) {
	return s.routineRuns(ctx, `WHERE routine_id=? ORDER BY created_at DESC LIMIT ?`, routineID, limit)
}

func (s *Store) routineRuns(ctx context.Context, where string, args ...any) ([]RoutineRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,routine_id,scheduled_for,attempt,status,run_id,error,created_at,finished_at FROM routine_runs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RoutineRun{}
	for rows.Next() {
		var item RoutineRun
		if err := rows.Scan(&item.ID, &item.RoutineID, &item.ScheduledFor, &item.Attempt, &item.Status, &item.RunID, &item.Error, &item.CreatedAt, &item.FinishedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) IncrementRoutineFailures(ctx context.Context, id string) (int, error) {
	var failures int
	err := s.db.QueryRowContext(ctx, `UPDATE routines SET consecutive_failures=consecutive_failures+1 WHERE id=? RETURNING consecutive_failures`, id).Scan(&failures)
	return failures, err
}

func (s *Store) ResetRoutineFailures(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routines SET consecutive_failures=0,
		enabled=CASE WHEN paused_reason<>'' AND next_run_at IS NOT NULL THEN 1 ELSE enabled END,
		paused_reason='',retry_slot=NULL,retry_attempt=0,retry_at=NULL,updated_at=? WHERE id=?`, time.Now().UTC(), id)
	return err
}

func (s *Store) PauseRoutine(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routines SET paused_reason=?, retry_slot=NULL, retry_attempt=0, retry_at=NULL, updated_at=? WHERE id=?`, reason, time.Now().UTC(), id)
	return err
}

func (s *Store) ScheduleRoutineRetry(ctx context.Context, id string, slot time.Time, attempt int, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE routines SET retry_slot=?, retry_attempt=?, retry_at=? WHERE id=?`, slot, attempt, at, id)
	return err
}

// previewSchedule validates a schedule while the user edits it and lists
// its next runs.
func (h *Host) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var input Routine
	if !decode(w, r, &input) {
		return
	}
	input.Title, input.Prompt = "preview", "preview"
	now := time.Now()
	if err := input.Validate(now); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "next_runs": []time.Time{}})
		return
	}
	runs := []time.Time{}
	at := now
	for range 5 {
		at = input.NextSlot(at)
		runs = append(runs, at)
	}
	writeJSON(w, http.StatusOK, map[string]any{"next_runs": runs})
}

// answerMissed runs or skips a routine slot that was missed while the Mac
// was off.
func (h *Host) answerMissed(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"` // run or skip
	}
	if !decode(w, r, &input) {
		return
	}
	status := map[string]string{"run": "caught_up", "skip": "skipped"}[input.Action]
	if status == "" {
		writeError(w, http.StatusBadRequest, errors.New("action must be run or skip"))
		return
	}
	missed, err := h.Store.ResolveMissed(r.Context(), r.PathValue("id"), status)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("this missed run was already answered"))
		return
	}
	if input.Action == "run" {
		if _, err := h.Routines.RunNow(r.Context(), missed.RoutineID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
