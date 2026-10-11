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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "cf_clearance", Value: "plain-ok", MaxAge: 86400, Path: "/", HttpOnly: true})
		}
		cookie, _ := r.Cookie("cf_clearance")
		value := ""
		if cookie != nil {
			value = cookie.Value
		}
		fmt.Fprintf(w, "<title>cookie=%s</title>", value)
	}))
	defer server.Close()
	profile := t.TempDir()

	browser, err := startPlainBrowser(executable, plainBrowserArgs(profile, server.URL+"/set"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
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
