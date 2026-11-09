package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/auth"
	"masters-thesis/backend/internal/rpc"
	"masters-thesis/backend/internal/storage"
)

func TestTokenSubjectOwnsRPCAndSSEData(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration")
	}
	ctx := context.Background()
	store, err := storage.Open(ctx, isolatedDatabase(t, base))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	own, err := store.CreateChat(ctx, "user-1", "Own chat")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateChat(ctx, "user-2", "Private chat")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.PrepareRun(ctx, uuid.NewString(), foreign.ID, foreign.UserID, "Private request")
	if err != nil {
		t.Fatal(err)
	}
	service := agent.NewService(ctx, store, nil, bytes.Repeat([]byte{0x11}, 32))
	defer service.Close()
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"active": true, "iss": "http://" + r.Host, "exp": time.Now().Add(time.Hour).Unix(),
			"aud": "test-project", "sub": "user-1",
			"urn:zitadel:iam:org:project:test-project:roles": map[string]map[string]string{
				"admin": {"test-org": "org.invalid"},
			},
		})
	}))
	defer introspection.Close()
	a, err := auth.New(auth.Config{Issuer: introspection.URL, ProjectID: "test-project", ClientID: "test-client", ClientSecret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /rpc", a.Middleware(auth.RequireUserRole(rpc.NewHandler(store.SaveUserMemory, rpc.ChatMethods(store), rpc.AgentMethods(service)))))
	mux.Handle("GET /agent/runs/{run_id}/events", a.Middleware(auth.RequireUserRole(http.HandlerFunc(service.Stream))))
	invoke := func(method string, params map[string]any) (json.RawMessage, int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		request := httptest.NewRequest("POST", "/rpc", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer test-user-token")
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, request)
		var payload struct {
			Result json.RawMessage
			Error  *struct{ Code int }
		}
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &payload) != nil {
			t.Fatalf("RPC failed: %d %s", result.Code, result.Body.String())
		}
		if payload.Error != nil {
			return payload.Result, payload.Error.Code
		}
		return payload.Result, 0
	}

	for _, method := range []string{"GetChat", "RenameChat", "DeleteChat", "SendMessage"} {
		params := map[string]any{"chat_id": foreign.ID}
		if method == "RenameChat" {
			params["new_title"] = "Stolen"
		}
		if method == "SendMessage" {
			params["request_id"] = uuid.NewString()
			params["content"] = "Private"
		}
		_, code := invoke(method, params)
		if code != int(rpc.ChatNotFound) {
			t.Fatalf("%s allowed a foreign chat: %d", method, code)
		}
	}
	result, code := invoke("ListChats", map[string]any{})
	var chats []storage.Chat
	if code != 0 || json.Unmarshal(result, &chats) != nil || len(chats) != 1 || chats[0].ID != own.ID {
		t.Fatal("ListChats did not use the verified subject")
	}
	result, code = invoke("CreateChat", map[string]any{"title": "New"})
	var created storage.Chat
	if code != 0 || json.Unmarshal(result, &created) != nil || created.UserID != own.UserID {
		t.Fatal("CreateChat did not use the verified subject")
	}
	result, code = invoke("SaveUserMemory", map[string]any{"summary": "Own memory", "expected_version": 0})
	var memory storage.UserMemory
	if code != 0 || json.Unmarshal(result, &memory) != nil || memory.UserID != own.UserID {
		t.Fatal("SaveUserMemory did not use the verified subject")
	}
	if memory, err := store.GetUserMemory(ctx, foreign.UserID); err != nil || memory.Version != 0 {
		t.Fatal("foreign memory changed")
	}
	for _, token := range []string{"", "Bearer test-user-token"} {
		request := httptest.NewRequest("GET", "/agent/runs/"+run.ID+"/events?user_id="+foreign.UserID, nil)
		request.Header.Set("Authorization", token)
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, request)
		want := 404
		if token == "" {
			want = 401
		}
		if result.Code != want {
			t.Fatalf("SSE ownership check with admin role expected %d, got %d", want, result.Code)
		}
	}
	if _, code := invoke("DeleteAllChats", map[string]any{}); code != 0 {
		t.Fatal("DeleteAllChats failed")
	}
	if _, err := store.GetChat(ctx, foreign.ID, foreign.UserID); err != nil {
		t.Fatal("DeleteAllChats removed a foreign chat")
	}
}
