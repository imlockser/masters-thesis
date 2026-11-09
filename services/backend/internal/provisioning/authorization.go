package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authorizationService = "/zitadel.authorization.v2.AuthorizationService/"

var errAPI = errors.New("ZITADEL provisioning API unavailable")

func (h *Handler) ensureViewer(ctx context.Context, userID string) error {
	var project struct {
		Project struct {
			ID      string `json:"id"`
			Details struct {
				ResourceOwner string `json:"resourceOwner"`
			} `json:"details"`
		} `json:"project"`
	}
	_, err := h.request(ctx, http.MethodGet, "/management/v1/projects/"+url.PathEscape(h.config.ProjectID), nil, &project)
	if err != nil || project.Project.ID != h.config.ProjectID || project.Project.Details.ResourceOwner != h.config.OrganizationID {
		return errAPI
	}

	for attempt := range 4 {
		var existing struct {
			Authorizations []json.RawMessage `json:"authorizations"`
			Pagination     *json.RawMessage  `json:"pagination"`
		}
		status, err := h.request(ctx, http.MethodPost, authorizationService+"ListAuthorizations", map[string]any{
			"pagination": map[string]int{"limit": 1},
			"filters": []any{
				map[string]any{"inUserIds": map[string]any{"ids": []string{userID}}},
				map[string]any{"projectId": map[string]string{"id": h.config.ProjectID}},
			},
		}, &existing)
		if err == nil {
			if existing.Pagination == nil {
				return errAPI
			}
			// Preserve every existing assignment, including inactive or custom roles.
			if len(existing.Authorizations) > 0 {
				return nil
			}
			var created struct {
				ID string `json:"id"`
			}
			status, err = h.request(ctx, http.MethodPost, authorizationService+"CreateAuthorization", map[string]any{
				"userId": userID, "projectId": h.config.ProjectID,
				"organizationId": h.config.OrganizationID, "roleKeys": []string{"viewer"},
			}, &created)
			if err == nil {
				if created.ID == "" {
					return errAPI
				}
				return nil
			}
		}
		// Re-read after conflicts or transient failures; never update an assignment.
		if attempt == 3 || (status != 0 && status != 400 && status != 404 && status != 409 && status != 429 && status < 500) {
			return errAPI
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond << attempt):
		}
	}
	return errAPI
}

func (h *Handler) request(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, errAPI
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.config.Issuer, "/")+path, body)
	if err != nil {
		return 0, errAPI
	}
	request.Header.Set("Authorization", "Bearer "+h.config.PAT)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("x-zitadel-orgid", h.config.OrganizationID)
	response, err := h.client.Do(request)
	if err != nil {
		return 0, errAPI
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, errAPI
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, output) != nil {
		return response.StatusCode, errAPI
	}
	return response.StatusCode, nil
}
