package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"masters-thesis/backend/internal/storage"
)

func (s *Service) Stream(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.Context(), r.PathValue("run_id"), r.URL.Query().Get("user_id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrRunNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, storage.ErrInvalidInput) {
			status = http.StatusBadRequest
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	lastID := -1
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		lastID, err = strconv.Atoi(value)
		if err != nil || lastID < 0 {
			http.Error(w, "Invalid Last-Event-ID", http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events, err := s.queue.Events(ctx, run.ID)
	if err != nil {
		http.Error(w, "Agent stream unavailable", http.StatusServiceUnavailable)
		return
	}
	if run.State == "pending" {
		s.watch(run)
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "Agent stream unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil || controller.Flush() != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	nextSequence := 0
	streamError := func() {
		fmt.Fprint(w, "event: stream_error\ndata: {\"type\":\"stream_error\",\"error\":\"Agent stream unavailable; reconnect to resume.\"}\n\n")
		controller.Flush()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case value, open := <-events:
			if value.Err != nil {
				streamError()
				return
			}
			if !open || value.Closed {
				if err := s.finalize(ctx, run); err != nil {
					streamError()
					return
				}
				if nextSequence > lastID {
					fmt.Fprintf(w, "id: %d\nevent: run_failed\ndata: {\"sequence\":%d,\"type\":\"run_failed\",\"error\":\"MissingTerminalEvent\"}\n\n", nextSequence, nextSequence)
					controller.Flush()
				}
				return
			}

			var event struct {
				Sequence int    `json:"sequence"`
				Type     string `json:"type"`
			}
			if json.Unmarshal(value.Value, &event) != nil || event.Sequence != nextSequence || event.Type == "" {
				streamError()
				return
			}
			nextSequence++
			terminal := event.Type == "run_completed" || event.Type == "run_failed"
			if terminal {
				if err := s.finalize(ctx, run); err != nil {
					streamError()
					return
				}
				stored, err := s.store.GetRun(ctx, run.ID, run.Payload.UserID)
				if err != nil {
					streamError()
					return
				}
				if stored.State == "failed" && event.Type == "run_completed" {
					event.Type = "run_failed"
					value.Value, _ = json.Marshal(map[string]any{"sequence": event.Sequence, "type": event.Type, "error": stored.Error})
				}
			}
			if event.Sequence > lastID {
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, value.Value); err != nil || controller.Flush() != nil {
					return
				}
			}
			if terminal {
				return
			}
		}
	}
}
