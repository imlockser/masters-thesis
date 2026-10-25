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
	defer bridge.Close()
	for _, test := range []struct {
		method string
		params string
	}{
		{"CreateChat", `{}`},
		{"CreateChat", `{"user_id":"user","title":" "}`},
		{"CreateChat", `{"user_id":"user","title":"Chat","extra":true}`},
		{"CreateChat", `{"user_id":"user","title":42}`},
		{"ListChats", `null`},
		{"ListChats", `[]`},
		{"ListChats", `{"user_id":" "}`},
		{"ListChats", `{"user_id":"user","limit":5}`},
		{"GetChat", `{"chat_id":"invalid","user_id":"user"}`},
		{"GetChat", `{"chat_id":"00000000-0000-0000-0000-000000000001"}`},
		{"RenameChat", `{"chat_id":"00000000-0000-0000-0000-000000000001","user_id":"user","new_title":" "}`},
		{"DeleteChat", `{}`},
		{"DeleteAllChats", `{"user_id":" "}`},
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
		result := call(t, bridge, "GetChat", `{"chat_id":"00000000-0000-0000-0000-000000000001","user_id":"user"}`)
		bridge.Close()
		if result.Error == nil || result.Error.Code != test.code || strings.Contains(result.Error.Message, "private") || len(result.Error.Data) != 0 {
			t.Fatalf("Unexpected error: %+v", result)
		}
	}
}
