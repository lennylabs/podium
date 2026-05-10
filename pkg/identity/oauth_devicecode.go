package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceCodeFlow implements the OAuth 2.0 Device Authorization Grant
// (RFC 8628), which §6.3 oauth-device-code prescribes for developer
// hosts that cannot complete a normal browser redirect flow.
//
// Surfaces:
//
//	flow := DeviceCodeFlow{
//	    DeviceAuthURL:    "https://idp.example.com/oauth2/device",
//	    TokenURL:         "https://idp.example.com/oauth2/token",
//	    ClientID:         "podium-cli",
//	    Scopes:           []string{"openid", "profile", "groups"},
//	    Audience:         "https://podium.example.com",
//	}
//	auth, err := flow.Initiate(ctx)            // shows code + URL
//	tokens, err := flow.Poll(ctx, auth)        // blocks until completion
//
// The flow is callable by both the CLI (which prints to stderr) and
// the MCP server (which surfaces via MCP elicitation).
type DeviceCodeFlow struct {
	// DeviceAuthURL is the IdP's device authorization endpoint
	// (RFC 8628 §3.1).
	DeviceAuthURL string
	// TokenURL is the IdP's token endpoint (RFC 8628 §3.4).
	TokenURL string
	// ClientID identifies the Podium CLI / MCP client to the IdP.
	ClientID string
	// ClientSecret is optional; some IdPs require it for device-code
	// clients (most do not).
	ClientSecret string
	// Scopes requested in the initial device authorization.
	Scopes []string
	// Audience optionally pins the resulting access token's `aud`
	// claim to a specific resource server.
	Audience string
	// Client is the HTTP client; defaults to http.DefaultClient.
	Client *http.Client
}

// DeviceAuth captures the IdP's response to the initial device
// authorization request (RFC 8628 §3.2).
type DeviceAuth struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	// VerificationURLComplete includes the user_code so the user only
	// needs to click; not every IdP returns this.
	VerificationURLComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// Tokens carries the IdP's eventual response (RFC 6749 §5.1) once
// the user completes the flow.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    time.Duration
	IDToken      string
}

// Errors returned by the flow.
var (
	// ErrAuthorizationPending matches the IdP's standard
	// "authorization_pending" response. Maps to
	// auth.device_code_pending in §6.10. Callers retain the
	// DeviceAuth and continue polling.
	ErrAuthorizationPending = errors.New("device-code: authorization_pending")
	// ErrSlowDown signals the IdP requested a slower polling cadence.
	// Callers add 5s to their interval (RFC 8628 §3.5).
	ErrSlowDown = errors.New("device-code: slow_down")
	// ErrAccessDenied signals the user denied the request.
	ErrAccessDenied = errors.New("device-code: access_denied")
	// ErrExpiredToken signals the device_code expired before the user
	// completed the flow.
	ErrExpiredToken = errors.New("device-code: expired_token")
)

// Initiate POSTs to the device authorization endpoint and returns the
// DeviceAuth payload. The caller surfaces VerificationURL and UserCode
// to the user.
func (f DeviceCodeFlow) Initiate(ctx context.Context) (*DeviceAuth, error) {
	if f.DeviceAuthURL == "" {
		return nil, errors.New("device-code: DeviceAuthURL is required")
	}
	form := url.Values{}
	form.Set("client_id", f.ClientID)
	if len(f.Scopes) > 0 {
		form.Set("scope", strings.Join(f.Scopes, " "))
	}
	if f.Audience != "" {
		form.Set("audience", f.Audience)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", f.DeviceAuthURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var envelope errorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		return nil, fmt.Errorf("device-code initiate: HTTP %d: %s",
			resp.StatusCode, envelope.Error)
	}
	var raw struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.DeviceCode == "" || raw.VerificationURI == "" {
		return nil, errors.New("device-code: malformed device_authorization response")
	}
	interval := time.Duration(raw.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second // RFC 8628 §3.2 default.
	}
	return &DeviceAuth{
		DeviceCode:              raw.DeviceCode,
		UserCode:                raw.UserCode,
		VerificationURL:         raw.VerificationURI,
		VerificationURLComplete: raw.VerificationURIComplete,
		ExpiresIn:               time.Duration(raw.ExpiresIn) * time.Second,
		Interval:                interval,
	}, nil
}

