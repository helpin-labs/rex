package rex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
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
	// Plain is the browser the user is using, started without automation;
	// exited closes when it quits.
	Plain *plainBrowser
}

// hideAutomation keeps Chrome from setting navigator.webdriver in the bots'
// browsers. Sites such as Cloudflare's human check refuse browsers that
// say they are automated.
const hideAutomation = "--disable-blink-features=AutomationControlled"

func NewBrowserControl(host *Host) *BrowserControl {
	return &BrowserControl{host: host, held: map[string]heldBrowser{}, active: map[string]string{}, prepared: map[string]int{}, open: map[string]bool{}, ensuring: map[string]*sync.Mutex{}}
}

func browserSessionName(runID string) string {
	sum := sha256.Sum256([]byte(AppID + "/" + runID))
	return "ar-" + hex.EncodeToString(sum[:])[:20]
}

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
	// Cloud browsers are created by the runtime in Kernel; there is no
	// local profile to open.
	if botID == "" || b.cloud(ctx) {
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
	args := []string{"--session", session, "--profile", b.profileDir(botID), "--args", hideAutomation}
	if headed || b.visible(ctx) {
		args = append(args, "--headed")
	} else if agent := b.userAgent(ctx); agent != "" {
		args = append(args, "--user-agent", agent)
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
	held := b.held
	b.mu.Unlock()
	for botID, browser := range held {
		b.closePlain(browser, b.profileDir(botID))
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

// cloud reports whether bots browse in Kernel's cloud instead of a local
// browser with their own profile.
func (b *BrowserControl) cloud(ctx context.Context) bool {
	settings, err := b.host.Settings.Get(ctx)
	return err == nil && settings.CloudBrowsers()
}

// TakeOver closes the bot's automated browser and opens its profile in a
// plain window for the user, so checks for automated browsers pass and what
// the user signs in to is saved in the bot's profile.
func (b *BrowserControl) TakeOver(ctx context.Context, botID string) (map[string]any, error) {
	if b.Held(botID) {
		return map[string]any{"held": true, "message": "You already have the bot's browser. Hand it back when you're done."}, nil
	}
	if b.cloud(ctx) {
		return nil, errors.New("bots use cloud browsers (Kernel), which can't be taken over. Turn cloud browsers off in Settings → General to use your Mac's browser")
	}
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
	b.closeBotSessions(ctx, botID, "")
	profile := b.profileDir(botID)
	if url == "" {
		url = firstNonEmpty(withoutChallenge(lastVisited(profile)), "about:blank")
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err == nil {
		stopProfileHolders(profile)
	}
	clearChallengeCookies(profile)
	held := heldBrowser{RunID: runID, URL: url, Since: time.Now()}
	command, err := b.openPlain(ctx, profile, url)
	if err != nil {
		return nil, fmt.Errorf("could not open the browser window: %w", err)
	}
	held.Plain = command
	b.mu.Lock()
	b.held[botID] = held
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
	url := b.closePlain(held, b.profileDir(botID))
	if url == "" {
		url = held.URL
	}
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

// openPlain starts the bots' browser on a bot's profile the way a person
// would: no automation flags and no DevTools client attached, so sites that
// check for automated browsers (Cloudflare's human check, some sign-ins)
// treat it as the user's own. The keychain flags match agent-browser's, so
// both read the same saved cookies.
func (b *BrowserControl) openPlain(ctx context.Context, profile, url string) (*plainBrowser, error) {
	settings, err := b.host.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	executable := firstNonEmpty(BrowserExecutable(settings), bundledChrome())
	if executable == "" {
		return nil, errors.New("no browser found. Install Google Chrome or another Chromium browser")
	}
	return startPlainBrowser(executable, plainBrowserArgs(profile, url))
}

// plainBrowserArgs keeps a DevTools pipe (file descriptors 3 and 4) that
// nothing uses while the user browses; Rex uses it only to read the open
// page and quit cleanly, which saves the cookies. Chrome marks a browser
// with a DevTools pipe as automated (navigator.webdriver), so that is
// turned off too.
func plainBrowserArgs(profile, url string) []string {
	return []string{"--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check",
		"--password-store=basic", "--use-mock-keychain", "--remote-debugging-pipe", hideAutomation, "--new-window", url}
}

// bundledChrome is the newest Chrome for Testing agent-browser installed,
// which it uses when no other browser is chosen.
func bundledChrome() string {
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".agent-browser", "browsers", "chrome-*", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"))
	newest := ""
	for _, match := range matches {
		if newest == "" || compareVersions(versionOf(match), versionOf(newest)) > 0 {
			newest = match
		}
	}
	return newest
}

func versionOf(path string) string {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if version, ok := strings.CutPrefix(part, "chrome-"); ok {
			return version
		}
	}
	return ""
}

// compareVersions compares dotted version numbers.
func compareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) || i < len(right); i++ {
		var x, y int
		if i < len(left) {
			fmt.Sscan(left[i], &x)
		}
		if i < len(right) {
			fmt.Sscan(right[i], &y)
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// closePlain quits the user's window cleanly, so the browser saves its
// cookies, and returns the page the user was on.
func (b *BrowserControl) closePlain(held heldBrowser, profile string) string {
	if held.Plain == nil {
		return ""
	}
	url := held.Plain.close(10 * time.Second)
	if !held.Plain.waitExit(0) {
		stopProfileHolders(profile)
	}
	if url == "" {
		url = lastVisited(profile)
	}
	return url
}

// clearChallengeCookies removes Cloudflare's challenge cookies from a closed
// profile. A clearance is tied to the browser that earned it; sent by a
// different one (the user's plain window, or the bot's browser after a
// change) it makes Cloudflare challenge again and again. Without it the
// user gets one fresh check, and the clearance they earn is kept for the
// bot.
func clearChallengeCookies(profile string) {
	for _, path := range []string{filepath.Join(profile, "Default", "Network", "Cookies"), filepath.Join(profile, "Default", "Cookies")} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(2000)")
		if err != nil {
			continue
		}
		if _, err := db.Exec(`DELETE FROM cookies WHERE name IN ('cf_clearance','__cf_bm') OR name LIKE 'cf\_chl%' ESCAPE '\'`); err != nil {
			slog.Warn("could not clear challenge cookies", "error", err)
		}
		db.Close()
	}
}

// userAgent is what the bots' browser says when it shows a window. A
// hidden browser would otherwise say HeadlessChrome, which sites refuse,
// and a clearance the user earned in a window would not match it.
func (b *BrowserControl) userAgent(ctx context.Context) string {
	settings, err := b.host.Settings.Get(ctx)
	if err != nil {
		return ""
	}
	return chromeUserAgent(firstNonEmpty(BrowserExecutable(settings), bundledChrome()))
}

func chromeUserAgent(executable string) string {
	index := strings.Index(executable, ".app/")
	if index < 0 {
		return ""
	}
	output, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print CFBundleShortVersionString", filepath.Join(executable[:index+4], "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	return userAgentFor(strings.TrimSpace(string(output)))
}

// userAgentFor builds Chrome's reduced user agent from a Chromium-based
// browser's version, whose first number is Chromium's major version.
func userAgentFor(version string) string {
	major, _, _ := strings.Cut(version, ".")
	if major == "" || strings.Trim(major, "0123456789") != "" {
		return ""
	}
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
}

// withoutChallenge removes Cloudflare's challenge parameters from a page
// address, so reopening it starts a fresh check.
func withoutChallenge(address string) string {
	parsed, err := neturl.Parse(address)
	if err != nil || parsed.RawQuery == "" {
		return address
	}
	query := parsed.Query()
	for key := range query {
		if strings.HasPrefix(key, "__cf_chl_") {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// lastVisited reads the most recent web page from a closed profile's
// history.
func lastVisited(profile string) string {
	path := filepath.Join(profile, "Default", "History")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var url string
	if db.QueryRow(`SELECT url FROM urls WHERE url LIKE 'http%' ORDER BY last_visit_time DESC LIMIT 1`).Scan(&url) != nil {
		return ""
	}
	return url
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
	// Rex does not open cloud browsers, so it has no record of them; the
	// current run's session is the one to watch.
	b.mu.Lock()
	if runID := b.active[botID]; runID != "" {
		sessions = append([]string{browserSessionName(runID)}, sessions...)
	}
	b.mu.Unlock()
	// The user's own window is not automated, so it cannot be watched.
	if view.Held {
		return view, nil
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
