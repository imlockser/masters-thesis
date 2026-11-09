package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

type userKey struct{}

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

func UserID(ctx context.Context) string {
	userID, _ := ctx.Value(userKey{}).(string)
	return userID
}

type Config struct {
	Issuer       string
	ProjectID    string
	ClientID     string
	ClientSecret string
}

type Authenticator struct {
	config Config
	client *http.Client
}

func New(config Config) (*Authenticator, error) {
	for _, value := range []string{config.Issuer, config.ProjectID, config.ClientID, config.ClientSecret} {
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "PASTE_") || strings.HasPrefix(value, "REPLACE_") {
			return nil, errors.New("ZITADEL_ISSUER, ZITADEL_PROJECT_ID, ZITADEL_CLIENT_ID and ZITADEL_CLIENT_SECRET must be configured")
		}
	}
	issuer, err := url.Parse(config.Issuer)
	if err != nil || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" ||
		(issuer.Scheme != "http" && issuer.Scheme != "https") {
		return nil, errors.New("ZITADEL_ISSUER must be an HTTP or HTTPS issuer URL")
	}

	return &Authenticator{config: config, client: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type audience []string

func (a *audience) UnmarshalJSON(value []byte) error {
	var values []string
	if err := json.Unmarshal(value, &values); err == nil {
		*a = values
		return nil
	}
	var single string
	if err := json.Unmarshal(value, &single); err != nil {
		return err
	}
	*a = []string{single}
	return nil
}

var (
	errInvalidToken = errors.New("invalid access token")
	bearerPattern   = regexp.MustCompile(`(?i)^Bearer +([a-z0-9._~+/-]+=*)$`)
)

func (a *Authenticator) introspect(ctx context.Context, token string) (string, []string, error) {
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.config.Issuer, "/")+"/oauth/v2/introspect", strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	// OAuth client_secret_basic form-encodes both credentials before Base64.
	request.SetBasicAuth(url.QueryEscape(a.config.ClientID), url.QueryEscape(a.config.ClientSecret))
	response, err := a.client.Do(request)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", nil, errors.New("introspection unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return "", nil, errors.New("invalid introspection response")
	}
	var claims struct {
		Active   *bool    `json:"active"`
		Issuer   string   `json:"iss"`
		Expires  int64    `json:"exp"`
		Audience audience `json:"aud"`
		Subject  string   `json:"sub"`
	}
	if err := json.Unmarshal(body, &claims); err != nil || claims.Active == nil {
		return "", nil, errors.New("invalid introspection response")
	}
	if !*claims.Active || claims.Issuer != a.config.Issuer || claims.Expires <= time.Now().Unix() ||
		!slices.Contains(claims.Audience, a.config.ProjectID) || claims.Subject == "" ||
		claims.Subject != strings.TrimSpace(claims.Subject) || len(claims.Subject) > 255 ||
		strings.IndexFunc(claims.Subject, unicode.IsControl) >= 0 {
		return "", nil, errInvalidToken
	}
	roles, err := projectRoles(body, a.config.ProjectID)
	if err != nil {
		return "", nil, errors.New("invalid introspection roles")
	}
	return claims.Subject, roles, nil
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		var parts []string
		if len(values) == 1 {
			parts = bearerPattern.FindStringSubmatch(values[0])
		}
		if len(parts) != 2 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		userID, roles, err := a.introspect(r.Context(), parts[1])
		if errors.Is(err, errInvalidToken) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx := context.WithValue(WithUserID(r.Context(), userID), rolesKey{}, roles)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
