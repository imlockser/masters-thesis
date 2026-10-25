package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const HistoryTurns = 5

type RunPayload struct {
	UserID        string       `json:"user_id"`
	Messages      []LLMMessage `json:"messages"`
	MemorySummary string       `json:"memory_summary"`
	MemoryVersion int64        `json:"memory_version"`
}

type AgentRun struct {
	ID            string          `gorm:"type:uuid;primaryKey" json:"id"`
	ChatID        string          `json:"chat_id"`
	UserMessageID int64           `json:"user_message_id"`
	State         string          `gorm:"default:pending" json:"state"`
	Payload       RunPayload      `gorm:"type:jsonb;serializer:json" json:"-"`
	Artifacts     json.RawMessage `gorm:"type:jsonb;default:'[]'" json:"artifacts"`
	Error         string          `json:"error,omitempty"`
	CreatedAt     time.Time       `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
}

func (s *Store) PrepareRun(ctx context.Context, requestID, chatID, userID, content string) (AgentRun, error) {
	if err := validateChat(chatID, userID); err != nil {
		return AgentRun{}, err
	}
	if uuid.Validate(requestID) != nil || strings.TrimSpace(content) == "" {
		return AgentRun{}, fmt.Errorf("%w: request_id must be a UUID and content must be non-empty", ErrInvalidInput)
	}
	requestID = uuid.MustParse(requestID).String()
	chatID = uuid.MustParse(chatID).String()
	var run AgentRun

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chat Chat
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", chatID, userID).First(&chat).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatNotFound
		}
		if err != nil {
			return err
		}

		err = tx.Where("id = ?", requestID).First(&run).Error
		if err == nil {
			last := run.Payload.Messages[len(run.Payload.Messages)-1]
			encoded, _ := json.Marshal(content)
			if run.ChatID != chatID || run.Payload.UserID != userID || string(last.Content) != string(encoded) {
				return ErrRequestReuse
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var active int64
		if err := tx.Model(&AgentRun{}).Where("chat_id = ? AND state = 'pending'", chatID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return ErrChatBusy
		}

		store := &Store{db: tx}
		history, err := store.GetHistory(ctx, chatID, userID, HistoryTurns)
		if err != nil {
			return err
		}
		memory, err := store.GetUserMemory(ctx, userID)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(content)
		message := Message{ChatID: chatID, Role: "user", Payload: MessageContent{Content: encoded}}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}

		run = AgentRun{
			ID: requestID, ChatID: chatID, UserMessageID: message.ID,
			Payload: RunPayload{
				UserID: userID, MemorySummary: memory.Summary, MemoryVersion: memory.Version,
				Messages: append(history, LLMMessage{Role: "user", MessageContent: message.Payload}),
			},
		}
		return tx.Create(&run).Error
	})
	var constraint *pgconn.PgError
	if errors.As(err, &constraint) && constraint.Code == "23505" && constraint.ConstraintName == "agent_runs_pkey" {
		err = ErrRequestReuse
	}
	return run, err
}

func (s *Store) GetRun(ctx context.Context, runID, userID string) (AgentRun, error) {
	if uuid.Validate(runID) != nil || validateUser(userID) != nil {
		return AgentRun{}, ErrInvalidInput
	}
	var run AgentRun
	err := s.db.WithContext(ctx).Where("id = ? AND chat_id IN (SELECT id FROM chats WHERE user_id = ?)", runID, userID).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrRunNotFound
	}
	return run, err
}

func (s *Store) PendingRuns(ctx context.Context) ([]AgentRun, error) {
	var runs []AgentRun
	err := s.db.WithContext(ctx).Where("state = 'pending'").Find(&runs).Error
	return runs, err
}

func (s *Store) FinishRun(ctx context.Context, runID string, messages []LLMMessage, artifacts json.RawMessage, failure string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run AgentRun
		if err := tx.Where("id = ?", runID).First(&run).Error; err != nil {
			return err
		}
		var chat Chat
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.ChatID).First(&chat).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", runID).First(&run).Error; err != nil {
			return err
		}
		if run.State != "pending" {
			return nil
		}

		state := "failed"
		if failure == "" {
			if len(artifacts) > 0 {
				var entries []json.RawMessage
				if json.Unmarshal(artifacts, &entries) != nil || entries == nil {
					return fmt.Errorf("%w: artifacts must be a JSON array", ErrInvalidInput)
				}
			}
			if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || len(messages[len(messages)-1].ToolCalls) != 0 {
				return fmt.Errorf("%w: agent result must end with a final assistant message", ErrInvalidOrder)
			}
			var turn []Message
			if err := tx.Where("chat_id = ? AND id >= ?", run.ChatID, run.UserMessageID).Order("id").Find(&turn).Error; err != nil {
				return err
			}
			if len(turn) != 1 || turn[0].ID != run.UserMessageID || turn[0].Role != "user" {
				return ErrInvalidOrder
			}
			for _, message := range messages {
				if message.Role == "user" {
					return ErrInvalidOrder
				}
				if err := validateContent(message.Role, message.MessageContent); err != nil {
					return err
				}
				if err := validateOrder(turn, message.Role, message.MessageContent); err != nil {
					return err
				}
				stored := Message{ChatID: run.ChatID, Role: message.Role, Payload: message.MessageContent}
				if err := tx.Create(&stored).Error; err != nil {
					return err
				}
				turn = append(turn, stored)
			}
			state = "completed"
		}
		if len(artifacts) == 0 {
			artifacts = json.RawMessage(`[]`)
		}
		return tx.Model(&run).Updates(map[string]any{
			"state": state, "error": failure, "artifacts": artifacts, "completed_at": time.Now().UTC(),
		}).Error
	})
}
