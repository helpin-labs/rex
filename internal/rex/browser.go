package rex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BrowserControl gives each bot a persistent browser profile and lets the
// user take over that browser, for example to sign in.
//
// The runtime's browser tools drive agent-browser with one session per run,
// named from the app and run IDs. Before a run of a browser bot starts or
// resumes, Rex launches that session itself with the bot's profile folder,
// so the runtime reuses a browser that keeps the bot's logins. Takeover is
// only possible while the bot's run is paused, so the bot cannot act while
// the user holds the browser.
type BrowserControl struct {
	host *Host
	mu   sync.Mutex
	held map[string]heldBrowser
	// active maps bot ID to the run whose session uses the bot's profile.
	active map[string]string
	// prepared counts Prepare calls per bot, so closing an idle browser can
	// tell that the bot has started working again in the meantime.
	prepared map[string]int
	// open lists sessions Rex launched and has not closed; ensuring guards
	// one launch per session at a time.
	open     map[string]bool
	ensuring map[string]*sync.Mutex
	// OpenEagerly opens the browser when a run starts, for when Rex's
	// agent-browser wrapper is unavailable.
	OpenEagerly bool
}

// browserIdleTimeout is how long a bot's browser stays open after its run
// stops working. Busy sites keep using CPU and memory in the background, so
// an idle browser is closed and reopened on the same page when needed.
const browserIdleTimeout = 2 * time.Minute

type heldBrowser struct {
	RunID string
	URL   string
	Since time.Time
	// InPlace means the user is using the bot's own visible window.
	InPlace bool
}

func NewBrowserControl(host *Host) *BrowserControl {
	return &BrowserControl{host: host, held: map[string]heldBrowser{}, active: map[string]string{}, prepared: map[string]int{}, open: map[string]bool{}, ensuring: map[string]*sync.Mutex{}}
}

func browserSessionName(runID string) string {
	sum := sha256.Sum256([]byte(AppID + "/" + runID))
	return "ar-" + hex.EncodeToString(sum[:])[:20]
}

func takeoverSessionName(botID string) string { return "rex-takeover-" + Slug(botID) }

func (b *BrowserControl) profileDir(botID string) string {
	return filepath.Join(b.host.cfg.DataDir, "browser-profiles", Slug(botID))
}

// BrowserApp is an installed browser bots can drive.
type BrowserApp struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DetectBrowsers lists installed Chromium-based browsers, in preference
// order. Safari and Firefox cannot be automated this way.
func DetectBrowsers() []BrowserApp {
	home, _ := os.UserHomeDir()
	var found []BrowserApp
	for _, app := range []BrowserApp{
		{"Google Chrome", "Google Chrome.app/Contents/MacOS/Google Chrome"},
		{"Brave", "Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"Microsoft Edge", "Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		{"Chromium", "Chromium.app/Contents/MacOS/Chromium"},
		{"Vivaldi", "Vivaldi.app/Contents/MacOS/Vivaldi"},
	} {
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			path := filepath.Join(root, app.Path)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				found = append(found, BrowserApp{Name: app.Name, Path: path})
				break
			}
		}
	}
	return found
}

// BrowserExecutable is the browser bots use: the chosen one if it still
// exists, else the first installed one, else agent-browser's bundled Chrome
// for Testing ("").
func BrowserExecutable(settings Settings) string {
	if settings.BrowserPath != "" {
		if _, err := os.Stat(settings.BrowserPath); err == nil {
			return settings.BrowserPath
		}
	}
	if found := DetectBrowsers(); len(found) > 0 {
		return found[0].Path
	}
	return ""
}

// run executes agent-browser with the browser bots are configured to use.
func (b *BrowserControl) run(ctx context.Context, args ...string) (string, error) {
	var env []string
	if settings, err := b.host.Settings.Get(ctx); err == nil {
		if executable := BrowserExecutable(settings); executable != "" {
			env = append(env, "AGENT_BROWSER_EXECUTABLE_PATH="+executable)
		}
	}
	return agentBrowserEnv(ctx, env, args...)
}

