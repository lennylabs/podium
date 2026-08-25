package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"
)

// Spec: §6.3.4 — the authorization-request and token-request tables, and the
// callback parameters ParseCallback reads without deciding anything.

func testFlow(authEndpoint, tokenURL string) AuthCodeFlow {
	return AuthCodeFlow{
		AuthorizationEndpoint: authEndpoint,
		TokenURL:              tokenURL,
		ClientID:              "podium-web-ui",
		ClientSecret:          "s3cret",
		RedirectURI:           "https://registry.acme.com/v1/ui/auth/callback",
		Scopes:                []string{"openid", "profile", "email", "groups"},
		Audience:              "https://registry.acme.com",
	}
}

// Spec: §6.3.4 — the authorization request carries one query parameter per
// row of the table and no others, each carrying that row's value.
func TestAuthCodeFlow_AuthorizationRequestParameters(t *testing.T) {
	t.Parallel()
	flow := testFlow("https://idp.example.com/oauth2/authorize?tenant=acme", "")
	tx := AuthTransaction{State: "state-value", Verifier: "verifier-value"}

	raw, err := flow.AuthorizationRequest(tx)
	if err != nil {
		t.Fatalf("AuthorizationRequest: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != "https://idp.example.com/oauth2/authorize" {
		t.Errorf("redirect target = %q, want the configured authorization endpoint", raw)
	}
	q := u.Query()

	sum := sha256.Sum256([]byte(tx.Verifier))
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "podium-web-ui",
		"redirect_uri":          "https://registry.acme.com/v1/ui/auth/callback",
		"scope":                 "openid profile email groups",
		"audience":              "https://registry.acme.com",
		"state":                 "state-value",
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
		"code_challenge_method": "S256",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query %s = %q, want %q", k, got, v)
		}
	}
	// The enumeration is closed: the redirect carries the table's rows, plus
	// whatever the configured endpoint already carried in its own query.
	for k := range q {
		if _, ok := want[k]; !ok && k != "tenant" {
			t.Errorf("unexpected query parameter %q; the authorization-request table is closed", k)
		}
	}
	if q.Get("nonce") != "" {
		t.Error("the flow consumes the ID token for nothing, so it sends no nonce")
	}
}

// Spec: §6.3.4 — the state and the PKCE verifier are 32 bytes from
// crypto/rand in base64.RawURLEncoding, which makes the verifier 43
// characters and satisfies RFC 7636 §4.1.
func TestNewAuthTransaction_Entropy(t *testing.T) {
	t.Parallel()
	a, err := NewAuthTransaction()
	if err != nil {
		t.Fatalf("NewAuthTransaction: %v", err)
	}
	b, err := NewAuthTransaction()
	if err != nil {
		t.Fatalf("NewAuthTransaction: %v", err)
	}
	if a.State == b.State || a.Verifier == b.Verifier {
		t.Error("two transactions repeated a minted value")
	}
	for _, v := range []string{a.State, a.Verifier} {
		if len(v) != 43 {
			t.Errorf("minted value %q is %d characters, want 43", v, len(v))
		}
		if _, err := base64.RawURLEncoding.DecodeString(v); err != nil {
			t.Errorf("minted value %q is not base64url without padding: %v", v, err)
		}
	}
}

