package rex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func stopProfileHolders(profile string) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return
	}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		switch strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])) {
		case "chrome.exe", "msedge.exe", "brave.exe", "vivaldi.exe", "chromium.exe":
		default:
			continue
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, entry.ProcessID)
		if err != nil {
			continue
		}
		args, err := processArguments(handle)
		if err == nil && browserUsesProfile(args, profile) {
			// Keep the process handle open until taskkill returns, preventing PID reuse.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(ctx, filepath.Join(system, "taskkill.exe"), "/PID", strconv.FormatUint(uint64(entry.ProcessID), 10), "/T", "/F").Run()
			cancel()
		}
		windows.CloseHandle(handle)
	}
	_ = os.Remove(filepath.Join(profile, "SingletonLock"))
}

func processArguments(handle windows.Handle) ([]string, error) {
	var size uint32
	err := windows.NtQueryInformationProcess(handle, windows.ProcessCommandLineInformation, nil, 0, &size)
	if size < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) || size > 1<<20 {
		return nil, errors.New("process command line unavailable")
	}
	buffer := make([]byte, size)
	if err = windows.NtQueryInformationProcess(handle, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), size, &size); err != nil {
		return nil, err
	}
	header := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	offset := uintptr(unsafe.Pointer(header.Buffer)) - uintptr(unsafe.Pointer(&buffer[0]))
	length := uintptr(header.Length)
	if length == 0 || length%2 != 0 || offset%2 != 0 || offset >= uintptr(len(buffer)) || length > uintptr(len(buffer))-offset {
		return nil, errors.New("invalid process command line")
	}
	chars := unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[offset])), int(length/2))
	return windows.DecomposeCommandLine(windows.UTF16ToString(chars))
}

func browserUsesProfile(args []string, profile string) bool {
	found := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--type" || strings.HasPrefix(arg, "--type=") {
			return false
		}
		value, ok := strings.CutPrefix(arg, "--user-data-dir=")
		if arg == "--user-data-dir" && i+1 < len(args) {
			i++
			value, ok = args[i], true
		}
		if !ok {
			continue
		}
		if !strings.EqualFold(filepath.Clean(value), filepath.Clean(profile)) {
			return false
		}
		found = true
	}
	return found
}
