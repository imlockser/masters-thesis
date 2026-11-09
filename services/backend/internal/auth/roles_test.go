package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMiddlewareRoles(t *testing.T) {
	const generic = "urn:zitadel:iam:org:project:roles"
	const project = "urn:zitadel:iam:org:project:398272774335889413:roles"
	const foreign = "urn:zitadel:iam:org:project:other-project:roles"
	assignment := map[string]string{"test-org": "org.invalid"}
	viewer := map[string]any{"viewer": assignment}
	admin := map[string]any{"admin": assignment}
	for _, test := range []struct {
		name   string
		claims map[string]any
		want   []string
		status int
	}{
		{"project roles", map[string]any{project: map[string]any{"viewer": assignment, "developer": assignment, "admin": assignment}}, []string{"viewer", "developer", "admin"}, 200},
		{"legacy roles", map[string]any{generic: viewer}, []string{"viewer"}, 200},
		{"developer has no implicit viewer", map[string]any{project: map[string]any{"developer": assignment}}, []string{"developer"}, 200},
		{"admin has no implicit roles", map[string]any{project: admin}, []string{"admin"}, 200},
		{"missing roles", nil, nil, 200},
		{"foreign project ignored", map[string]any{foreign: admin}, nil, 200},
		{"foreign roles not merged", map[string]any{project: viewer, foreign: admin}, []string{"viewer"}, 200},
		{"scoped claim takes precedence", map[string]any{project: viewer, generic: admin}, []string{"viewer"}, 200},
		{"empty scoped claim takes precedence", map[string]any{project: map[string]any{}, generic: admin}, nil, 200},
		{"unknown role ignored", map[string]any{project: map[string]any{"owner": assignment}}, nil, 200},
		{"empty assignment ignored", map[string]any{project: map[string]any{"admin": map[string]string{}}}, nil, 200},
		{"invalid organization ignored", map[string]any{project: map[string]any{"admin": map[string]string{"": "org.invalid"}}}, nil, 200},
		{"null roles", map[string]any{project: nil}, nil, 200},
		{"malformed roles", map[string]any{project: []string{"admin"}}, nil, 503},
		{"malformed assignment", map[string]any{project: map[string]any{"admin": true}}, nil, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			var issuer string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims := validClaims(issuer)
				for key, value := range test.claims {
					claims[key] = value
				}
				json.NewEncoder(w).Encode(claims)
			}))
			defer server.Close()
			issuer = server.URL
			called := false
			h := newTestAuth(t, server).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if UserID(r.Context()) != "user-1" || !slices.Equal(Roles(r.Context()), test.want) {
					t.Fatalf("unexpected identity: %q, roles: %v", UserID(r.Context()), Roles(r.Context()))
				}
				for _, role := range []string{"viewer", "developer", "admin", "owner", "ADMIN", ""} {
					if HasRole(r.Context(), role) != slices.Contains(test.want, role) {
						t.Errorf("incorrect role check for %q", role)
					}
				}
				roles := Roles(r.Context())
				if len(roles) > 0 {
					roles[0] = "forged"
					if !slices.Equal(Roles(r.Context()), test.want) {
						t.Error("caller mutated verified roles")
					}
				}
			}))
			request := httptest.NewRequest("POST", "/rpc?roles=admin", strings.NewReader(`{"roles":["admin"]}`))
			request.Header.Set("Authorization", "Bearer test-access-token")
			request.Header.Set("X-Roles", "admin")
			request = request.WithContext(context.WithValue(request.Context(), rolesKey{}, []string{"admin"}))
			result := httptest.NewRecorder()
			h.ServeHTTP(result, request)
			if result.Code != test.status || called != (test.status == 200) {
				t.Fatalf("HTTP %d, handler=%v; want HTTP %d", result.Code, called, test.status)
			}
		})
	}
	if Roles(context.Background()) != nil || HasRole(context.Background(), "admin") {
		t.Fatal("context without verified roles granted access")
	}
}

func TestRequireUserRoleForRPCAndSSE(t *testing.T) {
	for _, test := range []struct {
		role    string
		project string
		status  int
	}{
		{"viewer", "398272774335889413", 200},
		{"developer", "398272774335889413", 200},
		{"admin", "398272774335889413", 200},
		{"", "398272774335889413", 403},
		{"owner", "398272774335889413", 403},
		{"admin", "other-project", 403},
	} {
		t.Run(test.project+"/"+test.role, func(t *testing.T) {
			var issuer string
			var introspections atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				introspections.Add(1)
				claims := validClaims(issuer)
				if test.role != "" {
					claims["urn:zitadel:iam:org:project:"+test.project+":roles"] = map[string]map[string]string{
						test.role: {"test-org": "org.invalid"},
					}
				}
				json.NewEncoder(w).Encode(claims)
			}))
			defer server.Close()
			issuer = server.URL
			calls := 0
			h := newTestAuth(t, server).Middleware(RequireUserRole(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if _, ok := w.(http.Flusher); !ok {
					t.Error("SSE flushing support lost")
				}
				w.Write([]byte(`{"result":"unchanged"}`))
			})))
			for _, route := range []struct{ method, path string }{
				{"POST", "/rpc"},
				{"GET", "/agent/runs/test-run/events"},
			} {
				request := httptest.NewRequest(route.method, route.path+"?roles=admin", nil)
				request.Header.Set("Authorization", "Bearer test-access-token")
				request.Header.Set("X-Roles", "admin")
				result := httptest.NewRecorder()
				h.ServeHTTP(result, request)
				if result.Code != test.status {
					t.Fatalf("%s: HTTP %d, want %d", route.path, result.Code, test.status)
				}
				if test.status == 200 && result.Body.String() != `{"result":"unchanged"}` {
					t.Fatal("successful response changed")
				}
			}
			if (calls == 2) != (test.status == 200) || introspections.Load() != 2 {
				t.Fatalf("handler calls=%d, introspections=%d", calls, introspections.Load())
			}
		})
	}
}
