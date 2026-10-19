package rex

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPlainBrowserHandsCookiesToTheBot runs a real browser: set
// REX_BROWSER_E2E to its executable. A cookie set in the user's plain window
// must reach the bot's automated browser on the same profile.
func TestPlainBrowserHandsCookiesToTheBot(t *testing.T) {
	executable := os.Getenv("REX_BROWSER_E2E")
	if executable == "" {
		t.Skip("set REX_BROWSER_E2E to a Chromium browser executable")
	}
	cookieReady := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			// Slow responses must finish before handing the browser profile back.
			time.Sleep(5 * time.Second)
			http.SetCookie(w, &http.Cookie{Name: "cf_clearance", Value: "plain-ok", MaxAge: 86400, Path: "/", HttpOnly: true})
		}
		cookie, _ := r.Cookie("cf_clearance")
		value := ""
		if cookie != nil {
			value = cookie.Value
		}
		fmt.Fprintf(w, "<title>cookie=%s</title>", value)
		if r.URL.Path == "/set" {
			// The follow-up request proves the browser processed the delayed cookie.
			fmt.Fprint(w, `<script>fetch('/ready')</script>`)
		}
		if r.URL.Path == "/ready" && value == "plain-ok" {
			select {
			case cookieReady <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()
	profile := t.TempDir()

	browser, err := startPlainBrowser(executable, plainBrowserArgs(profile, server.URL+"/set"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { browser.close(5 * time.Second) })
	select {
	case <-cookieReady:
	case <-time.After(30 * time.Second):
		t.Fatal("plain browser did not complete the login request with its cookie")
	}
	if url := browser.close(10 * time.Second); url != server.URL+"/set" {
		t.Fatalf("open page = %q", url)
	}
	if !browser.waitExit(0) {
		t.Fatal("the browser did not quit")
	}

	session := fmt.Sprintf("rex-e2e-%d", time.Now().UnixNano())
	env := []string{"AGENT_BROWSER_EXECUTABLE_PATH=" + executable}
	if _, err := agentBrowserEnv(t.Context(), env, "--session", session, "--profile", profile, "--args", hideAutomation, "open", server.URL+"/check"); err != nil {
		t.Fatal(err)
	}
	defer agentBrowser(t.Context(), "--session", session, "close")
	output, err := agentBrowser(t.Context(), "--session", session, "eval", `document.title + " webdriver=" + navigator.webdriver`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "cookie=plain-ok webdriver=false") {
		t.Fatalf("bot browser saw %s", output)
	}
}

// Verify the native pipe handshake without requiring the agent-browser CLI.
func TestPlainBrowserNativePipe(t *testing.T) {
	executable := os.Getenv("REX_BROWSER_PIPE_E2E")
	if executable == "" {
		t.Skip("set REX_BROWSER_PIPE_E2E to a Chromium executable")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<title>Rex pipe fixture</title>") }))
	defer fixture.Close()
	browser, err := startPlainBrowser(executable, plainBrowserArgs(t.TempDir(), fixture.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { browser.close(5 * time.Second) })
	if _, err := browser.call("Browser.getVersion", map[string]any{}, time.Now().Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for browser.currentURL(deadline) != fixture.URL+"/" {
		if time.Now().After(deadline) {
			t.Fatal("native pipe did not return the open page")
		}
		time.Sleep(100 * time.Millisecond)
	}
	browser.close(5 * time.Second)
	if !browser.waitExit(0) {
		t.Fatal("native browser survived close")
	}
}
