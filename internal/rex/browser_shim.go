package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The agent runtime drives each bot's browser with agent-browser. Rex
// points the runtime at itself, linked under the name agent-browser: before
// handing a command to the real agent-browser it asks the host to open that
// session's browser with the bot's profile, so a browser opens only when a
// bot actually uses it.

// BrowserShimName is the name rex-host answers to as the wrapper.
const BrowserShimName = "agent-browser"

// shimConfig sits next to the wrapper link (the runtime gives agent-browser
// a sanitized environment, so settings cannot travel as variables).
type shimConfig struct {
	EnsureURL   string `json:"ensure_url"`
	Token       string `json:"token"`
	RealBrowser string `json:"agent_browser"`
}

const shimConfigName = "agent-browser.json"

// agent-browser flags that take a value, so the command is found correctly.
var browserValueFlags = map[string]bool{"--session": true, "--profile": true, "--max-output": true, "--executable-path": true,
	"--proxy": true, "--user-agent": true, "--timeout": true, "--cdp": true, "--headers": true, "--args": true}

// browserCommand returns the session and the command (open, snapshot…).
func browserCommand(args []string) (session, command string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if name, value, ok := strings.Cut(arg, "="); ok && browserValueFlags[name] {
			if name == "--session" {
				session = value
			}
			continue
		}
		if browserValueFlags[arg] {
			if arg == "--session" && i+1 < len(args) {
				session = args[i+1]
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if command == "" {
			command = arg
		}
	}
	return session, command
}

// RunBrowserShim opens the session's browser through the host if needed,
// then replaces itself with the real agent-browser.
func RunBrowserShim(args []string) error {
	var config shimConfig
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), shimConfigName)); err == nil {
		_ = json.Unmarshal(raw, &config)
	}
	session, command := browserCommand(args)
	if session != "" && config.EnsureURL != "" && command != "close" && command != "session" && command != "stream" {
		payload, _ := json.Marshal(map[string]string{"session": session})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.EnsureURL, bytes.NewReader(payload))
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+config.Token)
			request.Header.Set("Content-Type", "application/json")
			if response, err := http.DefaultClient.Do(request); err == nil {
				response.Body.Close()
			}
		}
		cancel()
	}
	real := config.RealBrowser
	if real == "" {
		real = agentBrowserPath()
	}
	// A session the host did not open is launched by agent-browser itself;
	// it should not say it is automated either.
	env := os.Environ()
	if os.Getenv("AGENT_BROWSER_ARGS") == "" {
		env = append(env, "AGENT_BROWSER_ARGS="+hideAutomation)
	}
	return syscall.Exec(real, append([]string{real}, args...), env)
}

// BrowserShimEnv links rex-host as agent-browser in the data folder, writes
// its settings next to it (readable only by the user) and returns the
// runtime environment that routes browser commands through it.
func BrowserShimEnv(dataDir, hostURL, token string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	link := filepath.Join(dir, BrowserShimName)
	_ = os.Remove(link)
	if err := os.Symlink(self, link); err != nil {
		return nil, err
	}
	config, _ := json.Marshal(shimConfig{EnsureURL: hostURL + "/runtime/browser/ensure", Token: token, RealBrowser: agentBrowserPath()})
	if err := os.WriteFile(filepath.Join(dir, shimConfigName), config, 0o600); err != nil {
		return nil, err
	}
	return []string{"AGENT_BROWSER_BINARY=" + link}, nil
}

// ensureBrowser answers the wrapper.
func (h *Host) ensureBrowser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Session string `json:"session"`
	}
	if !decode(w, r, &input) {
		return
	}
	h.Browser.Ensure(r.Context(), input.Session)
	w.WriteHeader(http.StatusNoContent)
}
