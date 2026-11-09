package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/auth"
	"masters-thesis/backend/internal/storage"
)

func TestVerifiedIdentityAcrossRPCRequests(t *testing.T) {
	var issuer string
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		json.NewEncoder(w).Encode(map[string]any{
			"active": true, "iss": issuer, "exp": time.Now().Add(time.Hour).Unix(),
			"aud": "test-project", "sub": r.Form.Get("token"),
			"urn:zitadel:iam:org:project:test-project:roles": map[string]map[string]string{
				"viewer": {"test-org": "org.invalid"},
			},
		})
	}))
	defer introspection.Close()
	issuer = introspection.URL
	a, err := auth.New(auth.Config{Issuer: issuer, ProjectID: "test-project", ClientID: "test-client", ClientSecret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewHandler(func(ctx context.Context, userID, summary string, version int64) (storage.UserMemory, error) {
		if userID != auth.UserID(ctx) || userID == "victim" {
			t.Error("unverified identity reached storage")
		}
		if !auth.HasRole(ctx, "viewer") || auth.HasRole(ctx, "admin") {
			t.Error("verified roles were lost or replaced in RPC context")
		}
		return storage.UserMemory{UserID: userID, Summary: summary, Version: version + 1}, nil
	})
	h := a.Middleware(auth.RequireUserRole(bridge))
	var workers sync.WaitGroup
	for i := range 10 {
		workers.Go(func() {
			userID := fmt.Sprintf("test-user-%d", i)
			request := httptest.NewRequest("POST", "/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"SaveUserMemory","params":{"summary":"Own memory","expected_version":0}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+userID)
			request.Header.Set("X-Roles", "admin")
			result := httptest.NewRecorder()
			h.ServeHTTP(result, request)
			var payload struct {
				Result storage.UserMemory
				Error  any
			}
			if json.Unmarshal(result.Body.Bytes(), &payload) != nil || result.Code != 200 || payload.Error != nil || payload.Result.UserID != userID {
				t.Errorf("request identity was lost or mixed: %d %s", result.Code, result.Body.String())
			}
		})
	}
	workers.Wait()
}

func TestRPCRejectsIdentityParameters(t *testing.T) {
	store := &storage.Store{}
	bridge := NewHandler(func(context.Context, string, string, int64) (storage.UserMemory, error) {
		t.Error("forged identity reached storage")
		return storage.UserMemory{}, nil
	}, ChatMethods(store), AgentMethods(&agent.Service{}))
	for _, params := range []string{`{"user_id":"victim"}`, `{"roles":["admin"]}`} {
		for _, method := range []string{"CreateChat", "ListChats", "GetChat", "RenameChat", "DeleteChat", "DeleteAllChats", "SendMessage", "SaveUserMemory"} {
			result := call(t, bridge, method, params)
			if result.Error == nil || result.Error.Code != jrpc2.InvalidParams {
				t.Fatalf("%s accepted identity parameters %s: %+v", method, params, result)
			}
		}
	}
}

func TestRPCRequiresVerifiedContext(t *testing.T) {
	bridge := NewHandler(func(context.Context, string, string, int64) (storage.UserMemory, error) {
		t.Error("unauthenticated request reached storage")
		return storage.UserMemory{}, nil
	})
	request := httptest.NewRequest("POST", "/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"SaveUserMemory","params":{"summary":"Memory","expected_version":0}}`))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	bridge.ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", result.Code)
	}
}
