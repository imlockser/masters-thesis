package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"

	"masters-thesis/backend/internal/auth"
	"masters-thesis/backend/internal/rpc"
	"masters-thesis/backend/internal/storage"
)

func TestFrontendChatProcedures(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration")
	}
	ctx := context.Background()
	store, err := storage.Open(ctx, isolatedDatabase(t, base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	bridge := rpc.NewHandler(store.SaveUserMemory, rpc.ChatMethods(store))
	server := httptest.NewServer(fixtureAuthentication(t).Middleware(auth.RequireUserRole(bridge)))
	t.Cleanup(server.Close)
	invoke := func(userID, method string, params map[string]any, target any) {
		t.Helper()
		result, code := callMethod(t, server.URL, userID, method, params)
		if code != 0 {
			t.Fatalf("%s failed with code %d", method, code)
		}
		if target != nil {
			if err := json.Unmarshal(result, target); err != nil {
				t.Fatal(err)
			}
		} else if string(result) != "null" {
			t.Fatalf("Expected null deletion result, got %s", result)
		}
	}
	user := map[string]any{}
	var chats []storage.Chat
	invoke("user-1", "ListChats", user, &chats)
	if chats == nil || len(chats) != 0 {
		t.Fatalf("Expected an empty JSON array: %+v", chats)
	}
	var chat, newer, other storage.Chat
	invoke("user-1", "CreateChat", map[string]any{"title": " Solar "}, &chat)
	invoke("user-1", "CreateChat", map[string]any{"title": "Newer"}, &newer)
	invoke("user-2", "CreateChat", map[string]any{"title": "Other"}, &other)
	if chat.Title != "Solar" || uuid.Validate(chat.ID) != nil || chat.CreatedAt.IsZero() {
		t.Fatalf("Invalid created chat: %+v", chat)
	}
	invoke("user-1", "ListChats", user, &chats)
	if len(chats) != 2 || chats[0].ID != newer.ID || chats[1].ID != chat.ID {
		t.Fatalf("Chat list order/ownership is incorrect: %+v", chats)
	}
	params := map[string]any{"chat_id": chat.ID}
	var details storage.ChatDetails
	invoke("user-1", "GetChat", params, &details)
	if details.Messages == nil || len(details.Messages) != 0 || details.ActiveRunID != "" {
		t.Fatalf("Invalid empty chat view: %+v", details)
	}
	for range 6 {
		for _, role := range []string{"user", "assistant"} {
			if _, err := store.AddMessage(ctx, chat.ID, chat.UserID, role, storage.MessageContent{Content: json.RawMessage(`"Text"`)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	run, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, chat.UserID, "Pending")
	if err != nil {
		t.Fatal(err)
	}
	invoke("user-1", "GetChat", params, &details)
	if len(details.Messages) != 13 || details.ActiveRunID != run.ID || details.Messages[12].CreatedAt.IsZero() {
		t.Fatalf("GetChat truncated history or omitted the active run: %+v", details)
	}
	for _, method := range []string{"GetChat", "RenameChat", "DeleteChat"} {
		foreign := map[string]any{"chat_id": chat.ID}
		if method == "RenameChat" {
			foreign["new_title"] = "Forbidden"
		}
		if _, code := callMethod(t, server.URL, "user-2", method, foreign); code != int(rpc.ChatNotFound) {
			t.Fatalf("%s did not reject foreign chat: %d", method, code)
		}
	}
	if _, code := callMethod(t, server.URL, "user-1", "DeleteChat", params); code != int(rpc.ChatBusy) {
		t.Fatalf("Active chat could be deleted: %d", code)
	}
	if _, code := callMethod(t, server.URL, "user-1", "DeleteAllChats", user); code != int(rpc.ChatBusy) {
		t.Fatalf("Active chats could be bulk deleted: %d", code)
	}
	invoke("user-1", "ListChats", user, &chats)
	if len(chats) != 2 {
		t.Fatal("Rejected bulk deletion partially removed chats")
	}
	invoke("user-1", "RenameChat", map[string]any{"chat_id": chat.ID, "new_title": " Renamed "}, &chat)
	if chat.Title != "Renamed" {
		t.Fatalf("Incorrect renamed title: %s", chat.Title)
	}
	if err := store.FinishRun(ctx, run.ID, nil, nil, "AgentWorkflowFailed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUserMemory(ctx, chat.UserID, "Keep this memory.", 0); err != nil {
		t.Fatal(err)
	}
	invoke("user-1", "DeleteChat", params, nil)
	if _, code := callMethod(t, server.URL, "user-1", "GetChat", params); code != int(rpc.ChatNotFound) {
		t.Fatalf("Deleted chat is still readable: %d", code)
	}
	if _, err := store.GetRun(ctx, run.ID, chat.UserID); !errors.Is(err, storage.ErrRunNotFound) {
		t.Fatalf("Chat deletion did not cascade to runs: %v", err)
	}
	invoke("user-1", "DeleteAllChats", user, nil)
	invoke("user-1", "DeleteAllChats", user, nil)
	invoke("user-1", "ListChats", user, &chats)
	if len(chats) != 0 {
		t.Fatal("Bulk deletion did not remove all owned chats")
	}
	invoke("user-2", "ListChats", map[string]any{}, &chats)
	if len(chats) != 1 || chats[0].ID != other.ID {
		t.Fatal("Bulk deletion affected another user")
	}
	memory, err := store.GetUserMemory(ctx, "user-1")
	if err != nil || memory.Summary != "Keep this memory." {
		t.Fatalf("Chat deletion changed global memory: %+v, %v", memory, err)
	}
}
