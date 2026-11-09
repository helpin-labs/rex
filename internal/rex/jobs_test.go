package rex

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/helpin-ai/agent-runtime-go"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJobItemsFromFilesAndInline(t *testing.T) {
	dir := t.TempDir()
	bot := Bot{WorkspaceDir: dir}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("schools.csv", "\ufeffid,name,city\nnces-1,Mount Airy High,Mount Airy\nnces-2,\"Whittier High, CA\",Whittier\n")
	write("schools.json", `[{"id":"a","input":{"name":"A"}},{"id":7,"name":"Seven","city":"X"}]`)
	write("schools.jsonl", "{\"id\":\"x\",\"name\":\"X\"}\n\n{\"id\":\"y\",\"name\":\"Y\"}\n")
	write("noid.csv", "name\nA\n")

	items, err := jobItems(bot, startJobInput{ItemsFile: "schools.csv"})
	if err != nil || len(items) != 2 || items[1].ID != "nces-2" {
		t.Fatalf("csv = %+v, %v", items, err)
	}
	var row map[string]string
	_ = json.Unmarshal(items[1].Input, &row)
	if row["name"] != "Whittier High, CA" || row["city"] != "Whittier" {
		t.Fatalf("csv row = %v", row)
	}
	items, err = jobItems(bot, startJobInput{ItemsFile: "schools.json"})
	if err != nil || len(items) != 2 || string(items[0].Input) != `{"name":"A"}` || items[1].ID != "7" {
		t.Fatalf("json = %+v, %v", items, err)
	}
	items, err = jobItems(bot, startJobInput{ItemsFile: "schools.jsonl"})
	if err != nil || len(items) != 2 || items[1].ID != "y" {
		t.Fatalf("jsonl = %+v, %v", items, err)
	}
	if _, err := jobItems(bot, startJobInput{ItemsFile: "noid.csv"}); err == nil || !strings.Contains(err.Error(), "id column") {
		t.Fatalf("csv without id = %v", err)
	}
	if _, err := jobItems(bot, startJobInput{ItemsFile: "../outside.json"}); err == nil {
		t.Fatal("items_file outside the bot's folder was read")
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	_ = os.WriteFile(outside, []byte(`[{"id":"s"}]`), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.json")); err == nil {
		if _, err := jobItems(bot, startJobInput{ItemsFile: "link.json"}); err == nil {
			t.Fatal("a symlink out of the bot's folder was followed")
		}
	}
	if _, err := jobItems(bot, startJobInput{}); err == nil {
		t.Fatal("no items accepted")
	}
}

func TestItemLabel(t *testing.T) {
	for input, want := range map[string]string{
		`{"name":"Mount Airy High","city":"Mount Airy","state":"NC"}`: "Mount Airy High, Mount Airy, NC",
		`{"company":"Acme"}`:            "Acme",
		`{"url":"https://example.com"}`: "https://example.com",
		`{"n":1}`:                       "item-1",
		`not json`:                      "item-1",
	} {
		if got := itemLabel("item-1", json.RawMessage(input)); got != want {
			t.Fatalf("itemLabel(%s) = %q, want %q", input, got, want)
		}
	}
}

func TestResultColumnsSpreadsFields(t *testing.T) {
	columns, cells := resultColumns([]json.RawMessage{
		json.RawMessage(`{"language":"Go","year":2009,"creators":["Griesemer","Pike","Thompson"],"meta":{"a":1}}`),
		nil,
		json.RawMessage(`{"language":"Zig","notes":null,"extra":true}`),
	})
	if got := strings.Join(columns, ","); got != "language,year,creators,meta,notes,extra" {
		t.Fatalf("columns = %s", got)
	}
	if cells[0]["creators"] != "Griesemer; Pike; Thompson" || cells[0]["year"] != "2009" || cells[0]["meta"] != `{"a":1}` {
		t.Fatalf("row 0 = %v", cells[0])
	}
	if len(cells[1]) != 0 || cells[2]["notes"] != "" || cells[2]["extra"] != "true" {
		t.Fatalf("rows = %v", cells)
	}
	columns, cells = resultColumns([]json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage(`"plain"`)})
	if len(columns) != 1 || columns[0] != "result" || cells[1]["result"] != `"plain"` {
		t.Fatalf("mixed results = %v %v", columns, cells)
	}
}

func TestJobBrowserNoteForStandardBrowser(t *testing.T) {
	store := openTestStore(t)
	host := &Host{Store: store, Settings: NewSettingsManager(store, &MemorySecrets{})}
	ctx := context.Background()
	for _, tc := range []struct {
		backend string
		browser bool
		want    bool
	}{{"standard", true, true}, {"extension", true, true}, {"standard", false, false}} {
		bot := Bot{ID: "bot-" + tc.backend + fmt.Sprint(tc.browser), Name: "Bot", BrowserBackend: tc.backend, Capabilities: Capabilities{Browser: tc.browser}}
		if err := host.Store.SaveBot(ctx, &bot); err != nil {
			t.Fatal(err)
		}
		if got := host.jobBrowserNote(ctx, bot.ID) != ""; got != tc.want {
			t.Errorf("%s browser=%v: note shown = %v, want %v", tc.backend, tc.browser, got, tc.want)
		}
	}
}

