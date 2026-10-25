package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) CreateChat(ctx context.Context, userID, title string) (Chat, error) {
	if err := validateUser(userID); err != nil {
		return Chat{}, err
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return Chat{}, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	chat := Chat{UserID: userID, Title: title}
	err := s.db.WithContext(ctx).Create(&chat).Error
	return chat, err
}

func (s *Store) ListChats(ctx context.Context, userID string) ([]Chat, error) {
	if err := validateUser(userID); err != nil {
		return nil, err
	}

	chats := make([]Chat, 0)
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC, id DESC").Find(&chats).Error
	return chats, err
}

func (s *Store) GetChat(ctx context.Context, chatID, userID string) (ChatDetails, error) {
	if err := validateChat(chatID, userID); err != nil {
		return ChatDetails{}, err
	}

	details := ChatDetails{Messages: make([]Message, 0)}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("id = ? AND user_id = ?", chatID, userID).First(&details.Chat).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChatNotFound
		}
		if err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chatID).Order("id").Find(&details.Messages).Error; err != nil {
			return err
		}

		var active []string
		if err := tx.Model(&AgentRun{}).Where("chat_id = ? AND state = 'pending'", chatID).Pluck("id", &active).Error; err != nil {
			return err
		}
		if len(active) > 0 {
			details.ActiveRunID = active[0]
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return details, err
}

func (s *Store) DeleteChat(ctx context.Context, chatID, userID string) error {
	if err := validateChat(chatID, userID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chat Chat
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", chatID, userID).First(&chat).Error
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
		return tx.Delete(&chat).Error
	})
}

func (s *Store) DeleteAllChats(ctx context.Context, userID string) error {
	if err := validateUser(userID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chats []Chat
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Order("id").Find(&chats).Error; err != nil {
			return err
		}
		if len(chats) == 0 {
			return nil
		}

		ids := make([]string, 0, len(chats))
		for _, chat := range chats {
			ids = append(ids, chat.ID)
		}
		var active int64
		if err := tx.Model(&AgentRun{}).Where("chat_id IN ? AND state = 'pending'", ids).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return ErrChatBusy
		}
		return tx.Where("id IN ?", ids).Delete(&Chat{}).Error
	})
}

func (s *Store) RenameChat(ctx context.Context, chatID, userID, newTitle string) (Chat, error) {
	if err := validateChat(chatID, userID); err != nil {
		return Chat{}, err
	}

	newTitle = strings.TrimSpace(newTitle)
	if newTitle == "" {
		return Chat{}, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	var chat Chat
	result := s.db.WithContext(ctx).Model(&chat).Clauses(clause.Returning{}).
		Where("id = ? AND user_id = ?", chatID, userID).Update("title", newTitle)
	return chat, chatResult(result)
}
