package rex

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestBotApprovalModes(t *testing.T) {
	for _, mode := range []string{ApprovalAsk, ApprovalAuto, ApprovalAllowAll} {
		for _, setup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/setup=%v", mode, setup), func(t *testing.T) {
				bot := Bot{Name: "Ops", ApprovalMode: mode, Setup: setup, Capabilities: Capabilities{Shell: true}}
				if err := bot.Normalize(t.TempDir()); err != nil {
					t.Fatal(err)
				}
				if bot.ApprovalMode != mode || !bot.Capabilities.FilesRead {
					t.Fatal("shell changed approval mode or lost file access")
				}
				agent := bot.RuntimeAgent("")
				want := "risk_based"
				if mode == ApprovalAllowAll && !setup {
					want = "never"
				}
				if agent.ApprovalMode != want {
					t.Fatalf("runtime mode=%s, want %s", agent.ApprovalMode, want)
				}
				var config struct {
					Review struct {
						Enabled     bool `json:"enabled"`
						AutoApprove bool `json:"auto_approve"`
					} `json:"approval_review"`
				}
				if err := json.Unmarshal(agent.ExecutionConfig, &config); err != nil {
					t.Fatal(err)
				}
				review := mode == ApprovalAuto && !setup
				if config.Review.Enabled != review || config.Review.AutoApprove != review {
					t.Fatalf("review=%+v", config.Review)
				}
				for _, tool := range []string{"run_command", "request_approval", "request_user_input"} {
					if !slices.Contains(agent.AllowedTools, tool) {
						t.Fatalf("missing tool %s", tool)
					}
				}
				store := openTestStore(t)
				if err := store.SaveBot(t.Context(), &bot); err != nil {
					t.Fatal(err)
				}
				saved, err := store.GetBot(t.Context(), bot.ID)
				if err != nil || saved.ApprovalMode != mode {
					t.Fatalf("mode did not persist: %v", err)
				}
			})
		}
	}
	bot := Bot{Name: "Ops", ApprovalMode: "unknown"}
	if err := bot.Normalize(t.TempDir()); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestApprovalModeMigration(t *testing.T) {
	for _, legacy := range []string{"", `{"typesafe_auto_approve":false}`, `{"typesafe_auto_approve":true}`} {
		t.Run(legacy, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rex.db")
			store, err := OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			// The new migration changes only data; removing its version recreates the prior schema.
			version := migrationVersion(t, "UPDATE bots SET approval_mode='allow_all' WHERE approval_mode='auto'")
			if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version>=?`, version); err != nil {
				t.Fatal(err)
			}
			if legacy != "" {
				if err := store.SetSetting(t.Context(), settingsKey, legacy); err != nil {
					t.Fatal(err)
				}
			}
			for _, bot := range []Bot{
				{ID: "old-auto", Name: "Old auto", ApprovalMode: "auto"},
				{ID: "old-ask", Name: "Old ask", ApprovalMode: "ask", Capabilities: Capabilities{Shell: true}},
				{ID: "setup", Name: "Setup", ApprovalMode: "ask", Setup: true},
			} {
				if err := store.SaveBot(t.Context(), &bot); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			askMode := ApprovalAsk
			if legacy == `{"typesafe_auto_approve":true}` {
				askMode = ApprovalAuto
			}
			for id, want := range map[string]string{"old-auto": ApprovalAllowAll, "old-ask": askMode, "setup": ApprovalAsk} {
				bot, err := store.GetBot(t.Context(), id)
				if err != nil || bot.ApprovalMode != want {
					t.Fatalf("%s: got %+v, want %s, err=%v", id, bot, want, err)
				}
			}
			bot := Bot{ID: "new-auto", Name: "New auto", ApprovalMode: ApprovalAuto}
			if err := store.SaveBot(t.Context(), &bot); err != nil {
				t.Fatal(err)
			}
			if err := store.migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			saved, err := store.GetBot(t.Context(), bot.ID)
			if err != nil || saved.ApprovalMode != ApprovalAuto {
				t.Fatal("migration ran twice")
			}
		})
	}
}

func TestJevAutoApprovalIsPerBot(t *testing.T) {
	settings := NewSettingsManager(openTestStore(t), NewMemorySecrets())
	current, err := settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.TypeSafe.APIKey = "review-test-key"
	current.TypeSafeAutoApprove = true // An old client must not enable global auto-approval.
	if _, _, err := settings.Save(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	env, err := settings.RuntimeEnv(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "AGENT_RUNTIME_TYPESAFE_ENABLED=true") || !slices.Contains(env, "AGENT_RUNTIME_TYPESAFE_AUTO_APPROVE=false") {
		t.Fatal("reviewer must be enabled with global auto-approval off")
	}
}