// A batch job worker's question is outside every conversation: it still
// reaches attention, pointing at the job's conversation, and the job view
// lists it for answering on the worker's run.
func TestJobWorkerQuestionsReachAttention(t *testing.T) {
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/work-jobs/workjob_1":
			_, _ = w.Write([]byte(`{"id":"workjob_1","state":"waiting","waiting_reason":"worker_input","active_runs":["run_a","run_b"],"counts":{},"usage":{},"spec":{"objective":"o"}}`))
		case "/v1/runs/run_a/interactions", "/v1/runs/run_b/interactions":
			run := strings.Split(r.URL.Path, "/")[3]
			_, _ = w.Write([]byte(`[{"id":"ask-` + run + `","run_id":"` + run + `","interaction_kind":"request_user_input","status":"pending","title":"Which source?","summary":"Pick one"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runtime.Close()
	store := openTestStore(t)
	host := &Host{Store: store, Runtime: newTestRuntimeClient(t, runtime.URL, "")}
	host.runtimeReady.Store(true)
	ctx := context.Background()
	if err := store.CreateJob(ctx, JobRecord{ID: "workjob_1", BotID: "bot-1", ConversationID: "conv-1", Title: "Schools", State: "running"}); err != nil {
		t.Fatal(err)
	}
	items, err := host.Attention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var questions []AttentionItem
	for _, item := range items {
		if item.Kind == "question" {
			questions = append(questions, item)
		}
	}
	if len(questions) != 2 || questions[0].ConversationID != "conv-1" || questions[0].JobID != "workjob_1" || questions[0].RunID == "" {
		t.Fatalf("attention = %+v", items)
	}
	record, _ := store.GetJob(ctx, "workjob_1")
	view := host.jobView(ctx, *record, true)
	if len(view.Pending) != 2 || view.Pending[0].RunID != "run_a" || view.Pending[0].Interaction.ID != "ask-run_a" {
		t.Fatalf("job pending = %+v", view.Pending)
	}
}

// A finished job whose files or report could not be delivered is not
// marked delivered: later checks retry until both work.
func TestFailedJobDeliveryIsRetried(t *testing.T) {
	var itemCalls atomic.Int32
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/work-jobs/workjob_1":
			_, _ = w.Write([]byte(`{"id":"workjob_1","state":"completed","active_runs":[],"counts":{"total":1,"completed":1,"found":1},"usage":{},"spec":{"objective":"o"}}`))
		case "/v1/work-jobs/workjob_1/items":
			if itemCalls.Add(1) == 1 {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"a","state":"completed","outcome":"completed","result":{"v":1}}],"next_after":""}`))
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer runtime.Close()
	store := openTestStore(t)
	host := &Host{Store: store, Runtime: newTestRuntimeClient(t, runtime.URL, "")}
	ctx := context.Background()
	bot := Bot{ID: "bot-1", Name: "Bot", WorkspaceDir: t.TempDir()}
	if err := store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJob(ctx, JobRecord{ID: "workjob_1", BotID: "bot-1", ConversationID: "missing-conversation", Title: "Schools", State: "running"}); err != nil {
		t.Fatal(err)
	}
	delivered := func() (JobRecord, bool) {
		t.Helper()
		record, err := store.GetJob(ctx, "workjob_1")
		if err != nil {
			t.Fatal(err)
		}
		return *record, record.NotifiedState == "completed"
	}
	// The export fails: nothing is delivered.
	host.checkJobs(ctx)
	if _, done := delivered(); done {
		t.Fatal("job marked delivered after its files failed to save")
	}
	// The next attempt waits for its backoff.
	host.checkJobs(ctx)
	if itemCalls.Load() != 1 {
		t.Fatalf("retried before the backoff: %d export attempts", itemCalls.Load())
	}
	// Due again: the files save, but the report cannot be delivered.
	host.jobRetries.Delete("workjob_1")
	host.checkJobs(ctx)
	record, done := delivered()
	if done || record.DatasetPath == "" {
		t.Fatalf("after a failed report: delivered=%v dataset=%q", done, record.DatasetPath)
	}
	if _, ok := host.jobRetries.Load("workjob_1"); !ok {
		t.Fatal("failed report not scheduled for retry")
	}
}

// A retried start returns the same job; a deliberate rerun, changed
// instructions or a start without a request identity make a new one.
func TestJobIdempotencyFollowsTheStartingRequest(t *testing.T) {
	input := startJobInput{Title: "Schools", Objective: "Find forums", Instructions: "Check the school site."}
	items := []sdk.WorkItemInput{{ID: "a"}, {ID: "b"}}
	call := jobRequestKey(protocol.Meta{"agent_runtime/run_id": "run-1", "agent_runtime/tool_call_id": "call-1"})
	if call == "" {
		t.Fatal("request identity not read from _meta")
	}
	first := jobIdempotencyKey("bot", call, input, items)
	if again := jobIdempotencyKey("bot", call, input, items); again != first {
		t.Fatal("a retried start must return the same job")
	}
	rerun := jobRequestKey(protocol.Meta{"agent_runtime/run_id": "run-1", "agent_runtime/tool_call_id": "call-2"})
	if jobIdempotencyKey("bot", rerun, input, items) == first {
		t.Fatal("a deliberate rerun returned the earlier job")
	}
	changed := input
	changed.Instructions = "Check the school site and its social pages."
	if jobIdempotencyKey("bot", call, changed, items) == first {
		t.Fatal("changed instructions returned the earlier job")
	}
	if jobRequestKey(protocol.Meta{}) != "" || jobIdempotencyKey("bot", "", input, items) == jobIdempotencyKey("bot", "", input, items) {
		t.Fatal("starts without a request identity must never match")
	}
}