func (b *BrowserControl) visible(ctx context.Context) bool {
	settings, err := b.host.Settings.Get(ctx)
	return err == nil && !settings.BrowserHidden
}

func agentBrowser(ctx context.Context, args ...string) (string, error) {
	return agentBrowserEnv(ctx, nil, args...)
}

// agentBrowserPath finds the agent-browser binary.
func agentBrowserPath() string {
	binary := firstNonEmpty(os.Getenv("AGENT_BROWSER_BINARY"), "agent-browser")
	if path, err := exec.LookPath(binary); err == nil {
		return path
	}
	if _, err := os.Stat("/opt/homebrew/bin/agent-browser"); err == nil {
		return "/opt/homebrew/bin/agent-browser"
	}
	return binary
}

func agentBrowserEnv(ctx context.Context, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, agentBrowserPath(), args...)
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("agent-browser %s: %w: %s", args[len(args)-1], err, strings.TrimSpace(excerpt(string(output), 300)))
	}
	return string(output), nil
}

func (b *BrowserControl) Held(botID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.held[botID]
	return ok
}

// Prepare assigns the bot's browser to a run. Nothing opens yet: the
// browser opens the first time the bot uses it (Ensure, called through
// Rex's agent-browser wrapper), so runs that never browse open no window.
func (b *BrowserControl) Prepare(ctx context.Context, bot Bot, runID string) {
	if !bot.Capabilities.Browser || runID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, held := b.held[bot.ID]; held {
		return
	}
	b.active[bot.ID] = runID
	b.prepared[bot.ID]++
	if b.OpenEagerly {
		b.mu.Unlock()
		b.Ensure(ctx, browserSessionName(runID))
		b.mu.Lock()
	}
}

// Ensure opens a run's browser session with its bot's profile before the
// bot's first browser command, on the page a restarted run was on. Unknown
// sessions are left to agent-browser.
func (b *BrowserControl) Ensure(ctx context.Context, session string) {
	b.mu.Lock()
	if b.open[session] {
		b.mu.Unlock()
		return
	}
	botID := ""
	for id, runID := range b.active {
		if browserSessionName(runID) == session {
			botID = id
		}
	}
	if b.ensuring[session] == nil {
		b.ensuring[session] = &sync.Mutex{}
	}
	lock := b.ensuring[session]
	b.mu.Unlock()
	if botID == "" {
		return
	}
	lock.Lock()
	defer lock.Unlock()
	b.mu.Lock()
	opened := b.open[session]
	b.mu.Unlock()
	if opened {
		return
	}
	// One Chrome process may own a profile at a time: close every other
	// session this bot has, including ones left over from before a restart.
	b.closeBotSessions(ctx, botID, session)
	if err := os.MkdirAll(b.profileDir(botID), 0o700); err != nil {
		slog.Warn("browser profile folder", "error", err)
		return
	}
	if b.sessionRunning(ctx, session) {
		b.markOpen(session, true)
		return
	}
	url := "about:blank"
	if saved, err := b.host.Store.GetSetting(ctx, resumeURLKey(session)); err == nil && saved != "" {
		url = saved
		_ = b.host.Store.SetSetting(ctx, resumeURLKey(session), "")
	}
	if err := b.launch(ctx, botID, session, "run", url, false); err != nil {
		slog.Warn("could not open the bot's browser", "bot", botID, "error", err)
	}
}

func (b *BrowserControl) markOpen(session string, open bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if open {
		b.open[session] = true
	} else {
		delete(b.open, session)
	}
}

