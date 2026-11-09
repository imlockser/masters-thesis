package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"masters-thesis/backend/internal/storage"
)

func (s *Service) SaveRunMemory(w http.ResponseWriter, r *http.Request) {
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 || !strings.HasPrefix(headers[0], "Bearer ") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var request struct {
		RunID           string `json:"run_id"`
		Summary         string `json:"summary"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion == nil {
		http.Error(w, "Invalid memory callback", http.StatusBadRequest)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid memory callback", http.StatusBadRequest)
		return
	}
	memory, err := s.store.SaveRunMemory(r.Context(), request.RunID,
		strings.TrimPrefix(headers[0], "Bearer "), request.Summary, *request.ExpectedVersion)
	switch {
	case errors.Is(err, storage.ErrRunMemoryGrant):
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	case errors.Is(err, storage.ErrMemoryConflict):
		http.Error(w, "User memory version conflict", http.StatusConflict)
	case errors.Is(err, storage.ErrInvalidInput):
		http.Error(w, "Invalid memory callback", http.StatusBadRequest)
	case err != nil:
		http.Error(w, "Memory callback unavailable", http.StatusServiceUnavailable)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"user_id": memory.UserID, "version": memory.Version})
	}
}
