package rex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"github.com/helpin-ai/agent-runtime/memory"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLearningReconcilesFinishedTurnsButPreservesActiveOrUnknownRuns(t *testing.T) {
	for _, test := range []struct {
		name, status string
		code         int
		want         string
	}{
		{"completed", "completed", 200, "settled"}, {"failed", "failed", 200, "failed"}, {"active", "running", 200, "working"}, {"missing", "", 404, "failed"}, {"unavailable", "", 503, "working"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newSetupTestHost(t)
			bot := Bot{ID: "researcher", Name: "Researcher", MemoryMode: MemoryPrivate, RetroOff: true}
			turn, _ := episodeFixture(t, h, bot, "Research", "settled")
			_, err := h.Store.db.Exec(`UPDATE turns SET status='working',settled_at=NULL WHERE id=?`, turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/messages") {
					writeJSON(w, 200, []Message{})
					return
				}
				if test.code != 200 {
					w.WriteHeader(test.code)
					return
				}
				writeJSON(w, 200, Run{ID: turn.RunID, Status: test.status})
			}))
			defer server.Close()
			h.Runtime = newTestRuntimeClient(t, server.URL, "")
			h.recoverTurns(context.Background())
			h.reconcileWorkingTurns(context.Background())
			current, err := h.Store.GetTurn(context.Background(), turn.ID)
			if err != nil || current.Status != test.want {
				t.Fatalf("turn=%+v err=%v", current, err)
			}
		})
	}
}

func TestManualConsolidationExplainsPendingExtraction(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "researcher", Name: "Researcher", MemoryMode: MemoryPrivate, RetroOff: true}
	_, e := episodeFixture(t, h, bot, "Research", "settled")
	saveEpisodeFixture(t, h, e, "tool_result")
	_, err := h.Memory.requestConsolidation(context.Background(), e.Bank)
	if err == nil || !strings.Contains(err.Error(), "extraction is pending for 1 tasks") {
		t.Fatalf("error=%v", err)
	}
}

func TestRuntimeLearningCallbackRejectsForeignAndForgottenEvidence(t *testing.T) {
	h := newSetupTestHost(t)
	learningFixture(t, h)
	bot := Bot{ID: "researcher", Name: "Researcher", MemoryMode: MemoryPrivate, RetroOff: true}
	_, e := episodeFixture(t, h, bot, "Research", "settled")
	saveEpisodeFixture(t, h, e, "tool_result")
	e, _ = h.Store.Episode(context.Background(), e.TurnID)
	for _, test := range []struct {
		name, app, bank, revision string
		code                      int
		invalid                   bool
	}{
		{"foreign app", "other", e.Bank, e.Revision, 400, false}, {"foreign bank", AppID, "bot:other", e.Revision, 200, true}, {"stale revision", AppID, e.Bank, "old", 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(sdk.MemoryLearningJob{AppID: test.app, BankID: test.bank, Kind: "extraction", SourceID: episodeDocument(e.TurnID), Revision: test.revision})
			w := httptest.NewRecorder()
			h.processMemoryLearning(w, httptest.NewRequest("POST", "/runtime/memory/learning", bytes.NewReader(body)))
			if w.Code != test.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.code == 200 {
				var result sdk.MemoryLearningResult
				json.Unmarshal(w.Body.Bytes(), &result)
				if result.InvalidSource != test.invalid {
					t.Fatalf("result=%+v", result)
				}
			}
		})
	}
	if err := h.Memory.Forget(context.Background(), e.Bank, episodeDocument(e.TurnID)); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(sdk.MemoryLearningJob{AppID: AppID, BankID: e.Bank, Kind: "extraction", SourceID: episodeDocument(e.TurnID), Revision: e.Revision})
	w := httptest.NewRecorder()
	h.processMemoryLearning(w, httptest.NewRequest("POST", "/runtime/memory/learning", bytes.NewReader(body)))
	var result sdk.MemoryLearningResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result.InvalidSource {
		t.Fatalf("forgotten result=%+v", result)
	}
}

