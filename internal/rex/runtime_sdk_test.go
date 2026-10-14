package rex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/helpin-ai/agent-runtime-go"
)

func newTestRuntimeClient(t *testing.T, baseURL, token string) *RuntimeClient {
	t.Helper()
	client, err := NewRuntimeClient(baseURL, token)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRuntimeSDKPreservesRunPolicyAndMessageProvenance(t *testing.T) {
	var starts []sdk.StartRunRequest
	var resumes []sdk.ResumeRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("runtime request lost authentication")
		}
		if r.URL.Path == "/v1/runs" {
			var spec sdk.StartRunRequest
			if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
				t.Error(err)
			}
			starts = append(starts, spec)
		} else {
			if r.URL.Query().Get("app_id") != AppID {
				t.Error("resume lost app scope")
			}
			var spec sdk.ResumeRunRequest
			if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
				t.Error(err)
			}
			resumes = append(resumes, spec)
		}
		json.NewEncoder(w).Encode(sdk.AgentRun{ID: "run", AppID: AppID})
	}))
	defer server.Close()
	client := newTestRuntimeClient(t, server.URL, "token")
	for _, chat := range []bool{false, true} {
		_, err := client.StartRun(t.Context(), StartRunSpec{AgentID: "bot", HostRunID: "turn", TargetType: "conversation", TargetID: "thread", Instructions: "Read evidence", Chat: chat, ExternalActorID: LocalActorID, Metadata: map[string]any{"source": "test"}, Model: &RunModel{Provider: "openai", Model: "model"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if starts[0].TurnPolicy.Mode != sdk.TurnPolicyCompleteOnFinish || starts[0].Mode != "manual" || starts[1].TurnPolicy.Mode != sdk.TurnPolicyPauseAfterAssist || starts[1].TurnPolicy.IdleTimeoutSeconds != 30*24*3600 || starts[1].Mode != "interactive" {
		t.Fatalf("run policies=%+v", starts)
	}
	for _, start := range starts {
		if start.AppID != AppID || start.HostRunID != "turn" || start.Target.ID != "thread" || start.ExternalActorID != LocalActorID || start.Model.Model != "model" || start.Metadata["source"] != "test" {
			t.Fatalf("lost run decisions: %+v", start)
		}
	}
	if _, err := client.ResumeRun(t.Context(), "run", "approve", "approved", "approval", "human-resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NotifyRun(t.Context(), "run", "delegated result", "system-resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResumeAuthCompleted(t.Context(), "run", "oauth"); err != nil {
		t.Fatal(err)
	}
	if resumes[0].MessageProvenance != "human" || resumes[0].ExternalActorID != LocalActorID || resumes[0].InteractionID != "approval" || resumes[0].ResumeID != "human-resume" {
		t.Fatalf("human provenance=%+v", resumes[0])
	}
	if resumes[1].MessageProvenance != "system_notification" || resumes[1].ExternalActorID != "" {
		t.Fatalf("notification became user input: %+v", resumes[1])
	}
	if resumes[2].Intent != "auth_completed" || resumes[2].InteractionID != "oauth" || resumes[2].MessageProvenance != "" {
		t.Fatalf("auth resume=%+v", resumes[2])
	}
}

func TestRuntimeSDKProxyForwardsSSEBytesUnchanged(t *testing.T) {
	frames := ": heartbeat\nid: 42\nevent: custom\ndata: untouched\ndata: second line\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("app_id") != AppID {
			t.Error("stream lost scope")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, frames)
	}))
	defer server.Close()
	h := newSetupTestHost(t)
	h.Runtime = newTestRuntimeClient(t, server.URL, "token")
	request := httptest.NewRequest("GET", "/api/runs/run/events", nil)
	request.SetPathValue("run_id", "run")
	response := httptest.NewRecorder()
	h.streamRunEvents(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/event-stream" || response.Body.String() != frames {
		t.Fatalf("stream proxy changed response: %d %q", response.Code, response.Body.String())
	}
}
