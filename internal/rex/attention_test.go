package rex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAttentionListsPendingInputAndFinishedRoutines(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"q1","run_id":"run-1","interaction_kind":"request_user_input","status":"pending","summary":"Solve the CAPTCHA"},
			{"id":"a0","run_id":"run-1","interaction_kind":"tool_approval","status":"resolved"}]`))
	}))
	defer runtime.Close()
	host := NewHost(Config{}, store, NewRuntimeClient(runtime.URL, ""), nil, nil, nil)
	host.SetRuntimeReady(true)

	bot := Bot{Name: "Spain trip"}
	_ = bot.Normalize(t.TempDir())
	if err := store.SaveBot(ctx, &bot); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, bot.ID, "Flights", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AttachRun(ctx, conversation.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	routine := Routine{BotID: bot.ID, Title: "Morning digest", Prompt: "p", ScheduleKind: "interval", IntervalMinutes: 60, Enabled: true}
	if err := store.SaveRoutine(ctx, &routine); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimRoutineRun(ctx, routine.ID, time.Now().UTC(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRoutineRun(ctx, claim.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}

	items, err := host.Attention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want the pending question and the finished routine, got %+v", items)
	}
	if items[0].ID != "q1" || items[0].Kind != "question" || items[0].ConversationID != conversation.ID || items[0].Detail != "Solve the CAPTCHA" {
		t.Fatalf("question item: %+v", items[0])
	}
	if items[1].Kind != "routine" || items[1].Title != "Morning digest" || items[1].Detail != "Finished" {
		t.Fatalf("routine item: %+v", items[1])
	}
}
