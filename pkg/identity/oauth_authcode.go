package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthCodeFlow implements the OAuth 2.0 authorization-code grant with PKCE
// (RFC 6749 §4.1, RFC 7636), which §6.3.4 prescribes for a browser that
// acquires the oidc-jwt credential through the registry instead of holding
// one of its own. It is DeviceCodeFlow with the grant swapped:
// AuthorizationEndpoint plays the role DeviceAuthURL plays there, and the
// remaining fields carry the same meaning.
//
// Surfaces:
//
//	flow := AuthCodeFlow{
//	    AuthorizationEndpoint: "https://idp.example.com/oauth2/authorize",
//	    TokenURL:              "https://idp.example.com/oauth2/token",
//	    ClientID:              "podium-web-ui",
//	    ClientSecret:          "…",
//	    RedirectURI:           "https://registry.acme.com/v1/ui/auth/callback",
//	    Scopes:                []string{"openid", "profile", "email", "groups"},
//	    Audience:              "https://registry.acme.com",
//	}
//	tx, err := NewAuthTransaction()             // state + PKCE verifier
//	redirect, err := flow.AuthorizationRequest(tx)
//	cb := flow.ParseCallback(query)             // state, error, code
//	tokens, err := flow.Exchange(ctx, cb.Code, tx.Verifier)
//
// Every OAuth parameter name the browser flow reads or writes is spelled in
// this package, so the registry-side handler orders the values and spells
// none of its own.
type AuthCodeFlow struct {
	// AuthorizationEndpoint is the IdP's authorization endpoint
	// (RFC 6749 §3.1), which the sign-in route redirects the browser to.
	AuthorizationEndpoint string
	// TokenURL is the IdP's token endpoint (RFC 6749 §3.2).
	TokenURL string
	// ClientID identifies the registry's browser client to the IdP.
	ClientID string
	// ClientSecret is the client credential the token request sends as a
	// form field. §6.3.4 requires a configured value, so it is always sent.
	ClientSecret string
	// RedirectURI is the callback URL the IdP returns the browser to. It is
	// sent byte-identically on the authorization request and on the token
	// request, which RFC 6749 §4.1.3 requires.
	RedirectURI string
	// Scopes requested in the authorization request, sent space-delimited.
	Scopes []string
	// Audience pins the resulting access token's `aud` claim to the
	// registry's resolved audience, so the token the cookie carries verifies
	// under §6.3.3 like a token any other consumer presents.
	Audience string
	// Client is the HTTP client; defaults to http.DefaultClient. The browser
	// flow's caller sets its Timeout, because a token endpoint that accepts
	// the connection and never answers would otherwise hold the handler.
	Client *http.Client
}

// AuthTransaction is the single-use pre-authorization transaction the sign-in
// route mints and the callback consumes: the `state` that binds the callback
// to the browser that started it, and the PKCE `code_verifier` that binds the
// exchange to the client that started it (§6.3.4).
type AuthTransaction struct {
	State    string
	Verifier string
}

// Callback carries the parameters the IdP's redirect back to the callback
// route can name (RFC 6749 §4.1.2, §4.1.2.1). ParseCallback reads them and
// decides nothing: the ordering of the comparisons and the disposition of
// each combination belong to the registry-side handler (§7.3.4).
type Callback struct {
	State string
	Code  string
	Error string
}

// ExchangeRefusedError reports a token endpoint that answered the
// authorization-code exchange and refused it at the OAuth protocol level
// (RFC 6749 §5.2). It is a permanent failure for that request: every retry
// fails identically, so §6.3.4 maps it to auth.exchange_failed rather than to
// the transient registry.unavailable an unreachable IdP takes.
type ExchangeRefusedError struct {
	// Status is the HTTP status the token endpoint answered with.
	Status int
	// Code is the RFC 6749 §5.2 `error` value, empty when the body did not
	// decode as that envelope.
	Code string
	// Description is the RFC 6749 §5.2 `error_description` value.
	Description string
}

func (e *ExchangeRefusedError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("auth-code exchange: refused with HTTP %d", e.Status)
	}
	if e.Description == "" {
		return fmt.Sprintf("auth-code exchange: refused with HTTP %d: %s", e.Status, e.Code)
	}
	return fmt.Sprintf("auth-code exchange: refused with HTTP %d: %s: %s", e.Status, e.Code, e.Description)
}

