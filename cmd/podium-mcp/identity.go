package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
)

// deviceFlowMaxWait bounds the device-code poll so a tool call never blocks
// forever waiting for the user to complete authentication. RFC 8628 device
// codes typically expire within minutes; the bridge stops polling at the
// device code's own expiry or this ceiling, whichever is sooner.
const deviceFlowMaxWait = 10 * time.Minute

// bearerToken returns the credential to attach to the next registry call,
// per the selected identity provider (§6.2 / §6.3):
//
//   - injected-session-token: the runtime-managed JWT read fresh from the
//     env var or file on every call (§6.3.2.1).
//   - oauth-device-code (default): a token cached in the OS keychain, or
//     one acquired via the RFC 8628 device flow when an IdP authorization
//     endpoint is configured and no cached token exists. With no IdP
//     configured the bridge falls back to any injected token, otherwise it
//     sends no credential so an anonymous registry still answers.
//   - any other value: treated as a label; the bridge sends whatever
//     injected token the host supplied.
func (s *mcpServer) bearerToken() (string, error) {
	switch s.cfg.identityProvider {
	case "oauth-device-code", "":
		if s.cfg.oauthAuthEndpoint == "" {
			return s.currentToken(), nil
		}
		return s.deviceCodeToken()
	default:
		return s.currentToken(), nil
	}
}

// deviceCodeToken returns a usable oauth-device-code access token. It
// satisfies the §6.9 "Auth token expired (oauth-device-code)" row:
//
//   - A cached access token that is still valid is returned as-is.
//   - A cached access token past its expiry triggers a silent refresh with
//     the stored refresh token (§6.3 "Refreshes transparently"). The renewed
//     access (and any rotated refresh) token is persisted for reuse.
//   - When no token is cached, or the refresh is rejected (revoked / expired
//     refresh token) so interactive reauth is required, the RFC 8628 device
//     flow runs and the verification URL + user code surface to the host via
//     MCP elicitation (§6.3); the user completes the flow at the IdP while the
//     bridge polls the token endpoint.
//
// A freshly acquired token is saved to the keychain for reuse by later bridge
// processes (§14: "Token caches in the OS keychain").
func (s *mcpServer) deviceCodeToken() (string, error) {
	label := s.cfg.registry
	if s.tokens != nil {
		if tok, err := s.tokens.Load(label); err == nil && tok != "" {
			if !accessTokenExpired(tok, time.Now()) {
				return tok, nil
			}
			// §6.9: the cached access token is past its exp. Trigger a
			// transparent refresh before falling back to interactive reauth.
			if refreshed, ok := s.refreshAccessToken(label); ok {
				return refreshed, nil
			}
		}
	}
	return s.runDeviceFlow(label)
}

// refreshAccessToken attempts the §6.9 "Trigger refresh" step: exchange the
// stored refresh token for a fresh access token (RFC 6749 §6) and persist the
// result. It returns ok=false when no refresh token is cached, or the IdP
// rejects the refresh (revoked / expired), so the caller drives the
// interactive device flow instead.
func (s *mcpServer) refreshAccessToken(label string) (string, bool) {
	if s.tokens == nil {
		return "", false
	}
	refresh, err := s.tokens.Load(identity.RefreshLabel(label))
	if err != nil || refresh == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tokens, err := s.deviceFlow().Refresh(ctx, refresh)
	if err != nil || tokens.AccessToken == "" {
		return "", false
	}
	_ = s.tokens.Save(label, tokens.AccessToken)
	if tokens.RefreshToken != "" {
		_ = s.tokens.Save(identity.RefreshLabel(label), tokens.RefreshToken)
	}
	return tokens.AccessToken, true
}

// runDeviceFlow runs the RFC 8628 device-code flow end to end, surfacing the
// verification URL + user code via MCP elicitation and persisting both the
// access and refresh tokens on success (§6.3 / §6.9).
func (s *mcpServer) runDeviceFlow(label string) (string, error) {
	flow := s.deviceFlow()
	ctx, cancel := context.WithTimeout(context.Background(), deviceFlowMaxWait)
	defer cancel()
	auth, err := flow.Initiate(ctx)
	if err != nil {
		return "", fmt.Errorf("device authorization: %w", err)
	}
	s.elicitDeviceCode(auth)
	tokens, err := flow.Poll(ctx, auth)
	if err != nil {
		return "", fmt.Errorf("device-code poll: %w", err)
	}
	if s.tokens != nil {
		_ = s.tokens.Save(label, tokens.AccessToken)
		if tokens.RefreshToken != "" {
			_ = s.tokens.Save(identity.RefreshLabel(label), tokens.RefreshToken)
		}
	}
	return tokens.AccessToken, nil
}

// deviceFlow builds the configured RFC 8628 flow client, shared by the
// interactive device flow and the silent refresh path.
func (s *mcpServer) deviceFlow() identity.DeviceCodeFlow {
	return identity.DeviceCodeFlow{
		DeviceAuthURL: s.cfg.oauthAuthEndpoint,
		TokenURL:      s.deviceTokenURL(),
		ClientID:      s.cfg.oauthClientID,
		Scopes:        splitCSV(s.cfg.oauthScopes),
		Audience:      s.cfg.oauthAudience,
		Client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// accessTokenExpired reports whether a cached access token is a JWT whose
// `exp` claim is at or before now (with a small skew so a token about to
// expire is refreshed ahead of the next call). An opaque (non-JWT) token, or
// one without a numeric exp, cannot be evaluated locally and is treated as
// not-expired: such a token is used as-is and a server-side rejection
// (revocation, or expiry the bridge cannot see) surfaces through the
// registry's §6.10 auth.token_expired envelope instead.
func accessTokenExpired(token string, now time.Time) bool {
	const skew = 30 * time.Second
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.StdEncoding.DecodeString(parts[1]); err != nil {
			return false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return false
	}
	return !now.Add(skew).Before(time.Unix(claims.Exp, 0))
}

// deviceTokenURL resolves the device-code token endpoint, defaulting to the
// authorization endpoint's sibling when PODIUM_OAUTH_TOKEN_URL is unset.
func (s *mcpServer) deviceTokenURL() string {
	if s.cfg.oauthTokenURL != "" {
		return s.cfg.oauthTokenURL
	}
	return s.cfg.oauthAuthEndpoint + "/token"
}

// elicitDeviceCode surfaces the device-flow verification URL and user code
// to the host via an MCP `elicitation/create` request (§6.3: "the host
// displays the URL and code in the agent UI"). The bridge does not block on
// the host's response; the user acts at the IdP and the bridge learns of
// completion by polling the token endpoint, so the message is informational.
func (s *mcpServer) elicitDeviceCode(auth *identity.DeviceAuth) {
	message := fmt.Sprintf(
		"Sign in to Podium: visit %s and enter code %s",
		auth.VerificationURL, auth.UserCode)
	if auth.VerificationURLComplete != "" {
		message = fmt.Sprintf(
			"Sign in to Podium: open %s (or visit %s and enter code %s)",
			auth.VerificationURLComplete, auth.VerificationURL, auth.UserCode)
	}
	id := "podium-auth-" + strconv.FormatInt(s.serverID.Add(1), 10)
	_ = s.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "elicitation/create",
		"params": map[string]any{
			"message": message,
			"requestedSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	})
	// Also write to stderr so a host without elicitation support still
	// surfaces the prompt to the developer.
	fmt.Fprintln(os.Stderr, message)
}
