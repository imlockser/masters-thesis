package storage

import (
	"encoding/json"
	"time"
)

type Chat struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
}

type ChatDetails struct {
	Chat
	Messages    []Message `json:"messages"`
	ActiveRunID string    `json:"active_run_id,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type MessageContent struct {
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type Message struct {
	ID        int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID    string         `gorm:"type:uuid" json:"chat_id"`
	Role      string         `json:"role"`
	Payload   MessageContent `gorm:"type:jsonb;serializer:json" json:"payload"`
	CreatedAt time.Time      `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
}

type LLMMessage struct {
	Role string `json:"role"`
	MessageContent
}

type UserMemory struct {
	UserID    string    `gorm:"primaryKey" json:"user_id"`
	Summary   string    `json:"summary"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (UserMemory) TableName() string { return "user_memory" }
