package rex

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsWSLOwnershipRejectsForeignTargets(t *testing.T) {
	s := NewCommandSandbox(t.TempDir(), "")
	nonce := strings.Repeat("a", 32)
	owned := ownedDistribution{Name: "Rex-Sandbox-" + nonce, Nonce: nonce, Directory: filepath.Join(s.dataDir, "wsl", nonce)}
	if err := s.validateOwnership(owned); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ownedDistribution{{Name: "Ubuntu", Nonce: nonce, Directory: owned.Directory}, {Name: owned.Name, Nonce: nonce, Directory: t.TempDir()}, {Name: owned.Name, Nonce: "../bad", Directory: owned.Directory}} {
		if s.validateOwnership(bad) == nil || s.owns(t.Context(), bad) {
			t.Fatalf("accepted foreign distribution: %+v", bad)
		}
	}
}

func TestWindowsWSLSetupRepairAndProbe(t *testing.T) {
	resources := os.Getenv("REX_WSL_E2E_ROOTFS")
	if resources == "" {
		t.Skip("requires the built rootfs and a real WSL2 kernel")
	}
	data := t.TempDir()
	if err := SecureDataDirectory(data); err != nil {
		t.Fatal(err)
	}
	s := NewCommandSandbox(data, resources)
	// All cleanup is scoped to the random distributions created by this test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		installed, err := s.installation()
		if err != nil {
			return
		}
		for _, d := range append(installed.Retired, installed.Active) {
			if s.owns(ctx, d) {
				_, _ = runWSL(ctx, "--unregister", d.Name)
			}
		}
	})
	if err := s.install(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	if !s.owns(t.Context(), first.Active) {
		t.Fatal("new distribution has no ownership proof")
	}
	if env := s.RuntimeEnv(t.Context()); len(env) != 4 || env[0] != "AGENT_RUNTIME_EXECUTION_ISOLATION=wsl" {
		t.Fatalf("verified sandbox disabled: %v %+v", env, s.Status())
	}
	if err := s.install(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	if first.Active.Name == second.Active.Name || len(second.Retired) != 1 {
		t.Fatalf("repair did not stage a separate environment: %+v", second)
	}
	if !s.owns(t.Context(), first.Active) {
		t.Fatal("repair removed the previous environment before activation")
	}
	if env := s.RuntimeEnv(t.Context()); len(env) != 4 {
		t.Fatalf("repaired environment not enabled: %v", env)
	}
	s.resources = t.TempDir()
	if err := s.install(t.Context()); err == nil {
		t.Fatal("incomplete bundle installed")
	}
	unchanged, err := s.installation()
	if err != nil || unchanged.Active.Name != second.Active.Name {
		t.Fatalf("failed install replaced working sandbox: %+v %v", unchanged, err)
	}
}

func TestWindowsWSLStorageACLDoesNotExposeOtherData(t *testing.T) {
	data := t.TempDir()
	if err := SecureDataDirectory(data); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(data, "connection.json")
	if err := os.WriteFile(private, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(data, "wsl")
	if err := os.Mkdir(storage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := secureDataDirectory(storage, true); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(storage, "disk.vhdx")
	if err := os.WriteFile(disk, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{data, private, storage, disk} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			t.Fatal("missing ACL", err)
		}
		system := false
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
				t.Fatal("unexpected ACL entry")
			}
			if sid.String() == "S-1-5-18" {
				system = true
			} else if !sid.Equals(user.User.Sid) {
				t.Fatal("storage grants another principal access")
			}
		}
		wantSystem := path == storage || path == disk
		if system != wantSystem {
			t.Fatalf("SYSTEM access for %s: %v, expected %v", path, system, wantSystem)
		}
	}
}
