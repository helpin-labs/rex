package rex

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestBrowserCommandFindsSessionAndCommand(t *testing.T) {
	for _, tc := range []struct {
		args             []string
		session, command string
	}{
		{[]string{"--session", "ar-1", "--json", "--content-boundaries", "--max-output", "20000", "open", "https://example.com"}, "ar-1", "open"},
		{[]string{"--session=ar-2", "snapshot", "-i"}, "ar-2", "snapshot"},
		{[]string{"--session", "ar-3", "close"}, "ar-3", "close"},
		{[]string{"session", "list"}, "", "session"},
	} {
		session, command := browserCommand(tc.args)
		if session != tc.session || command != tc.command {
			t.Fatalf("%v: got %q %q", tc.args, session, command)
		}
	}
}

func TestStandardShimRejectsExtensionSession(t *testing.T) {
	h := extensionTestHost(t)
	h.Browser.active["first"] = "run-extension"
	server := httptest.NewServer(h.Handler())
	defer server.Close()
	payload, _ := json.Marshal(map[string]string{"session": browserSessionName("run-extension")})
	if err := ensureBrowserSession(t.Context(), shimConfig{EnsureURL: server.URL + "/runtime/browser/ensure", Token: h.cfg.InternalToken}, payload); err == nil {
		t.Fatal("Standard shim allowed agent-browser for an Extension run")
	}
	if err := ensureBrowserSession(t.Context(), shimConfig{EnsureURL: server.URL + "/runtime/browser/ensure", Token: "wrong"}, payload); err == nil {
		t.Fatal("failed authorization bypassed host")
	}
}
