package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"masters-thesis/backend/migrations"
)

var (
	ErrChatNotFound   = errors.New("chat not found")
	ErrInvalidInput   = errors.New("invalid input")
	ErrInvalidOrder   = errors.New("invalid message order")
	ErrMemoryConflict = errors.New("user memory version conflict")
	ErrRunNotFound    = errors.New("agent run not found")
	ErrChatBusy       = errors.New("chat already has an active agent run")
	ErrRequestReuse   = errors.New("request_id already used for another message")
)

type Store struct {
	db *gorm.DB
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidInput)
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
	if err != nil {
		return nil, err
	}

	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(5)
	pool.SetConnMaxLifetime(time.Hour)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}

	return pool.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}

	return migrations.Up(ctx, pool)
}

func validateUser(userID string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("%w: user_id is required", ErrInvalidInput)
	}

	return nil
}

func validateChat(chatID, userID string) error {
	if err := validateUser(userID); err != nil {
		return err
	}
	if err := uuid.Validate(chatID); err != nil {
		return fmt.Errorf("%w: chat_id must be a UUID", ErrInvalidInput)
	}

	return nil
}

func chatResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrChatNotFound
	}

	return nil
}
