package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) AddMessage(ctx context.Context, chatID, userID, role string, content MessageContent) (Message, error) {
	if err := validateChat(chatID, userID); err != nil {
		return Message{}, err
	}
	if err := validateContent(role, content); err != nil {
		return Message{}, err
	}

	message := Message{ChatID: chatID, Role: role, Payload: content}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chat Chat
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", chatID, userID).First(&chat).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatNotFound
		}
		if err != nil {
			return err
		}

		var active int64
		if err := tx.Model(&AgentRun{}).Where("chat_id = ? AND state = 'pending'", chatID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return ErrChatBusy
		}

		if role != "user" {
			var turn []Message
			err := tx.Where("chat_id = ? AND id >= (SELECT COALESCE(MAX(id), 0) FROM messages WHERE chat_id = ? AND role = 'user')", chatID, chatID).
				Order("id").Find(&turn).Error
			if err != nil {
				return err
			}
			if err := validateOrder(turn, role, content); err != nil {
				return err
			}
		}

		return tx.Create(&message).Error
	})
	return message, err
}

func (s *Store) GetHistory(ctx context.Context, chatID, userID string, n int) ([]LLMMessage, error) {
	if err := validateChat(chatID, userID); err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, fmt.Errorf("%w: n must be positive", ErrInvalidInput)
	}

	history := make([]LLMMessage, 0)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chat Chat
		err := tx.Where("id = ? AND user_id = ?", chatID, userID).First(&chat).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatNotFound
		}
		if err != nil {
			return err
		}

		var messages []Message
		// shortcut: user messages delimit turns; add turn IDs before allowing overlapping runs in one chat.
		err = tx.Raw(`
WITH turns AS (
    SELECT id AS start_id, LEAD(id) OVER (ORDER BY id) AS next_id
    FROM messages WHERE chat_id = ? AND role = 'user'
), completed AS (
    SELECT turns.* FROM turns
    JOIN LATERAL (
        SELECT role, payload FROM messages
        WHERE chat_id = ? AND id >= turns.start_id
          AND (turns.next_id IS NULL OR id < turns.next_id)
        ORDER BY id DESC LIMIT 1
    ) AS last_message ON true
    WHERE last_message.role = 'assistant'
      AND COALESCE(jsonb_array_length(last_message.payload -> 'tool_calls'), 0) = 0
    ORDER BY turns.start_id DESC LIMIT ?
)
SELECT messages.* FROM messages JOIN completed
    ON messages.id >= completed.start_id
   AND (completed.next_id IS NULL OR messages.id < completed.next_id)
WHERE messages.chat_id = ? ORDER BY messages.id
`, chatID, chatID, n, chatID).Find(&messages).Error
		if err != nil {
			return err
		}
		for _, message := range messages {
			history = append(history, LLMMessage{Role: message.Role, MessageContent: message.Payload})
		}

		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return history, err
}

func validateContent(role string, payload MessageContent) error {
	var content any
	if len(payload.Content) > 0 {
		if err := json.Unmarshal(payload.Content, &content); err != nil {
			return fmt.Errorf("%w: content must be valid JSON", ErrInvalidInput)
		}
	}

	hasContent := false
	switch value := content.(type) {
	case string:
		hasContent = strings.TrimSpace(value) != "" || role == "tool"
	case []any:
		hasContent = len(value) > 0
	case nil:
	default:
		return fmt.Errorf("%w: content must be text or a content-block array", ErrInvalidInput)
	}

	valid := false
	switch role {
	case "user":
		valid = hasContent && len(payload.ToolCalls) == 0 && payload.ToolCallID == ""
	case "assistant":
		valid = (hasContent || len(payload.ToolCalls) > 0) && payload.ToolCallID == ""
	case "tool":
		valid = hasContent && len(payload.ToolCalls) == 0 && strings.TrimSpace(payload.ToolCallID) != ""
	}
	if !valid {
		return fmt.Errorf("%w: content does not match message role", ErrInvalidInput)
	}

	seen := make(map[string]bool)
	for _, call := range payload.ToolCalls {
		var arguments map[string]json.RawMessage
		if strings.TrimSpace(call.ID) == "" || seen[call.ID] || call.Type != "function" ||
			strings.TrimSpace(call.Function.Name) == "" ||
			json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil {
			return fmt.Errorf("%w: invalid or duplicate tool call", ErrInvalidInput)
		}
		seen[call.ID] = true
	}

	return nil
}

func validateOrder(turn []Message, role string, content MessageContent) error {
	if len(turn) == 0 || turn[0].Role != "user" ||
		(turn[len(turn)-1].Role == "assistant" && len(turn[len(turn)-1].Payload.ToolCalls) == 0) {
		return fmt.Errorf("%w: start a new user turn first", ErrInvalidOrder)
	}

	pending := make(map[string]bool)
	seen := make(map[string]bool)
	for _, message := range turn {
		for _, call := range message.Payload.ToolCalls {
			pending[call.ID], seen[call.ID] = true, true
		}
		if message.Role == "tool" {
			delete(pending, message.Payload.ToolCallID)
		}
	}
	if role == "tool" {
		if !pending[content.ToolCallID] {
			return fmt.Errorf("%w: tool_call_id must match an unanswered call", ErrInvalidOrder)
		}

		return nil
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w: tool results are still missing", ErrInvalidOrder)
	}
	for _, call := range content.ToolCalls {
		if seen[call.ID] {
			return fmt.Errorf("%w: tool call ID already used in this turn", ErrInvalidOrder)
		}
	}

	return nil
}
