package rex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsJobKillsDescendantsOnHostDeath(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child.pid")
	root := exec.Command(os.Args[0], "-test.run=^TestWindowsJobHelper$")
	root.Env = append(os.Environ(), "REX_JOB_HELPER=root", "REX_JOB_MARKER="+marker)
	input, err := root.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); root.Process.Kill(); root.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(marker); err == nil {
			childPID, _ = strconv.Atoi(string(raw))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("job-owned child did not start")
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := root.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	root.Wait()
	result, err := windows.WaitForSingleObject(process, 10000)
	if err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("child survived host death: wait=%d err=%v", result, err)
	}
}

func TestWindowsJobHelper(t *testing.T) {
	mode := os.Getenv("REX_JOB_HELPER")
	if mode == "" {
		return
	}
	if mode == "root" {
		if err := OwnProcessTree(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsJobHelper$")
		child.Env = append(os.Environ(), "REX_JOB_HELPER=leaf")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		var b [1]byte
		os.Stdin.Read(b[:])
		os.Exit(0)
	}
	if err := os.WriteFile(os.Getenv("REX_JOB_MARKER"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(4)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestWindowsCredentialManagerLargeToken(t *testing.T) {
	store := KeychainSecrets{Service: "ai.rex.desktop.windows-test." + strconv.FormatInt(time.Now().UnixNano(), 10)}
	t.Cleanup(func() { store.Delete("chatgpt") })
	for _, value := range []string{strings.Repeat("access-token-界", 1000), strings.Repeat("refreshed-token", 2000)} {
		if err := store.Set("chatgpt", value); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get("chatgpt")
		if err != nil || got != value {
			t.Fatal("Windows Credential Manager did not preserve large token", err)
		}
	}
	if err := store.Delete("chatgpt"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get("chatgpt"); err != nil || got != "" {
		t.Fatal("Windows credential was not deleted", err)
	}
}

func TestWindowsDataDirectoryACL(t *testing.T) {
	directory := t.TempDir()
	if err := SecureDataDirectory(directory); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(directory, "connection.json")
	if err := os.WriteFile(child, []byte("not-a-real-token"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, child} {
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := descriptor.DACL()
		if err != nil || acl == nil || acl.AceCount != 1 {
			t.Fatalf("expected owner-only ACL for %s: %v", path, err)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(user.User.Sid) {
			t.Fatal("data directory grants another principal access")
		}
	}
	if err := SecureDataDirectory(filepath.VolumeName(directory) + string(filepath.Separator)); err == nil {
		t.Fatal("accepted drive root")
	}
}
