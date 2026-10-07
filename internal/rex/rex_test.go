package rex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "rex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCapabilitiesMapToRuntimeTools(t *testing.T) {
	bot := Bot{Name: "Reader", Capabilities: Capabilities{FilesRead: true, Web: true}, MemoryMode: MemoryOff}
	if err := bot.Normalize("/tmp/rex"); err != nil {
		t.Fatal(err)
	}
	tools := bot.AllowedTools()
	for _, want := range []string{"read_files", "list_directory", "web_search", "fetch_url", "request_approval"} {
		if !slices.Contains(tools, want) {
			t.Errorf("missing %s in %v", want, tools)
		}
	}
	for _, unwanted := range []string{"write_file", "edit_file", "run_command", "browser_open"} {
		if slices.Contains(tools, unwanted) {
			t.Errorf("read-only bot must not get %s", unwanted)
		}
	}
	if string(bot.executionConfig()) != `{"workspace":{"access":"read_only","mode":"host_prepared"}}` {
		t.Errorf("unexpected execution config %s", bot.executionConfig())
	}
}

func TestShellForcesApprovalAndFileAccess(t *testing.T) {
	bot := Bot{Name: "Ops", Capabilities: Capabilities{Shell: true}, ApprovalMode: ApprovalAuto}
	if err := bot.Normalize("/tmp/rex"); err != nil {
		t.Fatal(err)
	}
	if bot.ApprovalMode != ApprovalAsk || !bot.Capabilities.FilesRead {
		t.Fatalf("shell must imply ask + read access, got %+v", bot)
	}
	if bot.RuntimeApprovalMode() != "risk_based" {
		t.Fatalf("got %s", bot.RuntimeApprovalMode())
	}
}

func TestWorkspaceMustBeDedicated(t *testing.T) {
	for _, dir := range []string{"/", homeDir(), "relative/path"} {
		bot := Bot{Name: "X", WorkspaceDir: dir}
		if err := bot.Normalize("/tmp/rex"); err == nil {
			t.Errorf("workspace %q should be rejected", dir)
		}
	}
}

func TestMemoryBanksAreSeparateByDefault(t *testing.T) {
	a := Bot{ID: "a", MemoryMode: MemoryPrivate}
	b := Bot{ID: "b", MemoryMode: MemoryPrivate}
	c := Bot{ID: "c", MemoryMode: MemoryShared}
	if a.MemoryBank() == b.MemoryBank() {
		t.Fatal("private bots must not share a bank")
	}
	if c.MemoryBank() != sharedBank || (Bot{MemoryMode: MemoryOff}).MemoryBank() != "" {
		t.Fatal("unexpected shared/off banks")
	}
}

