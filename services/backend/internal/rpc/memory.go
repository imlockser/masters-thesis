package rpc

import (
	"context"
	"errors"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"

	"masters-thesis/backend/internal/storage"
)

const MemoryConflict jrpc2.Code = -32001

type SaveUserMemoryParams struct {
	UserID          string `json:"user_id"`
	Summary         string `json:"summary"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func NewHandler(save func(context.Context, string, string, int64) (storage.UserMemory, error), methods ...handler.Map) jhttp.Bridge {
	procedures := handler.Map{
		"SaveUserMemory": handler.New(func(ctx context.Context, request *jrpc2.Request) (storage.UserMemory, error) {
			var params SaveUserMemoryParams
			if err := request.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid SaveUserMemory parameters")
			}
			if strings.TrimSpace(params.UserID) == "" || strings.TrimSpace(params.Summary) == "" ||
				params.ExpectedVersion == nil || *params.ExpectedVersion < 0 {
				return storage.UserMemory{}, jrpc2.Errorf(jrpc2.InvalidParams, "user_id, summary, and a non-negative expected_version are required")
			}

			memory, err := save(ctx, params.UserID, params.Summary, *params.ExpectedVersion)
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
	return jhttp.NewBridge(procedures, nil)
}
