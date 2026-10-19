package rex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

type operationalFixtureModel struct {
	err   error
	calls int
}

func (f *operationalFixtureModel) Extract(_ context.Context, r memory.RetainRequest) ([]memory.ExtractedFact, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var evidence operationalEvidence
	_ = json.Unmarshal([]byte(r.Content), &evidence)
	refs := []string{}
	kind, verified := "unfinished", "false"
	for _, entry := range evidence.Entries {
		if entry.Kind == "tool_result" {
			kind, verified = "success", "true"
			refs = append(refs, entry.ID)
		}
		if entry.Kind == "tool_failure" {
			kind, verified = "failure", "true"
			refs = append(refs, entry.ID)
		}
	}
	if len(refs) == 0 {
		refs = []string{"request/" + r.Metadata["turn_id"]}
	}
	if r.Metadata["user_correction"] == "true" {
		kind = "correction"
	}
	var revisions map[string]string
	_ = json.Unmarshal([]byte(r.Metadata["evidence_revisions"]), &revisions)
	selected, _ := json.Marshal(map[string]string{r.Metadata["turn_id"]: revisions[r.Metadata["turn_id"]]})
	raw, _ := json.Marshal(refs)
	return []memory.ExtractedFact{{Text: "A task-scoped procedure with recorded evidence.", Type: "experience", Metadata: map[string]string{"source_ids": string(raw), "source_revisions": string(selected), "verified": verified, "evidence_kind": kind}}}, nil
}
func (f *operationalFixtureModel) Consolidate(_ context.Context, facts []memory.Fact, observations []memory.Observation) (memory.ConsolidationPlan, error) {
	if f.err != nil {
		return memory.ConsolidationPlan{}, f.err
	}
	f.calls++
	ids := []string{}
	for _, fact := range facts {
		ids = append(ids, fact.ID)
	}
	if len(observations) > 0 {

		return memory.ConsolidationPlan{Updates: []memory.ConsolidationAction{{ObservationID: observations[0].ID, Text: "Use this supported procedure under the recorded conditions.", SourceFactIDs: ids}}}, nil
	}
	return memory.ConsolidationPlan{Creates: []memory.ConsolidationAction{{Text: "Use this supported procedure under the recorded conditions.", SourceFactIDs: ids}}}, nil
}

type operationalFixtureEmbedding struct{}

