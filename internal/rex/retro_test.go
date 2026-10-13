package rex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetroSignals(t *testing.T) {
	cases := []struct {
		user, answer, origin, section string
		failed                        int
		want                          string
	}{
		{user: "No, that's wrong, use the Swedish site", want: "corrected"},
		{user: "From now on send it as a table", want: "from now on"},
		{user: "Check the vendors", answer: "Palo Alto returned a CAPTCHA, so I skipped it.", want: "blocked"},
		{user: "Check the vendors", failed: 2, want: "2 tool call"},
		{user: "Thanks, looks good", answer: "Great!", want: ""},
		// A routine's prompt is not the user correcting the bot.
		{user: "Never claim success without a ticket ID.", section: "⏰ Daily check", want: ""},
		{user: "Always use AP.", origin: "rex", want: ""},
	}
	for _, c := range cases {
		got := strings.Join(retroSignals(Turn{UserText: c.user, Origin: c.origin, Section: c.section}, c.answer, c.failed), "; ")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%q / %q: got %q, want %q", c.user, c.answer, got, c.want)
		}
	}
}

func TestRetrospectiveSuggestsAndRecordsOutcome(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{MemoryMode: MemoryPrivate, Name: "Domain monitor", Instructions: "You check vendor categories where public forms permit.", Capabilities: Capabilities{Browser: true}}
	if err := host.SaveBot(ctx, bot); err != nil {
		t.Fatal(err)
	}

	// Signals add to one queued look-back.
	if err := host.Store.QueueRetrospective(ctx, bot.ID, "you reported something as blocked"); err != nil {
		t.Fatal(err)
	}
	_ = host.Store.QueueRetrospective(ctx, bot.ID, "the user corrected you")
	_ = host.Store.QueueRetrospective(ctx, bot.ID, "the user corrected you")
	queued, _ := host.Store.retrospectives(ctx, `WHERE bot_id=? AND status='queued'`, bot.ID)
	if len(queued) != 1 || queued[0].Trigger != "you reported something as blocked, the user corrected you" {
		t.Fatalf("queued = %+v", queued)
	}
	retro := queued[0]
	if !host.retroReady(ctx, *bot, retro) {
		t.Fatal("an idle bot with no waiting suggestion is ready")
	}
	if _, err := host.recordSuggestion(ctx, retro.ID, "nothing", "", "", ""); err == nil {
		t.Fatal("only a running look-back can answer")
	}
	_ = host.Store.updateRetro(ctx, retro.ID, "running", "run-1", "")

	if _, err := host.recordSuggestion(ctx, retro.ID, "update_instructions", "", "", "You ask the user to take over on CAPTCHAs."); err == nil {
		t.Fatal("a suggestion needs a summary")
	}
	learningFixture(t, host)
	_, episode := episodeFixture(t, host, *bot, "No, from now on preserve the reporting format", "settled")
	saveEpisodeFixture(t, host, episode, "tool_result")
	if !host.Memory.retainEpisode(ctx) {
		t.Fatal("missing learning")
	}
	if _, err := host.reviewMemory(ctx, retro.ID, *bot, "review", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := host.capabilitySearch(ctx, retro.ID, *bot, "browser"); err != nil {
		t.Fatal(err)
	}
	proof := ProposalEvidence{GapType: "instruction_contract", Sources: []string{episodeDocument(episode.TurnID)}, Gap: "Repeatedly ignores the retrieved report contract", MemoryRemedy: "Reviewed the saved reporting correction", MemoryInsufficient: "Repeatedly ignored the correction after recall", CapabilityRemedy: "Reviewed enabled browser and skills", CapabilityInsufficient: "Tools do not define the report contract", Expected: "Reports follow the existing source requirements"}
	newInstructions := "You check vendor categories. When a page shows a CAPTCHA, ask the user to take over the browser instead of skipping the vendor."
	outcome, err := host.recordSuggestion(ctx, retro.ID, "update_instructions", "Ask you to solve CAPTCHAs instead of skipping vendors", "Palo Alto and Broadcom were skipped because of CAPTCHAs.", newInstructions, proof)
	if err != nil || !strings.HasPrefix(outcome, "suggested:") {
		t.Fatalf("outcome %q, %v", outcome, err)
	}
	pending, _ := host.Store.PendingProposals(ctx)
	if len(pending) != 1 || pending[0].Source != ProposalFromRetrospective || pending[0].Reason == "" || pending[0].Instructions != newInstructions {
		t.Fatalf("pending = %+v", pending)
	}
	if done, _ := host.Store.GetRetrospective(ctx, retro.ID); done.Status != "running" || done.Outcome == "" {
		t.Fatalf("look-back result not recorded: %+v", done)
	}
	if unchanged, _ := host.Store.GetBot(ctx, bot.ID); unchanged.Instructions != bot.Instructions {
		t.Fatal("a suggestion changes nothing by itself")
	}

	_ = host.Store.updateRetro(ctx, retro.ID, "done", "", outcome)
	// While a suggestion waits, no new look-back starts.
	_ = host.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	next, _ := host.Store.retrospectives(ctx, `WHERE bot_id=? AND status='queued'`, bot.ID)
	if host.retroReady(ctx, *bot, next[0]) {
		t.Fatal("a waiting suggestion holds the next look-back")
	}
	if err := host.AnswerProposal(ctx, pending[0].ID, false); err != nil {
		t.Fatal(err)
	}
	history, _ := host.Store.proposalHistory(ctx, bot.ID, 5)
	if len(history) != 1 || history[0].Outcome != "dismissed" || history[0].Source != ProposalFromRetrospective {
		t.Fatalf("history = %+v", history)
	}
	// Asked for: no cooldown.
	if !host.retroReady(ctx, *bot, next[0]) {
		t.Fatal("a look-back the user asked for skips the cooldown")
	}
	// Otherwise the cooldown applies after a finished look-back.
	if host.retroReady(ctx, *bot, Retrospective{ID: "x", BotID: bot.ID, Trigger: "the user corrected you"}) {
		t.Fatal("cooldown should hold an automatic look-back")
	}

	brief := retroBrief(*bot, nil, []Turn{{UserText: "Run the checks", AssistantText: "Skipped Palo Alto: CAPTCHA.", Status: "settled", StartedAt: time.Now()}}, nil, history, "the user corrected you")
	for _, want := range []string{"where public forms permit", "Skipped Palo Alto", "dismissed: Ask you to solve CAPTCHAs", "Tools turned on: browser"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q:\n%s", want, brief)
		}
	}
}

