package rex

import (
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestUnavailableSandboxKeepsBotChatAndPreferences(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "coding", Name: "Coder", Capabilities: Capabilities{FilesRead: true, FilesWrite: true, Shell: true}, ContextWindow: 32000}
	if err := h.Store.SaveBot(t.Context(), &bot); err != nil {
		t.Fatal(err)
	}
	h.writeTools.Store(true)
	before := h.botFingerprint(t.Context(), bot)
	h.SetRuntimeReady(false)
	agent, _ := h.runAgent(t.Context(), bot, &RunModel{Provider: "openai", Model: "test"})
	for _, tool := range []string{"write_file", "edit_file", "run_command", "run_python"} {
		if slices.Contains(agent.AllowedTools, tool) {
			t.Fatalf("unavailable tool retained: %s", tool)
		}
	}
	if !slices.Contains(agent.AllowedTools, "read_files") {
		t.Fatal("lost file reading without the sandbox")
	}
	if before == h.botFingerprint(t.Context(), bot) {
		t.Fatal("paused runs would keep the old command policy")
	}
	saved, err := h.Store.GetBot(t.Context(), bot.ID)
	if err != nil || !saved.Capabilities.Shell || !saved.Capabilities.FilesWrite {
		t.Fatalf("temporary failure erased saved permissions: %+v %v", saved, err)
	}
}

func TestUnavailableSandboxAllowsEditingExistingBotWithoutNewCommands(t *testing.T) {
	h := newSetupTestHost(t)
	bot := Bot{ID: "existing", Name: "Coder", Capabilities: Capabilities{FilesRead: true, FilesWrite: true, Shell: true}}
	if err := h.Store.SaveBot(t.Context(), &bot); err != nil {
		t.Fatal(err)
	}
	bot.Name = "Renamed"
	if err := h.SaveBot(t.Context(), &bot); err != nil {
		t.Fatalf("unavailable sandbox blocked unrelated edit: %v", err)
	}
	bot.Capabilities.Shell = false
	if err := h.SaveBot(t.Context(), &bot); err != nil {
		t.Fatal(err)
	}
	bot.Capabilities.Shell = true
	if err := h.SaveBot(t.Context(), &bot); err != ErrWriteToolsUnavailable {
		t.Fatalf("new shell permission enabled without sandbox: %v", err)
	}
}
