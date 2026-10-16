package rex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

func TestConversationWaitsForPausedRunBeforeOfferingInteractions(t *testing.T) {
	for _, status := range []string{"queued", "running", "paused", "completed"} {
		t.Run(status, func(t *testing.T) {
			h := newSetupTestHost(t)
			if err := h.Store.SaveBot(t.Context(), &Bot{ID: "bot", Name: "Bot", Provider: ProviderLocal, MemoryMode: MemoryOff}); err != nil {
				t.Fatal(err)
			}
			conversation, err := h.Store.CreateConversation(t.Context(), "bot", "approval", "chat")
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Store.AttachRun(t.Context(), conversation.ID, "run"); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/runs/run":
					json.NewEncoder(w).Encode(sdk.AgentRun{ID: "run", AppID: AppID, Status: status})
				case "/v1/runs/run/interactions":
					json.NewEncoder(w).Encode([]sdk.AgentRunInteraction{
						{ID: "approval", RunID: "run", Status: "pending", InteractionKind: "approval_request"},
						{ID: "question", RunID: "run", Status: "pending", InteractionKind: "request_user_input"},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			h.Runtime = newTestRuntimeClient(t, server.URL, "")
			h.SetRuntimeReady(true)
			request := httptest.NewRequest("GET", "/api/conversations/"+conversation.ID, nil)
			request.SetPathValue("id", conversation.ID)
			response := httptest.NewRecorder()
			h.getConversation(response, request)
			var detail ConversationDetail
			if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			want := 0
			if status == "paused" {
				want = 2
			}
			if response.Code != 200 || len(detail.Interactions) != want {
				t.Fatalf("status=%s HTTP=%d interactions=%+v", status, response.Code, detail.Interactions)
			}
		})
	}
}
