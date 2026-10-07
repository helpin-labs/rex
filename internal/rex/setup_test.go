package rex

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func newSetupTestHost(t *testing.T) *Host {
	t.Helper()
	store := openTestStore(t)
	settings := NewSettingsManager(store, NewMemorySecrets())
	memory := NewMemoryService(filepath.Join(t.TempDir(), "memory.db"), store, settings)
	t.Cleanup(func() { memory.Close() })
	return NewHost(Config{WorkspaceRoot: t.TempDir(), HostURL: "http://127.0.0.1:1", MCPSecret: []byte("secret")}, store, NewRuntimeClient("http://127.0.0.1:1", ""), settings, memory, &SkillLibrary{Dir: t.TempDir()})
}

func TestNewBotSetsItselfUp(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	draft, err := host.DraftBot(ctx, "Flight watcher", "travel", "#6aa9de")
	if err != nil {
		t.Fatal(err)
	}
	if !draft.Setup || draft.Name != "Flight watcher" || draft.Emoji != "travel" || draft.Color != "#6aa9de" || draft.RuntimeApprovalMode() != "risk_based" {
		t.Fatalf("a draft starts in setup and always asks: %+v", draft)
	}
	if prompt := draft.SystemPrompt(nil); !strings.Contains(prompt, "mcp__setup__configure_myself") {
		t.Fatal("a draft bot is told to set itself up")
	}
	if servers, _ := host.runMCPServers(ctx, *draft); !hasServer(servers, "setup") || !hasServer(servers, "catalog") {
		t.Fatalf("a draft bot gets the setup and catalog tools: %+v", servers)
	}
	if thread, err := host.Store.ThreadForBot(ctx, *draft); err != nil || thread.BotID != draft.ID {
		t.Fatalf("a draft bot has its conversation: %v", err)
	}

	if _, err := host.ConfigureBot(ctx, draft.ID, "Flight watcher", "not-a-face", "You watch flight prices for the user."); err == nil {
		t.Fatal("unknown icons are rejected")
	}
	bot, err := host.ConfigureBot(ctx, draft.ID, "Flight watcher", "travel", "You watch flight prices for the user.")
	if err != nil {
		t.Fatal(err)
	}
	if bot.Setup || bot.Name != "Flight watcher" || bot.Emoji != "travel" || bot.Color != "#6aa9de" || !strings.HasSuffix(bot.WorkspaceDir, "flight-watcher") {
		t.Fatalf("configuring ends setup and applies the choices: %+v", bot)
	}
	if servers, _ := host.runMCPServers(ctx, *bot); len(servers) > 0 && servers[len(servers)-1].ServerName == "setup" {
		t.Fatal("a configured bot no longer gets the setup tools")
	}

	// Without a command sandbox, file editing and commands stay off.
	if _, err := host.EnableBotTools(ctx, bot.ID, false, false, true, false); !errors.Is(err, ErrWriteToolsUnavailable) {
		t.Fatalf("write tools need the sandbox, got %v", err)
	}
	enabled, err := host.EnableBotTools(ctx, bot.ID, true, true, false, false)
	if err != nil || strings.Join(enabled, ",") != "browser,files_read" {
		t.Fatalf("browser and reading files are enabled: %v %v", enabled, err)
	}
	saved, _ := host.Store.GetBot(ctx, bot.ID)
	if !saved.Capabilities.Browser || !saved.Capabilities.FilesRead {
		t.Fatalf("capabilities are saved: %+v", saved.Capabilities)
	}

	routine := Routine{BotID: bot.ID, Title: "Price check", Prompt: "Check ARN to MAD prices", ScheduleKind: "daily", DailyTime: "08:00", Enabled: true}
	if err := host.CreateRoutine(ctx, &routine); err != nil || routine.NextRunAt == nil {
		t.Fatalf("routine is scheduled: %v %+v", err, routine)
	}
}

func TestBotsProposeInstructionsTheUserApplies(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{Name: "Newsletter", Instructions: "You write a weekly AI newsletter."}
	if err := host.SaveBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	if servers, _ := host.runMCPServers(ctx, *bot); !hasServer(servers, "self") {
		t.Fatalf("configured bots can propose instructions: %+v", servers)
	}
	if prompt := bot.SystemPrompt(nil); !strings.Contains(prompt, "Never say your instructions") {
		t.Fatal("bots are told not to claim changes that did not happen")
	}

	first, _ := host.Store.ProposeInstructions(ctx, bot.ID, "You write a weekly AI newsletter. Use AP and Ground News.", "Add AP and Ground News", ProposalFromBot, "")
	second, err := host.Store.ProposeInstructions(ctx, bot.ID, "You write a weekly AI newsletter. Sources: AP, Ground News, The Conversation.", "Add three impartial sources", ProposalFromBot, "")
	if err != nil {
		t.Fatal(err)
	}
	if pending, _ := host.Store.PendingProposals(ctx); len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("a new proposal replaces the unanswered one: %+v", pending)
	}
	unchanged, _ := host.Store.GetBot(ctx, bot.ID)
	if strings.Contains(unchanged.Instructions, "AP") {
		t.Fatal("a proposal changes nothing by itself")
	}
	if err := host.AnswerProposal(ctx, first.ID, true); err == nil {
		t.Fatal("a replaced proposal cannot be applied")
	}
	if err := host.AnswerProposal(ctx, second.ID, true); err != nil {
		t.Fatal(err)
	}
	applied, _ := host.Store.GetBot(ctx, bot.ID)
	if !strings.Contains(applied.Instructions, "The Conversation") {
		t.Fatalf("applying updates the instructions: %q", applied.Instructions)
	}
	third, _ := host.Store.ProposeInstructions(ctx, bot.ID, "You write a daily AI newsletter instead of a weekly one.", "Make it daily", ProposalFromBot, "")
	if err := host.AnswerProposal(ctx, third.ID, false); err != nil {
		t.Fatal(err)
	}
	if kept, _ := host.Store.GetBot(ctx, bot.ID); strings.Contains(kept.Instructions, "daily") {
		t.Fatal("discarding leaves the instructions as they were")
	}
}

func TestProposalsKeepOnlyTheUsersInstructions(t *testing.T) {
	bot := Bot{Name: "Newsletter test", Instructions: "You write a newsletter. Use AP."}
	copied := strings.Replace(bot.SystemPrompt(nil), "Use AP.", "Use AP and Ground News.", 1)
	if got := userInstructions(copied); got != "You write a newsletter. Use AP and Ground News." {
		t.Fatalf("Rex's rules are stripped from a copied system prompt: %q", got)
	}
	if got := userInstructions("  You plan trips.  "); got != "You plan trips." {
		t.Fatalf("plain instructions are kept: %q", got)
	}
}

func TestFingerprintChangesWithTheBotsSetup(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := &Bot{Name: "Data", Instructions: "You extract data."}
	_ = host.SaveBot(ctx, bot)
	before := host.botFingerprint(ctx, *bot)
	if host.botFingerprint(ctx, *bot) != before {
		t.Fatal("the same setup has the same fingerprint")
	}
	bot.Skills = []string{"research-report"}
	if host.botFingerprint(ctx, *bot) == before {
		t.Fatal("attaching a skill changes the fingerprint")
	}
	bot.Skills = nil
	bot.Capabilities.Browser = true
	if host.botFingerprint(ctx, *bot) == before {
		t.Fatal("new tools change the fingerprint")
	}
}
