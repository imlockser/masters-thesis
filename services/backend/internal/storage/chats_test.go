package storage

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestChatViewIncludesAllMessagesAndPendingRun(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user-1")
	for range 6 {
		add(t, store, chat, "user", text("Question"))
		add(t, store, chat, "assistant", text("Answer"))
	}
	run, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Pending question")
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.GetChat(ctx, chat.ID, chat.UserID)
	if err != nil || len(view.Messages) != 13 || view.ActiveRunID != run.ID {
		t.Fatalf("Incorrect full chat view: %+v, %v", view, err)
	}
	for i, message := range view.Messages {
		if message.ChatID != chat.ID || message.ID == 0 || message.CreatedAt.IsZero() || (i > 0 && message.ID <= view.Messages[i-1].ID) {
			t.Fatalf("Incorrect message metadata/order: %+v", message)
		}
	}
	if _, err := store.GetChat(ctx, chat.ID, "other"); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("Foreign messages exposed: %v", err)
	}
	if _, err := store.GetChat(ctx, uuid.NewString(), chat.UserID); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("Missing chat should be not found: %v", err)
	}
	if err := store.FinishRun(ctx, run.ID, nil, nil, "AgentWorkflowFailed"); err != nil {
		t.Fatal(err)
	}
	view, err = store.GetChat(ctx, chat.ID, chat.UserID)
	if err != nil || view.ActiveRunID != "" || len(view.Messages) != 13 {
		t.Fatalf("Failed turn should remain visible without an active run: %+v, %v", view, err)
	}
}

func TestChatDeletionRejectsPendingRunsAtomically(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	active := createChat(t, store, "user-1")
	idle := createChat(t, store, "user-1")
	other := createChat(t, store, "user-2")
	run, err := store.PrepareRun(ctx, uuid.NewString(), active.ID, active.UserID, "Forecast")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteChat(ctx, active.ID, active.UserID); !errors.Is(err, ErrChatBusy) {
		t.Fatalf("Active chat was deleted: %v", err)
	}
	if err := store.DeleteAllChats(ctx, active.UserID); !errors.Is(err, ErrChatBusy) {
		t.Fatalf("Bulk deletion did not reject active chat: %v", err)
	}
	if _, err := store.GetChat(ctx, idle.ID, idle.UserID); err != nil {
		t.Fatalf("Rejected bulk deletion partially removed chats: %v", err)
	}
	if err := store.FinishRun(ctx, run.ID, nil, nil, "AgentWorkflowFailed"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAllChats(ctx, active.UserID); err != nil {
		t.Fatal(err)
	}
	chats, err := store.ListChats(ctx, active.UserID)
	if err != nil || chats == nil || len(chats) != 0 {
		t.Fatalf("Expected empty chat list: %+v, %v", chats, err)
	}
	if _, err := store.GetChat(ctx, other.ID, other.UserID); err != nil {
		t.Fatalf("Another user's chat was deleted: %v", err)
	}
}

func TestChatDeletionAndRunPreparationAreSerialized(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	for range 10 {
		chat := createChat(t, store, "user-1")
		var workers sync.WaitGroup
		start := make(chan struct{})
		var run AgentRun
		var prepareErr, deleteErr error
		workers.Go(func() {
			<-start
			run, prepareErr = store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Forecast")
		})
		workers.Go(func() {
			<-start
			deleteErr = store.DeleteChat(ctx, chat.ID, chat.UserID)
		})
		close(start)
		workers.Wait()
		if prepareErr == nil {
			if !errors.Is(deleteErr, ErrChatBusy) {
				t.Fatalf("Run and deletion both succeeded: %v", deleteErr)
			}
			if _, err := store.GetRun(ctx, run.ID, chat.UserID); err != nil {
				t.Fatalf("Active run lost its chat: %v", err)
			}
		} else if !errors.Is(prepareErr, ErrChatNotFound) || deleteErr != nil {
			t.Fatalf("Unexpected concurrent result: prepare=%v, delete=%v", prepareErr, deleteErr)
		}
	}
}
