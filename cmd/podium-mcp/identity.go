package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
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

// deviceCodeToken returns a cached oauth-device-code access token, running
// the RFC 8628 device flow when none is cached. The verification URL and
// user code surface to the host via MCP elicitation (§6.3); the user
// completes the flow at the IdP while the bridge polls the token endpoint.
// A freshly acquired token is saved to the keychain for reuse by later
// bridge processes (§14: "Token caches in the OS keychain").
func (s *mcpServer) deviceCodeToken() (string, error) {
	label := s.cfg.registry
	if s.tokens != nil {
		if tok, err := s.tokens.Load(label); err == nil && tok != "" {
			return tok, nil
		}
	}
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: s.cfg.oauthAuthEndpoint,
		TokenURL:      s.deviceTokenURL(),
		ClientID:      s.cfg.oauthClientID,
		Scopes:        splitCSV(s.cfg.oauthScopes),
		Audience:      s.cfg.oauthAudience,
		Client:        &http.Client{Timeout: 30 * time.Second},
	}
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
	}
	return tokens.AccessToken, nil
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
