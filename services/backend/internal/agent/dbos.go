package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"masters-thesis/backend/internal/storage"
)

var ErrWorkflowFailed = errors.New("agent workflow failed")

type Request struct {
	UserID        string               `json:"user_id"`
	ChatID        string               `json:"chat_id"`
	Messages      []storage.LLMMessage `json:"messages"`
	MemoryVersion int64                `json:"memory_version"`
}

type Result struct {
	ChatID    string               `json:"chat_id"`
	Messages  []storage.LLMMessage `json:"messages"`
	Artifacts json.RawMessage      `json:"artifacts"`
}

type Queue interface {
	Enqueue(context.Context, string, Request) error
	Events(context.Context, string) (<-chan dbos.StreamValue[json.RawMessage], error)
	Result(context.Context, string) (Result, error)
}

type DBOSQueue struct {
	client dbos.Context
}

func NewDBOSQueue(ctx context.Context, databaseURL string) (*DBOSQueue, error) {
	if databaseURL == "" {
		return nil, errors.New("DBOS_SYSTEM_DATABASE_URL is required")
	}
	client, err := dbos.NewClient(ctx, dbos.ClientConfig{DatabaseURL: databaseURL, AppName: "energy-agent"})
	if err != nil {
		return nil, err
	}
	// The standalone SDK client implements Context for deriving cancellation without launching a worker.
	return &DBOSQueue{client: client.(dbos.Context)}, nil
}

func (q *DBOSQueue) Close() error {
	return dbos.Shutdown(q.client, 5*time.Second)
}

func (q *DBOSQueue) Enqueue(ctx context.Context, runID string, request Request) error {
	_, err := dbos.Enqueue[Result](dbos.From(q.client, ctx), "agent", "agent_request",
		dbos.PortableWorkflowArgs{PositionalArgs: []any{request}},
		dbos.WithEnqueueWorkflowID(runID),
	)
	return err
}

func (q *DBOSQueue) Events(ctx context.Context, runID string) (<-chan dbos.StreamValue[json.RawMessage], error) {
	return dbos.ReadStreamAsync[json.RawMessage](dbos.From(q.client, ctx), runID, "events")
}

func (q *DBOSQueue) Result(ctx context.Context, runID string) (Result, error) {
	handle, err := dbos.RetrieveWorkflow[Result](dbos.From(q.client, ctx), runID)
	if err != nil {
		return Result{}, err
	}
	result, err := handle.GetResult()
	if err != nil && ctx.Err() == nil {
		status, statusErr := handle.GetStatus()
		if statusErr == nil && status.Status != dbos.WorkflowStatusPending && status.Status != dbos.WorkflowStatusEnqueued && status.Status != dbos.WorkflowStatusSuccess {
			return Result{}, fmt.Errorf("%w: %s", ErrWorkflowFailed, status.Status)
		}
	}
	return result, err
}
