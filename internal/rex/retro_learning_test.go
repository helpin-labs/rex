package rex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryFirstReviewRepairsWithoutProposalAndGatesPrompt(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/memory/jobs" {
			writeJSON(w, 200, []any{})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	h.Runtime = newTestRuntimeClient(t, server.URL, "")
	bot := Bot{ID: "review", Name: "Review", MemoryMode: MemoryPrivate, Instructions: "You verify sources and deliver concise reports."}
	_, e := episodeFixture(t, h, bot, "No, from now on follow the required source contract", "failed")
	saveEpisodeFixture(t, h, e, "tool_failure")
	_ = h.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	queued, _ := h.Store.retrospectives(ctx, `WHERE status='queued'`)
	retro := queued[0]
	_ = h.Store.updateRetro(ctx, retro.ID, "running", "retro", " ")
	proof := ProposalEvidence{GapType: "instruction_contract", Sources: []string{episodeDocument(e.TurnID)}, Gap: "Source fidelity", MemoryRemedy: "Review", MemoryInsufficient: "No lesson yet", CapabilityRemedy: "Review", CapabilityInsufficient: "No missing capability", Expected: "Correct source"}
	if _, err := h.recordSuggestion(ctx, retro.ID, "update_instructions", "Change report instructions", "reason", "You verify every source before reporting it.", proof); err == nil {
		t.Fatal("prompt suggested without review")
	}
	if _, err := h.reviewMemory(ctx, retro.ID, bot, "review", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.capabilitySearch(ctx, retro.ID, bot, "sources"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.recordSuggestion(ctx, retro.ID, "update_instructions", "Change report instructions", "reason", "You verify every source before reporting it.", proof); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatal("pending memory bypassed gate", err)
	}
	if _, err := h.reviewMemory(ctx, retro.ID, bot, "retry", episodeDocument(e.TurnID)); err != nil {
		t.Fatal(err)
	}
	outcome, err := h.recordSuggestion(ctx, retro.ID, "nothing", "Queued recorded source evidence for learning; no prompt change needed.", "", "")
	if err != nil || outcome != "review complete" {
		t.Fatal(outcome, err)
	}
	pending, _ := h.Store.PendingProposals(ctx)
	if len(pending) != 0 {
		t.Fatal("memory repair manufactured a proposal")
	}
}
func TestCapabilityProposalHasEvidenceAndNeverConnectsOrInstalls(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	learningFixture(t, h)
	bot := Bot{ID: "capability", Name: "Capability", MemoryMode: MemoryPrivate, Instructions: "You maintain issues for this project."}
	_, e := episodeFixture(t, h, bot, "Issue API missing", "failed")
	saveEpisodeFixture(t, h, e, "tool_failure")
	_ = h.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	queued, _ := h.Store.retrospectives(ctx, `WHERE status='queued'`)
	retro := queued[0]
	_ = h.Store.updateRetro(ctx, retro.ID, "running", "retro", "")
	if _, err := h.reviewMemory(ctx, retro.ID, bot, "review", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.capabilitySearch(ctx, retro.ID, bot, "issues"); err != nil {
		t.Fatal(err)
	}
	proof := ProposalEvidence{Sources: []string{episodeDocument(e.TurnID)}, GapType: "missing_capability", Gap: "No project issue API", MemoryRemedy: "Read recorded failure", MemoryInsufficient: "Memory cannot provide an API", CapabilityRemedy: "Checked existing connections", CapabilityInsufficient: "No issue connection enabled", Documentation: []string{"https://mcp.linear.app/mcp"}, Access: "OAuth-selected project access", SetupCost: "Sign in; subscription cost unchecked", Expected: "Read project issues through the issue API"}
	if _, err := h.recordSuggestion(ctx, retro.ID, "connect_mcp", "Review Linear", "Missing issue API", "linear", proof); err != nil {
		t.Fatal(err)
	}
	connections, _ := h.Store.ListMCPConnections(ctx)
	if len(connections) != 0 {
		t.Fatal("review connected an MCP")
	}
	pending, _ := h.Store.PendingProposals(ctx)
	if len(pending) != 1 || pending[0].Kind != ProposalConnectMCP {
		t.Fatal(pending)
	}
	var saved ProposalEvidence
	if err := json.Unmarshal(pending[0].Evidence, &saved); err != nil || len(saved.Sources) != 1 {
		t.Fatal("proof not persisted", err)
	}
	if err := h.AnswerProposal(ctx, pending[0].ID, true); err == nil {
		t.Fatal("MCP apply bypassed Apps connection flow")
	}
	if err := h.AnswerProposal(ctx, pending[0].ID, false); err != nil {
		t.Fatal(err)
	}
	_ = h.Store.updateRetro(ctx, retro.ID, "done", "", "reviewed")
	// An identical dismissal requires genuinely new evidence, even in a new review.
	_ = h.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	next, _ := h.Store.retrospectives(ctx, `WHERE status='queued'`)
	_ = h.Store.updateRetro(ctx, next[0].ID, "running", "next", "")
	_, _ = h.reviewMemory(ctx, next[0].ID, bot, "review", "")
	_, _ = h.capabilitySearch(ctx, next[0].ID, bot, "issues")
	if _, err := h.recordSuggestion(ctx, next[0].ID, "connect_mcp", "Review Linear", "Missing issue API", "linear", proof); err == nil {
		t.Fatal("dismissed suggestion repeated without new evidence")
	}
}

func TestRetrospectiveRetryIsDurableAndBounded(t *testing.T) {
	ctx := context.Background()
	h := newSetupTestHost(t)
	bot := Bot{ID: "retry-review", Name: "Review", MemoryMode: MemoryPrivate}
	_, _ = episodeFixture(t, h, bot, "Check sources", "failed")
	_ = h.Store.QueueRetrospective(ctx, bot.ID, retroAsked)
	queued, _ := h.Store.retrospectives(ctx, `WHERE status='queued'`)
	id := queued[0].ID
	for attempt := 1; attempt <= 3; attempt++ {
		if err := h.Store.updateRetro(ctx, id, "running", "run", ""); err != nil {
			t.Fatal(err)
		}
		if err := h.Store.updateRetro(ctx, id, "failed", "", "model unavailable"); err != nil {
			t.Fatal(err)
		}
		review, err := h.Store.GetRetrospective(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if review.Attempts != attempt {
			t.Fatal("attempt lost", review)
		}
		if attempt < 3 {
			if review.Status != "queued" || review.RetryAt == nil || h.retroReady(ctx, bot, *review) {
				t.Fatal("failed review didn't back off", review)
			}
		} else if review.Status != "failed" {
			t.Fatal("review retries unbounded", review)
		}
	}
}
