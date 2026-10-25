package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) GetUserMemory(ctx context.Context, userID string) (UserMemory, error) {
	if err := validateUser(userID); err != nil {
		return UserMemory{}, err
	}

	memory := UserMemory{UserID: userID}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&memory).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return memory, nil
	}

	return memory, err
}

func (s *Store) SaveUserMemory(ctx context.Context, userID, summary string, expectedVersion int64) (UserMemory, error) {
	if err := validateUser(userID); err != nil {
		return UserMemory{}, err
	}

	summary = strings.TrimSpace(summary)
	if summary == "" || expectedVersion < 0 {
		return UserMemory{}, fmt.Errorf("%w: summary must be non-empty and version non-negative", ErrInvalidInput)
	}

	memory := UserMemory{UserID: userID, Summary: summary, Version: 1}
	var result *gorm.DB
	if expectedVersion == 0 {
		result = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&memory)
	} else {
		result = s.db.WithContext(ctx).Model(&memory).Clauses(clause.Returning{}).
			Where("user_id = ? AND version = ?", userID, expectedVersion).
			Updates(map[string]any{
				"summary":    summary,
				"version":    gorm.Expr("version + 1"),
				"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
			})
	}
	if result.Error != nil {
		return UserMemory{}, result.Error
	}
	if result.RowsAffected == 0 {
		return UserMemory{}, ErrMemoryConflict
	}

	return memory, nil
}
