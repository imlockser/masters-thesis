package rpc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"

	"masters-thesis/backend/internal/auth"
	"masters-thesis/backend/internal/storage"
)

const MemoryConflict jrpc2.Code = -32001

type SaveUserMemoryParams struct {
	Summary         string `json:"summary"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func NewHandler(save func(context.Context, string, string, int64) (storage.UserMemory, error), methods ...handler.Map) http.Handler {
	procedures := handler.Map{
		"SaveUserMemory": handler.New(func(ctx context.Context, request *jrpc2.Request) (storage.UserMemory, error) {
			var params SaveUserMemoryParams
			if err := request.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid SaveUserMemory parameters")
			}
			if strings.TrimSpace(params.Summary) == "" ||
				params.ExpectedVersion == nil || *params.ExpectedVersion < 0 {
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InvalidParams, "summary and a non-negative expected_version are required")
			}

			memory, err := save(ctx, auth.UserID(ctx), params.Summary, *params.ExpectedVersion)
			switch {
			case err == nil:
				return memory, nil
			case errors.Is(err, storage.ErrInvalidInput):
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid SaveUserMemory parameters")
			case errors.Is(err, storage.ErrMemoryConflict):
				return storage.UserMemory{}, jrpc2.Errorf(MemoryConflict, "User memory version conflict")
			default:
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InternalError, "Internal error")
			}
		}),
	}
	for _, extra := range methods {
		for name, method := range extra {
			procedures[name] = method
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserID(r.Context()) == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		// A shared bridge does not propagate each HTTP request's context to RPC.
		bridge := jhttp.NewBridge(procedures, &jhttp.BridgeOptions{
			Server: &jrpc2.ServerOptions{NewContext: func() context.Context { return r.Context() }},
		})
		defer bridge.Close()
		bridge.ServeHTTP(w, r)
	})
}
