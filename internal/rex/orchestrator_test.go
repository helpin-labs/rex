package rex

import (
	"context"
	"strings"
	"testing"
)

func TestMainBotDelegatesWithinRules(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)

	// The first-run bot named Rex becomes the main bot; nothing is duplicated.
	seeded := Bot{Name: mainBotName}
	if err := host.SaveBot(ctx, &seeded); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := host.EnsureMainBot(ctx); err != nil {
			t.Fatal(err)
		}
	}
	main := host.MainBot(ctx)
	if main == nil || main.ID != seeded.ID {
		t.Fatalf("the seeded Rex bot becomes the main bot: %+v", main)
	}
	if bots, _ := host.Store.ListBots(ctx); len(bots) != 1 {
		t.Fatalf("no second main bot is created, got %d bots", len(bots))
	}
	if prompt := main.SystemPrompt(nil); !strings.Contains(prompt, "mcp__bots__delegate_task") {
		t.Fatal("the main bot is told how to hand work over")
	}
	if servers, _ := host.runMCPServers(ctx, *main); !hasServer(servers, "bots") || !hasServer(servers, "catalog") {
		t.Fatalf("the main bot gets the bots and catalog tools: %+v", servers)
	}

	travel := Bot{Name: "Spain trip", Instructions: "You plan family trips to Spain.", Capabilities: Capabilities{Browser: true, Web: true}}
	if err := host.SaveBot(ctx, &travel); err != nil {
		t.Fatal(err)
	}
	if servers, _ := host.runMCPServers(ctx, travel); len(servers) > 0 && servers[len(servers)-1].ServerName == "bots" {
		t.Fatal("only the main bot can hand work over")
	}
	roster := host.rosterContext(ctx)
	if !strings.Contains(roster, "Spain trip (id "+travel.ID+", idle); tools: browser, web: You plan family trips") || strings.Contains(roster, "- Rex") {
		t.Fatalf("the roster lists the other bots: %s", roster)
	}

	if _, err := host.Delegate(ctx, *main, main.ID, "do it", ""); err == nil {
		t.Fatal("the main bot does not hand tasks to itself")
	}
	if _, err := host.Delegate(ctx, *main, "nope", "do it", ""); err == nil || !strings.Contains(err.Error(), "list_bots") {
		t.Fatalf("unknown bots are reported: %v", err)
	}
	draft, _ := host.DraftBot(ctx, "Gym buddy", "fitness", "")
	if _, err := host.Delegate(ctx, *main, draft.ID, "plan a workout", ""); err == nil || !strings.Contains(err.Error(), "being set up") {
		t.Fatalf("bots in setup take no tasks: %v", err)
	}
}

func TestRexMessagesAreNotTheUsersWords(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	bot := Bot{Name: "Spain trip"}
	_ = host.SaveBot(ctx, &bot)
	thread, _ := host.Store.ThreadForBot(ctx, bot)
	_ = host.Store.CreateTurn(ctx, &Turn{ConversationID: thread.ID, BotID: bot.ID, UserText: "find flights to Madrid"})
	_ = host.Store.CreateTurn(ctx, &Turn{ConversationID: thread.ID, BotID: bot.ID, UserText: "book the cheapest one", Origin: "rex"})
	if trusted := host.trustedUserMessages(ctx, thread.ID); len(trusted) != 1 || trusted[0] != "find flights to Madrid" {
		t.Fatalf("only the user's own messages are trusted: %v", trusted)
	}
}
