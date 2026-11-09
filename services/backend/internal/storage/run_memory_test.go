package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunMemoryGrantAndReplay(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	key := bytes.Repeat([]byte{0x11}, 32)
	newRun := func(userID string) (AgentRun, string) {
		chat := createChat(t, store, userID)
		run, err := store.PrepareRun(ctx, uuid.NewString(), chat.ID, userID, "Remember")
		if err != nil {
			t.Fatal(err)
		}
		run, err = store.PrepareRunMemoryGrant(ctx, run.ID, key)
		if err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(run.ID + ":" + run.MemoryGrantNonce))
		return run, hex.EncodeToString(mac.Sum(nil))
	}
	run, secret := newRun("user-1")
	recovered, err := store.PrepareRunMemoryGrant(ctx, run.ID, key)
	if err != nil || recovered.MemoryGrantNonce != run.MemoryGrantNonce || !bytes.Equal(recovered.MemoryGrantHash, run.MemoryGrantHash) {
		t.Fatal("enqueue recovery changed the grant")
	}
	digest := sha256.Sum256([]byte(secret))
	if !bytes.Equal(run.MemoryGrantHash, digest[:]) || bytes.Equal(run.MemoryGrantHash, []byte(secret)) {
		t.Fatal("grant was not stored as a hash")
	}
	other, _ := newRun("user-2")
	invalid, _ := hex.DecodeString(secret)
	invalid[0] ^= 1
	for _, target := range []struct{ id, token string }{{run.ID, hex.EncodeToString(invalid)}, {other.ID, secret}} {
		if _, err := store.SaveRunMemory(ctx, target.id, target.token, "Forbidden", 0); !errors.Is(err, ErrRunMemoryGrant) {
			t.Fatalf("invalid/cross-run grant accepted: %v", err)
		}
	}
	var workers sync.WaitGroup
	for range 5 {
		workers.Go(func() {
			memory, err := store.SaveRunMemory(ctx, run.ID, secret, "Summary", 0)
			if err != nil || memory.UserID != "user-1" || memory.Version != 1 {
				t.Errorf("callback failed: %v", err)
			}
		})
	}
	workers.Wait()
	if _, err := store.SaveRunMemory(ctx, run.ID, secret, "Different", 0); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	if _, err := store.SaveRunMemory(ctx, run.ID, secret, "Next", 1); err != nil {
		t.Fatal(err)
	}
	replay, err := store.SaveRunMemory(ctx, run.ID, secret, "Summary", 0)
	if err != nil || replay.Version != 1 {
		t.Fatal("replay did not return original result")
	}
	memory, _ := store.GetUserMemory(ctx, "user-1")
	if memory.Version != 2 || memory.Summary != "Next" {
		t.Fatal("replay duplicated or replaced memory")
	}
	expired := time.Now().Add(-time.Second)
	store.db.Model(&AgentRun{}).Where("id = ?", run.ID).Update("memory_grant_expires_at", expired)
	if _, err := store.SaveRunMemory(ctx, run.ID, secret, "Expired", 2); !errors.Is(err, ErrRunMemoryGrant) {
		t.Fatal("expired grant accepted")
	}
	if err := store.FinishRun(ctx, other.ID, nil, nil, "failed"); err != nil {
		t.Fatal(err)
	}
	otherMac := hmac.New(sha256.New, key)
	otherMac.Write([]byte(other.ID + ":" + other.MemoryGrantNonce))
	if _, err := store.SaveRunMemory(ctx, other.ID, hex.EncodeToString(otherMac.Sum(nil)), "Finished", 0); !errors.Is(err, ErrRunMemoryGrant) {
		t.Fatal("finished run accepted a callback")
	}
}