// launch opens a session on the bot's profile and records it. If another
// process still holds the profile, it stops that Chrome and retries once.
func (b *BrowserControl) launch(ctx context.Context, botID, session, kind, url string, headed bool) error {
	args := []string{"--session", session, "--profile", b.profileDir(botID)}
	if headed || b.visible(ctx) {
		args = append(args, "--headed")
	}
	args = append(args, "open", url)
	_, err := b.run(ctx, args...)
	if err != nil && profileLocked(err) {
		slog.Warn("bot browser profile was locked; stopping the browser that holds it", "bot", botID)
		_, _ = agentBrowser(ctx, "--session", session, "close")
		stopProfileHolders(b.profileDir(botID))
		_, err = b.run(ctx, args...)
	}
	if err != nil {
		return err
	}
	b.markOpen(session, true)
	_ = b.host.Store.RecordBrowserSession(ctx, session, botID, kind)
	return nil
}

func profileLocked(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "exit code: 21") || strings.Contains(message, "devtoolsactiveport") || strings.Contains(message, "profile") && strings.Contains(message, "in use")
}

// stopProfileHolders terminates Chrome processes started with this exact
// profile folder. Only Rex's own bot profiles are ever passed here.
func stopProfileHolders(profile string) {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return
	}
	marker := "--user-data-dir=" + profile
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, marker) || strings.Contains(line, "--type=") {
			continue
		}
		if pid, _, ok := strings.Cut(line, " "); ok {
			_ = exec.Command("kill", pid).Run()
		}
	}
	time.Sleep(1500 * time.Millisecond)
	_ = os.Remove(filepath.Join(profile, "SingletonLock"))
}

// closeBotSessions closes every recorded browser session of a bot except one.
func (b *BrowserControl) closeBotSessions(ctx context.Context, botID, except string) {
	sessions, err := b.host.Store.BrowserSessions(ctx, botID)
	if err != nil {
		return
	}
	for _, session := range sessions {
		if session == except {
			continue
		}
		_, _ = agentBrowser(ctx, "--session", session, "close")
		_ = b.host.Store.DeleteBrowserSession(ctx, session)
		b.markOpen(session, false)
	}
}

// CloseAll closes every bot browser Rex opened. It runs at startup (to
// clear sessions left by a previous process) and at shutdown.
func (b *BrowserControl) CloseAll(ctx context.Context) {
	sessions, err := b.host.Store.BrowserSessions(ctx, "")
	if err != nil {
		return
	}
	for _, session := range sessions {
		if url := currentURL(ctx, session); url != "" {
			_ = b.host.Store.SetSetting(ctx, resumeURLKey(session), url)
		}
		_, _ = agentBrowser(ctx, "--session", session, "close")
		_ = b.host.Store.DeleteBrowserSession(ctx, session)
	}
	b.mu.Lock()
	b.held = map[string]heldBrowser{}
	b.active = map[string]string{}
	b.open = map[string]bool{}
	b.mu.Unlock()
}

// resumeURLKey stores the page a session was on when Rex closed it.
func resumeURLKey(session string) string { return "browser.resume_url." + session }

// lastURL is the page a run's browser shows, or showed when it was closed.
func (b *BrowserControl) lastURL(ctx context.Context, session string) string {
	if url := currentURL(ctx, session); url != "" {
		return url
	}
	saved, _ := b.host.Store.GetSetting(ctx, resumeURLKey(session))
	return saved
}

// CloseWhenIdle closes the bot's browser once its run has stayed idle for
// browserIdleTimeout. It stays open while the bot works again, waits for
// the user, or the user holds it. The next Prepare reopens the same page.
func (b *BrowserControl) CloseWhenIdle(botID, runID string) {
	b.mu.Lock()
	generation := b.prepared[botID]
	b.mu.Unlock()
	time.AfterFunc(browserIdleTimeout, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		b.mu.Lock()
		_, held := b.held[botID]
		current := b.prepared[botID] == generation && b.active[botID] == runID
		b.mu.Unlock()
		if held || !current {
			return
		}
		if run, err := b.host.Runtime.GetRun(ctx, runID); err != nil || run.Status == "running" || run.Status == "queued" {
			return
		}
		interactions, err := b.host.Runtime.ListInteractions(ctx, runID)
		if err != nil {
			return
		}
		for _, interaction := range interactions {
			if interaction.Status == "pending" {
				return
			}
		}
		session := browserSessionName(runID)
		if !b.sessionRunning(ctx, session) {
			return
		}
		if url := currentURL(ctx, session); url != "" {
			_ = b.host.Store.SetSetting(ctx, resumeURLKey(session), url)
		}
		_, _ = agentBrowser(ctx, "--session", session, "close")
		_ = b.host.Store.DeleteBrowserSession(ctx, session)
		b.markOpen(session, false)
		slog.Info("closed an idle bot browser", "bot", botID)
	})
}

