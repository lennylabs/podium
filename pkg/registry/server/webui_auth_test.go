package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §6.3.4 / §7.3.4 — the sign-in, callback, and sign-out routes, the two
// cookies they set and clear, the order the callback compares in, and the
// §6.10 code each refusal returns. The cases here drive the handlers against a
// stub token endpoint the fixture controls; the boot-assembled cases live in
// internal/serverboot.

// authStub is a token endpoint whose answer each case fixes.
type authStub struct {
	srv *httptest.Server
	// answer writes the token-endpoint response.
	answer func(w http.ResponseWriter, r *http.Request)
	// form records the last exchange the stub received.
	form url.Values
}

func newAuthStub(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *authStub {
	t.Helper()
	stub := &authStub{answer: answer}
	stub.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.form, _ = url.ParseQuery(string(body))
		stub.answer(w, r)
	}))
	t.Cleanup(stub.srv.Close)
	return stub
}

func issuesToken(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{"access_token":"the-access-token","id_token":"the-id-token","token_type":"Bearer"}`))
}

func authEndpoint(t *testing.T, tokenURL string, ttl time.Duration) *server.BrowserAuthEndpoint {
	t.Helper()
	return server.NewBrowserAuthEndpoint(server.BrowserAuthConfig{
		Flow: identity.AuthCodeFlow{
			AuthorizationEndpoint: "https://idp.example.com/authorize",
			TokenURL:              tokenURL,
			ClientID:              "podium-web-ui",
			ClientSecret:          "s3cret",
			RedirectURI:           "https://registry.acme.com/v1/ui/auth/callback",
			Scopes:                []string{"openid", "groups"},
			Audience:              "https://registry.acme.com",
			Client:                &http.Client{Timeout: 2 * time.Second},
		},
		TransactionTTL: ttl,
	})
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func envelopeCode(t *testing.T, resp *http.Response) server.ErrorResponse {
	t.Helper()
	var e server.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return e
}

// noRedirect keeps the test client from following the routes' 302s, so each
// response's cookies and Location are observable.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// Spec: §7.3.4 — sign-in mints one transaction, returns it in
// __Host-podium_auth with the configured TTL as its Max-Age, and redirects to
// the configured authorization endpoint.
func TestBrowserAuth_SignInSetsTransactionCookie(t *testing.T) {
	t.Parallel()
	ep := authEndpoint(t, "", 90*time.Second)
	ts := httptest.NewServer(ep.SignInHandler())
	defer ts.Close()

	resp, err := noRedirect().Get(ts.URL + server.PathWebUISignIn)
	if err != nil {
		t.Fatalf("GET sign-in: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host != "idp.example.com" || loc.Path != "/authorize" {
		t.Errorf("Location = %q, want the configured authorization endpoint", resp.Header.Get("Location"))
	}
	c := cookieNamed(resp, server.CookieAuthTransaction)
	if c == nil {
		t.Fatal("sign-in set no __Host-podium_auth cookie")
	}
	if c.MaxAge != 90 {
		t.Errorf("Max-Age = %d, want the configured transaction TTL in seconds", c.MaxAge)
	}
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || c.Domain != "" {
		t.Errorf("cookie attributes = %+v, want HttpOnly, Secure, Path=/, SameSite=Lax, no Domain", c)
	}
	// The state the redirect carries is the state the cookie holds: the mint
	// point is the Location header and the consume point is the callback.
	state, _, ok := strings.Cut(c.Value, ".")
	if !ok || state == "" || state != loc.Query().Get("state") {
		t.Errorf("cookie state %q does not bind the redirect's state %q", c.Value, loc.Query().Get("state"))
	}
	if resp.Header.Get("Set-Cookie") == "" || cookieNamed(resp, server.CookieSession) != nil {
		t.Error("sign-in set a session cookie; only the callback does")
	}
}

// Spec: §7.3.4 — a sign-in window below one second still bounds the
// transaction cookie. The flag path accepts a duration below one second, and a
// Max-Age truncated to 0 is the session-cookie form rather than a bounded
// window. The non-positive arms pin the floor a misconfigured endpoint hits.
func TestBrowserAuth_SignInSubSecondTTLStillBounds(t *testing.T) {
	t.Parallel()
	for _, ttl := range []time.Duration{500 * time.Millisecond, 999 * time.Millisecond, time.Nanosecond, 0, -time.Minute} {
		ts := httptest.NewServer(authEndpoint(t, "", ttl).SignInHandler())
		defer ts.Close()

		resp, err := noRedirect().Get(ts.URL + server.PathWebUISignIn)
		if err != nil {
			t.Fatalf("GET sign-in: %v", err)
		}
		resp.Body.Close()
		c := cookieNamed(resp, server.CookieAuthTransaction)
		if c == nil {
			t.Fatalf("ttl %s: sign-in set no __Host-podium_auth cookie", ttl)
		}
		if c.MaxAge < 1 {
			t.Errorf("ttl %s: Max-Age = %d, want a positive bound on the sign-in window", ttl, c.MaxAge)
		}
	}
}

// signIn runs the sign-in leg and returns the transaction cookie and the
// authorization redirect it produced.
func signIn(t *testing.T, ep *server.BrowserAuthEndpoint) (*http.Cookie, *url.URL) {
	t.Helper()
	rec := httptest.NewRecorder()
	ep.SignInHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, server.PathWebUISignIn, nil))
	resp := rec.Result()
	defer resp.Body.Close()
	c := cookieNamed(resp, server.CookieAuthTransaction)
	if c == nil {
		t.Fatal("sign-in set no transaction cookie")
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return c, loc
}

// callback drives the callback route with the given query and cookies.
func callback(t *testing.T, ep *server.BrowserAuthEndpoint, query string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, server.PathWebUICallback+"?"+query, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	ep.CallbackHandler().ServeHTTP(rec, req)
	return rec.Result()
}

// Spec: §7.3.4 — a successful callback exchanges the code server-side,
// returns the access token in __Host-podium_session, and clears the
// transaction cookie.
func TestBrowserAuth_CallbackSuccess(t *testing.T) {
	t.Parallel()
	stub := newAuthStub(t, issuesToken)
	ep := authEndpoint(t, stub.srv.URL, time.Minute)
	tx, loc := signIn(t, ep)

	resp := callback(t, ep, "state="+url.QueryEscape(loc.Query().Get("state"))+"&code=the-code", tx)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
		t.Fatalf("status = %d, Location = %q, want 302 to /ui/", resp.StatusCode, resp.Header.Get("Location"))
	}
	session := cookieNamed(resp, server.CookieSession)
	if session == nil {
		t.Fatal("the callback set no session cookie")
	}
	if session.Value != "the-access-token" {
		t.Errorf("session cookie carries %q, want the access token (the ID token is consumed for nothing)", session.Value)
	}
	if session.MaxAge != 0 || !session.HttpOnly || !session.Secure || session.Path != "/" || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie = %+v, want HttpOnly, Secure, Path=/, SameSite=Lax and no Max-Age", session)
	}
	cleared := cookieNamed(resp, server.CookieAuthTransaction)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("transaction cookie = %+v, want an explicit clearing Set-Cookie", cleared)
	}
	// The verifier the exchange sends is the one the transaction minted.
	_, verifier, _ := strings.Cut(tx.Value, ".")
	if got := stub.form.Get("code_verifier"); got != verifier {
		t.Errorf("exchange sent code_verifier %q, want the minted verifier %q", got, verifier)
	}
	if got := stub.form.Get("code"); got != "the-code" {
		t.Errorf("exchange sent code %q, want the callback's code", got)
	}
}

// Spec: §7.3.4 — the state comparison runs before the error branch, so a
// callback whose transaction is absent, expired, or carries a different state
// is refused whatever else the query carries. Every response clears the
// transaction cookie and sets no session cookie.
//
// Matrix: §6.10 (auth.csrf_invalid)
func TestBrowserAuth_CallbackTransactionRefusals(t *testing.T) {
	t.Parallel()
	stub := newAuthStub(t, issuesToken)
	ep := authEndpoint(t, stub.srv.URL, time.Minute)
	tx, loc := signIn(t, ep)
	state := loc.Query().Get("state")

	other := &http.Cookie{Name: server.CookieAuthTransaction, Value: "other-state.other-verifier"}
	cases := []struct {
		name    string
		query   string
		cookies []*http.Cookie
	}{
		// An expired cookie is one the browser does not send, so it reaches
		// the callback as an absent one.
		{"absent cookie, code", "state=" + state + "&code=c", nil},
		{"absent cookie, error", "state=" + state + "&error=access_denied", nil},
		{"differing state, code", "state=" + state + "&code=c", []*http.Cookie{other}},
		{"differing state, error", "state=" + state + "&error=access_denied", []*http.Cookie{other}},
		{"no state in the query", "code=c", []*http.Cookie{tx}},
		{"malformed cookie", "state=" + state + "&code=c", []*http.Cookie{{Name: server.CookieAuthTransaction, Value: "no-separator"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := callback(t, ep, tc.query, tc.cookies...)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
			e := envelopeCode(t, resp)
			if e.Code != "auth.csrf_invalid" {
				t.Errorf("code = %q, want auth.csrf_invalid", e.Code)
			}
			if e.Retryable {
				t.Error("auth.csrf_invalid is not retryable")
			}
			if e.SuggestedAction == "" {
				t.Error("auth.csrf_invalid carries no suggested_action")
			}
			if c := cookieNamed(resp, server.CookieAuthTransaction); c == nil || c.MaxAge >= 0 {
				t.Errorf("transaction cookie = %+v, want a clearing Set-Cookie on every response", c)
			}
			if cookieNamed(resp, server.CookieSession) != nil {
				t.Error("a refusal emitted a Set-Cookie for the session cookie")
			}
		})
	}
}

// Spec: §7.3.4 — a matching transaction whose query carries the IdP's error
// parameter runs no exchange, takes no error code, and returns the browser to
// the web UI root without establishing or replacing a session.
func TestBrowserAuth_CallbackDeclinedConsent(t *testing.T) {
	t.Parallel()
	exchanged := false
	stub := newAuthStub(t, func(w http.ResponseWriter, r *http.Request) {
		exchanged = true
		issuesToken(w, r)
	})
	ep := authEndpoint(t, stub.srv.URL, time.Minute)
	tx, loc := signIn(t, ep)

	// Both arms of the partition: an error alone, and an error beside a code.
	for _, q := range []string{"error=access_denied", "error=access_denied&code=c"} {
		resp := callback(t, ep, "state="+loc.Query().Get("state")+"&"+q,
			tx, &http.Cookie{Name: server.CookieSession, Value: "an-earlier-session"})
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
				t.Fatalf("%s: status = %d, Location = %q, want 302 to /ui/", q, resp.StatusCode, resp.Header.Get("Location"))
			}
			if cookieNamed(resp, server.CookieSession) != nil {
				t.Errorf("%s: the declined-consent outcome touched the session cookie", q)
			}
			if c := cookieNamed(resp, server.CookieAuthTransaction); c == nil || c.MaxAge >= 0 {
				t.Errorf("%s: transaction cookie = %+v, want a clearing Set-Cookie", q, c)
			}
		}()
	}
	if exchanged {
		t.Error("a query carrying the error parameter ran an exchange")
	}
}

// Spec: §7.3.4 — an exchange the IdP answers and refuses is 502
// auth.exchange_failed; an IdP the registry cannot reach, and one answering
// with a 5xx, are 500 registry.unavailable.
//
// Matrix: §6.10 (auth.exchange_failed)
func TestBrowserAuth_CallbackExchangeFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		answer     func(http.ResponseWriter, *http.Request)
		wantStatus int
		wantCode   string
		retryable  bool
	}{
		{
			name: "oauth refusal",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "auth.exchange_failed",
		},
		{
			name: "idp server error",
			answer: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "registry.unavailable",
			retryable:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := newAuthStub(t, tc.answer)
			ep := authEndpoint(t, stub.srv.URL, time.Minute)
			tx, loc := signIn(t, ep)
			resp := callback(t, ep, "state="+loc.Query().Get("state")+"&code=c", tx)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			e := envelopeCode(t, resp)
			if e.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", e.Code, tc.wantCode)
			}
			if e.Retryable != tc.retryable {
				t.Errorf("retryable = %v, want %v", e.Retryable, tc.retryable)
			}
			if e.SuggestedAction == "" {
				t.Errorf("code %q carries no suggested_action", e.Code)
			}
			if cookieNamed(resp, server.CookieSession) != nil {
				t.Error("a failed exchange emitted a Set-Cookie for the session cookie")
			}
			if c := cookieNamed(resp, server.CookieAuthTransaction); c == nil || c.MaxAge >= 0 {
				t.Errorf("transaction cookie = %+v, want a clearing Set-Cookie", c)
			}
		})
	}
}

// Spec: §7.3.4 — a token endpoint that never answers ends on the configured
// deadline with 500 registry.unavailable rather than holding the handler.
func TestBrowserAuth_CallbackExchangeDeadline(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	stub := newAuthStub(t, func(http.ResponseWriter, *http.Request) { <-block })
	t.Cleanup(func() { close(block) })
	ep := server.NewBrowserAuthEndpoint(server.BrowserAuthConfig{
		Flow: identity.AuthCodeFlow{
			AuthorizationEndpoint: "https://idp.example.com/authorize",
			TokenURL:              stub.srv.URL,
			Client:                &http.Client{Timeout: 150 * time.Millisecond},
		},
		TransactionTTL: time.Minute,
	})
	tx, loc := signIn(t, ep)

	done := make(chan *http.Response, 1)
	go func() { done <- callback(t, ep, "state="+loc.Query().Get("state")+"&code=c", tx) }()
	select {
	case resp := <-done:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
		if e := envelopeCode(t, resp); e.Code != "registry.unavailable" {
			t.Errorf("code = %q, want registry.unavailable", e.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never answered; the exchange carries no deadline")
	}
}

// Spec: §7.3.4 — the registry keeps no session state, so the transaction
// travels with the browser and any replica serves the callback.
func TestBrowserAuth_AnyReplicaServesTheCallback(t *testing.T) {
	t.Parallel()
	stub := newAuthStub(t, issuesToken)
	first := authEndpoint(t, stub.srv.URL, time.Minute)
	second := authEndpoint(t, stub.srv.URL, time.Minute)

	tx, loc := signIn(t, first)
	resp := callback(t, second, "state="+loc.Query().Get("state")+"&code=c", tx)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 from the second replica", resp.StatusCode)
	}
	if c := cookieNamed(resp, server.CookieSession); c == nil || c.Value != "the-access-token" {
		t.Errorf("session cookie = %+v, want the exchanged access token", c)
	}
}

// Spec: §7.3.4 — sign-out clears both cookies on every request it serves.
func TestBrowserAuth_SignOutClearsBothCookies(t *testing.T) {
	t.Parallel()
	ep := authEndpoint(t, "", time.Minute)
	req := httptest.NewRequest(http.MethodPost, server.PathWebUISignOut, nil)
	rec := httptest.NewRecorder()
	ep.SignOutHandler().ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	for _, name := range []string{server.CookieSession, server.CookieAuthTransaction} {
		c := cookieNamed(resp, name)
		if c == nil || c.MaxAge >= 0 {
			t.Errorf("%s = %+v, want a clearing Set-Cookie", name, c)
		}
	}
}

// Spec: §7.3.4 — each route answers on one method. Sign-out's POST is what
// places it inside the §6.3.4 gate.
func TestBrowserAuth_RouteMethods(t *testing.T) {
	t.Parallel()
	ep := authEndpoint(t, "", time.Minute)
	cases := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
	}{
		{"sign-in refuses POST", ep.SignInHandler(), http.MethodPost, server.PathWebUISignIn},
		{"callback refuses POST", ep.CallbackHandler(), http.MethodPost, server.PathWebUICallback},
		{"sign-out refuses GET", ep.SignOutHandler(), http.MethodGet, server.PathWebUISignOut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rec.Code)
			}
		})
	}
}

// Spec: §7.3.4 — the callback clears __Host-podium_auth on every response it
// emits, the method refusal included. The §6.3.4 gate excludes the callback by
// name, so a cross-origin POST reaches that arm, and a refusal that carried no
// clearing header would leave the transaction replayable.
func TestBrowserAuth_CallbackMethodRefusalClearsTransaction(t *testing.T) {
	t.Parallel()
	ep := authEndpoint(t, "", time.Minute)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, server.PathWebUICallback, nil)
	req.AddCookie(&http.Cookie{Name: server.CookieAuthTransaction, Value: "the-state.the-verifier"})
	ep.CallbackHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	c := cookieNamed(rec.Result(), server.CookieAuthTransaction)
	if c == nil || c.MaxAge >= 0 {
		t.Errorf("Set-Cookie = %q, want an expiring __Host-podium_auth", rec.Header().Values("Set-Cookie"))
	}
}

// Spec: §7.3.4 — the sign-in route refuses rather than redirecting nowhere
// when the authorization request cannot be built.
func TestBrowserAuth_SignInUnbuildableRedirect(t *testing.T) {
	t.Parallel()
	ep := server.NewBrowserAuthEndpoint(server.BrowserAuthConfig{TransactionTTL: time.Minute})
	rec := httptest.NewRecorder()
	ep.SignInHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, server.PathWebUISignIn, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
