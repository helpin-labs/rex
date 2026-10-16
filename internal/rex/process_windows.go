package rex

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Keep the sole job handle in this host: Windows closes it on host death and
// terminates descendants. Children inherit membership, but never this handle.
var processJob windows.Handle

func OwnProcessTree() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
	}
	if err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("own Rex process tree: %w", err)
	}
	processJob = job
	return nil
}

func ConfigureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func SignalProcessTree(cmd *exec.Cmd, force bool) error {
	if !force {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kill := exec.CommandContext(ctx, filepath.Join(system, "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// A stale PID is not authority to kill another Windows process. Job ownership
// handles cleanup after crashes; remove the diagnostic file only.
func ReapOrphan(path, name string) { _ = os.Remove(path) }

func forwardBrowserProcess(real string, args, env []string) error {
	cmd := exec.Command(real, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func linkBrowserShim(self, link string) error {
	if err := os.Link(self, link); err == nil {
		return nil
	}
	input, err := os.Open(self)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(link), ".browser-shim-*.exe")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(output.Name(), link)
}

// Keep databases, callback tokens, and browser profiles private on NTFS.
func SecureDataDirectory(path string) error { return secureDataDirectory(path, false) }

func secureDataDirectory(path string, systemAccess bool) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if filepath.Dir(absolute) == absolute {
		return fmt.Errorf("Rex data needs a dedicated directory")
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("Rex data directory must not be a reparse point")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	rules := "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")"
	if systemAccess {
		rules += "(A;OICI;FA;;;SY)"
	}
	descriptor, err := windows.SecurityDescriptorFromString(rules)
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func revealPath(path string, selectFile bool) error {
	root, err := windows.GetWindowsDirectory()
	if err != nil {
		return err
	}
	args := []string{filepath.Clean(path)}
	if selectFile {
		args = append([]string{"/select,"}, args...)
	}
	return exec.Command(filepath.Join(root, "explorer.exe"), args...).Start()
}

// Chromium names inherited Windows HANDLEs instead of Unix descriptors 3 and 4.
func configureBrowserPipes(command *exec.Cmd, childRead, parentWrite, parentRead, childWrite *os.File) error {
	for _, file := range []*os.File{parentWrite, parentRead} {
		if err := windows.SetHandleInformation(windows.Handle(file.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return err
		}
	}
	ConfigureProcess(command)
	command.SysProcAttr.AdditionalInheritedHandles = []syscall.Handle{syscall.Handle(childRead.Fd()), syscall.Handle(childWrite.Fd())}
	flag := "--remote-debugging-io-pipes=" + strconv.FormatUint(uint64(childRead.Fd()), 10) + "," + strconv.FormatUint(uint64(childWrite.Fd()), 10)
	command.Args = slices.Insert(command.Args, 1, flag)
	return nil
}

func browserVersion(executable string) string {
	size, err := windows.GetFileVersionInfoSize(executable, nil)
	if err != nil || size == 0 {
		return ""
	}
	data := make([]byte, size)
	if err := windows.GetFileVersionInfo(executable, 0, size, unsafe.Pointer(&data[0])); err != nil {
		return ""
	}
	var version *windows.VS_FIXEDFILEINFO
	var length uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&data[0]), `\`, unsafe.Pointer(&version), &length); err != nil || version == nil || length < uint32(unsafe.Sizeof(*version)) {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", version.FileVersionMS>>16, version.FileVersionMS&65535, version.FileVersionLS>>16, version.FileVersionLS&65535)
}

func registeredBrowserExecutable(name string) string {
	for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			key, err := registry.OpenKey(hive, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE|view)
			if err != nil {
				continue
			}
			value, _, err := key.GetStringValue("")
			key.Close()
			if err != nil {
				continue
			}
			path := strings.Trim(strings.TrimSpace(value), `"`)
			if !filepath.IsAbs(path) {
				continue
			}
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	return ""
}
