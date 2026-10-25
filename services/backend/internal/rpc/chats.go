package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"masters-thesis/backend/internal/storage"
)

type UserParams struct {
	UserID string `json:"user_id"`
}

type ChatParams struct {
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id"`
}

type CreateChatParams struct {
	UserID string `json:"user_id"`
	Title  string `json:"title"`
}

type RenameChatParams struct {
	ChatID   string `json:"chat_id"`
	UserID   string `json:"user_id"`
	NewTitle string `json:"new_title"`
}

func chatMethod[P, R any](call func(context.Context, P) (R, error)) handler.Func {
	return handler.New(func(ctx context.Context, request *jrpc2.Request) (R, error) {
		var params P
		if err := request.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
			var empty R
			return empty, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid chat parameters")
		}
		result, err := call(ctx, params)
		switch {
		case err == nil:
			return result, nil
		case errors.Is(err, storage.ErrInvalidInput):
			return result, jrpc2.Errorf(jrpc2.InvalidParams, "Invalid or missing chat parameters")
		case errors.Is(err, storage.ErrChatNotFound):
			return result, jrpc2.Errorf(ChatNotFound, "Chat not found")
		case errors.Is(err, storage.ErrChatBusy):
			return result, jrpc2.Errorf(ChatBusy, "Chat has an active agent run")
		default:
			return result, jrpc2.Errorf(jrpc2.InternalError, "Chat operation failed")
		}
	})
}

func ChatMethods(store *storage.Store) handler.Map {
	return handler.Map{
		"CreateChat": chatMethod(func(ctx context.Context, params CreateChatParams) (storage.Chat, error) {
			return store.CreateChat(ctx, params.UserID, params.Title)
		}),
		"ListChats": chatMethod(func(ctx context.Context, params UserParams) ([]storage.Chat, error) {
			return store.ListChats(ctx, params.UserID)
		}),
		"GetChat": chatMethod(func(ctx context.Context, params ChatParams) (storage.ChatDetails, error) {
			return store.GetChat(ctx, params.ChatID, params.UserID)
		}),
		"RenameChat": chatMethod(func(ctx context.Context, params RenameChatParams) (storage.Chat, error) {
			return store.RenameChat(ctx, params.ChatID, params.UserID, params.NewTitle)
		}),
		"DeleteChat": chatMethod(func(ctx context.Context, params ChatParams) (any, error) {
			return nil, store.DeleteChat(ctx, params.ChatID, params.UserID)
		}),
		"DeleteAllChats": chatMethod(func(ctx context.Context, params UserParams) (any, error) {
			return nil, store.DeleteAllChats(ctx, params.UserID)
		}),
	}
}