func TestLearningMigrationPreservesEvidenceAndRetryState(t *testing.T) {
	h := newSetupTestHost(t)
	model := learningFixture(t, h)
	bot := Bot{ID: "researcher", Name: "Researcher", MemoryMode: MemoryPrivate, RetroOff: true}
	_, e := episodeFixture(t, h, bot, "Research", "settled")
	saveEpisodeFixture(t, h, e, "tool_result")
	e, _ = h.Store.Episode(context.Background(), e.TurnID)
	retry := time.Now().UTC().Add(time.Minute)
	_, err := h.Store.db.Exec(`UPDATE task_episodes SET attempts=2,retry_at=?,error='temporary outage' WHERE turn_id=?`, retry, e.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	var submitted []sdk.MemoryLearningJob
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, 200, []sdk.MemoryLearningJob{})
			return
		}
		var j sdk.MemoryLearningJob
		if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
			t.Error(err)
		}
		j.ID = "job"
		submitted = append(submitted, j)
		writeJSON(w, 200, j)
	}))
	defer server.Close()
	h.Runtime = newTestRuntimeClient(t, server.URL, "")
	if err := h.Memory.syncLearningJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(submitted) != 1 || submitted[0].SourceID != episodeDocument(e.TurnID) || submitted[0].Revision != e.Revision || submitted[0].Attempts != 2 || submitted[0].RetryAt == nil || !submitted[0].RetryAt.Equal(retry) {
		t.Fatalf("import=%+v", submitted)
	}
	current, err := h.Store.Episode(context.Background(), e.TurnID)
	if err != nil || current.Revision != e.Revision || current.Attempts != 2 {
		t.Fatalf("episode=%+v err=%v", current, err)
	}
	if model.calls != 0 {
		t.Fatal("import performed model inference")
	}
	var history int
	h.Store.db.QueryRow(`SELECT count(*) FROM task_history WHERE turn_id=?`, e.TurnID).Scan(&history)
	if history != 2 {
		t.Fatalf("history changed: %d", history)
	}
}

type parallelLearningExtractor struct {
	started chan struct{}
	release chan struct{}
}

func (e parallelLearningExtractor) Extract(ctx context.Context, _ memory.RetainRequest) ([]memory.ExtractedFact, error) {
	e.started <- struct{}{}
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []memory.ExtractedFact{{Text: "An independent source was verified.", Type: "experience"}}, nil
}
func TestRuntimeLearningCallbacksExtractIndependentSourcesConcurrently(t *testing.T) {
	h := newSetupTestHost(t)
	ctx := context.Background()
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	backend, err := memory.OpenSQLite(ctx, memory.SQLiteConfig{Path: h.Memory.path, Extractor: parallelLearningExtractor{started, release}, Embedder: operationalFixtureEmbedding{}})
	if err != nil {
		t.Fatal(err)
	}
	h.Memory.backend = backend
	done := make(chan sdk.MemoryLearningResult, 3)
	for i := 0; i < 3; i++ {
		note := MemoryNote{Bank: "bot:researcher", Content: fmt.Sprintf("Independent verification note %d.", i)}
		if err := h.Store.CreateMemoryNote(ctx, &note); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(note.Content))
		body, _ := json.Marshal(sdk.MemoryLearningJob{AppID: AppID, BankID: note.Bank, Kind: "extraction", SourceID: noteDocument(note.ID), Revision: hex.EncodeToString(digest[:])})
		go func() {
			w := httptest.NewRecorder()
			h.processMemoryLearning(w, httptest.NewRequest("POST", "/runtime/memory/learning", bytes.NewReader(body)))
			var result sdk.MemoryLearningResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Error(err)
			}
			done <- result
		}()
	}
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			for i := 0; i < 3; i++ {
				<-done
			}
			t.Fatal("host serialized independent extraction")
		}
	}
	close(release)
	for i := 0; i < 3; i++ {
		result := <-done
		if result.Error != "" || result.Facts != 1 || result.Interrupted {
			t.Fatalf("extraction=%+v", result)
		}
	}
}
