package rex

import (
	"bytes"
	"encoding/json"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerContextSkipsChatRecall(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "researcher", Name: "Researcher", MemoryMode: MemoryPrivate, RetroOff: true}
	turn, _ := episodeFixture(t, h, bot, "Unrelated conversation history", "settled")
	// Any attempt to recall parent chat memory would panic here.
	h.Memory = nil
	raw, _ := json.Marshal(sdk.TargetContextRequest{AgentID: bot.ID, RunID: "worker", Target: sdk.TargetRef{Type: "conversation", ID: turn.ConversationID}, Metadata: map[string]any{"work_attempt_id": "attempt-1"}})
	req := httptest.NewRequest("POST", "/context", bytes.NewReader(raw))
	response := httptest.NewRecorder()
	h.runtimeContext(response, req)
	if response.Code != 200 {
		t.Fatalf("context: %s", response.Body.String())
	}
	var got sdk.TargetContext
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data["context_scope"] != "assigned_batch" || strings.Contains(got.Summary, "Unrelated conversation history") {
		t.Fatalf("worker inherited chat history: %+v", got)
	}
}
