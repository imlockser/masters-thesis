package provisioning

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Issuer         string
	ProjectID      string
	OrganizationID string
	InstanceID     string
	SigningKey     string
	PAT            string
}

type Handler struct {
	config Config
	client *http.Client
}

func New(config Config) *Handler {
	return &Handler{config: config, client: &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, value := range []string{h.config.Issuer, h.config.ProjectID, h.config.OrganizationID,
		h.config.InstanceID, h.config.SigningKey, h.config.PAT} {
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "PASTE_") || strings.HasPrefix(value, "REPLACE_") {
			http.Error(w, "Provisioning is not configured", http.StatusServiceUnavailable)
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Event too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Invalid event body", http.StatusBadRequest)
		}
		return
	}
	headers := r.Header.Values("ZITADEL-Signature")
	if len(headers) != 1 || !validSignature(body, headers[0], h.config.SigningKey, time.Now()) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var event struct {
		AggregateID   string `json:"aggregateID"`
		AggregateType string `json:"aggregateType"`
		ResourceOwner string `json:"resourceOwner"`
		InstanceID    string `json:"instanceID"`
		Type          string `json:"event_type"`
	}
	if json.Unmarshal(body, &event) != nil {
		http.Error(w, "Invalid event", http.StatusBadRequest)
		return
	}
	if event.Type != "user.human.added" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if event.ResourceOwner != h.config.OrganizationID || event.InstanceID != h.config.InstanceID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if event.AggregateType != "user" || event.AggregateID == "" ||
		event.AggregateID != strings.TrimSpace(event.AggregateID) || len(event.AggregateID) > 200 {
		http.Error(w, "Invalid user event", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := h.ensureViewer(ctx, event.AggregateID); err != nil {
		http.Error(w, "Provisioning unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validSignature(body []byte, header, key string, now time.Time) bool {
	var timestamp string
	var signatures [][]byte
	for _, part := range strings.Split(header, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return false
		}
		switch name {
		case "t":
			if timestamp != "" {
				return false
			}
			timestamp = value
		case "v1":
			if signature, err := hex.DecodeString(value); err == nil && len(signature) == sha256.Size {
				signatures = append(signatures, signature)
			}
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < now.Add(-5*time.Minute).Unix() || seconds > now.Add(5*time.Minute).Unix() {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(strconv.FormatInt(seconds, 10) + "."))
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		if hmac.Equal(expected, signature) {
			return true
		}
	}
	return false
}
