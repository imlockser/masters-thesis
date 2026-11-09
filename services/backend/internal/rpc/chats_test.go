package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"masters-thesis/backend/internal/storage"
)

func TestChatProceduresRejectInvalidParams(t *testing.T) {
	store := &storage.Store{}
	bridge := NewHandler(store.SaveUserMemory, ChatMethods(store))
	for _, test := range []struct {
		method string
		params string
	}{
		{"CreateChat", `{}`},
		{"CreateChat", `{"title":" "}`},
		{"CreateChat", `{"title":"Chat","extra":true}`},
		{"CreateChat", `{"title":42}`},
		{"ListChats", `[]`},
		{"ListChats", `{"limit":5}`},
		{"GetChat", `{"chat_id":"invalid"}`},
		{"RenameChat", `{"chat_id":"00000000-0000-0000-0000-000000000001","new_title":" "}`},
		{"DeleteChat", `{}`},
	} {
		t.Run(test.method+test.params, func(t *testing.T) {
			result := call(t, bridge, test.method, test.params)
			if result.Error == nil || result.Error.Code != jrpc2.InvalidParams {
				t.Fatalf("Expected invalid parameters: %+v", result)
			}
		})
	}
}

func TestChatErrorsHideStorageDetails(t *testing.T) {
	for _, test := range []struct {
		err  error
		code jrpc2.Code
	}{
		{errors.Join(storage.ErrChatNotFound, errors.New("private details")), ChatNotFound},
		{storage.ErrChatBusy, ChatBusy},
		{storage.ErrInvalidInput, jrpc2.InvalidParams},
		{errors.New("private details"), jrpc2.InternalError},
	} {
		bridge := NewHandler((&storage.Store{}).SaveUserMemory, handler.Map{
			"GetChat": chatMethod(func(context.Context, ChatParams) (storage.ChatDetails, error) {
				return storage.ChatDetails{}, test.err
			}),
		})
		result := call(t, bridge, "GetChat", `{"chat_id":"00000000-0000-0000-0000-000000000001"}`)
		if result.Error == nil || result.Error.Code != test.code || strings.Contains(result.Error.Message, "private") || len(result.Error.Data) != 0 {
			t.Fatalf("Unexpected error: %+v", result)
		}
	}
}
