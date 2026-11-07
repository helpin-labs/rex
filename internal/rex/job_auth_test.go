package rex

import (
	"context"
	"encoding/json"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJobAuthRecoveryWaitsAfterRejectedRefreshButRetriesLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.recordJobRecovery(ctx, "job", "crm", "interaction-1", "fingerprint-1"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		job, connection, interaction, fingerprint string
		blocked                                   bool
	}{
		{"job", "crm", "interaction-1", "fingerprint-1", false},
		{"job", "crm", "interaction-2", "fingerprint-1", true},
		{"job", "crm", "interaction-2", "fingerprint-2", false},
		{"other-job", "crm", "interaction-2", "fingerprint-1", false},
		{"job", "other-connection", "interaction-2", "fingerprint-1", false},
	} {
		blocked, err := store.rejectedJobRecovery(ctx, test.job, test.connection, test.interaction, test.fingerprint)
		if err != nil || blocked != test.blocked {
			t.Fatalf("%+v: blocked=%v err=%v", test, blocked, err)
		}
	}
}

func TestJobAuthBlockersIdentifyMCPWithoutLeakingPayload(t *testing.T) {
	pending := []JobInteraction{{RunID: "worker", Interaction: sdk.AgentRunInteraction{ID: "interaction", InteractionKind: "authentication", RequestPayload: json.RawMessage(`{"provider":"mcp","server_id":"crm","access_token":"secret"}`)}}}
	encoded, err := json.Marshal(jobBlockers(pending))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), `"connection_id":"crm"`) {
		t.Fatalf("unsafe or misleading blockers: %s", encoded)
	}
	if got := jobAuthExplanation(pending); !strings.Contains(got, "MCP") || strings.Contains(got, "model") {
		t.Fatal(got)
	}
}

func TestJobReconnectRotatesTemplateAndHiddenWorkerBeforeResuming(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	connection := &MCPConnection{ID: "crm", Name: "CRM", URL: "https://crm.example/mcp", Auth: "bearer"}
	if err := host.Store.SaveMCPConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	if err := host.Settings.secrets.Set(mcpTokenKey("crm"), "fresh-token"); err != nil {
		t.Fatal(err)
	}
	var mutations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			mutations = append(mutations, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/runs/worker/interactions":
			json.NewEncoder(w).Encode([]Interaction{{ID: "auth-1", Status: "pending", InteractionKind: "authentication", RequestPayload: json.RawMessage(`{"provider":"mcp","server_id":"crm","credential_fingerprint":"old"}`)}})
		case "/v1/work-jobs/job/credentials":
			if r.Method == http.MethodPut {
				var req sdk.UpdateWorkJobCredentialsRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ExpectedVersion != "version" || req.MCP["crm"].AccessToken != "fresh-token" {
					t.Error("template not refreshed")
				}
			}
			json.NewEncoder(w).Encode(sdk.WorkJobCredentialState{Version: "version", MCPServerIDs: []string{"crm"}})
		case "/v1/runs/worker/mcp-servers/crm/credential":
			json.NewEncoder(w).Encode(map[string]any{"run_id": "worker"})
		case "/v1/work-jobs/job":
			json.NewEncoder(w).Encode(sdk.WorkJob{ID: "job", State: sdk.WorkJobStateWaiting})
		case "/v1/runs/worker/resume":
			var req sdk.ResumeRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.ResumeID != "job-auth-auth-1" || req.InteractionID != "auth-1" {
				t.Error("resume not tied to interaction")
			}
			json.NewEncoder(w).Encode(sdk.AgentRun{ID: "worker", Status: "running"})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host.Runtime = newTestRuntimeClient(t, server.URL, "")
	job := &sdk.WorkJob{ID: "job", State: sdk.WorkJobStateWaiting, ActiveRuns: []string{"worker"}}
	if !host.recoverJobAuthentication(ctx, JobRecord{ID: "job"}, job) {
		t.Fatal("worker did not recover")
	}
	want := []string{"/v1/work-jobs/job/credentials", "/v1/runs/worker/mcp-servers/crm/credential", "/v1/runs/worker/resume"}
	if !reflect.DeepEqual(mutations, want) {
		t.Fatalf("unsafe recovery order: %v", mutations)
	}
	mutations = nil
	job.State = sdk.WorkJobStatePaused
	if host.recoverJobAuthentication(ctx, JobRecord{ID: "job"}, job) || len(mutations) != 0 {
		t.Fatal("explicit pause was ignored")
	}
}
