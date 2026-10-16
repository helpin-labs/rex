package rex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommandSandboxSetupDoesNotStartDuringTasksOrAnotherSetup(t *testing.T) {
	for _, busy := range []bool{false, true} {
		h := &Host{Sandbox: NewCommandSandbox(t.TempDir(), ""), watched: map[string]bool{}}
		h.Sandbox.status.Supported = true
		if busy {
			h.Sandbox.status.Busy = true
		} else {
			h.watched["task"] = true
		}
		response := httptest.NewRecorder()
		h.setupCommandSandbox(response, httptest.NewRequest(http.MethodPost, "/api/command-sandbox/setup", nil))
		if response.Code != http.StatusConflict {
			t.Fatalf("setup admitted with busy=%v: %d", busy, response.Code)
		}
	}
}

func TestRuntimeRestartRevokesWriteToolsUntilVerifiedAgain(t *testing.T) {
	h := &Host{}
	h.writeTools.Store(true)
	h.SetRuntimeReady(false)
	if h.WriteTools() {
		t.Fatal("stopped runtime retained command permissions")
	}
	h.SetRuntimeReady(true)
	if h.WriteTools() {
		t.Fatal("readiness alone enabled commands without capabilities")
	}
}
