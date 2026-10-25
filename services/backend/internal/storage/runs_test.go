package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRunsSerializeSameChatAndKeepRequestIdentity(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user-1")
	var workers sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		workers.Go(func() {
			_, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Forecast")
			results <- err
		})
	}
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrChatBusy) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("Expected one active request, got %d", succeeded)
	}
	runs, err := store.PendingRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("Unexpected active runs: %+v, %v", runs, err)
	}
	run := runs[0]
	duplicate, err := store.PrepareRun(ctx, strings.ToUpper(run.ID), strings.ToUpper(chat.ID), chat.UserID, "Forecast")
	if err != nil || duplicate.UserMessageID != run.UserMessageID {
		t.Fatalf("Duplicate request inserted another message: %+v, %v", duplicate, err)
	}
	if _, err := store.GetRun(ctx, run.ID, "other"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("Foreign run was exposed: %v", err)
	}
	other := createChat(t, store, chat.UserID)
	if _, err := store.PrepareRun(ctx, run.ID, other.ID, chat.UserID, "Forecast"); !errors.Is(err, ErrRequestReuse) {
		t.Fatalf("Request ID could be reused across chats: %v", err)
	}
	var count int64
	store.db.Model(&Message{}).Where("chat_id = ?", chat.ID).Count(&count)
	if count != 1 {
		t.Fatalf("Rejected requests left %d messages", count)
	}
}

func TestRunCompletionIsAtomicAndIdempotent(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user-1")
	run, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Forecast")
	if err != nil {
		t.Fatal(err)
	}
	invalid := []LLMMessage{
		{Role: "assistant", MessageContent: toolCalls("a")},
		{Role: "assistant", MessageContent: text("Missing tool result")},
	}
	if err := store.FinishRun(ctx, run.ID, invalid, nil, ""); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("Invalid result accepted: %v", err)
	}
	var count int64
	store.db.Model(&Message{}).Where("chat_id = ?", chat.ID).Count(&count)
	if count != 1 {
		t.Fatalf("Failed completion left partial messages: %d", count)
	}
	messages := []LLMMessage{
		{Role: "assistant", MessageContent: toolCalls("a", "b")},
		{Role: "tool", MessageContent: MessageContent{Content: json.RawMessage(`"B"`), ToolCallID: "b"}},
		{Role: "tool", MessageContent: MessageContent{Content: json.RawMessage(`"A"`), ToolCallID: "a"}},
		{Role: "assistant", MessageContent: text("Done")},
	}
	if err := store.FinishRun(ctx, run.ID, messages, json.RawMessage(`{"invalid":true}`), ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Invalid artifacts accepted: %v", err)
	}
	for range 2 {
		if err := store.FinishRun(ctx, run.ID, messages, json.RawMessage(`[{"name":"forecast","data":{}}]`), ""); err != nil {
			t.Fatal(err)
		}
	}
	history, err := store.GetHistory(ctx, chat.ID, chat.UserID, 1)
	if err != nil || len(history) != 5 {
		t.Fatalf("Unexpected persisted result: %+v, %v", history, err)
	}
	stored, err := store.GetRun(ctx, run.ID, chat.UserID)
	if err != nil || stored.State != "completed" || stored.CompletedAt == nil {
		t.Fatalf("Run did not complete: %+v, %v", stored, err)
	}
	if _, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Next question"); err != nil {
		t.Fatalf("Completed run did not release chat: %v", err)
	}
}

func TestFailedRunIsExcludedFromHistoryAndReleasesChat(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user-1")
	run, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Failed question")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, run.ID, nil, nil, "AgentWorkflowFailed"); err != nil {
		t.Fatal(err)
	}
	next, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "New question")
	if err != nil || len(next.Payload.Messages) != 1 || string(next.Payload.Messages[0].Content) != `"New question"` {
		t.Fatalf("Failed turn leaked into next history: %+v, %v", next, err)
	}
}
