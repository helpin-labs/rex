package rex

import "testing"

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
