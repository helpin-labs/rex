package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func extensionTestHost(t *testing.T) *Host {
	t.Helper()
	h := newSetupTestHost(t)
	h.cfg.DataDir = t.TempDir()
	h.cfg.InternalToken = "runtime-secret"
	for _, id := range []string{"first", "second"} {
		if err := h.Store.SaveBot(t.Context(), &Bot{ID: id, Name: id, BrowserBackend: "extension", Capabilities: Capabilities{Browser: true}}); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestExtensionLaunchHasNoDebuggerOrAutomationFlags(t *testing.T) {
	args := extensionBrowserArgs("/tmp/profile", "https://example.com", "/tmp/extension")
	for _, arg := range args {
		if strings.Contains(arg, "debug") || strings.Contains(arg, "Automation") || strings.Contains(arg, "enable-automation") {
			t.Fatalf("automation flag: %s", arg)
		}
	}
	raw, _ := browserExtensionFiles.ReadFile("browser_extension/manifest.json")
	var manifest struct {
		Permissions           []string `json:"permissions"`
		ExternallyConnectable any      `json:"externally_connectable"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(manifest.Permissions, "debugger") || manifest.ExternallyConnectable != nil {
		t.Fatal("extension must have no debugger or web-page control channel")
	}
}

func TestExtensionBridgeScopesResponsesAndRejectsPageOrigins(t *testing.T) {
	h := extensionTestHost(t)
	first, err := h.Browser.extension(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Browser.extension(t.Context(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if first.token == second.token {
		t.Fatal("shared bridge credential")
	}
	handler := h.Handler()
	post := func(token, origin, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/extension/browser", strings.NewReader(`{"id":"`+id+`","result":{"success":true,"data":{"text":"ok"}}}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	if got := post(first.token, "https://attacker.example", "x").Code; got != 403 {
		t.Fatalf("page origin: %d", got)
	}
	if got := post("wrong", browserExtensionOrigin, "x").Code; got != 401 {
		t.Fatalf("bad token: %d", got)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() { _, err := first.execute(ctx, "run-first", []string{"get", "text"}); completed <- err }()
	command := <-first.queue
	if got := post(second.token, browserExtensionOrigin, command.ID).Code; got != 409 {
		t.Fatalf("other bot response: %d", got)
	}
	if got := post(first.token, browserExtensionOrigin, command.ID).Code; got != 204 {
		t.Fatalf("owner response: %d", got)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if got := post(first.token, browserExtensionOrigin, command.ID).Code; got != 409 {
		t.Fatalf("replayed response: %d", got)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := first.execute(ctx, "expired", []string{"click", "@e1"}); err == nil {
		t.Fatal("cancelled command accepted")
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if len(first.pending) != 0 {
		t.Fatal("expired command retained")
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

// Exercise the production launcher with automatic extension loading, never CDP.
func TestExtensionBrowserWithoutDevTools(t *testing.T) {
	if os.Getenv("REX_EXTENSION_E2E") != "1" {
		t.Skip("set REX_EXTENSION_E2E=1 to run Chrome for Testing without DevTools")
	}
	executable := bundledChrome()
	if executable == "" {
		t.Fatal("Chrome for Testing is not installed")
	}
	h := extensionTestHost(t)
	server := httptest.NewServer(h.Handler())
	defer server.Close()
	h.cfg.HostURL = server.URL
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			cookie, _ := r.Cookie("rex")
			value := ""
			if cookie != nil {
				value = cookie.Value
			}
			_, _ = w.Write([]byte(`<title>Next</title><p>Done</p><p id="cookie">` + value + `</p>`))
			return
		}
		_, _ = w.Write([]byte(`<title>Fixture</title><label>Email<input id="email"></label><button onclick="document.getElementById('result').textContent=document.getElementById('email').value">Save</button><p id="result"></p><p id="webdriver"></p><a href="/next">Next</a><script>document.cookie='rex=kept; path=/; max-age=86400';document.getElementById('webdriver').textContent=String(navigator.webdriver)</script>`))
	}))
	defer fixture.Close()
	ext, err := h.Browser.extension(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Browser.launchExtension(t.Context(), "first", "about:blank", ext); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if ext.process != nil {
			_ = ext.process.Process.Kill()
			<-ext.exited
		}
	}()
	call := func(args ...string) map[string]any {
		t.Helper()
		raw, err := ext.execute(t.Context(), "test-session", args)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Success bool           `json:"success"`
			Error   string         `json:"error"`
			Data    map[string]any `json:"data"`
		}
		if json.Unmarshal(raw, &out) != nil || !out.Success {
			t.Fatalf("%v: %s", args, raw)
		}
		return out.Data
	}
	call("open", fixture.URL)
	if got := call("get", "text", "#webdriver")["text"]; got != "false" {
		t.Fatalf("browser reports automation: %v", got)
	}
	snap := call("snapshot", "-i")["snapshot"].(string)
	find := func(label string) string {
		for _, line := range strings.Split(snap, "\n") {
			if strings.Contains(line, label) {
				_, rest, _ := strings.Cut(line, "[ref=")
				ref, _, _ := strings.Cut(rest, "]")
				return "@" + ref
			}
		}
		t.Fatalf("missing %s: %s", label, snap)
		return ""
	}
	email, save, next := find("Email"), find("Save"), find("Next")
	call("fill", email, "extension works")
	call("click", save)
	if text := call("get", "text", "#result")["text"]; text != "extension works" {
		t.Fatalf("input result: %v", text)
	}
	raw, _ := ext.execute(t.Context(), "test-session", []string{"get", "text", "body"})
	if !bytes.Contains(raw, []byte("extension works")) {
		t.Fatal("read lost page text")
	}
	image := call("screenshot", "png")["image"].(string)
	if !strings.HasPrefix(image, "data:image/png;base64,") {
		t.Fatal("no visible-tab screenshot")
	}
	call("click", next)
	call("wait", "500")
	if got := call("get", "url")["url"]; got != fixture.URL+"/next" {
		t.Fatalf("navigation: %v", got)
	}
	call("snapshot", "-i")
	raw, err = ext.execute(t.Context(), "test-session", []string{"click", save})
	if err != nil || !bytes.Contains(raw, []byte("Stale element reference")) {
		t.Fatalf("stale ref: %s %v", raw, err)
	}
	call("open", fixture.URL)
	snap = call("snapshot", "-i")["snapshot"].(string)
	email = find("Email")
	h.Browser.held["first"] = heldBrowser{Extension: true}
	if err := h.Browser.Release(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	raw, err = ext.execute(t.Context(), "test-session", []string{"fill", email, "stale after takeover"})
	if err != nil || !bytes.Contains(raw, []byte("Stale element reference")) {
		t.Fatalf("takeover ref survived: %s %v", raw, err)
	}
	call("close")
	time.Sleep(500 * time.Millisecond)
	_ = ext.process.Process.Signal(os.Interrupt)
	select {
	case <-ext.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not close")
	}
	if err := h.Browser.launchExtension(t.Context(), "first", "about:blank", ext); err != nil {
		t.Fatal(err)
	}
	call("open", fixture.URL+"/next")
	if got := call("get", "text", "#cookie")["text"]; got != "kept" {
		t.Fatalf("profile lost cookie across restart: %v", got)
	}
}

func TestBrowserDriverFreezesModeAndBlocksTakeoverAndWrongScope(t *testing.T) {
	h := extensionTestHost(t)
	initial, _ := h.Store.GetBot(t.Context(), "first")
	initial.BrowserBackend = ""
	if err := h.Store.SaveBot(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	conversation, err := h.Store.CreateConversation(t.Context(), "first", "Browser", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.AttachRun(t.Context(), conversation.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	h.Browser.active["first"] = "run-1"
	call := func(runID, session, action string, command []string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]any{"app_id": AppID, "run_id": runID, "session": session, "action": action, "command": command})
		req := httptest.NewRequest("POST", "/runtime/browser/driver", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+h.cfg.InternalToken)
		out := httptest.NewRecorder()
		h.Handler().ServeHTTP(out, req)
		return out
	}
	session := browserSessionName("run-1")
	first := call("run-1", session, "resolve", nil)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "extension") {
		t.Fatalf("resolve: %d %s", first.Code, first.Body)
	}
	bot, _ := h.Store.GetBot(t.Context(), "first")
	bot.BrowserBackend = "standard"
	_ = h.Store.SaveBot(t.Context(), bot)
	if next := call("run-1", session, "resolve", nil); !strings.Contains(next.Body.String(), "extension") {
		t.Fatal("changed an existing run's driver")
	}
	if out := call("run-1", "wrong-session", "resolve", nil); out.Code != 403 {
		t.Fatalf("wrong session: %d", out.Code)
	}
	if out := call("unknown", browserSessionName("unknown"), "resolve", nil); out.Code != 403 {
		t.Fatalf("unknown run: %d", out.Code)
	}
	h.Browser.held["first"] = heldBrowser{RunID: "run-1"}
	if out := call("run-1", session, "command", []string{"click", "@e1"}); out.Code != 409 {
		t.Fatalf("action during takeover: %d %s", out.Code, out.Body)
	}
	delete(h.Browser.held, "first")
	if out := call("run-1", session, "command", []string{"press", "Enter"}); out.Code != 400 {
		t.Fatalf("unsupported action: %d", out.Code)
	}
}

func TestBrowserModeChangeRequiresGivingUpCurrentControl(t *testing.T) {
	h := extensionTestHost(t)
	bot, _ := h.Store.GetBot(t.Context(), "first")
	bot.BrowserBackend = "standard"
	h.Browser.held[bot.ID] = heldBrowser{Extension: true}
	if err := h.SaveBot(t.Context(), bot); err == nil || !strings.Contains(err.Error(), "hand back") {
		t.Fatalf("mode changed during takeover: %v", err)
	}
	delete(h.Browser.held, bot.ID)
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"run-1","status":"running"}`))
	}))
	defer runtime.Close()
	h.Runtime = NewRuntimeClient(runtime.URL, "")
	h.Browser.active[bot.ID] = "run-1"
	if err := h.SaveBot(t.Context(), bot); err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("mode changed during task: %v", err)
	}
	saved, _ := h.Store.GetBot(t.Context(), bot.ID)
	if saved.BrowserBackend != "extension" {
		t.Fatal("blocked save mutated mode")
	}
}

func TestCancelledDeliveredActionKeepsTakeoverGateUntilDeadline(t *testing.T) {
	e := &extensionBrowser{epoch: "epoch", queue: make(chan *extensionCommand), pending: map[string]*extensionCommand{}}
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	completed := make(chan error, 1)
	go func() { _, err := e.execute(ctx, "run", []string{"click", "@e1"}); completed <- err }()
	<-e.queue
	cancel()
	select {
	case <-completed:
		t.Fatal("delivered action abandoned before its deadline")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case err := <-completed:
		if err == nil {
			t.Fatal("cancelled action succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not release the action")
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