func (b *BrowserControl) sessionRunning(ctx context.Context, name string) bool {
	output, err := agentBrowser(ctx, "session", "list")
	return err == nil && strings.Contains(output, name)
}

func currentURL(ctx context.Context, session string) string {
	output, err := agentBrowser(ctx, "--session", session, "get", "url")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(output)
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	return ""
}

// TakeOver opens the bot's browser in a visible window for the user.
func (b *BrowserControl) TakeOver(ctx context.Context, botID string) (map[string]any, error) {
	bot, err := b.host.Store.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	if !bot.Capabilities.Browser {
		return nil, errors.New("this bot has no browser")
	}
	b.mu.Lock()
	runID := b.active[botID]
	b.mu.Unlock()
	if runID != "" {
		if run, err := b.host.Runtime.GetRun(ctx, runID); err == nil && (run.Status == "running" || run.Status == "queued") {
			return nil, errors.New("the bot is using its browser right now. Wait until it asks for help, or stop it first")
		}
	}
	url := ""
	if runID != "" {
		url = b.lastURL(ctx, browserSessionName(runID))
	}
	// With visible bot browsers the user simply uses the bot's own window:
	// the bot is paused, so nothing else acts in it until it is handed back.
	if runID != "" && b.visible(ctx) && b.sessionRunning(ctx, browserSessionName(runID)) {
		b.mu.Lock()
		b.held[botID] = heldBrowser{RunID: runID, URL: url, Since: time.Now(), InPlace: true}
		b.mu.Unlock()
		b.bringToFront(ctx)
		return map[string]any{"held": true, "url": url, "message": "The bot's browser window is in front. Sign in or finish the step there, then hand it back."}, nil
	}
	if url == "" {
		url = "about:blank"
	}
	b.closeBotSessions(ctx, botID, "")
	if err := os.MkdirAll(b.profileDir(botID), 0o700); err != nil {
		return nil, err
	}
	if err := b.launch(ctx, botID, takeoverSessionName(botID), "takeover", url, true); err != nil {
		return nil, fmt.Errorf("could not open the browser window: %w", err)
	}
	b.mu.Lock()
	b.held[botID] = heldBrowser{RunID: runID, URL: url, Since: time.Now()}
	b.mu.Unlock()
	return map[string]any{"held": true, "url": url, "message": "The bot's browser is open in a new window. Sign in or finish the step, then hand it back."}, nil
}

// Release closes the user's window and gives the bot its browser back at
// the page the user left it on.
func (b *BrowserControl) Release(ctx context.Context, botID string) error {
	b.mu.Lock()
	held, ok := b.held[botID]
	b.mu.Unlock()
	if !ok {
		return nil
	}
	if held.InPlace {
		b.mu.Lock()
		delete(b.held, botID)
		b.mu.Unlock()
		return nil
	}
	url := currentURL(ctx, takeoverSessionName(botID))
	if url == "" {
		url = held.URL
	}
	_, _ = agentBrowser(ctx, "--session", takeoverSessionName(botID), "close")
	_ = b.host.Store.DeleteBrowserSession(ctx, takeoverSessionName(botID))
	b.mu.Lock()
	delete(b.held, botID)
	b.mu.Unlock()
	if held.RunID != "" && url != "" && url != "about:blank" {
		if err := b.launch(ctx, botID, browserSessionName(held.RunID), "run", url, false); err != nil {
			slog.Warn("could not reopen the bot's browser", "bot", botID, "error", err)
		}
	}
	return nil
}

