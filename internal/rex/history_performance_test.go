package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndexedHistoryReadsDoNotFetchRuntime(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "history", Name: "History", MemoryMode: MemoryPrivate}
	_, e := episodeFixture(t, h, bot, "Current task", "working")
	entry := HistoryEntry{ID: "tool/cached", TurnID: e.TurnID, Kind: "tool_result", Text: "cachedneedle saved evidence", CreatedAt: e.CreatedAt}
	if err := h.Store.saveHistory(t.Context(), e, []HistoryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "[]")
	}))
	defer runtime.Close()
	h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
	h.SetRuntimeReady(true)
	for _, tool := range []struct {
		body    string
		handler http.HandlerFunc
	}{
		{`{"source_id":"tool/cached","limit":1000}`, h.memoryHistoryRead},
		{`{"query":"cachedneedle"}`, h.memoryHistorySearch},
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tool.body))
		request.SetPathValue("bank", e.Bank)
		response := httptest.NewRecorder()
		tool.handler(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "cachedneedle") {
			t.Fatalf("response=%d %s", response.Code, response.Body)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("history reads fetched runtime %d times", calls.Load())
	}
}

func TestHistoryCaptureContinuesWhileLearningIsBusy(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "live-history", Name: "Live history", MemoryMode: MemoryPrivate}
	turn, e := episodeFixture(t, h, bot, "Current task", "working")
	// Working turns have no settlement time; this fixture normally settles them.
	if _, err := h.Store.db.ExecContext(t.Context(), `UPDATE turns SET settled_at=NULL WHERE id=?`, turn.ID); err != nil {
		t.Fatal(err)
	}
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any = []any{}
		if strings.HasSuffix(r.URL.Path, "/tool-calls") {
			value = []ToolCall{{ID: "live", ToolName: "write_file", Output: json.RawMessage(`{"success":true}`), CreatedAt: e.CreatedAt}}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer runtime.Close()
	h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
	h.SetRuntimeReady(true)
	h.Memory.workMu.Lock()
	defer h.Memory.workMu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); h.Memory.captureWorker(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		captured, _ := h.Store.Episode(ctx, e.TurnID)
		if captured.Status == "captured" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture waited for learning: %+v", captured)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := h.Store.ReadHistory(ctx, e.Bank, HistoryRead{SourceID: "tool/live"}); err != nil {
		t.Fatal("working tool evidence was not archived", err)
	}
	if queued, err := h.Store.episodeQueue(ctx, "queued"); err != nil || queued != nil {
		t.Fatalf("working task entered learning queue: %+v %v", queued, err)
	}
	if next, err := h.Store.episodeQueue(ctx, "capture"); err != nil || next != nil {
		t.Fatalf("fresh snapshot immediately recaptured: %+v %v", next, err)
	}
	if _, err := h.Store.db.ExecContext(ctx, `UPDATE task_episodes SET updated_at=? WHERE turn_id=?`, time.Now().UTC().Add(-time.Minute), e.TurnID); err != nil {
		t.Fatal(err)
	}
	if next, err := h.Store.episodeQueue(ctx, "capture"); err != nil || next == nil || next.TurnID != e.TurnID {
		t.Fatalf("stale working snapshot not scheduled: %+v %v", next, err)
	}
}

func TestHistoryCaptureRedactsWithOnePasswordSnapshot(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "redacted-history", Name: "Redacted history", MemoryMode: MemoryPrivate}
	_, e := episodeFixture(t, h, bot, "Never save snapshot-private-value", "settled")
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any = []any{}
		if strings.HasSuffix(r.URL.Path, "/tool-calls") {
			value = []ToolCall{{ID: "private", ToolName: "browser_type", Input: json.RawMessage(`{"selector":"input[type=password]","value":"field-private-value"}`), Output: json.RawMessage(`{"output":"snapshot-private-value"}`), CreatedAt: e.CreatedAt}}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer runtime.Close()
	h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
	previous := storedPasswords
	defer func() { storedPasswords = previous }()
	lookups := 0
	storedPasswords = func() []string { lookups++; return []string{"snapshot-private-value"} }
	if err := h.Memory.captureEpisode(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatalf("password store read %d times", lookups)
	}
	entries, err := h.Store.historyEntries(t.Context(), e.Bank, e.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Text, "snapshot-private-value") || strings.Contains(entry.Text, "field-private-value") {
			t.Fatal("unredacted evidence persisted", entry.ID)
		}
	}
}

