package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsProfileMatchingIsExact(t *testing.T) {
	profile := `C:\Rex\bot with spaces`
	for _, test := range []struct {
		args []string
		want bool
	}{
		{[]string{"chrome.exe", "--user-data-dir=" + profile}, true},
		{[]string{"chrome.exe", "--user-data-dir", profile}, true},
		{[]string{"chrome.exe", "--user-data-dir=" + profile + "-other"}, false},
		{[]string{"chrome.exe", "--user-data-dir=" + profile, "--type=renderer"}, false},
		{[]string{"chrome.exe", "--user-data-dir=" + profile, "--user-data-dir=C:\\Other"}, false},
	} {
		if got := browserUsesProfile(test.args, profile); got != test.want {
			t.Fatalf("profile match %q: %v", test.args, got)
		}
	}
}

func TestWindowsProfileHolderChild(t *testing.T) {
	if os.Getenv("REX_TEST_PROFILE_HOLDER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestWindowsClosesOnlyExactProfileHolder(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "chrome.exe")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := linkBrowserShim(self, binary); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "bot with spaces")
	start := func(path string) *exec.Cmd {
		cmd := exec.Command(binary, "-test.run=^TestWindowsProfileHolderChild$", "--", "--user-data-dir="+path)
		cmd.Env = append(os.Environ(), "REX_TEST_PROFILE_HOLDER=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	owned, other := start(profile), start(profile+"-other")
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(owned.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	args, err := processArguments(handle)
	windows.CloseHandle(handle)
	if err != nil || !browserUsesProfile(args, profile) {
		t.Fatalf("native process query failed: %q %v", args, err)
	}
	stopProfileHolders(profile)
	for _, test := range []struct {
		cmd   *exec.Cmd
		alive bool
	}{{owned, false}, {other, true}} {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(test.cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		var status uint32
		err = windows.GetExitCodeProcess(handle, &status)
		windows.CloseHandle(handle)
		if err != nil || (status == 259) != test.alive {
			t.Fatalf("process %d status %d alive expected %v: %v", test.cmd.Process.Pid, status, test.alive, err)
		}
	}
}

func TestWindowsBrowserShimCanBeRecreatedWhileHostRuns(t *testing.T) {
	data := t.TempDir()
	for range 2 {
		if _, err := BrowserShimEnv(data, "http://127.0.0.1:1", "fixture"); err != nil {
			t.Fatalf("could not recreate shim: %v", err)
		}
	}
}
