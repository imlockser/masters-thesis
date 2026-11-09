package provisioning

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "test-target-signing-key"
const testEvent = `{"aggregateID":"new-user","aggregateType":"user","resourceOwner":"test-org","instanceID":"test-instance","event_type":"user.human.added","userID":"actor-not-new-user","event_payload":{"email":"private@example.invalid"}}`

func signedHeader(body string, timestamp time.Time, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%d.%s", timestamp.Unix(), body)
	return fmt.Sprintf("t=%d,v1=%x", timestamp.Unix(), mac.Sum(nil))
}

func deliver(h http.Handler, body, signature string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/internal/zitadel/events", strings.NewReader(body))
	r.Header.Set("ZITADEL-Signature", signature)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func testHandler(issuer string) *Handler {
	return New(Config{Issuer: issuer, ProjectID: "test-project", OrganizationID: "test-org",
		InstanceID: "test-instance", SigningKey: testKey, PAT: "test-provisioner-pat"})
}

func TestWebhookValidation(t *testing.T) {
	var calls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer api.Close()
	h := testHandler(api.URL)
	for _, test := range []struct {
		name string
		body string
		key  string
		age  time.Duration
		want int
	}{
		{"wrong signature", testEvent, "wrong-key", 0, 401},
		{"expired signature", testEvent, testKey, -6 * time.Minute, 401},
		{"future signature", testEvent, testKey, 6 * time.Minute, 401},
		{"wrong organization", strings.Replace(testEvent, "test-org", "other-org", 1), testKey, 0, 403},
		{"wrong instance", strings.Replace(testEvent, "test-instance", "other-instance", 1), testKey, 0, 403},
		{"other event ignored", strings.Replace(testEvent, "user.human.added", "user.human.changed", 1), testKey, 0, 204},
		{"invalid JSON", "{", testKey, 0, 400},
		{"missing new user ID", strings.Replace(testEvent, `"aggregateID":"new-user",`, "", 1), testKey, 0, 400},
		{"wrong aggregate type", strings.Replace(testEvent, `"aggregateType":"user"`, `"aggregateType":"org"`, 1), testKey, 0, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := deliver(h, test.body, signedHeader(test.body, time.Now().Add(test.age), test.key))
			if response.Code != test.want {
				t.Fatalf("HTTP %d, want %d", response.Code, test.want)
			}
		})
	}
	for _, signature := range []string{"", "t=invalid,v1=abcd", "t=1,t=2,v1=abcd"} {
		if response := deliver(h, testEvent, signature); response.Code != 401 {
			t.Fatalf("invalid header accepted: %q", signature)
		}
	}
	if response := deliver(h, testEvent+" ", signedHeader(testEvent, time.Now(), testKey)); response.Code != 401 {
		t.Fatal("modified raw body accepted")
	}
	if response := deliver(h, strings.Repeat("x", (1<<20)+1), ""); response.Code != 413 {
		t.Fatal("oversized event accepted")
	}
	if response := deliver(testHandler(api.URL), testEvent, ""); response.Code != 401 || calls.Load() != 0 {
		t.Fatal("unverified event reached the API")
	}
	h.config.SigningKey = "PASTE_TARGET_SIGNING_KEY_HERE"
	if response := deliver(h, testEvent, ""); response.Code != 503 {
		t.Fatal("unconfigured webhook enabled")
	}
}

