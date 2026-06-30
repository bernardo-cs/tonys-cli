package toniecloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default OpenID Connect endpoint and client used by the official my tonies app.
const (
	DefaultTokenURL = "https://login.tonies.com/auth/realms/tonies/protocol/openid-connect/token"
	DefaultClientID = "my-tonies"
	defaultScope    = "openid"
	clockSkew       = 30 * time.Second
)

// Authenticator turns a username/password into a bearer token, transparently
// caching it and refreshing via the OpenID refresh_token grant when possible so
// that repeated CLI invocations (e.g. from a bot) do not hammer the SSO server.
type Authenticator struct {
	TokenURL string
	ClientID string
	Username string
	Password string

	HTTP  *http.Client
	Cache *TokenCache // optional; nil disables persistence

	// now is overridable in tests.
	now func() time.Time
}

func (a *Authenticator) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *Authenticator) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *Authenticator) tokenURL() string {
	if a.TokenURL != "" {
		return a.TokenURL
	}
	return DefaultTokenURL
}

func (a *Authenticator) clientID() string {
	if a.ClientID != "" {
		return a.ClientID
	}
	return DefaultClientID
}

// tokenResponse mirrors a Keycloak token endpoint reply.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`

	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// resolveKey decides which cache entry this authenticator maps to.
func (a *Authenticator) resolveKey() string {
	if k := normalizeUser(a.Username); k != "" {
		return k
	}
	if a.Cache != nil {
		if only, ok := a.Cache.SoleKey(); ok {
			return only
		}
	}
	return ""
}

func normalizeUser(u string) string {
	return strings.ToLower(strings.TrimSpace(u))
}

// NormalizeUser returns the cache-key form of a username (lowercased, trimmed).
// Exposed so the CLI computes identical keys.
func NormalizeUser(u string) string {
	return normalizeUser(u)
}

func (a *Authenticator) valid(t time.Time) bool {
	return !t.IsZero() && t.After(a.clock().Add(clockSkew))
}

// Token returns a valid bearer token, acquiring or refreshing one as needed.
func (a *Authenticator) Token(ctx context.Context) (string, error) {
	key := a.resolveKey()

	if a.Cache != nil && key != "" {
		if e, ok := a.Cache.Get(key); ok {
			if a.valid(e.ExpiresAt) {
				return e.AccessToken, nil
			}
			if e.RefreshToken != "" && a.valid(e.RefreshExpiresAt) {
				if tok, err := a.grant(ctx, url.Values{
					"grant_type":    {"refresh_token"},
					"client_id":     {a.clientID()},
					"refresh_token": {e.RefreshToken},
					"scope":         {defaultScope},
				}); err == nil {
					a.store(key, tok)
					return tok.AccessToken, nil
				}
				// Refresh failed (e.g. revoked); fall through to password grant.
			}
		}
	}

	if a.Username == "" || a.Password == "" {
		return "", ErrNotAuthenticated
	}

	tok, err := a.grant(ctx, url.Values{
		"grant_type": {"password"},
		"client_id":  {a.clientID()},
		"scope":      {defaultScope},
		"username":   {a.Username},
		"password":   {a.Password},
	})
	if err != nil {
		return "", err
	}
	if a.Cache != nil {
		a.store(normalizeUser(a.Username), tok)
	}
	return tok.AccessToken, nil
}

// Login forces a fresh password grant (ignoring any cached access token) and
// persists the result. Used by `tonys auth login`.
func (a *Authenticator) Login(ctx context.Context) (TokenEntry, error) {
	if a.Username == "" || a.Password == "" {
		return TokenEntry{}, ErrNotAuthenticated
	}
	tok, err := a.grant(ctx, url.Values{
		"grant_type": {"password"},
		"client_id":  {a.clientID()},
		"scope":      {defaultScope},
		"username":   {a.Username},
		"password":   {a.Password},
	})
	if err != nil {
		return TokenEntry{}, err
	}
	entry := a.entryFor(tok)
	if a.Cache != nil {
		a.Cache.Put(normalizeUser(a.Username), entry)
	}
	return entry, nil
}

func (a *Authenticator) store(key string, tok *tokenResponse) {
	e := a.entryFor(tok)
	if e.Username == "" {
		// No username was supplied (sole-key cache reuse); keep the entry's
		// recorded identity rather than blanking it on refresh.
		e.Username = key
	}
	a.Cache.Put(key, e)
}

func (a *Authenticator) entryFor(tok *tokenResponse) TokenEntry {
	now := a.clock()
	e := TokenEntry{
		Username:     normalizeUser(a.Username),
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
	}
	if tok.ExpiresIn > 0 {
		e.ExpiresAt = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	} else if exp, ok := jwtExpiry(tok.AccessToken); ok {
		e.ExpiresAt = exp
	}
	if tok.RefreshExpiresIn > 0 {
		e.RefreshExpiresAt = now.Add(time.Duration(tok.RefreshExpiresIn) * time.Second)
	}
	return e
}

// grant performs a form-encoded POST to the token endpoint.
func (a *Authenticator) grant(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tok tokenResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &tok)
	}
	if resp.StatusCode/100 != 2 {
		detail := tok.ErrorDescription
		if detail == "" {
			detail = tok.Error
		}
		return nil, &APIError{Method: http.MethodPost, URL: a.tokenURL(), Status: resp.StatusCode, Detail: detail, Body: string(body)}
	}
	if tok.AccessToken == "" {
		return nil, &APIError{Method: http.MethodPost, URL: a.tokenURL(), Status: resp.StatusCode, Detail: "token endpoint returned no access_token", Body: string(body)}
	}
	return &tok, nil
}

// jwtExpiry extracts the exp claim from a JWT without verifying its signature.
func jwtExpiry(tokenStr string) (time.Time, bool) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}
