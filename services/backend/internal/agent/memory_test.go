package agent

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryCallbackRejectsUnauthenticatedAndArbitraryRequests(t *testing.T) {
	service := &Service{}
	for _, test := range []struct {
		name, header, body string
		status             int
	}{
		{"missing grant", "", `{}`, 401},
		{"invalid grant", "Bearer invalid", `{"run_id":"00000000-0000-0000-0000-000000000001","summary":"Summary","expected_version":0}`, 401},
		{"arbitrary RPC", "Bearer invalid", `{"method":"DeleteAllChats","params":{}}`, 400},
		{"untrusted identity", "Bearer invalid", `{"run_id":"run","user_id":"other","summary":"Summary","expected_version":0}`, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/internal/agent/memory", strings.NewReader(test.body))
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			service.SaveRunMemory(response, request)
			if response.Code != test.status {
				t.Fatalf("expected %d, got %d", test.status, response.Code)
			}
		})
	}
}
