package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}

	ctx := context.Background()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	database := "app_test_" + uuid.NewString()
	if err := admin.db.Exec(`CREATE DATABASE "` + database + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.db.Exec(`DROP DATABASE "` + database + `"`).Error; err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + database
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	return store
}

func text(value string) MessageContent {
	encoded, _ := json.Marshal(value)
	return MessageContent{Content: encoded}
}

func createChat(t *testing.T, store *Store, userID string) Chat {
	t.Helper()
	chat, err := store.CreateChat(context.Background(), userID, "Forecast")
	if err != nil {
		t.Fatal(err)
	}

	return chat
}

func add(t *testing.T, store *Store, chat Chat, role string, content MessageContent) Message {
	t.Helper()
	message, err := store.AddMessage(context.Background(), chat.ID, chat.UserID, role, content)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID == 0 || message.CreatedAt.IsZero() {
		t.Fatal("message ID and timestamp must be generated automatically")
	}

	return message
}

func toolCalls(ids ...string) MessageContent {
	content := MessageContent{}
	for _, id := range ids {
		content.ToolCalls = append(content.ToolCalls, ToolCall{
			ID: id, Type: "function", Function: FunctionCall{Name: "forecast", Arguments: `{"source":"solar"}`},
		})
	}

	return content
}

func TestChatsAndOwnership(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user-1")
	if uuid.Validate(chat.ID) != nil || chat.CreatedAt.IsZero() {
		t.Fatal("chat ID and timestamp must be generated automatically")
	}
	if _, err := store.AddMessage(ctx, chat.ID, "other", "user", text("Hello")); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("unexpected unauthorized insert error: %v", err)
	}
	if _, err := store.GetHistory(ctx, chat.ID, "other", 1); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("unexpected unauthorized history error: %v", err)
	}
	if _, err := store.RenameChat(ctx, chat.ID, "other", "Changed"); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("unexpected unauthorized rename error: %v", err)
	}
	if err := store.DeleteChat(ctx, chat.ID, "other"); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("unexpected unauthorized delete error: %v", err)
	}

	renamed, err := store.RenameChat(ctx, chat.ID, chat.UserID, " Solar ")
	if err != nil || renamed.Title != "Solar" || renamed.ID != chat.ID {
		t.Fatalf("rename failed: %+v, %v", renamed, err)
	}
	add(t, store, chat, "user", text("Hello"))
	if err := store.DeleteChat(ctx, chat.ID, chat.UserID); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := store.db.Model(&Message{}).Where("chat_id = ?", chat.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("cascade delete failed: count=%d, error=%v", count, err)
	}
	if err := store.DeleteChat(ctx, chat.ID, chat.UserID); !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("missing chat should report not found: %v", err)
	}
}

func TestDeleteAllChatsPreservesOtherUsersAndMemory(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	for range 2 {
		chat := createChat(t, store, "user-1")
		add(t, store, chat, "user", text("Hello"))
	}

	other := createChat(t, store, "user-2")
	if _, err := store.SaveUserMemory(ctx, "user-1", "Prefers solar.", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAllChats(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := store.db.Model(&Message{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("messages were not deleted: %d, %v", count, err)
	}
	if _, err := store.GetHistory(ctx, other.ID, other.UserID, 1); err != nil {
		t.Fatal(err)
	}

	memory, err := store.GetUserMemory(ctx, "user-1")
	if err != nil || memory.Summary != "Prefers solar." {
		t.Fatalf("chat deletion changed user memory: %+v, %v", memory, err)
	}
	if err := store.DeleteAllChats(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryReturnsCompleteTurnsInLLMOrder(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user")
	add(t, store, chat, "user", text("First question"))
	add(t, store, chat, "assistant", text("First answer"))
	add(t, store, chat, "user", text("Second question"))
	add(t, store, chat, "assistant", toolCalls("call-1", "call-2"))
	for _, id := range []string{"call-2", "call-1"} {
		result := text("Result " + id)
		result.ToolCallID = id
		add(t, store, chat, "tool", result)
	}
	add(t, store, chat, "assistant", toolCalls("call-3"))
	result := text("Third tool result")
	result.ToolCallID = "call-3"
	add(t, store, chat, "tool", result)
	add(t, store, chat, "assistant", text("Second answer"))
	add(t, store, chat, "user", text("Unfinished question"))
	add(t, store, chat, "assistant", toolCalls("unfinished"))
	history, err := store.GetHistory(ctx, chat.ID, chat.UserID, 1)
	if err != nil {
		t.Fatal(err)
	}

	wantRoles := []string{"user", "assistant", "tool", "tool", "assistant", "tool", "assistant"}
	roles := make([]string, 0, len(history))
	for _, message := range history {
		roles = append(roles, message.Role)
	}
	if !reflect.DeepEqual(roles, wantRoles) || history[0].Content[0] != '"' ||
		string(history[0].Content) != `"Second question"` || history[2].ToolCallID != "call-2" {
		t.Fatalf("unexpected history: %+v", history)
	}

	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}

	var llm []map[string]any
	if err := json.Unmarshal(encoded, &llm); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := llm[0]["payload"]; wrapped || llm[0]["content"] != "Second question" {
		t.Fatalf("history is not in LLM format: %s", encoded)
	}
	add(t, store, chat, "user", text("New question after abandoned turn"))
	add(t, store, chat, "assistant", text("Final answer"))
	history, err = store.GetHistory(ctx, chat.ID, chat.UserID, 10)
	if err != nil || len(history) != 11 {
		t.Fatalf("expected three complete turns, got %d messages: %v", len(history), err)
	}

	history, err = store.GetHistory(ctx, chat.ID, chat.UserID, 2)
	if err != nil || len(history) != 9 || string(history[len(history)-1].Content) != `"Final answer"` {
		t.Fatalf("last two turns are incorrect: %+v, %v", history, err)
	}

	other := createChat(t, store, "user")
	history, err = store.GetHistory(ctx, other.ID, other.UserID, 1)
	if err != nil || history == nil || len(history) != 0 {
		t.Fatalf("empty chat must return an empty list: %+v, %v", history, err)
	}
}

func TestToolResultsMustMatchPendingCalls(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	chat := createChat(t, store, "user")
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "assistant", text("Answer")); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("assistant without user accepted: %v", err)
	}
	add(t, store, chat, "user", text("Question"))
	add(t, store, chat, "assistant", toolCalls("a", "b"))
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "assistant", text("Too early")); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("unanswered calls accepted: %v", err)
	}

	result := text("Result")
	result.ToolCallID = "unknown"
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "tool", result); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("unknown call accepted: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		result.ToolCallID = id
		add(t, store, chat, "tool", result)
	}
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "tool", result); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("duplicate result accepted: %v", err)
	}
	if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, "assistant", toolCalls("a")); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("reused call ID accepted: %v", err)
	}
	add(t, store, chat, "assistant", text("Done"))
}

