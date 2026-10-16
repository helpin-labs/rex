package rex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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