func TestRetentionSkipsSmallTalkAndRedactsSecrets(t *testing.T) {
	if skipRetention("thanks!") == "" || skipRetention("ok") == "" {
		t.Fatal("small talk should be skipped")
	}
	if skipRetention("I'm allergic to peanuts and prefer vegetarian food") != "" {
		t.Fatal("a durable fact must be retained")
	}
	redacted := redactSecrets("my key is sk-abcdefghijklmnopqrstuvwxyz123456 and password: hunter2")
	if redacted == "" || containsAny(redacted, "sk-abcdef", "hunter2") {
		t.Fatalf("secrets not redacted: %q", redacted)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func TestPlausibleTextRejectsDegenerateOutput(t *testing.T) {
	if plausibleText("…..………………………………??") {
		t.Fatal("degenerate output accepted")
	}
	if !plausibleText("The user prefers answers in bullet points.") {
		t.Fatal("valid fact rejected")
	}
}

func TestRoutineValidationAndSlots(t *testing.T) {
	routine := Routine{Title: "t", Prompt: "p", ScheduleKind: "interval", IntervalMinutes: 5}
	if err := routine.Validate(time.Now()); err == nil {
		t.Fatal("intervals under 15 minutes must be rejected")
	}
	routine.IntervalMinutes = 60
	now := time.Date(2026, 10, 5, 10, 17, 30, 0, time.UTC)
	if got := routine.NextSlot(now); !got.Equal(time.Date(2026, 10, 5, 11, 17, 0, 0, time.UTC)) {
		t.Fatalf("interval slot %v", got)
	}
	daily := Routine{ScheduleKind: "daily", DailyTime: "08:30"}
	local := time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)
	next := daily.NextSlot(local).Local()
	if next.Day() != 6 || next.Hour() != 8 || next.Minute() != 30 {
		t.Fatalf("daily slot after 09:00 should be tomorrow 08:30, got %v", next)
	}
}

func TestRoutineSlotIsClaimedOnce(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "R"}
	_ = bot.Normalize(t.TempDir())
	if err := store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	routine := Routine{BotID: bot.ID, Title: "t", Prompt: "p", ScheduleKind: "interval", IntervalMinutes: 60, Enabled: true}
	if err := store.SaveRoutine(ctx, &routine); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetRoutine(ctx, routine.ID)
	if err != nil || saved.NextRunAt == nil {
		t.Fatalf("routine not scheduled: %v %+v", err, saved)
	}
	slot := *saved.NextRunAt
	next := slot.Add(time.Hour)
	if err := store.AdvanceRoutine(ctx, routine.ID, slot, next); err != nil {
		t.Fatalf("first advance: %v", err)
	}
	if err := store.AdvanceRoutine(ctx, routine.ID, slot, next); err == nil {
		t.Fatal("a slot must only be taken once")
	}
	if _, err := store.ClaimRoutineRun(ctx, routine.ID, slot, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimRoutineRun(ctx, routine.ID, slot, 1); err == nil {
		t.Fatal("duplicate claim for the same slot attempt must fail")
	}
	if _, err := store.ClaimRoutineRun(ctx, routine.ID, slot, 2); err != nil {
		t.Fatalf("a retry attempt is a separate claim: %v", err)
	}
}

func TestOnlyTheLatestMissedSlotIsOffered(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "R"}
	_ = bot.Normalize(t.TempDir())
	_ = store.SaveBot(ctx, &bot)
	// A daily routine whose slot was three hours ago, last run ten days ago.
	today := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	tenDaysAgo := today.AddDate(0, 0, -10).UTC()
	routine := Routine{BotID: bot.ID, Title: "News", Prompt: "p", ScheduleKind: "daily", DailyTime: today.Local().Format("15:04"), Enabled: true, NextRunAt: &tenDaysAgo}
	if err := store.SaveRoutine(ctx, &routine); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(&Host{Store: store})
	scheduler.Tick(ctx)
	runs, _ := store.RoutineRuns(ctx, routine.ID, 20)
	if len(runs) != 1 || runs[0].Status != "missed" || !runs[0].ScheduledFor.Equal(today.UTC()) {
		t.Fatalf("ten skipped days are one missed run for the latest day, got %+v", runs)
	}
	updated, _ := store.GetRoutine(ctx, routine.ID)
	if updated.NextRunAt == nil || !updated.NextRunAt.After(time.Now()) {
		t.Fatalf("next run should move to the future, got %v", updated.NextRunAt)
	}

	// A later miss replaces the unanswered one.
	later := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	if err := store.RecordMissedRoutine(ctx, routine.ID, *updated.NextRunAt, later, later.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	runs, _ = store.RoutineRuns(ctx, routine.ID, 20)
	pending := 0
	for _, run := range runs {
		if run.Status == "missed" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("only the latest miss waits for an answer, got %+v", runs)
	}
	if _, err := store.ResolveMissed(ctx, runs[0].ID, "skipped"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveMissed(ctx, runs[0].ID, "skipped"); err == nil {
		t.Fatal("a miss is answered once")
	}
}

func TestCronSchedules(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local) // a Friday
	weekdays := Routine{Title: "t", Prompt: "p", ScheduleKind: "cron", Cron: "0 8 * * 1-5"}
	if err := weekdays.Validate(now); err != nil {
		t.Fatal(err)
	}
	if next := weekdays.NextSlot(now).Local(); next.Weekday() != time.Monday || next.Hour() != 8 {
		t.Fatalf("after Friday noon the next weekday run is Monday 08:00, got %v", next)
	}
	tooOften := Routine{Title: "t", Prompt: "p", ScheduleKind: "cron", Cron: "*/5 * * * *"}
	if err := tooOften.Validate(now); err == nil {
		t.Fatal("schedules more often than every 15 minutes are rejected")
	}
	broken := Routine{Title: "t", Prompt: "p", ScheduleKind: "cron", Cron: "every morning"}
	if err := broken.Validate(now); err == nil {
		t.Fatal("invalid cron expressions are rejected")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rex.db")
	for i := 0; i < 2; i++ {
		store, err := OpenStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		store.Close()
	}
}

func TestMemoryRebuildsWhenEmbeddingModelChanges(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE memory_banks (app_id TEXT, bank_id TEXT, model_id TEXT, dimensions INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memory_banks VALUES ('rex','bot:a','http://127.0.0.1:1234/v1/old-model/x',768)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	bot := Bot{Name: "A"}
	_ = bot.Normalize(dir)
	_ = store.SaveBot(ctx, &bot)
	conversation, _ := store.CreateConversation(ctx, bot.ID, "c", "chat")
	retained := &Turn{ConversationID: conversation.ID, BotID: bot.ID, UserText: "I like tea", MemoryBank: "bot:" + bot.ID}
	forgotten := &Turn{ConversationID: conversation.ID, BotID: bot.ID, UserText: "I like coffee", MemoryBank: "bot:" + bot.ID}
	for _, turn := range []*Turn{retained, forgotten} {
		_ = store.CreateTurn(ctx, turn)
		_ = store.SettleTurn(ctx, turn.ID, "settled", "ok")
	}
	_ = store.SetTurnMemory(ctx, retained.ID, "retained", "", 1)
	_ = store.SetTurnMemory(ctx, forgotten.ID, "forgotten", "", 0)

	service := NewMemoryService(path, store, nil)
	if err := service.rebuildIfEmbeddingChanged(ctx, bundledEmbeddingModelID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old memory database should have been moved aside")
	}
	backups, _ := filepath.Glob(path + ".*.bak")
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}
	if turn, _ := store.GetTurn(ctx, retained.ID); turn.MemoryStatus != "pending" {
		t.Fatalf("retained source should be learned again, got %s", turn.MemoryStatus)
	}
	if turn, _ := store.GetTurn(ctx, forgotten.ID); turn.MemoryStatus != "forgotten" {
		t.Fatalf("forgotten source must stay forgotten, got %s", turn.MemoryStatus)
	}
	// Same model: nothing happens.
	if err := service.rebuildIfEmbeddingChanged(ctx, bundledEmbeddingModelID); err != nil {
		t.Fatal(err)
	}
}

func TestSearchKeysReachTheRuntime(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingsManager(openTestStore(t), NewMemorySecrets())
	current, _ := settings.Get(ctx)
	if current.WebSearchReady() {
		t.Fatal("no search without a key")
	}
	current.TinyFish.APIKey = "tf-secret"
	saved, restart, err := settings.Save(ctx, current)
	if err != nil || !restart || !saved.WebSearchReady() || saved.TinyFish.APIKey != "" {
		t.Fatalf("a search key is stored, hidden and restarts the runtime: %v %v %+v", err, restart, saved.TinyFish)
	}
	env, _ := settings.RuntimeEnv(ctx)
	if !slices.Contains(env, "TINYFISH_API_KEY=tf-secret") {
		t.Fatalf("the runtime gets the search key: %v", env)
	}
	if tools := (Bot{Capabilities: Capabilities{Web: true}}).AllowedTools(); !slices.Contains(tools, "crawl_url") {
		t.Fatalf("web bots can crawl sites: %v", tools)
	}
}
