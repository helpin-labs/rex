package rex

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsWSLStartupRecovery(t *testing.T) {
	for _, scenario := range []string{"healthy", "recover", "still broken", "ownership unavailable", "wrong owner", "termination fails", "cancelled", "foreign target"} {
		t.Run(scenario, func(t *testing.T) {
			s := NewCommandSandbox(t.TempDir(), "")
			nonce := strings.Repeat("a", 32)
			d := ownedDistribution{Name: "Rex-Sandbox-" + nonce, Nonce: nonce, Directory: filepath.Join(s.dataDir, "wsl", nonce)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var commands [][]string
			probes := 0
			if scenario == "foreign target" {
				d.Name = "Ubuntu"
			}
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				commands = append(commands, args)
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("unbounded WSL startup operation")
				}
				if args[0] == "--terminate" {
					if !reflect.DeepEqual(args, []string{"--terminate", d.Name}) {
						t.Fatalf("termination escaped Rex's environment: %v", args)
					}
					if scenario == "termination fails" {
						return nil, context.DeadlineExceeded
					}
					return nil, nil
				}
				if !reflect.DeepEqual(args, []string{"--distribution", d.Name, "--user", "root", "--exec", "/bin/cat", "/etc/rex-owner"}) {
					t.Fatalf("unexpected WSL operation: %v", args)
				}
				if scenario == "ownership unavailable" {
					return nil, context.DeadlineExceeded
				}
				if scenario == "wrong owner" {
					return []byte("someone else"), nil
				}
				return []byte(nonce), nil
			}
			err := s.verifyRuntimeSandbox(ctx, d, run, func(ctx context.Context) error {
				probes++
				if scenario == "cancelled" {
					cancel()
					return ctx.Err()
				}
				if scenario != "healthy" && (probes == 1 || scenario == "still broken") {
					return errors.New("WSL stopped responding after wake")
				}
				return nil
			})
			wantSuccess := scenario == "healthy" || scenario == "recover"
			if (err == nil) != wantSuccess {
				t.Fatalf("error=%v, commands=%v", err, commands)
			}
			wantCommands := map[string]int{"healthy": 1, "recover": 3, "still broken": 3, "ownership unavailable": 1, "wrong owner": 1, "termination fails": 2, "cancelled": 1, "foreign target": 0}[scenario]
			if len(commands) != wantCommands {
				t.Fatalf("commands=%v, want %d", commands, wantCommands)
			}
		})
	}
}

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
	probes := 0
	if err := s.verifyRuntimeSandbox(t.Context(), first.Active, runWSL, func(ctx context.Context) error {
		probes++
		if probes == 1 {
			return errors.New("simulate interrupted WSL preflight")
		}
		_, err := s.bridge(first.Active).Probe(ctx)
		return err
	}); err != nil || probes != 2 {
		t.Fatalf("owned sandbox did not recover and pass confinement again: probes=%d error=%v", probes, err)
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
	if registered, err := registeredWSLDistribution(second.Active.Name); err != nil || !registered {
		t.Fatalf("owned sandbox registry: %v %v", registered, err)
	}
	if err := s.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if registered, err := registeredWSLDistribution(second.Active.Name); err != nil || registered {
		t.Fatalf("sandbox survived removal: %v %v", registered, err)
	}
	if err := s.Remove(t.Context()); err != nil {
		t.Fatalf("repeated removal must be harmless: %v", err)
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

func TestWindowsWSLRemovalRejectsForeignRecord(t *testing.T) {
	s := NewCommandSandbox(t.TempDir(), "")
	outside := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(outside, []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(s.dataDir, "wsl")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sandboxInstallation{Active: ownedDistribution{Name: "Ubuntu", Nonce: strings.Repeat("a", 32), Directory: filepath.Dir(outside)}})
	path := filepath.Join(root, "installation.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context()); err == nil {
		t.Fatal("foreign ownership record accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "kept" {
		t.Fatal("foreign data changed", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("rejected record was removed", err)
	}
}
