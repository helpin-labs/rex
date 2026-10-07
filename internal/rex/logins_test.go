package rex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTwoStepCodesMatchRFC6238(t *testing.T) {
	// RFC 6238 test secret "12345678901234567890", SHA1, 8 digits.
	key, err := parseTwoStepKey("otpauth://totp/Test?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&digits=8")
	if err != nil {
		t.Fatal(err)
	}
	for at, want := range map[int64]string{59: "94287082", 1111111109: "07081804", 1234567890: "89005924", 2000000000: "69279037"} {
		if got := key.code(time.Unix(at, 0)); got != want {
			t.Fatalf("code at %d = %s, want %s", at, got, want)
		}
	}
	if _, err := parseTwoStepKey("not a key!"); err == nil {
		t.Fatal("invalid setup keys are refused")
	}
}

func TestLoginsKeepPasswordsInTheKeychain(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{Name: "Trips", Capabilities: Capabilities{Browser: true, Web: true}}
	_ = host.SaveBot(ctx, bot)
	other := &Bot{Name: "Other", Capabilities: Capabilities{Browser: true}}
	_ = host.SaveBot(ctx, other)

	login, err := host.SaveLogin(ctx, bot.ID, "", LoginInput{Site: "https://www.momondo.se/flights", Username: "amad@example.com", Password: "s3cret-pass", TwoStepKey: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"})
	if err != nil {
		t.Fatal(err)
	}
	if login.Site != "momondo.se" || !login.HasTwoStep {
		t.Fatalf("the site is the registrable domain and 2FA is noted: %+v", login)
	}
	listed, _ := host.Store.BotLogins(ctx, bot.ID)
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), "s3cret-pass") || strings.Contains(string(raw), "GEZDGNBV") {
		t.Fatalf("secrets never leave the Keychain: %s", raw)
	}
	// Updating without a password keeps the stored one.
	if _, err := host.SaveLogin(ctx, bot.ID, login.ID, LoginInput{Site: "momondo.se", Username: "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, secrets, _ := host.loginFor(ctx, bot.ID, login.ID); secrets.Password != "s3cret-pass" {
		t.Fatal("the password is kept when not replaced")
	}
	if _, _, err := host.loginFor(ctx, other.ID, login.ID); err == nil {
		t.Fatal("another bot cannot use this login")
	}
	if got := redactSecrets("my momondo password is s3cret-pass"); strings.Contains(got, "s3cret-pass") {
		t.Fatalf("stored passwords are redacted from kept text: %q", got)
	}
	if servers, _ := host.runMCPServers(ctx, *bot); servers[len(servers)-1].ServerName != "logins" {
		t.Fatalf("browser bots get the login tools: %+v", servers)
	}
	if err := host.DeleteLogin(ctx, bot.ID, login.ID); err != nil {
		t.Fatal(err)
	}
	if raw, _ := host.Settings.secrets.Get(loginSecretKey(login.ID)); raw != "" {
		t.Fatal("deleting a login removes it from the Keychain")
	}
}

func TestSignInOnlyTypesOnTheLoginsSite(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{Name: "Trips", Capabilities: Capabilities{Browser: true}}
	_ = host.SaveBot(ctx, bot)
	login, _ := host.SaveLogin(ctx, bot.ID, "", LoginInput{Site: "momondo.se", Username: "amad@example.com", Password: "s3cret-pass"})
	host.Browser.active[bot.ID] = "run-1"

	page, field := "https://www.momondo.se/login", "password"
	var typed [][]string
	oldURL, oldField, oldBatch := browserPageURL, browserFieldType, browserBatch
	defer func() { browserPageURL, browserFieldType, browserBatch = oldURL, oldField, oldBatch }()
	browserPageURL = func(context.Context, string) string { return page }
	browserFieldType = func(context.Context, string, string) string { return field }
	browserBatch = func(_ context.Context, _ string, commands [][]string) error { typed = commands; return nil }

	for _, bad := range []string{"https://momondo-login.xyz/login", "https://momondo.se.evil.com/login", "http://www.momondo.se/login", ""} {
		page, typed = bad, nil
		if _, err := host.SignIn(ctx, bot.ID, login.ID, "@e1", "@e2", ""); err == nil || typed != nil {
			t.Fatalf("nothing is typed on %q", bad)
		}
	}
	page, field = "https://accounts.momondo.se/signin", "text"
	if _, err := host.SignIn(ctx, bot.ID, login.ID, "@e1", "@e2", ""); err == nil {
		t.Fatal("the password goes only into a password field")
	}
	field = "password"
	if _, err := host.SignIn(ctx, bot.ID, login.ID, "@e1", "#password", ""); err == nil {
		t.Fatal("only snapshot refs are accepted")
	}
	used, err := host.SignIn(ctx, bot.ID, login.ID, "@e1", "@e2", "@e3")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"fill", "@e1", "amad@example.com"}, {"fill", "@e2", "s3cret-pass"}, {"click", "@e3"}}
	if len(typed) != 3 || strings.Join(typed[1], " ") != strings.Join(want[1], " ") || strings.Join(typed[2], " ") != strings.Join(want[2], " ") {
		t.Fatalf("Rex types the username and password and submits: %v", typed)
	}
	if used.LastUsedAt == nil {
		t.Fatal("using a login records when")
	}
	host.Browser.held[bot.ID] = heldBrowser{RunID: "run-1"}
	if _, err := host.SignIn(ctx, bot.ID, login.ID, "@e1", "@e2", ""); err == nil {
		t.Fatal("no signing in while the user holds the browser")
	}
}
