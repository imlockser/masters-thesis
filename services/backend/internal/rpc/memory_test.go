package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"masters-thesis/backend/internal/storage"
)

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jrpc2.Error    `json:"error"`
}

func call(t *testing.T, h http.Handler, method, params string) response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":"request-1","method":"` + method + `","params":` + params + `}`
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}

	var result response
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.JSONRPC != "2.0" || string(result.ID) != `"request-1"` {
		t.Fatalf("invalid response envelope: %+v", result)
	}

	return result
}

func TestSaveUserMemoryProcedure(t *testing.T) {
	updatedAt := time.Now().UTC()
	invocations := 0
	bridge := NewHandler(func(ctx context.Context, userID, summary string, version int64) (storage.UserMemory, error) {
		invocations++
		if userID != "user-1" || summary != "Prefers solar." || version != 0 || ctx.Err() != nil {
			t.Errorf("incorrect arguments: %s, %s, %d, %v", userID, summary, version, ctx.Err())
		}

		return storage.UserMemory{UserID: userID, Summary: summary, Version: 1, UpdatedAt: updatedAt}, nil
	})
	defer bridge.Close()
	result := call(t, bridge, "SaveUserMemory", `{"user_id":"user-1","summary":"Prefers solar.","expected_version":0}`)
	if result.Error != nil || invocations != 1 {
		t.Fatalf("procedure failed: %+v, calls=%d", result, invocations)
	}

	var memory storage.UserMemory
	if err := json.Unmarshal(result.Result, &memory); err != nil {
		t.Fatal(err)
	}
	if memory.UserID != "user-1" || memory.Summary != "Prefers solar." || memory.Version != 1 || !memory.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("incorrect result: %+v", memory)
	}
}

func TestInvalidParamsDoNotSave(t *testing.T) {
	invocations := 0
	bridge := NewHandler(func(context.Context, string, string, int64) (storage.UserMemory, error) {
		invocations++
		return storage.UserMemory{}, nil
	})
	defer bridge.Close()
	for _, params := range []string{
		`{}`,
		`null`,
		`[]`,
		`{"user_id":"user","summary":"Summary"}`,
		`{"user_id":"user","summary":"Summary","expected_version":null}`,
		`{"user_id":"user","summary":"Summary","expected_version":-1}`,
		`{"user_id":"user","summary":"Summary","expected_version":"0"}`,
		`{"user_id":"user","summary":"Summary","expected_version":0.5}`,
		`{"user_id":"user","summary":"Summary","expected_version":0,"extra":true}`,
		`{"user_id":" ","summary":"Summary","expected_version":0}`,
		`{"user_id":"user","summary":" ","expected_version":0}`,
	} {
		t.Run(params, func(t *testing.T) {
			result := call(t, bridge, "SaveUserMemory", params)
			if result.Error == nil || result.Error.Code != jrpc2.InvalidParams {
				t.Fatalf("expected InvalidParams, got %+v", result)
			}
		})
	}
	if invocations != 0 {
		t.Fatalf("invalid requests reached storage %d times", invocations)
	}
}

func TestStorageErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code jrpc2.Code
	}{
		{"conflict", errors.Join(errors.New("wrapped"), storage.ErrMemoryConflict), MemoryConflict},
		{"invalid", storage.ErrInvalidInput, jrpc2.InvalidParams},
		{"database", errors.New("sensitive database connection details"), jrpc2.InternalError},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := NewHandler(func(context.Context, string, string, int64) (storage.UserMemory, error) {
				return storage.UserMemory{}, test.err
			})
			defer bridge.Close()
			result := call(t, bridge, "SaveUserMemory", `{"user_id":"user","summary":"Summary","expected_version":1}`)
			if result.Error == nil || result.Error.Code != test.code || len(result.Result) != 0 {
				t.Fatalf("unexpected error response: %+v", result)
			}
			if strings.Contains(result.Error.Message, "sensitive") || len(result.Error.Data) != 0 {
				t.Fatalf("database details leaked: %+v", result.Error)
			}
		})
	}
}

func TestInternalStorageMethodsAreNotExposed(t *testing.T) {
	bridge := NewHandler(func(context.Context, string, string, int64) (storage.UserMemory, error) {
		t.Error("unknown method reached storage")
		return storage.UserMemory{}, nil
	}, ChatMethods(&storage.Store{}))
	defer bridge.Close()
	for _, method := range []string{"GetUserMemory", "AddMessage", "GetHistory"} {
		result := call(t, bridge, method, `{}`)
		if result.Error == nil || result.Error.Code != jrpc2.MethodNotFound {
			t.Fatalf("unexpected response for %s: %+v", method, result)
		}
	}
}
