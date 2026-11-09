package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func validClaims(issuer string) map[string]any {
	return map[string]any{
		"active": true, "iss": issuer, "exp": time.Now().Add(time.Hour).Unix(),
		"aud": []string{"398272774335889413", "other-client"}, "sub": "user-1",
	}
}

func newTestAuth(t *testing.T, server *httptest.Server) *Authenticator {
	t.Helper()
	a, err := New(Config{Issuer: server.URL, ProjectID: "398272774335889413",
		ClientID: "test-api@example", ClientSecret: "test-secret: &+"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestConfigurationMustBeComplete(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Issuer = "" },
		func(c *Config) { c.Issuer = "file:///tmp/issuer" },
		func(c *Config) { c.ProjectID = "" },
		func(c *Config) { c.ClientID = "PASTE_CLIENT_ID_HERE" },
		func(c *Config) { c.ClientSecret = "PASTE_CLIENT_SECRET_HERE" },
	} {
		config := Config{Issuer: "http://localhost:8080", ProjectID: "test-project", ClientID: "test-client", ClientSecret: "test-secret"}
		change(&config)
		if _, err := New(config); err == nil {
			t.Fatal("incomplete authentication configuration was accepted")
		}
	}
}

func TestMiddlewareClaims(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		status int
	}{
		{"valid array audience", func(map[string]any) {}, 200},
		{"valid string audience", func(c map[string]any) { c["aud"] = "398272774335889413" }, 200},
		{"inactive", func(c map[string]any) { c["active"] = false }, 401},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() }, 401},
		{"expiry boundary", func(c map[string]any) { c["exp"] = time.Now().Unix() }, 401},
		{"missing expiry", func(c map[string]any) { delete(c, "exp") }, 401},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://other.invalid" }, 401},
		{"wrong audience", func(c map[string]any) { c["aud"] = []string{"other-project"} }, 401},
		{"missing audience", func(c map[string]any) { delete(c, "aud") }, 401},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }, 401},
		{"blank subject", func(c map[string]any) { c["sub"] = " " }, 401},
		{"invalid subject", func(c map[string]any) { c["sub"] = "user\n1" }, 401},
		{"missing active", func(c map[string]any) { delete(c, "active") }, 503},
		{"malformed audience", func(c map[string]any) { c["aud"] = 123 }, 503},
		{"malformed active", func(c map[string]any) { c["active"] = "true" }, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			var issuer string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/oauth/v2/introspect" {
					t.Errorf("wrong introspection endpoint: %s %s", r.Method, r.URL.Path)
				}
				username, password, ok := r.BasicAuth()
				if !ok || username != url.QueryEscape("test-api@example") || password != url.QueryEscape("test-secret: &+") {
					t.Error("incorrect OAuth Basic credentials")
				}
				if r.ParseForm() != nil || r.Form.Get("token") != "test-access-token" || r.Form.Get("token_type_hint") != "access_token" {
					t.Error("incorrect introspection form")
				}
				claims := validClaims(issuer)
				test.change(claims)
				json.NewEncoder(w).Encode(claims)
			}))
			defer server.Close()
			issuer = server.URL
			a := newTestAuth(t, server)
			called := false
			h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if UserID(r.Context()) != "user-1" {
					t.Error("verified subject was not placed in context")
				}
			}))
			request := httptest.NewRequest("POST", "/rpc", nil)
			request = request.WithContext(WithUserID(request.Context(), "untrusted-user"))
			request.Header.Set("Authorization", "Bearer test-access-token")
			result := httptest.NewRecorder()
			h.ServeHTTP(result, request)
			if result.Code != test.status || called != (test.status == 200) {
				t.Fatalf("HTTP %d, handler=%v; expected HTTP %d", result.Code, called, test.status)
			}
			if test.status == 401 && result.Header().Get("WWW-Authenticate") == "" {
				t.Error("missing Bearer challenge")
			}
		})
	}
}

func TestMiddlewareRejectsMalformedBearer(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	h := newTestAuth(t, server).Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") }))
	for _, header := range []string{"", "Basic abc", "Bearer", "Bearer ", "Bearer a b", "Bearer a,b", "Bearer\ttoken", "Bearer a\nb", "Bearer invalid!"} {
		t.Run(header, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/rpc", nil)
			request.Header.Set("Authorization", header)
			result := httptest.NewRecorder()
			h.ServeHTTP(result, request)
			if result.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", result.Code)
			}
		})
	}
	request := httptest.NewRequest("GET", "/agent/runs/run-1/events", nil)
	request.Header.Add("Authorization", "Bearer first")
	request.Header.Add("Authorization", "Bearer second")
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	if result.Code != 401 || calls.Load() != 0 {
		t.Fatal("invalid headers reached introspection")
	}
}

func TestIntrospectionFailuresAre503(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream unavailable", 503, "sensitive response"},
		{"invalid API credentials", 401, "test-secret: &+"},
		{"redirect", 302, "redirect"},
		{"invalid JSON", 200, "not JSON"},
		{"null", 200, "null"},
		{"missing active", 200, "{}"},
		{"trailing JSON", 200, `{"active":false}{}`},
		{"oversized", 200, strings.Repeat("x", (1<<20)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/should-not-follow")
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			h := newTestAuth(t, server).Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") }))
			request := httptest.NewRequest("POST", "/rpc", nil)
			request.Header.Set("Authorization", "Bearer test-access-token")
			result := httptest.NewRecorder()
			h.ServeHTTP(result, request)
			if result.Code != 503 || result.Body.String() != "Authentication service unavailable\n" {
				t.Fatalf("unexpected error: %d %s", result.Code, result.Body.String())
			}
		})
	}
}

func TestUnavailableIntrospection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	a := newTestAuth(t, server)
	server.Close()
	request := httptest.NewRequest("POST", "/rpc", nil)
	request.Header.Set("Authorization", "Bearer test-access-token")
	result := httptest.NewRecorder()
	a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") })).ServeHTTP(result, request)
	if result.Code != 503 {
		t.Fatalf("expected 503, got %d", result.Code)
	}
}

func TestIntrospectionTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"timeout", "request cancellation"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-r.Context().Done():
				case <-time.After(time.Second):
				}
			}))
			defer upstream.Close()
			a := newTestAuth(t, upstream)
			a.client.Timeout = 50 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest("POST", "/rpc", nil).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer test-access-token")
			result := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") })).ServeHTTP(result, request)
				close(done)
			}()
			<-started
			if mode == "request cancellation" {
				cancel()
			}
			select {
			case <-done:
				if result.Code != 503 {
					t.Fatalf("expected 503, got %d", result.Code)
				}
			case <-time.After(time.Second):
				t.Fatal("introspection did not respect timeout/cancellation")
			}
		})
	}
}

func TestStreamingFlushAndCancellation(t *testing.T) {
	var issuer string
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(validClaims(issuer))
	}))
	defer introspection.Close()
	issuer = introspection.URL
	closed := make(chan struct{})
	h := newTestAuth(t, introspection).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) != "user-1" || r.Header.Get("Last-Event-ID") != "0" {
			t.Error("stream identity or resume cursor lost")
		}
		if _, deadline := r.Context().Deadline(); deadline {
			t.Error("introspection timeout leaked into stream context")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": connected\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
		close(closed)
	}))
	server := httptest.NewServer(h)
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL+"/agent/runs/run-1/events", nil)
	request.Header.Set("Authorization", "Bearer test-access-token")
	request.Header.Set("Last-Event-ID", "0")
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("stream returned HTTP %d", response.StatusCode)
	}
	response.Body.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stream request was not cancelled")
	}
}