// Spec: §6.3.4 — ParseCallback reads state, error, and code and decides
// nothing. The arms are the ones "the callback order and outcomes" partitions.
func TestAuthCodeFlow_ParseCallback(t *testing.T) {
	t.Parallel()
	flow := testFlow("", "")
	cases := []struct {
		name  string
		query string
		want  Callback
	}{
		{"state and code", "state=s&code=c", Callback{State: "s", Code: "c"}},
		{"state and error", "state=s&error=access_denied", Callback{State: "s", Error: "access_denied"}},
		{"code and error", "state=s&code=c&error=access_denied", Callback{State: "s", Code: "c", Error: "access_denied"}},
		{"neither", "state=s", Callback{State: "s"}},
		{"no state", "code=c", Callback{Code: "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if got := flow.ParseCallback(q); got != tc.want {
				t.Errorf("ParseCallback(%q) = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
}

// Spec: §6.3.4 — the token request posts one form field per row of the table
// and no others, with the documented Content-Type and Accept pair.
func TestAuthCodeFlow_ExchangeRequest(t *testing.T) {
	t.Parallel()
	var (
		gotForm    url.Values
		gotHeaders http.Header
		gotMethod  string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","id_token":"it","token_type":"Bearer","expires_in":300}`))
	}))
	defer ts.Close()

	flow := testFlow("", ts.URL)
	tokens, err := flow.Exchange(context.Background(), "auth-code", "verifier-value")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tokens.AccessToken != "at" || tokens.IDToken != "it" {
		t.Errorf("tokens = %+v, want the stub's access and ID tokens", tokens)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if ct := gotHeaders.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
	}
	if acc := gotHeaders.Get("Accept"); acc != "application/json" {
		t.Errorf("Accept = %q, want application/json", acc)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "auth-code",
		"redirect_uri":  "https://registry.acme.com/v1/ui/auth/callback",
		"client_id":     "podium-web-ui",
		"client_secret": "s3cret",
		"code_verifier": "verifier-value",
	}
	for k, v := range want {
		if got := gotForm.Get(k); got != v {
			t.Errorf("form field %s = %q, want %q", k, got, v)
		}
	}
	for k := range gotForm {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected form field %q; the token-request table is closed", k)
		}
	}
	if gotHeaders.Get("Authorization") != "" {
		t.Error("the client credential is a form field, not an HTTP Basic credential")
	}
}

// Spec: §6.3.4 — a token endpoint that answers and refuses the exchange
// returns the permanent arm, whether or not its body decodes as the RFC 6749
// §5.2 envelope; one answering with a 5xx and one that never answers are
// transient.
func TestAuthCodeFlow_ExchangeFailures(t *testing.T) {
	t.Parallel()

	t.Run("oauth refusal decodes the RFC 6749 §5.2 envelope", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code already used"}`))
		}))
		defer ts.Close()
		_, err := testFlow("", ts.URL).Exchange(context.Background(), "c", "v")
		var refused *ExchangeRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("err = %v, want *ExchangeRefusedError", err)
		}
		if refused.Code != "invalid_grant" || refused.Description != "code already used" {
			t.Errorf("refusal = %+v, want the decoded envelope", refused)
		}
	})

	t.Run("undecodable refusal body", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("not json"))
		}))
		defer ts.Close()
		_, err := testFlow("", ts.URL).Exchange(context.Background(), "c", "v")
		var refused *ExchangeRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("err = %v, want *ExchangeRefusedError", err)
		}
		if refused.Status != http.StatusUnauthorized || refused.Code != "" {
			t.Errorf("refusal = %+v, want the status alone", refused)
		}
		if refused.Error() == "" {
			t.Error("the refusal renders no message")
		}
	})

	t.Run("server error is transient", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer ts.Close()
		_, err := testFlow("", ts.URL).Exchange(context.Background(), "c", "v")
		var refused *ExchangeRefusedError
		if err == nil || errors.As(err, &refused) {
			t.Fatalf("err = %v, want a transient error rather than a refusal", err)
		}
	})

	t.Run("unanswered exchange ends on the deadline", func(t *testing.T) {
		t.Parallel()
		block := make(chan struct{})
		ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			<-block
		}))
		defer func() { close(block); ts.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := testFlow("", ts.URL).Exchange(ctx, "c", "v")
		var refused *ExchangeRefusedError
		if err == nil || errors.As(err, &refused) {
			t.Fatalf("err = %v, want a transient error rather than a refusal", err)
		}
	})

	t.Run("a 200 without an access token is an error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
		}))
		defer ts.Close()
		if _, err := testFlow("", ts.URL).Exchange(context.Background(), "c", "v"); err == nil {
			t.Error("Exchange accepted a token response carrying no access_token")
		}
	})

	t.Run("an undecodable 200 body is an error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer ts.Close()
		if _, err := testFlow("", ts.URL).Exchange(context.Background(), "c", "v"); err == nil {
			t.Error("Exchange accepted an undecodable token response")
		}
	})
}

// Spec: §6.3.4 — the flow refuses to build a request against an unconfigured
// endpoint rather than sending one nowhere.
func TestAuthCodeFlow_UnconfiguredEndpoints(t *testing.T) {
	t.Parallel()
	if _, err := (AuthCodeFlow{}).AuthorizationRequest(AuthTransaction{}); err == nil {
		t.Error("AuthorizationRequest accepted an empty AuthorizationEndpoint")
	}
	if _, err := (AuthCodeFlow{AuthorizationEndpoint: "://"}).AuthorizationRequest(AuthTransaction{}); err == nil {
		t.Error("AuthorizationRequest accepted an unparsable AuthorizationEndpoint")
	}
	if _, err := (AuthCodeFlow{}).Exchange(context.Background(), "c", "v"); err == nil {
		t.Error("Exchange accepted an empty TokenURL")
	}
}

// Spec: §6.3.4 — the flow defaults its HTTP client the way the shipped
// device-code flow does.
func TestAuthCodeFlow_ClientDefault(t *testing.T) {
	t.Parallel()
	if (AuthCodeFlow{}).client() != http.DefaultClient {
		t.Error("an unset Client does not default to http.DefaultClient")
	}
	own := &http.Client{Timeout: time.Second}
	if (AuthCodeFlow{Client: own}).client() != own {
		t.Error("a configured Client is not used")
	}
}

// Spec: §6.3.4 — the scope set is sent space-delimited in the order
// configured, which is what an IdP keys a group claim on.
func TestAuthCodeFlow_ScopeSetOrder(t *testing.T) {
	t.Parallel()
	flow := testFlow("https://idp.example.com/authorize", "")
	flow.Scopes = []string{"openid", "podium-groups"}
	raw, err := flow.AuthorizationRequest(AuthTransaction{State: "s", Verifier: "v"})
	if err != nil {
		t.Fatalf("AuthorizationRequest: %v", err)
	}
	u, _ := url.Parse(raw)
	if got := u.Query().Get("scope"); got != "openid podium-groups" {
		t.Errorf("scope = %q, want the configured set space-delimited", got)
	}
	if !slices.Equal(flow.Scopes, []string{"openid", "podium-groups"}) {
		t.Error("AuthorizationRequest mutated the configured scope set")
	}
}

// Spec: §6.3.4 — the refusal renders the IdP's RFC 6749 §5.2 envelope where
// it carried one, so an operator reading a log sees which refusal it was.
func TestExchangeRefusedError_Message(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  ExchangeRefusedError
		want string
	}{
		{ExchangeRefusedError{Status: 401}, "auth-code exchange: refused with HTTP 401"},
		{ExchangeRefusedError{Status: 400, Code: "invalid_grant"}, "auth-code exchange: refused with HTTP 400: invalid_grant"},
		{
			ExchangeRefusedError{Status: 400, Code: "invalid_grant", Description: "expired"},
			"auth-code exchange: refused with HTTP 400: invalid_grant: expired",
		},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}
