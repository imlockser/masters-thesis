package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"masters-thesis/backend/internal/agent"
	"masters-thesis/backend/internal/storage"
)

const (
	ChatNotFound jrpc2.Code = -32002
	ChatBusy     jrpc2.Code = -32003
	RequestReuse jrpc2.Code = -32004
)

type SendMessageParams struct {
	RequestID string `json:"request_id"`
	ChatID    string `json:"chat_id"`
	UserID    string `json:"user_id"`
	Content   string `json:"content"`
}

func AgentMethods(service *agent.Service) handler.Map {
	return handler.Map{
		"SendMessage": handler.New(func(ctx context.Context, request *jrpc2.Request) (agent.StartedRun, error) {
			var params SendMessageParams
			if err := request.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
				return agent.StartedRun{}, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid SendMessage parameters")
			}
			result, err := service.SendMessage(ctx, params.RequestID, params.ChatID, params.UserID, params.Content)
			switch {
			case err == nil:
				return result, nil
			case errors.Is(err, storage.ErrInvalidInput):
				return agent.StartedRun{}, jrpc2.Errorf(jrpc2.InvalidParams, "request_id and chat_id must be UUIDs; user_id and content are required")
			case errors.Is(err, storage.ErrChatNotFound):
				return agent.StartedRun{}, jrpc2.Errorf(ChatNotFound, "Chat not found")
			case errors.Is(err, storage.ErrChatBusy):
				return agent.StartedRun{}, jrpc2.Errorf(ChatBusy, "Chat already has an active agent run")
			case errors.Is(err, storage.ErrRequestReuse):
				return agent.StartedRun{}, jrpc2.Errorf(RequestReuse, "request_id already used for another message")
			default:
				return agent.StartedRun{}, jrpc2.Errorf(jrpc2.InternalError, "Could not start agent run; retry with the same request_id")
			}
		}),
	}
}
