package rex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func extensionTestHost(t *testing.T) *Host {
	t.Helper()
	h := newSetupTestHost(t)
	h.cfg.InternalToken = "runtime-secret"
	if err := h.Store.SaveBot(t.Context(), &Bot{ID: "first", Name: "first", BrowserBackend: "extension", Capabilities: Capabilities{Browser: true}}); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestRexForwardsExtensionControlsToRuntime(t *testing.T) {
	h := extensionTestHost(t)
	held := false
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/browser/first" || r.URL.Query().Get("app_id") != AppID || r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Errorf("invalid scope or authorization")
			w.WriteHeader(403)
			return
		}
		action := "status"
		if r.Method == http.MethodPost {
			var input struct {
				Action string `json:"action"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			action = input.Action
		}
		actions = append(actions, action)
		switch action {
		case "open", "takeover":
			held = true
		case "handback":
			held = false
		}
		_ = json.NewEncoder(w).Encode(RuntimeBrowserStatus{Backend: "extension", Active: true, Held: held})
	}))
	defer server.Close()
	h.Runtime = newTestRuntimeClient(t, server.URL, "runtime-token")
	if _, err := h.Browser.TakeOver(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	live, err := h.Browser.Live(t.Context(), "first")
	if err != nil || !live.Held || live.Backend != "extension" {
		t.Fatalf("live=%+v err=%v", live, err)
	}
	// A restart loses Rex's local cache; handback must still reach runtime.
	h.Browser.mu.Lock()
	delete(h.Browser.held, "first")
	h.Browser.mu.Unlock()
	if err := h.Browser.Release(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if held || strings.Join(actions, ",") != "takeover,status,handback" {
		t.Fatalf("held=%v actions=%v", held, actions)
	}
}

func TestRexSendsBrowserPolicyAndPreservesProfileIdentity(t *testing.T) {
	bot := Bot{ID: "known-bot", BrowserBackend: "extension", Capabilities: Capabilities{Browser: true}}
	var config struct {
		Browser struct {
			Backend string `json:"backend"`
			Profile string `json:"profile"`
		} `json:"browser"`
	}
	if err := json.Unmarshal(bot.RuntimeAgent("").ExecutionConfig, &config); err != nil {
		t.Fatal(err)
	}
	if config.Browser.Backend != "extension" || config.Browser.Profile != Slug(bot.ID) {
		t.Fatalf("config=%+v", config)
	}
}

func TestChangingModeChecksRuntimeHoldAndClosesOldProfile(t *testing.T) {
	h := extensionTestHost(t)
	held := true
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/agents/") {
			var agent Agent
			_ = json.NewDecoder(r.Body).Decode(&agent)
			_ = json.NewEncoder(w).Encode(agent)
			return
		}
		action := "status"
		if r.Method == http.MethodPost {
			var input struct {
				Action string `json:"action"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			action = input.Action
		}
		actions = append(actions, action)
		_ = json.NewEncoder(w).Encode(RuntimeBrowserStatus{Backend: "extension", Held: held})
	}))
	defer server.Close()
	h.Runtime = newTestRuntimeClient(t, server.URL, "runtime-token")
	bot, _ := h.Store.GetBot(t.Context(), "first")
	bot.BrowserBackend = "standard"
	if err := h.SaveBot(t.Context(), bot); err == nil || !strings.Contains(err.Error(), "hand back") {
		t.Fatalf("runtime-held browser allowed a mode change: %v", err)
	}
	saved, _ := h.Store.GetBot(t.Context(), "first")
	if saved.BrowserBackend != "extension" {
		t.Fatal("failed mode change was saved")
	}
	// Handback clears both the runtime state and Rex's cache.
	held = false
	h.Browser.mu.Lock()
	delete(h.Browser.held, "first")
	h.Browser.mu.Unlock()
	if err := h.SaveBot(t.Context(), bot); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "status,status,close" {
		t.Fatal(actions)
	}
}
func TestBrowserBackendPersistsAndStartsNewFingerprint(t *testing.T) {
	h := extensionTestHost(t)
	bot, _ := h.Store.GetBot(t.Context(), "first")
	if bot.BrowserBackend != "extension" {
		t.Fatal("backend was not saved")
	}
	before := h.botFingerprint(t.Context(), *bot)
	bot.BrowserBackend = "standard"
	if h.botFingerprint(t.Context(), *bot) == before {
		t.Fatal("backend change reused run fingerprint")
	}
	bot.BrowserBackend = "unknown"
	if bot.Normalize(t.TempDir()) == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestBrowserModeDefaultsToExtensionAndPreservesExplicitChoices(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		cloud bool
		want  string
	}{
		{"", false, "extension"}, {"", true, "cloud"},
		{"standard", false, "standard"}, {"standard", true, "standard"},
		{"extension", true, "extension"}, {"cloud", false, "cloud"},
	} {
		bot := Bot{BrowserBackend: tc.mode}
		settings := Settings{KernelBrowser: tc.cloud}
		settings.Kernel.Configured = true
		if got := bot.browserMode(settings); got != tc.want {
			t.Fatalf("mode=%q cloud=%v got=%s want=%s", tc.mode, tc.cloud, got, tc.want)
		}
	}
	h := extensionTestHost(t)
	bot, _ := h.Store.GetBot(t.Context(), "first")
	bot.BrowserBackend = ""
	if err := h.Store.SaveBot(t.Context(), bot); err != nil {
		t.Fatal(err)
	}
	if got := h.Browser.backend(t.Context(), "first"); got != "extension" {
		t.Fatal(got)
	}
	prompt := bot.SystemPrompt(nil)
	if !strings.Contains(prompt, "extension driver") || strings.Contains(prompt, "mcp__logins__sign_in") {
		t.Fatal("default browser instructions do not describe Extension")
	}
}

func TestPackagedBrowserCLIHasPriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), ExecutableName("packaged-browser"))
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_AGENT_BROWSER_EXECUTABLE", path)
	t.Setenv("AGENT_BROWSER_BINARY", "missing-user-install")
	if got := agentBrowserPath(); got != path {
		t.Fatalf("packaged browser CLI ignored: %q", got)
	}
}
