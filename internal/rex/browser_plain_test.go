package rex

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestPlainBrowserIsNotAutomated(t *testing.T) {
	args := plainBrowserArgs("/profiles/bot", "https://example.com/")
	for _, flag := range args {
		if flag == "--enable-automation" || flag == "--headless" || len(flag) > 22 && flag[:22] == "--remote-debugging-por" {
			t.Fatalf("plain browser launched with %s", flag)
		}
	}
	// Cookies are encrypted with the same key agent-browser uses, so the
	// bot keeps what the user signed in to.
	for _, want := range []string{"--user-data-dir=/profiles/bot", hideAutomation, "--use-mock-keychain", "--password-store=basic", "https://example.com/"} {
		if !slices.Contains(args, want) {
			t.Fatalf("missing %s in %v", want, args)
		}
	}
}

func TestBundledChromeIsTheNewest(t *testing.T) {
	t.Setenv("REX_BROWSER_EXECUTABLE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, version := range []string{"148.0.7778.97", "149.0.7827.115", "99.0.1"} {
		path := filepath.Join(home, ".agent-browser", "browsers", "chrome-"+version, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := versionOf(bundledChrome()); got != "149.0.7827.115" {
		t.Fatalf("bundled Chrome = %s", got)
	}
}

func TestPackagedChromeHasPriorityOverDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	packaged := filepath.Join(home, "Rex.app", "Chrome")
	downloaded := filepath.Join(home, ".agent-browser", "browsers", "chrome-999.0.0", "chrome-win64", "chrome.exe")
	for _, path := range []string{packaged, downloaded} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("REX_BROWSER_EXECUTABLE", packaged)
	if got := bundledChrome(); got != packaged {
		t.Fatalf("packaged Chrome ignored: %q", got)
	}
	for _, missing := range []string{packaged + "-missing", home} {
		t.Setenv("REX_BROWSER_EXECUTABLE", missing)
		if got := bundledChrome(); got != downloaded {
			t.Fatalf("invalid packaged browser prevented fallback: %q", got)
		}
	}
	if got := BrowserExecutable(Settings{BrowserPath: packaged}); got != packaged {
		t.Fatalf("explicit browser selection ignored: %q", got)
	}
}

func TestLastVisitedReadsTheProfileHistory(t *testing.T) {
	profile := t.TempDir()
	if lastVisited(profile) != "" {
		t.Fatal("a profile without history has no last page")
	}
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(profile, "Default", "History"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE urls(id INTEGER PRIMARY KEY, url TEXT, last_visit_time INTEGER)`,
		`INSERT INTO urls(url,last_visit_time) VALUES('https://example.com/login',100),('https://example.com/done',300),('chrome://newtab/',400)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if got := lastVisited(profile); got != "https://example.com/done" {
		t.Fatalf("last visited = %q", got)
	}
}

func TestTakeoverClearsOnlyChallengeCookies(t *testing.T) {
	profile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, "Default", "Cookies")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE cookies(host_key TEXT, name TEXT)`,
		`INSERT INTO cookies VALUES('.hemnet.se','cf_clearance'),('www.hemnet.se','cf_chl_rc_ni'),('.hemnet.se','__cf_bm'),('.hemnet.se','_cfuvid'),('.hemnet.se','session'),('.example.com','cfchlx')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	clearChallengeCookies(profile)
	db, _ = sql.Open("sqlite", path)
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM cookies ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		kept = append(kept, name)
	}
	if !slices.Equal(kept, []string{"_cfuvid", "cfchlx", "session"}) {
		t.Fatalf("kept %v", kept)
	}
}

func TestHiddenBrowserSaysChrome(t *testing.T) {
	platform := "Macintosh; Intel Mac OS X 10_15_7"
	if runtime.GOOS == "windows" {
		platform = "Windows NT 10.0; Win64; x64"
	}
	if got := userAgentFor("154.1.96.60"); got != "Mozilla/5.0 ("+platform+") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36" {
		t.Fatalf("user agent = %s", got)
	}
	if userAgentFor("") != "" || userAgentFor("x.1") != "" {
		t.Fatal("unknown versions give no user agent")
	}
}

func TestReopenedPagesStartAFreshCheck(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.hemnet.se/?__cf_chl_rt_tk=abc":          "https://www.hemnet.se/",
		"https://www.hemnet.se/bostader?q=1&__cf_chl_f_tk=x": "https://www.hemnet.se/bostader?q=1",
		"https://example.com/a?b=c":                          "https://example.com/a?b=c",
		"":                                                   "",
	} {
		if got := withoutChallenge(in); got != want {
			t.Errorf("withoutChallenge(%q) = %q, want %q", in, got, want)
		}
	}
}
