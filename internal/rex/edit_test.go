package rex

import (
	"context"
	"testing"
	"time"
)

func TestWithoutCutsDropsWhatFollowsAnEditedMessage(t *testing.T) {
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return start.Add(time.Duration(minutes) * time.Minute) }
	messages := []Message{
		{RunID: "run-1", Role: "user", Content: "plan a trip", CreatedAt: at(0)},
		{RunID: "run-1", Role: "assistant", Content: "where to?", CreatedAt: at(1)},
		{RunID: "run-1", Role: "user", Content: "Lisbon, typo", CreatedAt: at(5)},
		{RunID: "run-1", Role: "assistant", Content: "Lisbon it is", CreatedAt: at(6)},
		{RunID: "run-2", Role: "user", Content: "other run", CreatedAt: at(7)},
	}
	kept := withoutCuts(messages, map[string]time.Time{"run-1": at(5)})
	if len(kept) != 3 || kept[0].Content != "plan a trip" || kept[1].Content != "where to?" || kept[2].Content != "other run" {
		t.Fatalf("kept %+v", kept)
	}
	if got := withoutCuts(messages, nil); len(got) != len(messages) {
		t.Fatalf("no cuts should keep everything, kept %d", len(got))
	}
}

func TestContextCutsKeepTheEarliestCutOfARun(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "Planner"}
	_ = bot.Normalize(t.TempDir())
	if err := store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, bot.ID, "Planner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	early := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	for _, after := range []time.Time{early.Add(time.Hour), early, early.Add(2 * time.Hour)} {
		if err := store.AddContextCut(ctx, conversation.ID, "run-1", after); err != nil {
			t.Fatal(err)
		}
	}
	cuts, err := store.ContextCuts(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) != 1 || !cuts["run-1"].Equal(early) {
		t.Fatalf("cuts %v, want run-1 at %v", cuts, early)
	}
}

func TestPerMillionPrices(t *testing.T) {
	if got := perMillion("0.000003"); got == nil || *got != 3 {
		t.Fatalf("perMillion = %v, want 3", got)
	}
	if got := perMillion("0.00000015"); got == nil || *got != 0.15 {
		t.Fatalf("perMillion = %v, want 0.15", got)
	}
	if perMillion("-1") != nil || perMillion("") != nil {
		t.Fatal("bad prices should be nil")
	}
}