func TestProvisioningPreservesExistingRolesAndRetries(t *testing.T) {
	for _, test := range []struct {
		name        string
		role        string
		unavailable string
		conflict    bool
		wrongOwner  bool
		want        int
	}{
		{"new user and duplicate delivery", "", "", false, false, 204},
		{"viewer unchanged", "viewer", "", false, false, 204},
		{"developer unchanged", "developer", "", false, false, 204},
		{"admin unchanged", "admin", "", false, false, 204},
		{"custom role unchanged", "custom-role", "", false, false, 204},
		{"concurrent assignment and delayed visibility", "", "", true, false, 204},
		{"project API unavailable", "", "/management/v1/projects/test-project", false, false, 503},
		{"list API unavailable", "", authorizationService + "ListAuthorizations", false, false, 503},
		{"create API unavailable", "", authorizationService + "CreateAuthorization", false, false, 503},
		{"project owner mismatch", "", "", false, true, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			role := test.role
			creates, lists := 0, 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-provisioner-pat" || r.Header.Get("x-zitadel-orgid") != "test-org" {
					t.Error("incorrect API authentication or organization")
				}
				if r.URL.Path == test.unavailable {
					http.Error(w, "sensitive upstream response", 503)
					return
				}
				switch r.URL.Path {
				case "/management/v1/projects/test-project":
					if test.wrongOwner {
						fmt.Fprint(w, `{"project":{"id":"test-project","details":{"resourceOwner":"other-org"}}}`)
					} else {
						fmt.Fprint(w, `{"project":{"id":"test-project","details":{"resourceOwner":"test-org"}}}`)
					}
				case authorizationService + "ListAuthorizations":
					lists++
					var request struct {
						Filters []map[string]json.RawMessage `json:"filters"`
					}
					json.NewDecoder(r.Body).Decode(&request)
					if len(request.Filters) != 2 || string(request.Filters[0]["inUserIds"]) != `{"ids":["new-user"]}` ||
						string(request.Filters[1]["projectId"]) != `{"id":"test-project"}` {
						t.Error("query did not select the new user and configured project")
					}
					if role == "" || (test.conflict && lists == 2) {
						fmt.Fprint(w, `{"pagination":{},"authorizations":[]}`)
					} else {
						fmt.Fprintf(w, `{"pagination":{},"authorizations":[{"roles":[{"key":%q}]}]}`, role)
					}
				case authorizationService + "CreateAuthorization":
					creates++
					var request map[string]any
					json.NewDecoder(r.Body).Decode(&request)
					want := map[string]any{"userId": "new-user", "projectId": "test-project", "organizationId": "test-org", "roleKeys": []any{"viewer"}}
					if !reflect.DeepEqual(request, want) {
						t.Errorf("incorrect authorization: %v", request)
					}
					role = "viewer"
					if test.conflict {
						http.Error(w, `{"code":"already_exists"}`, 409)
					} else {
						fmt.Fprint(w, `{"id":"new-authorization"}`)
					}
				default:
					t.Errorf("unexpected API call: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer api.Close()
			h := testHandler(api.URL)
			for range 2 {
				response := deliver(h, testEvent, signedHeader(testEvent, time.Now(), testKey))
				if response.Code != test.want {
					t.Fatalf("HTTP %d, want %d", response.Code, test.want)
				}
				if strings.Contains(response.Body.String(), "sensitive") || strings.Contains(response.Body.String(), h.config.PAT) {
					t.Fatal("upstream details or secrets leaked")
				}
			}
			wantCreates := 0
			if test.role == "" {
				wantCreates = 1
			}
			if test.conflict {
				wantCreates = 2
			}
			if test.want == 204 && creates != wantCreates {
				t.Fatalf("unexpected create count: %d", creates)
			}
		})
	}
}

func TestConcurrentWebhookDeliveries(t *testing.T) {
	var mu sync.Mutex
	created := false
	assignments := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/management/v1/projects/test-project":
			fmt.Fprint(w, `{"project":{"id":"test-project","details":{"resourceOwner":"test-org"}}}`)
		case authorizationService + "ListAuthorizations":
			if created {
				fmt.Fprint(w, `{"pagination":{},"authorizations":[{"roles":[{"key":"viewer"}]}]}`)
			} else {
				fmt.Fprint(w, `{"pagination":{},"authorizations":[]}`)
			}
		case authorizationService + "CreateAuthorization":
			if created {
				w.WriteHeader(409)
				return
			}
			created = true
			assignments++
			fmt.Fprint(w, `{"id":"one-authorization"}`)
		default:
			t.Errorf("unexpected API path: %s", r.URL.Path)
		}
	}))
	defer api.Close()
	h := testHandler(api.URL)
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			if response := deliver(h, testEvent, signedHeader(testEvent, time.Now(), testKey)); response.Code != 204 {
				t.Errorf("concurrent callback returned HTTP %d", response.Code)
			}
		})
	}
	workers.Wait()
	mu.Lock()
	defer mu.Unlock()
	if assignments != 1 {
		t.Fatalf("expected exactly one assignment, got %d", assignments)
	}
}