func (operationalFixtureEmbedding) ModelID() string { return "fixture" }
func (operationalFixtureEmbedding) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func learningFixture(t *testing.T, h *Host) *operationalFixtureModel {
	t.Helper()
	model := &operationalFixtureModel{}
	backend, err := memory.OpenSQLite(context.Background(), memory.SQLiteConfig{Path: h.Memory.path, Extractor: model, Embedder: operationalFixtureEmbedding{}})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", sqliteFileURI(h.Memory.path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	h.Memory.backend, h.Memory.reader = backend, reader
	return model
}
func episodeFixture(t *testing.T, h *Host, bot Bot, text, status string) (*Turn, TaskEpisode) {
	t.Helper()
	ctx := context.Background()
	if err := h.Store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	conversation, err := h.Store.ThreadForBot(ctx, bot)
	if err != nil {
		t.Fatal(err)
	}
	turn := &Turn{BotID: bot.ID, ConversationID: conversation.ID, UserText: text, MemoryBank: bot.MemoryBank(), MemoryStatus: "pending"}
	if err := h.Store.CreateTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	turn.RunID = "run-" + turn.ID
	_ = h.Store.SetTurnRun(ctx, turn.ID, turn.RunID)
	_ = h.Store.SettleTurn(ctx, turn.ID, status, "An unverified assistant claim.")
	turn, _ = h.Store.GetTurn(ctx, turn.ID)
	if err := h.Store.QueueEpisode(ctx, *turn); err != nil {
		t.Fatal(err)
	}
	episode, err := h.Store.Episode(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	return turn, episode
}
func saveEpisodeFixture(t *testing.T, h *Host, e TaskEpisode, kind string) {
	t.Helper()
	entries := []HistoryEntry{{ID: "request/" + e.TurnID, TurnID: e.TurnID, Kind: "user_request", Text: "Check source fidelity", CreatedAt: e.CreatedAt}, {ID: "tool/" + e.TurnID, TurnID: e.TurnID, Kind: kind, Text: "Recorded source outcome", CreatedAt: e.CreatedAt}}
	if err := h.Store.saveHistory(context.Background(), e, entries); err != nil {
		t.Fatal(err)
	}
}
func TestOperationalHistoryScopePagingRevisionsAndForgetting(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	bot := Bot{ID: "alpha", Name: "Alpha", MemoryMode: MemoryPrivate, Capabilities: Capabilities{Memory: true}}
	turn, e := episodeFixture(t, h, bot, "Old Hemnet listing", "cancelled")
	entries := []HistoryEntry{{ID: "request/" + turn.ID, TurnID: turn.ID, Kind: "user_request", Text: strings.Repeat("Hemnet 界", 300), CreatedAt: e.CreatedAt}}
	if err := h.Store.saveHistory(ctx, e, entries); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.saveHistory(ctx, e, entries); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		_, next := episodeFixture(t, h, bot, fmt.Sprintf("new task %d", i), "settled")
		saveEpisodeFixture(t, h, next, "tool_result")
	}
	page, err := h.Store.SearchHistory(ctx, bot.MemoryBank(), HistorySearch{Query: "Hemnet", Limit: 1})
	if err != nil || len(page.Entries) != 1 || !page.Entries[0].Truncated {
		t.Fatalf("history: %+v %v", page, err)
	}
	other, err := h.Store.SearchHistory(ctx, "bot:other", HistorySearch{Query: "Hemnet"})
	if err != nil || len(other.Entries) != 0 {
		t.Fatalf("scope: %+v %v", other, err)
	}
	if _, err := h.Store.ReadHistory(ctx, "bot:other", HistoryRead{SourceID: entries[0].ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-bank read accepted", err)
	}
	first, err := h.Store.ReadHistory(ctx, bot.MemoryBank(), HistoryRead{SourceID: entries[0].ID, Limit: 23})
	if err != nil || first.NextOffset == nil || len([]rune(first.Text)) != 23 {
		t.Fatalf("read: %+v %v", first, err)
	}
	next, _ := h.Store.ReadHistory(ctx, bot.MemoryBank(), HistoryRead{SourceID: entries[0].ID, Offset: *first.NextOffset, Limit: 23})
	if first.Text+next.Text != string([]rune(entries[0].Text)[:46]) {
		t.Fatal("cursor lost exact source characters")
	}
	e, _ = h.Store.Episode(ctx, turn.ID)
	refs, _ := json.Marshal([]string{entries[0].ID})
	revisions, _ := json.Marshal(map[string]string{turn.ID: e.Revision})
	fact := memory.Fact{DocumentID: episodeDocument(turn.ID), Metadata: map[string]string{"revision": e.Revision, "source_ids": string(refs), "source_revisions": string(revisions)}}
	if !h.Memory.operationalFactValid(ctx, e.Bank, fact) {
		t.Fatal("valid source rejected")
	}
	entries[0].Text = "Changed evidence"
	if err := h.Store.saveHistory(ctx, e, entries); err != nil {
		t.Fatal(err)
	}
	if h.Memory.operationalFactValid(ctx, e.Bank, fact) {
		t.Fatal("old revision still accepted")
	}
	if err := h.Memory.Forget(ctx, e.Bank, turnDocument(turn.ID)); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.retryEpisode(ctx, e.Bank, turn.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("forgotten task could retry", err)
	}
	if err := h.Store.QueueEpisode(ctx, *turn); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.ReadHistory(ctx, e.Bank, HistoryRead{SourceID: episodeDocument(turn.ID)}); !errors.Is(err, ErrNotFound) {
		t.Fatal("alias/backfill restored forgotten task", err)
	}
	cuts, _ := h.Store.ContextCuts(ctx, turn.ConversationID)
	if _, ok := cuts[turn.RunID]; !ok {
		t.Fatal("forgotten source carried into a future run")
	}
}
func TestEpisodeCaptureSeparatesClaimsRetrievalFailureAndPartialWork(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "capture", Name: "Capture", MemoryMode: MemoryPrivate}
	turn, e := episodeFixture(t, h, bot, "No, wrong source", "failed")
	at := turn.StartedAt.Add(time.Nanosecond)
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any = []any{}
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages"):
			result = []Message{{ID: "claim", Role: "assistant", Content: "Published", CreatedAt: at}, {ID: "old", Role: "assistant", Content: "wrong old task", CreatedAt: at.Add(-time.Hour)}}
		case strings.HasSuffix(r.URL.Path, "/tool-calls"):
			result = []ToolCall{{ID: "failed", ToolName: "browser_open", Status: "failed", Error: "navigation lost", CreatedAt: at}, {ID: "done", ToolName: "write_file", Status: "completed", Output: json.RawMessage(`{"success":true}`), CreatedAt: at}, {ID: "recall", ToolName: "mcp__memory__recall", Status: "completed", Output: json.RawMessage(`{"success":true}`), CreatedAt: at}}
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			result = []Artifact{{ID: "report", ArtifactType: "file", CreatedAt: at, Metadata: json.RawMessage(`{"path":"report.md"}`)}}
		case strings.HasSuffix(r.URL.Path, "/interactions"):
			result = []Interaction{{ID: "help", InteractionKind: "user_input", Title: "Takeover", CreatedAt: at}}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer runtime.Close()
	h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
	if err := h.Memory.captureEpisode(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	entries, err := h.Store.historyEntries(context.Background(), e.Bank, e.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, entry := range entries {
		kinds[entry.ID] = entry.Kind
	}
	for id, want := range map[string]string{"message/claim": "assistant_claim", "tool/failed": "tool_failure", "tool/done": "tool_result", "tool/recall": "retrieved_context", "artifact/report": "artifact_reference", "interaction/help": "interaction"} {
		if kinds[id] != want {
			t.Errorf("%s = %q want %s", id, kinds[id], want)
		}
	}
	if _, ok := kinds["message/old"]; ok {
		t.Fatal("captured evidence from another task")
	}
	if e, _ := h.Store.Episode(context.Background(), turn.ID); e.Status != "queued" {
		t.Fatal("failed task wasn't queued", e)
	}
}
func TestOperationalExtractionRequiresProofAndNeverConfirmsClaims(t *testing.T) {
	reply := `{"facts":[{"text":"Publication was claimed, but not verified.","kind":"outcome","sources":["claim"],"entities":[]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer server.Close()
	extractor := &FactExtractor{baseURL: server.URL, model: "fixture"}
	evidence, _ := json.Marshal(operationalEvidence{Entries: []HistoryEntry{{ID: "claim", Kind: "assistant_claim"}, {ID: "prior", Kind: "prior_context"}, {ID: "failed", Kind: "tool_failure"}}})
	req := memory.RetainRequest{Content: string(evidence), Metadata: map[string]string{"turn_id": "task", "user_correction": "false"}}
	facts, err := extractor.extractOperational(context.Background(), req)
	if err != nil || len(facts) != 1 || facts[0].Metadata["verified"] != "false" {
		t.Fatalf("claim: %+v %v", facts, err)
	}
	for _, bad := range []string{`{}`, `{"facts":[{"text":"A scoped claim.","kind":"success","sources":["other-bot"]}]}`, `{"facts":[{"text":"A copied old fact.","kind":"success","sources":["prior"]}]}`} {
		reply = bad
		if _, err := extractor.extractOperational(context.Background(), req); err == nil {
			t.Fatalf("invalid proof accepted: %s", bad)
		}
	}
	reply = `{"facts":[{"text":"The navigation failed with no confirmed diagnosis.","kind":"failure","sources":["failed"]}]}`
	facts, err = extractor.extractOperational(context.Background(), req)
	if err != nil || facts[0].Metadata["verified"] != "true" {
		t.Fatalf("failure: %+v %v", facts, err)
	}
}
func TestLearningConsolidationDistinctTasksAndForget(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	bot := Bot{ID: "learn", Name: "Learn", MemoryMode: MemoryPrivate, Capabilities: Capabilities{Memory: true}, RetroOff: true}
	first, e := episodeFixture(t, h, bot, "Check the source", "settled")
	saveEpisodeFixture(t, h, e, "tool_result")
	if !h.Memory.retainEpisode(ctx) {
		t.Fatal("no extraction")
	}
	if err := h.Store.retryEpisode(ctx, e.Bank, e.TurnID); err != nil {
		t.Fatal(err)
	}
	h.Memory.retainEpisode(ctx)
	result, err := h.Memory.consolidateBank(ctx, e.Bank)
	if err != nil || result.Processed != 1 {
		t.Fatalf("consolidation %+v %v", result, err)
	}
	lessons, err := h.Memory.Lessons(ctx, e.Bank)
	if err != nil || len(lessons) != 1 || lessons[0].State != "candidate" || lessons[0].TaskCount != 1 {
		t.Fatalf("independence %+v %v", lessons, err)
	}
	second, e2 := episodeFixture(t, h, bot, "Check another source", "settled")
	saveEpisodeFixture(t, h, e2, "tool_result")
	h.Memory.retainEpisode(ctx)
	_, err = h.Memory.consolidateBank(ctx, e.Bank)
	if err != nil {
		t.Fatal(err)
	}
	lessons, _ = h.Memory.Lessons(ctx, e.Bank)
	if len(lessons) != 1 || lessons[0].State != "supported" || lessons[0].TaskCount != 2 {
		t.Fatalf("repeat support: %+v", lessons)
	}
	if err := h.Memory.Forget(ctx, e.Bank, episodeDocument(first.ID)); err != nil {
		t.Fatal(err)
	}
	for _, lesson := range mustLessons(t, h, e.Bank) {
		for _, source := range lesson.SourceIDs {
			if source == episodeDocument(first.ID) {
				t.Fatal("forgotten lesson survived")
			}
		}
	}
	if second.ID == "" {
		t.Fatal("fixture failed")
	}
}
func mustLessons(t *testing.T, h *Host, bank string) []OperationalLesson {
	t.Helper()
	lessons, err := h.Memory.Lessons(context.Background(), bank)
	if err != nil {
		t.Fatal(err)
	}
	return lessons
}
func TestMemoryOffDoesNotStarveLearningAndLegacyForgottenBackfill(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	off := Bot{ID: "off", Name: "Off", MemoryMode: MemoryOff}
	_, e := episodeFixture(t, h, off, "Disabled learning", "failed")
	saveEpisodeFixture(t, h, e, "tool_failure")
	on := Bot{ID: "on", Name: "On", MemoryMode: MemoryPrivate}
	_, next := episodeFixture(t, h, on, "Enabled learning", "cancelled")
	saveEpisodeFixture(t, h, next, "tool_result")
	h.Memory.retainEpisode(ctx)
	h.Memory.retainEpisode(ctx)
	processed, _ := h.Store.Episode(ctx, next.TurnID)
	if processed.Status != "indexed" {
		t.Fatal("disabled bot starved enabled one", processed)
	}
	turn, _ := h.Store.GetTurn(ctx, next.TurnID)
	_, _ = h.Store.db.ExecContext(ctx, `DELETE FROM task_episodes WHERE turn_id=?`, turn.ID)
	_ = h.Store.SetTurnMemory(ctx, turn.ID, "forgotten", "", 0)
	if err := h.Store.reconcileEpisodes(ctx, 10); err != nil {
		t.Fatal(err)
	}
	forgotten, _ := h.Store.Episode(ctx, turn.ID)
	if !forgotten.Forgotten {
		t.Fatal("legacy tombstone lost")
	}
}
func TestContextLimitsActualRunModelLoadedWindowAndUnknown(t *testing.T) {
	h := newSetupTestHost(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") && strings.Contains(r.URL.Path, "/api/") {
			_, _ = w.Write([]byte(`{"data":[{"id":"actual","loaded_context_length":8192}]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"id":"actual","context_length":32768,"max_completion_tokens":1000}]}`))
		}
	}))
	defer server.Close()
	settings, _ := h.Settings.Get(context.Background())
	settings.Local.BaseURL = server.URL + "/v1"
	if _, _, err := h.Settings.Save(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	bot := Bot{ID: "b", Provider: ProviderLocal, Model: "other"}
	policy := h.resolveContext(context.Background(), bot, &RunModel{Model: "actual"})
	if !policy.Enabled || policy.Model != "actual" || policy.ContextWindow != 8192 || policy.MaxOutputTokens != 1000 || policy.InputLimit+policy.MaxOutputTokens+policy.SafetyTokens != policy.ContextWindow {
		t.Fatalf("policy %+v", policy)
	}
	unknown := h.resolveContext(context.Background(), bot, &RunModel{Model: "unknown"})
	if unknown.Enabled || unknown.Warning == "" {
		t.Fatalf("unknown %+v", unknown)
	}
	bot.ContextWindow = 4096
	manual := h.resolveContext(context.Background(), bot, &RunModel{Model: "actual"})
	if !manual.Enabled || manual.Source != "configured" {
		t.Fatal(manual)
	}
}
func TestOperationalMigrationBacksUpExistingSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rex.db")
	all := migrations
	migrations = all[:len(all)-1]
	store, err := OpenStore(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(context.Background(), "preserved", "old data"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err := store.GetSetting(context.Background(), "preserved")
	if err != nil || value != "old data" {
		t.Fatal("migration lost data", value, err)
	}
	backups, err := filepath.Glob(path + ".*.before-learning.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup %v %v", backups, err)
	}
	db, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity %s %v", integrity, err)
	}
}

func TestHostAttachmentAndIdleLearningPolicy(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	if h.Memory.host != h {
		t.Fatal("memory worker never attached to host")
	}
	h.lastActivity.Store(time.Now().Add(-4 * time.Minute).UnixNano())
	if !h.Memory.idleLearningReady(ctx) {
		t.Fatal("idle host not eligible for consolidation")
	}
	for _, id := range []string{"fair-a", "fair-b"} {
		bot := Bot{ID: id, Name: id, MemoryMode: MemoryPrivate, RetroOff: true}
		_, e := episodeFixture(t, h, bot, "Verify source", "settled")
		saveEpisodeFixture(t, h, e, "tool_result")
		h.Memory.retainEpisode(ctx)
	}
	_ = h.Store.QueueRetrospective(ctx, "fair-a", retroAsked)
	reviews, _ := h.Store.retrospectives(ctx, `WHERE status='queued'`)
	_ = h.Store.updateRetro(ctx, reviews[0].ID, "running", "review", "")
	if h.Memory.learningReady(ctx) || h.Memory.idleLearningReady(ctx) {
		t.Fatal("independent learning overlapped retrospective")
	}
}
func TestLocalChatPreemptsLearningWithoutUsingRetryBudget(t *testing.T) {
	h := newSetupTestHost(t)
	first, finishFirst := h.Memory.learningContext(context.Background())
	second, finishSecond := h.Memory.learningContext(context.Background())
	defer finishSecond()
	finishFirst()
	third, finishThird := h.Memory.learningContext(context.Background())
	defer finishThird()
	h.Memory.yieldToChat()
	for _, ctx := range []context.Context{first, second, third} {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("local chat couldn't yield every learning call")
		}
	}
}
func TestForgottenSourceStaysUnavailableWhileMemoryBackendIsOffline(t *testing.T) {
	h := newSetupTestHost(t)
	ctx := context.Background()
	bot := Bot{ID: "offline", Name: "Offline", MemoryMode: MemoryPrivate}
	turn, e := episodeFixture(t, h, bot, "Offline forgotten source", "settled")
	saveEpisodeFixture(t, h, e, "tool_result")
	if err := h.Memory.Forget(ctx, e.Bank, episodeDocument(turn.ID)); err != nil {
		t.Fatal(err)
	}
	if !h.Store.memoryForgotten(ctx, e.Bank, turnDocument(turn.ID)) {
		t.Fatal("canonical alias tombstone absent")
	}
	if _, err := h.Store.ReadHistory(ctx, e.Bank, HistoryRead{SourceID: episodeDocument(turn.ID)}); !errors.Is(err, ErrNotFound) {
		t.Fatal("offline history restored source", err)
	}
}

func TestCredentialRedactionInRecordedJSONAndPasswordFields(t *testing.T) {
	input := json.RawMessage(`{"selector":"input[type=password]","value":"hunter-secret","password":"another-secret"}`)
	redacted := redactToolInput(input)
	if strings.Contains(redacted, "hunter-secret") || strings.Contains(redacted, "another-secret") {
		t.Fatal("password input leaked into memory")
	}
	raw := `{"authorization":"Bearer abcdefghijklmnop","api_key":"custom-key-value","refresh_token":"refresh-secret"}`
	result := redactSecrets(raw)
	for _, secret := range []string{"abcdefghijklmnop", "custom-key-value", "refresh-secret"} {
		if strings.Contains(result, secret) {
			t.Fatal("credential JSON leaked", secret)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatal("redaction corrupted credential JSON", err)
	}
}
func TestLessonVersionsRemainScopedAndCannotResurrectForgottenSources(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	bot := Bot{ID: "versioned", Name: "Versioned", MemoryMode: MemoryPrivate, RetroOff: true}
	_, first := episodeFixture(t, h, bot, "First verified read", "settled")
	saveEpisodeFixture(t, h, first, "tool_result")
	h.Memory.retainEpisode(ctx)
	if _, err := h.Memory.consolidateBank(ctx, first.Bank); err != nil {
		t.Fatal(err)
	}
	lessons := mustLessons(t, h, first.Bank)
	id := lessons[0].ID
	initial, err := h.Store.ReadHistory(ctx, first.Bank, HistoryRead{SourceID: "lesson/" + id, Limit: 16000})
	if err != nil || initial.Kind != "lesson_version" {
		t.Fatal("lesson evidence unreadable", err)
	}
	if _, err := h.Store.ReadHistory(ctx, "bot:other", HistoryRead{SourceID: initial.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("lesson version leaked across banks", err)
	}
	_, second := episodeFixture(t, h, bot, "Second verified read", "settled")
	saveEpisodeFixture(t, h, second, "tool_result")
	h.Memory.retainEpisode(ctx)
	if _, err := h.Memory.consolidateBank(ctx, first.Bank); err != nil {
		t.Fatal(err)
	}
	var versions int
	_ = h.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM lesson_versions WHERE bank=? AND observation_id=?`, first.Bank, id).Scan(&versions)
	if versions != 2 {
		t.Fatal("previous lesson version was overwritten", versions)
	}
	if err := h.Memory.Forget(ctx, first.Bank, episodeDocument(first.TurnID)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.ReadHistory(ctx, first.Bank, HistoryRead{SourceID: initial.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("old lesson version resurrected forgotten evidence", err)
	}
}
