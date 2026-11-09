package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

type rolesKey struct{}

func Roles(ctx context.Context) []string {
	roles, _ := ctx.Value(rolesKey{}).([]string)
	return slices.Clone(roles)
}

func HasRole(ctx context.Context, role string) bool {
	switch role {
	case "viewer", "developer", "admin":
		return slices.Contains(Roles(ctx), role)
	default:
		return false
	}
}

func RequireUserRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !HasRole(r.Context(), "viewer") && !HasRole(r.Context(), "developer") && !HasRole(r.Context(), "admin") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func projectRoles(body []byte, projectID string) ([]string, error) {
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	claim, ok := claims["urn:zitadel:iam:org:project:"+projectID+":roles"]
	if !ok {
		// Introspection scopes the legacy claim to the API application's project.
		claim, ok = claims["urn:zitadel:iam:org:project:roles"]
	}
	if !ok {
		return nil, nil
	}
	var assignments map[string]map[string]string
	if err := json.Unmarshal(claim, &assignments); err != nil {
		return nil, err
	}

	var roles []string
	for _, role := range []string{"viewer", "developer", "admin"} {
		for orgID, domain := range assignments[role] {
			if strings.TrimSpace(orgID) != "" && strings.TrimSpace(domain) != "" {
				roles = append(roles, role)
				break
			}
		}
	}
	return roles, nil
}
