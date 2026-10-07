package rex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeThreadsGivesEachBotOneConversation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "Spain trip"}
	_ = bot.Normalize(t.TempDir())
	if err := store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	empty := Bot{Name: "Empty"}
	_ = empty.Normalize(t.TempDir())
	if err := store.SaveBot(ctx, &empty); err != nil {
		t.Fatal(err)
	}
	// Three conversations, oldest first; each has one run and one turn.
	var ids []string
	for index, title := range []string{"Flights", "Hotels", "Trains"} {
		conversation, err := store.CreateConversation(ctx, bot.ID, title, "chat")
		if err != nil {
			t.Fatal(err)
		}
		runID := "run-" + title
		if err := store.AttachRun(ctx, conversation.ID, runID); err != nil {
			t.Fatal(err)
		}
		started := time.Now().UTC().Add(time.Duration(index) * time.Minute)
		if err := store.CreateTurn(ctx, &Turn{ConversationID: conversation.ID, BotID: bot.ID, RunID: runID, UserText: "about " + title, StartedAt: started}); err != nil {
			t.Fatal(err)
		}
		_, _ = store.db.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, started, conversation.ID)
		_, _ = store.db.ExecContext(ctx, `UPDATE conversation_runs SET created_at=? WHERE run_id=?`, started, runID)
		ids = append(ids, conversation.ID)
	}
	routine := Routine{BotID: bot.ID, Title: "Digest", Prompt: "p", ScheduleKind: "interval", IntervalMinutes: 60, Enabled: true}
	if err := store.SaveRoutine(ctx, &routine); err != nil {
		t.Fatal(err)
	}
	_ = store.SetRoutineConversation(ctx, routine.ID, ids[0])

	backups := t.TempDir()
	if err := store.MergeThreads(ctx, backups); err != nil {
		t.Fatal(err)
	}
	if err := store.MergeThreads(ctx, backups); err != nil {
		t.Fatalf("merging again must be a no-op: %v", err)
	}

	conversations, _ := store.ListConversations(ctx, bot.ID)
	if len(conversations) != 1 || conversations[0].ID != ids[2] || conversations[0].Title != "Spain trip" {
		t.Fatalf("want the newest conversation kept and named after the bot, got %+v", conversations)
	}
	turns, _ := store.TurnsForConversation(ctx, ids[2])
	if len(turns) != 3 || turns[0].Section != "Flights" || turns[1].Section != "Hotels" || turns[2].Section != "Trains" {
		t.Fatalf("turns must move over in order with their conversation titles: %+v", turns)
	}
	runs, _ := store.ConversationRunIDs(ctx, ids[2])
	if len(runs) != 3 || runs[0] != "run-Flights" || runs[2] != "run-Trains" {
		t.Fatalf("runs must keep their order in time: %v", runs)
	}
	saved, _ := store.GetRoutine(ctx, routine.ID)
	if saved.ConversationID != ids[2] {
		t.Fatalf("routines must point at the thread, got %s", saved.ConversationID)
	}
	if others, _ := store.ListConversations(ctx, empty.ID); len(others) != 1 {
		t.Fatalf("a bot without conversations gets one, got %d", len(others))
	}
	entries, _ := os.ReadDir(backups)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".bak" {
		t.Fatalf("want one backup before merging, got %v", entries)
	}
}

func TestStartFreshLimitsCarriedContext(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "B"}
	_ = bot.Normalize(t.TempDir())
	_ = store.SaveBot(ctx, &bot)
	thread, err := store.ThreadForBot(ctx, bot)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AttachRun(ctx, thread.ID, "run-old")
	if ids, _ := store.ContextRunIDs(ctx, thread); len(ids) != 1 {
		t.Fatalf("before a fresh start the old run is carried: %v", ids)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.StartFresh(ctx, thread.ID); err != nil {
		t.Fatal(err)
	}
	thread, _ = store.GetConversation(ctx, thread.ID)
	if thread.ActiveRunID != nil || thread.ContextStart == nil {
		t.Fatalf("start fresh detaches the run: %+v", thread)
	}
	if ids, _ := store.ContextRunIDs(ctx, thread); len(ids) != 0 {
		t.Fatalf("after a fresh start nothing earlier is carried: %v", ids)
	}
}

func TestTurnRecordsRecalledMemories(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	bot := Bot{Name: "B"}
	_ = bot.Normalize(t.TempDir())
	_ = store.SaveBot(ctx, &bot)
	thread, _ := store.ThreadForBot(ctx, bot)
	turn := &Turn{ConversationID: thread.ID, BotID: bot.ID, UserText: "hi"}
	_ = store.CreateTurn(ctx, turn)
	if err := store.SetTurnRecalled(ctx, turn.ID, []RecalledMemory{{Text: "Likes window seats", Bank: "shared"}}); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.GetTurn(ctx, turn.ID)
	if len(saved.Recalled) != 1 || saved.Recalled[0].Bank != "shared" {
		t.Fatalf("recalled memories are kept on the turn: %+v", saved.Recalled)
	}
}
