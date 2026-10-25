package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"masters-thesis/backend/internal/storage"
)

func TestRequestContextPreservesHistoryMemoryAndUTC(t *testing.T) {
	run := storage.AgentRun{
		ChatID: "chat", CreatedAt: time.Date(2026, 10, 25, 15, 0, 0, 0, time.FixedZone("local", 3600)),
		Payload: storage.RunPayload{
			UserID: "user", MemorySummary: "Prefers solar.", MemoryVersion: 4,
			Messages: []storage.LLMMessage{{Role: "user", MessageContent: storage.MessageContent{Content: json.RawMessage(`"Forecast"`)}}},
		},
	}
	request := prepareRequest(run)
	if request.UserID != "user" || request.ChatID != "chat" || request.MemoryVersion != 4 || len(request.Messages) != 3 {
		t.Fatalf("Incorrect queued request: %+v", request)
	}
	if !strings.Contains(string(request.Messages[0].Content), "2026-10-25T14:00:00Z") || !strings.Contains(string(request.Messages[1].Content), "Prefers solar.") || request.Messages[2].Role != "user" {
		t.Fatalf("Incorrect context ordering: %+v", request.Messages)
	}
	if len(run.Payload.Messages) != 1 {
		t.Fatal("Request construction modified the stored history")
	}
	run.Payload.MemorySummary = ""
	if request := prepareRequest(run); len(request.Messages) != 2 {
		t.Fatal("Empty memory should not create a context message")
	}
}