func TestHistoryLargeArchiveUpdatesOnlyChangedEntries(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "large-history", Name: "Large history", MemoryMode: MemoryPrivate}
	_, old := episodeFixture(t, h, bot, "Old task", "settled")
	archive := make([]HistoryEntry, 40000)
	for i := range archive {
		archive[i] = HistoryEntry{ID: fmt.Sprintf("event/archive-%d", i), TurnID: old.TurnID, Kind: "event", Text: "Historical evidence", CreatedAt: old.CreatedAt}
	}
	if err := h.Store.saveHistory(t.Context(), old, archive); err != nil {
		t.Fatal(err)
	}
	_, current := episodeFixture(t, h, bot, "Current task", "working")
	entries := make([]HistoryEntry, 1200)
	for i := range entries {
		entries[i] = HistoryEntry{ID: fmt.Sprintf("tool/current-%d", i), TurnID: current.TurnID, Kind: "tool_result", Text: "Existing result", CreatedAt: current.CreatedAt}
	}
	entries[0].Text = "oldneedle"
	entries[len(entries)-1].Text = "removedneedle"
	if err := h.Store.saveHistory(t.Context(), current, entries); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE history_writes(kind TEXT)`,
		`CREATE TRIGGER count_history_insert AFTER INSERT ON task_history BEGIN INSERT INTO history_writes VALUES('insert'); END`,
		`CREATE TRIGGER count_history_update AFTER UPDATE ON task_history BEGIN INSERT INTO history_writes VALUES('update'); END`,
		`CREATE TRIGGER count_history_delete AFTER DELETE ON task_history BEGIN INSERT INTO history_writes VALUES('delete'); END`,
	} {
		if _, err := h.Store.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	var rowID int64
	if err := h.Store.db.QueryRowContext(t.Context(), `SELECT rowid FROM task_history WHERE id=?`, entries[0].ID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	entries[0].Text = "newneedle"
	entries[len(entries)-1] = HistoryEntry{ID: "tool/new", TurnID: current.TurnID, Kind: "tool_result", Text: "Added result", CreatedAt: current.CreatedAt}
	start := time.Now()
	if err := h.Store.saveHistory(t.Context(), current, entries); err != nil {
		t.Fatal(err)
	}
	t.Logf("40,000-row archive / 1,200-entry snapshot delta: %s", time.Since(start))
	for _, kind := range []string{"insert", "update", "delete"} {
		var count int
		if err := h.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM history_writes WHERE kind=?`, kind).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s writes=%d err=%v", kind, count, err)
		}
	}
	var newRowID int64
	if err := h.Store.db.QueryRowContext(t.Context(), `SELECT rowid FROM task_history WHERE id=?`, entries[0].ID).Scan(&newRowID); err != nil || newRowID != rowID {
		t.Fatalf("source row ID changed: %d -> %d %v", rowID, newRowID, err)
	}
	for _, word := range []string{"oldneedle", "removedneedle", "newneedle"} {
		page, err := h.Store.SearchHistory(t.Context(), current.Bank, HistorySearch{Query: word})
		want := 0
		if word == "newneedle" {
			want = 1
		}
		if err != nil || len(page.Entries) != want {
			t.Fatalf("stale search index for %s: %+v %v", word, page, err)
		}
	}
	var mismatches int
	if err := h.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM task_history h JOIN task_history_fts f ON h.id=f.id WHERE h.rowid<>f.rowid`).Scan(&mismatches); err != nil || mismatches != 0 {
		t.Fatalf("search row IDs are not aligned: %d %v", mismatches, err)
	}
	// A source ID belonging to another task must roll back the whole snapshot.
	collision := append([]HistoryEntry(nil), entries...)
	collision[1].ID = archive[0].ID
	if err := h.Store.saveHistory(t.Context(), current, collision); err == nil {
		t.Fatal("cross-task source overwritten")
	}
	proof, err := h.Store.ReadHistory(t.Context(), old.Bank, HistoryRead{SourceID: archive[0].ID})
	if err != nil || proof.Text != archive[0].Text {
		t.Fatalf("source changed: %+v %v", proof, err)
	}
}

func TestHistoryReadBudgetUsesVerifiedContext(t *testing.T) {
	h := newSetupTestHost(t)
	bank := "bot:budget"
	if got := h.historyToolBudget(t.Context(), bank); got != 4000 {
		t.Fatalf("unknown context budget=%d", got)
	}
	for _, limit := range []int{2048, 128000} {
		body, _ := json.Marshal(ResolvedContext{ContextPolicy: ContextPolicy{Enabled: true, InputLimit: limit}})
		if err := h.Store.SetSetting(t.Context(), "context_policy.budget", string(body)); err != nil {
			t.Fatal(err)
		}
		if got := h.historyToolBudget(t.Context(), bank); got != min(16000, limit/8) {
			t.Fatalf("input limit=%d budget=%d", limit, got)
		}
	}
}

func TestHistoryIndexUpgradePreservesSources(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "upgrade", Name: "Upgrade", MemoryMode: MemoryPrivate}
	_, e := episodeFixture(t, h, bot, "Existing evidence", "settled")
	entry := HistoryEntry{ID: "tool/existing", TurnID: e.TurnID, Kind: "tool_result", Text: "migrationneedle original evidence", CreatedAt: e.CreatedAt}
	if err := h.Store.saveHistory(t.Context(), e, []HistoryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TRIGGER task_history_deleted`,
		`DROP TRIGGER task_history_inserted`,
		`DROP TRIGGER task_history_updated`,
		`CREATE TRIGGER task_history_deleted AFTER DELETE ON task_history BEGIN DELETE FROM task_history_fts WHERE id=old.id; END`,
		`DELETE FROM task_history_fts`,
		`INSERT INTO task_history_fts(rowid,id,text) SELECT rowid+1000,id,text FROM task_history`,
	} {
		if _, err := h.Store.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Store.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=?`, len(migrations)); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(h.Store.path + ".*.before-learning.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("upgrade backup: %v %v", backups, err)
	}
	page, err := h.Store.SearchHistory(t.Context(), e.Bank, HistorySearch{Query: "migrationneedle"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != entry.ID {
		t.Fatalf("source lost after upgrade: %+v %v", page, err)
	}
	if _, err := h.Store.db.ExecContext(t.Context(), `DELETE FROM task_history WHERE id=?`, entry.ID); err != nil {
		t.Fatal(err)
	}
	page, err = h.Store.SearchHistory(t.Context(), e.Bank, HistorySearch{Query: "migrationneedle"})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("deleted source remained searchable: %+v %v", page, err)
	}
}