func TestUserMemoryVersionsAndConcurrentUpdates(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	memory, err := store.GetUserMemory(ctx, "user")
	if err != nil || memory.Version != 0 || memory.Summary != "" || memory.UserID != "user" {
		t.Fatalf("unexpected initial memory: %+v, %v", memory, err)
	}

	memory, err = store.SaveUserMemory(ctx, "user", " Solar ", 0)
	if err != nil || memory.Version != 1 || memory.Summary != "Solar" || memory.UpdatedAt.IsZero() {
		t.Fatalf("initial save failed: %+v, %v", memory, err)
	}
	if _, err := store.SaveUserMemory(ctx, "user", "Overwrite", 0); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("duplicate initial write accepted: %v", err)
	}

	results := make(chan error, 10)
	var writes sync.WaitGroup
	for index := range 10 {
		writes.Go(func() {
			_, err := store.SaveUserMemory(ctx, "user", fmt.Sprintf("Update %d", index), 1)
			results <- err
		})
	}
	writes.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrMemoryConflict) {
			t.Fatal(err)
		}
	}

	memory, err = store.GetUserMemory(ctx, "user")
	if succeeded != 1 || err != nil || memory.Version != 2 {
		t.Fatalf("expected one successful update: writes=%d, memory=%+v, error=%v", succeeded, memory, err)
	}
	if _, err := store.SaveUserMemory(ctx, "missing", "Update", 1); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("updating absent memory should conflict: %v", err)
	}
}

func TestParallelChatsAndMigrations(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 10)
	var runs sync.WaitGroup
	for index := range 10 {
		runs.Go(func() {
			userID := fmt.Sprintf("user-%d", index)
			chat, err := store.CreateChat(ctx, userID, "Forecast")
			if err == nil {
				_, err = store.AddMessage(ctx, chat.ID, userID, "user", text(userID))
			}
			if err == nil {
				_, err = store.AddMessage(ctx, chat.ID, userID, "assistant", text("Done"))
			}
			if err == nil {
				var history []LLMMessage
				history, err = store.GetHistory(ctx, chat.ID, userID, 1)
				if err == nil && (len(history) != 2 || string(history[0].Content) != string(text(userID).Content)) {
					err = errors.New("parallel chat histories were mixed")
				}
			}
			results <- err
		})
	}
	runs.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CreateChat(canceled, "user", "Title"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestInputValidation(t *testing.T) {
	store := &Store{}
	ctx := context.Background()
	chatID := uuid.NewString()
	checks := []func() error{
		func() error { _, err := Open(ctx, ""); return err },
		func() error { _, err := store.CreateChat(ctx, "", "Title"); return err },
		func() error { _, err := store.CreateChat(ctx, "user", " "); return err },
		func() error { return store.DeleteAllChats(ctx, " ") },
		func() error { return store.DeleteChat(ctx, "invalid", "user") },
		func() error { _, err := store.GetHistory(ctx, chatID, "user", 0); return err },
		func() error { _, err := store.RenameChat(ctx, chatID, "user", " "); return err },
		func() error { _, err := store.GetUserMemory(ctx, ""); return err },
		func() error { _, err := store.SaveUserMemory(ctx, "user", " ", 0); return err },
		func() error { _, err := store.SaveUserMemory(ctx, "user", "Summary", -1); return err },
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("check %d: expected invalid input, got %v", index, err)
		}
	}
	for _, test := range []struct {
		role    string
		content MessageContent
	}{
		{"system", text("Rules")},
		{"user", text(" ")},
		{"user", toolCalls("a")},
		{"assistant", MessageContent{}},
		{"tool", text("Missing call ID")},
		{"assistant", toolCalls("a", "a")},
		{"user", MessageContent{Content: json.RawMessage(`{"invalid":true}`)}},
	} {
		if _, err := store.AddMessage(ctx, chatID, "user", test.role, test.content); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("invalid message accepted: %+v, %v", test, err)
		}
	}
}