// NewAuthTransaction mints one transaction: 32 bytes from crypto/rand for the
// state and 32 more for the PKCE verifier, each in base64.RawURLEncoding, so
// the verifier is 43 characters and satisfies RFC 7636 §4.1. Minting lives
// here beside the only other place in the tree that mints an OAuth value, so
// the entropy and the encoding are stated once (§6.3.4).
func NewAuthTransaction() (AuthTransaction, error) {
	state, err := randomURLSafe(32)
	if err != nil {
		return AuthTransaction{}, fmt.Errorf("auth-code: mint state: %w", err)
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		return AuthTransaction{}, fmt.Errorf("auth-code: mint code_verifier: %w", err)
	}
	return AuthTransaction{State: state, Verifier: verifier}, nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	// crypto/rand.Read never returns an error on any supported platform, so
	// this arm carries no test: a caller cannot reach it.
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CodeChallenge returns the RFC 7636 S256 transform of the transaction's
// verifier, which is the value the authorization request carries.
func (t AuthTransaction) CodeChallenge() string {
	sum := sha256.Sum256([]byte(t.Verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizationRequest builds the URL the sign-in route redirects the browser
// to. It carries the §6.3.4 authorization-request parameters and no others.
func (f AuthCodeFlow) AuthorizationRequest(t AuthTransaction) (string, error) {
	if f.AuthorizationEndpoint == "" {
		return "", errors.New("auth-code: AuthorizationEndpoint is required")
	}
	u, err := url.Parse(f.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("auth-code: parse AuthorizationEndpoint %q: %w", f.AuthorizationEndpoint, err)
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", f.ClientID)
	q.Set("redirect_uri", f.RedirectURI)
	q.Set("scope", strings.Join(f.Scopes, " "))
	q.Set("audience", f.Audience)
	q.Set("state", t.State)
	q.Set("code_challenge", t.CodeChallenge())
	// RFC 7636 §4.3 makes `plain` the default when the parameter is absent,
	// and under `plain` the challenge is the verifier itself, travelling
	// through the browser's address bar and the IdP's redirect chain. The
	// method is therefore always sent (§6.3.4).
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ParseCallback reads the parameters the IdP's redirect carries and returns
// them without deciding anything (§6.3.4).
func (f AuthCodeFlow) ParseCallback(q url.Values) Callback {
	return Callback{
		State: q.Get("state"),
		Code:  q.Get("code"),
		Error: q.Get("error"),
	}
}

// Exchange posts the authorization code to the token endpoint with the
// §6.3.4 token-request fields and no others, and returns the tokens the IdP
// issued. A token endpoint that answers and refuses the exchange returns
// *ExchangeRefusedError; an unreachable endpoint and one answering with a 5xx
// return a plain error, which is the transient arm §6.3.4 maps to
// registry.unavailable.
func (f AuthCodeFlow) Exchange(ctx context.Context, code, verifier string) (*Tokens, error) {
	if f.TokenURL == "" {
		return nil, errors.New("auth-code: TokenURL is required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", f.RedirectURI)
	form.Set("client_id", f.ClientID)
	form.Set("client_secret", f.ClientSecret)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth-code exchange: %w", err)
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
			return nil, fmt.Errorf("auth-code exchange: decode token response: %w", err)
		}
		if raw.AccessToken == "" {
			return nil, errors.New("auth-code exchange: token response missing access_token")
		}
		return &Tokens{
			AccessToken:  raw.AccessToken,
			RefreshToken: raw.RefreshToken,
			TokenType:    raw.TokenType,
			ExpiresIn:    time.Duration(raw.ExpiresIn) * time.Second,
			IDToken:      raw.IDToken,
		}, nil
	}

	if resp.StatusCode >= 500 {
		// The IdP answered, but with a failure of its own rather than a
		// refusal of this exchange, which §6.3.4 treats as transient.
		return nil, fmt.Errorf("auth-code exchange: token endpoint answered HTTP %d", resp.StatusCode)
	}

	var envelope errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&envelope)
	return nil, &ExchangeRefusedError{
		Status:      resp.StatusCode,
		Code:        envelope.Error,
		Description: envelope.Description,
	}
}

func (f AuthCodeFlow) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}