func TestRetrospectiveStartsALookBackRun(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var agent map[string]any
	var run map[string]any
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/agents/"):
			_ = json.Unmarshal(body, &agent)
			_, _ = w.Write(body)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			_ = json.Unmarshal(body, &run)
			_, _ = w.Write([]byte(`{"id":"run-retro","status":"running"}`))
		case strings.HasSuffix(r.URL.Path, "/tool-calls"):
			_, _ = w.Write([]byte(`[{"id":"c1","tool_name":"browser_open","status":"failed","created_at":"2030-01-01T00:00:00Z"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runtime.Close()
	host := newSetupTestHost(t)
	host.Runtime = NewRuntimeClient(runtime.URL, "")
	bot := &Bot{ID: "monitor", Name: "Domain monitor", Instructions: "You check vendors.", Provider: ProviderLocal, Model: "qwen3"}
	if err := host.Store.SaveBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	thread, err := host.Store.ThreadForBot(ctx, *bot)
	if err != nil {
		t.Fatal(err)
	}
	turn := &Turn{ConversationID: thread.ID, BotID: bot.ID, UserText: "Run the checks"}
	if err := host.Store.CreateTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	_ = host.Store.SetTurnRun(ctx, turn.ID, "run-task")
	_ = host.Store.SettleTurn(ctx, turn.ID, "settled", "Skipped Palo Alto: CAPTCHA.")
	_ = host.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	queued, _ := host.Store.retrospectives(ctx, `WHERE status='queued'`)

	if err := host.startRetro(ctx, *bot, queued[0]); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if agent["id"] != "monitor--retro" || agent["approval_mode"] != "never" || !strings.Contains(agent["system_prompt"].(string), "suggest_improvement") {
		t.Fatalf("agent = %+v", agent)
	}
	if tools, _ := json.Marshal(agent["allowed_tools"]); strings.Contains(string(tools), "browser") {
		t.Fatalf("a look-back gets no task tools: %s", tools)
	}
	target, _ := run["target"].(map[string]any)
	servers, _ := json.Marshal(run["mcp_servers"])
	instructions, _ := run["instructions"].(string)
	if target["type"] != "routine" || !strings.Contains(string(servers), "suggest_improvement") || !strings.Contains(instructions, "Skipped Palo Alto") || !strings.Contains(instructions, "failed or were refused: 1") {
		t.Fatalf("run = %+v", run)
	}
	if started, _ := host.Store.GetRetrospective(ctx, queued[0].ID); started.Status != "running" || started.RunID != "run-retro" {
		t.Fatalf("retro = %+v", started)
	}
}