// bringToFront activates the browser app so the bot's window is visible.
func (b *BrowserControl) bringToFront(ctx context.Context) {
	settings, err := b.host.Settings.Get(ctx)
	if err != nil {
		return
	}
	executable := BrowserExecutable(settings)
	app := executable
	if index := strings.Index(executable, ".app/"); index > 0 {
		app = executable[:index+4]
	}
	if app != "" {
		_ = exec.CommandContext(ctx, "osascript", "-e", fmt.Sprintf("tell application %q to activate", app)).Run()
	}
}

// saveArtifact receives browser screenshots from the runtime and stores
// them in the bot's workspace under outputs/screenshots.
func (h *Host) saveArtifact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runID := r.FormValue("run_id")
	conversation, err := h.Store.ConversationForRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("unknown run"))
		return
	}
	bot, err := h.Store.GetBot(r.Context(), conversation.BotID)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("unknown bot"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defer file.Close()
	dir := filepath.Join(bot.WorkspaceDir, "outputs", "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name := time.Now().Format("2006-01-02-150405") + "-" + filepath.Base(header.Filename)
	target := filepath.Join(dir, name)
	out, err := os.Create(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	relative, _ := filepath.Rel(bot.WorkspaceDir, target)
	var metadata map[string]any
	_ = json.Unmarshal([]byte(r.FormValue("metadata")), &metadata)
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": "rex-" + randomHex(8), "artifact_ref": filepath.ToSlash(relative), "visibility": "private",
		"file_name": name, "content_type": header.Header.Get("Content-Type"),
	})
}

// LiveView describes how to watch a bot's browser: agent-browser streams
// each session's screen over a loopback WebSocket.
type LiveView struct {
	Active  bool   `json:"active"`
	Session string `json:"session,omitempty"`
	URL     string `json:"ws_url,omitempty"`
	Held    bool   `json:"held"`
}

// Live returns the stream of the bot's current browser session, preferring
// the user's takeover window, then the newest run session.
func (b *BrowserControl) Live(ctx context.Context, botID string) (LiveView, error) {
	view := LiveView{Held: b.Held(botID)}
	sessions, err := b.host.Store.BrowserSessions(ctx, botID)
	if err != nil {
		return view, err
	}
	if view.Held {
		sessions = append([]string{takeoverSessionName(botID)}, sessions...)
	}
	for _, session := range sessions {
		if !b.sessionRunning(ctx, session) {
			continue
		}
		output, err := agentBrowser(ctx, "--session", session, "stream", "status", "--json")
		if err != nil {
			continue
		}
		var status struct {
			Data struct {
				Enabled bool `json:"enabled"`
				Port    int  `json:"port"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(output), &status) != nil {
			continue
		}
		if !status.Data.Enabled || status.Data.Port == 0 {
			if output, err = agentBrowser(ctx, "--session", session, "stream", "enable", "--json"); err != nil || json.Unmarshal([]byte(output), &status) != nil || status.Data.Port == 0 {
				continue
			}
		}
		view.Active, view.Session, view.URL = true, session, fmt.Sprintf("ws://127.0.0.1:%d", status.Data.Port)
		return view, nil
	}
	return view, nil
}

// --- store ---

func (s *Store) RecordBrowserSession(ctx context.Context, session, botID, kind string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO browser_sessions(session,bot_id,kind,created_at) VALUES(?,?,?,?) ON CONFLICT(session) DO UPDATE SET bot_id=excluded.bot_id, kind=excluded.kind`, session, botID, kind, time.Now().UTC())
	return err
}

func (s *Store) DeleteBrowserSession(ctx context.Context, session string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM browser_sessions WHERE session=?`, session)
	return err
}

// BrowserSessions lists recorded sessions for a bot, or all when botID is "".
func (s *Store) BrowserSessions(ctx context.Context, botID string) ([]string, error) {
	if botID == "" {
		return s.strings(ctx, `SELECT session FROM browser_sessions`)
	}
	return s.strings(ctx, `SELECT session FROM browser_sessions WHERE bot_id=? ORDER BY created_at DESC`, botID)
}
