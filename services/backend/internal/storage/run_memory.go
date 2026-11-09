package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrRunMemoryGrant = errors.New("invalid run memory grant")

type RunMemoryCallback struct {
	RunID           string `gorm:"type:uuid;primaryKey"`
	ExpectedVersion int64  `gorm:"primaryKey;autoIncrement:false"`
	SummaryHash     []byte
	Version         int64
}

func (s *Store) PrepareRunMemoryGrant(ctx context.Context, runID string, key []byte) (AgentRun, error) {
	var run AgentRun
	if len(key) != 32 {
		return run, ErrInvalidInput
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if run.State != "pending" || run.MemoryGrantNonce != "" {
			return nil
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		run.MemoryGrantNonce = hex.EncodeToString(nonce)
		// A public nonce makes enqueue recovery deterministic without storing the secret.
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(run.ID + ":" + run.MemoryGrantNonce))
		secret := hex.EncodeToString(mac.Sum(nil))
		digest := sha256.Sum256([]byte(secret))
		run.MemoryGrantHash = digest[:]
		expires := time.Now().UTC().Add(24 * time.Hour)
		run.MemoryGrantExpiresAt = &expires
		return tx.Model(&run).Updates(map[string]any{
			"memory_grant_nonce": run.MemoryGrantNonce, "memory_grant_hash": run.MemoryGrantHash,
			"memory_grant_expires_at": expires,
		}).Error
	})
	return run, err
}

func (s *Store) SaveRunMemory(ctx context.Context, runID, secret, summary string, expectedVersion int64) (UserMemory, error) {
	var memory UserMemory
	if uuid.Validate(runID) != nil || len(secret) != 64 {
		return memory, ErrRunMemoryGrant
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || expectedVersion < 0 {
		return memory, ErrInvalidInput
	}
	digest := sha256.Sum256([]byte(secret))
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run AgentRun
		if err := tx.First(&run, "id = ?", runID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRunMemoryGrant
			}
			return err
		}
		// Match completion/deletion lock order; a finished run cannot write memory.
		if subtle.ConstantTimeCompare(digest[:], run.MemoryGrantHash) != 1 {
			return ErrRunMemoryGrant
		}
		var chat Chat
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&chat, "id = ?", run.ChatID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRunMemoryGrant
			}
			return err
		}
		if err := tx.First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(digest[:], run.MemoryGrantHash) != 1 || run.State != "pending" ||
			run.MemoryGrantExpiresAt == nil || !time.Now().Before(*run.MemoryGrantExpiresAt) {
			return ErrRunMemoryGrant
		}
		summaryHash := sha256.Sum256([]byte(summary))
		var receipt RunMemoryCallback
		err := tx.First(&receipt, "run_id = ? AND expected_version = ?", run.ID, expectedVersion).Error
		if err == nil {
			if subtle.ConstantTimeCompare(summaryHash[:], receipt.SummaryHash) != 1 {
				return ErrMemoryConflict
			}
			memory = UserMemory{UserID: chat.UserID, Version: receipt.Version}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		memory, err = (&Store{db: tx}).SaveUserMemory(ctx, chat.UserID, summary, expectedVersion)
		if err != nil {
			return err
		}
		return tx.Create(&RunMemoryCallback{RunID: run.ID, ExpectedVersion: expectedVersion,
			SummaryHash: summaryHash[:], Version: memory.Version}).Error
	})
	return memory, err
}