// PollOnce makes one token-endpoint request and returns Tokens or one
// of the ErrAuthorizationPending / ErrSlowDown / ErrAccessDenied /
// ErrExpiredToken sentinels.
func (f DeviceCodeFlow) PollOnce(ctx context.Context, auth *DeviceAuth) (*Tokens, error) {
	if f.TokenURL == "" {
		return nil, errors.New("device-code: TokenURL is required")
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", auth.DeviceCode)
	form.Set("client_id", f.ClientID)
	if f.ClientSecret != "" {
		form.Set("client_secret", f.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", f.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var raw struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in"`
			IDToken      string `json:"id_token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		if raw.AccessToken == "" {
			return nil, errors.New("device-code: token response missing access_token")
		}
		return &Tokens{
			AccessToken:  raw.AccessToken,
			RefreshToken: raw.RefreshToken,
			TokenType:    raw.TokenType,
			ExpiresIn:    time.Duration(raw.ExpiresIn) * time.Second,
			IDToken:      raw.IDToken,
		}, nil
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("device-code token: HTTP %d", resp.StatusCode)
	}
	switch envelope.Error {
	case "authorization_pending":
		return nil, ErrAuthorizationPending
	case "slow_down":
		return nil, ErrSlowDown
	case "access_denied":
		return nil, ErrAccessDenied
	case "expired_token":
		return nil, ErrExpiredToken
	default:
		return nil, fmt.Errorf("device-code token: %s — %s", envelope.Error, envelope.Description)
	}
}

// Refresh exchanges a refresh_token for a fresh access_token via
// RFC 6749 §6. The IdP may rotate the refresh token (some always
// do, some never do); the returned Tokens.RefreshToken carries the
// new value when present, otherwise the caller keeps the old one.
//
// Returns ErrAccessDenied when the IdP rejects the refresh
// (revoked, expired) so callers know to drop the cached token and
// drive the user through Initiate again.
func (f DeviceCodeFlow) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	if f.TokenURL == "" {
		return nil, errors.New("device-code: TokenURL is required")
	}
	if refreshToken == "" {
		return nil, errors.New("device-code: refresh_token is required")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", f.ClientID)
	if f.ClientSecret != "" {
		form.Set("client_secret", f.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", f.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var raw struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in"`
			IDToken      string `json:"id_token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		if raw.AccessToken == "" {
			return nil, errors.New("device-code: refresh response missing access_token")
		}
		// Carry the prior refresh token through when the IdP did
		// not rotate it.
		newRefresh := raw.RefreshToken
		if newRefresh == "" {
			newRefresh = refreshToken
		}
		return &Tokens{
			AccessToken:  raw.AccessToken,
			RefreshToken: newRefresh,
			TokenType:    raw.TokenType,
			ExpiresIn:    time.Duration(raw.ExpiresIn) * time.Second,
			IDToken:      raw.IDToken,
		}, nil
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("device-code refresh: HTTP %d", resp.StatusCode)
	}
	switch envelope.Error {
	case "invalid_grant", "access_denied":
		return nil, ErrAccessDenied
	default:
		return nil, fmt.Errorf("device-code refresh: %s — %s", envelope.Error, envelope.Description)
	}
}

// Poll keeps requesting the token endpoint until the user completes
// the flow, the device code expires, the user denies, or ctx is
// canceled. Honors slow_down by adding 5s to the polling interval per
// RFC 8628 §3.5.
func (f DeviceCodeFlow) Poll(ctx context.Context, auth *DeviceAuth) (*Tokens, error) {
	interval := auth.Interval
	for {
		tokens, err := f.PollOnce(ctx, auth)
		if err == nil {
			return tokens, nil
		}
		switch {
		case errors.Is(err, ErrAuthorizationPending):
			// Wait the prescribed interval and retry.
		case errors.Is(err, ErrSlowDown):
			interval += 5 * time.Second
		default:
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (f DeviceCodeFlow) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

// errorEnvelope matches the OAuth 2.0 error response (RFC 6749 §5.2).
type errorEnvelope struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
	URI         string `json:"error_uri,omitempty"`
}
