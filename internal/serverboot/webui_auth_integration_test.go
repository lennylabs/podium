package serverboot

// Integration coverage for the §6.3.4 browser acquisition flow and the
// §7.3.4 routes, driven over HTTP against a stub identity provider whose
// authorization and token endpoints the fixture controls. The registry is
// assembled the way the boot path assembles it: the layer endpoint and the
// authentication routes on a mux ahead of the meta-tool catch-all, with the
// browser-origin gate wrapping the whole mux. A case driven at a bare layer
// endpoint would pass whether or not the gate is installed there.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// The fixture's scope set is other than the default, and the scope the stub
// keys the group claim on is a member of it and not of the default set, so a
// run that sends "openid" alone or hardcodes the default fails the group
// assertion.
const (
	bfGroupScope   = "acme-groups"
	bfClientID     = "podium-web-ui"
	bfClientSecret = "s3cret"
	bfRedirectURI  = "https://registry.acme.test/v1/ui/auth/callback"
)

// oauthStub is the IdP's authorization and token endpoints. It honors the
// fixture contract: it refuses an authorization request whose
// code_challenge_method is absent or plain, it mints an access token whose
// aud is the audience the authorization request asked for, and it carries the
// group claim only where that request asked for the scope that carries it.
type oauthStub struct {
	srv *httptest.Server
	idp *jwksIdP

	mu sync.Mutex
	// codes maps an issued authorization code to the authorization request
	// that produced it.
	codes map[string]url.Values
	// lastAuthorize records the last authorization request the stub received.
	lastAuthorize url.Values
	// lastExchange records the last token request the stub received.
	lastExchange url.Values
	// refuseExchange, when set, answers the exchange with this status and
	// body instead of issuing tokens.
	refuseExchange func(http.ResponseWriter)
	// blockExchange, when non-nil, holds the token endpoint until it closes.
	blockExchange chan struct{}
}

func newOAuthStub(t *testing.T, idp *jwksIdP) *oauthStub {
	t.Helper()
	s := &oauthStub{idp: idp, codes: map[string]url.Values{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s.mu.Lock()
		s.lastAuthorize = q
		s.mu.Unlock()
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "unsupported code_challenge_method", http.StatusBadRequest)
			return
		}
		code := "code-" + q.Get("state")
		s.mu.Lock()
		s.codes[code] = q
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "state": q.Get("state")})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		s.mu.Lock()
		s.lastExchange = form
		block, refuse := s.blockExchange, s.refuseExchange
		authReq, known := s.codes[form.Get("code")]
		s.mu.Unlock()
		if block != nil {
			<-block
			return
		}
		if refuse != nil {
			refuse(w)
			return
		}
		if !known {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		tx := identity.AuthTransaction{Verifier: form.Get("code_verifier")}
		if tx.CodeChallenge() != authReq.Get("code_challenge") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code_verifier mismatch"}`))
			return
		}
		// The access token's aud is the audience the authorization request
		// asked for, which is what a request-driven IdP does and what makes an
		// omitted audience parameter fail a case rather than only fail in a
		// browser.
		aud := authReq.Get("audience")
		if aud == "" {
			aud = authReq.Get("client_id")
		}
		claims := jwt.MapClaims{
			"iss": s.idp.issuer(), "aud": aud, "sub": "alice@acme.com",
			"exp": time.Now().Add(10 * time.Minute).Unix(),
		}
		if slicesContains(strings.Fields(authReq.Get("scope")), bfGroupScope) {
			claims["groups"] = []any{"idp-eng"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": s.idp.sign(t, claims),
			"id_token":     s.idp.sign(t, jwt.MapClaims{"iss": s.idp.issuer(), "aud": bfClientID, "sub": "id-token-subject", "exp": time.Now().Add(time.Minute).Unix()}),
			"token_type":   "Bearer",
		})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func slicesContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// browserStack is a registry assembled as the boot path assembles it, with
// the browser flow enabled against the stub IdP.
type browserStack struct {
	ts   *httptest.Server
	stub *oauthStub
	idp  *jwksIdP
	mode *server.ModeTracker
}

type stackOpts struct {
	// audience is the registry's resolved audience, which the sign-in
	// redirect sends. Empty leaves the redirect's audience parameter empty,
	// which the stub then mints a token for the client identifier instead.
	audience string
	// browserAuth enables the §6.3.4 flow. When false the authentication
	// routes are unmounted and the verifier reads no cookie.
	browserAuth bool
	// scopes is the configured scope set.
	scopes []string
	// exchangeTimeout bounds the callback's token-endpoint request.
	exchangeTimeout time.Duration
	// transactionTTL is __Host-podium_auth's Max-Age.
	transactionTTL time.Duration
}

func newBrowserStack(t *testing.T, opts stackOpts) *browserStack {
	t.Helper()
	if opts.audience == "" {
		opts.audience = gwAudience
	}
	if len(opts.scopes) == 0 {
		opts.scopes = []string{"openid", bfGroupScope}
	}
	if opts.exchangeTimeout == 0 {
		opts.exchangeTimeout = 2 * time.Second
	}
	if opts.transactionTTL == 0 {
		opts.transactionTTL = 10 * time.Minute
	}
	idp := newJWKSIdP(t)
	stub := newOAuthStub(t, idp)

	st := store.NewMemory()
	const tenant = "bf-tenant"
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: tenant, Name: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	layers := []layer.Layer{
		{ID: "pub", Precedence: 1, Visibility: layer.Visibility{Public: true}},
		{ID: "eng", Precedence: 2, Visibility: layer.Visibility{Groups: []string{"engineering"}}},
	}
	if err := st.PutManifest(t.Context(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "eng/secret", Version: "1.0.0",
		ContentHash: "sha256:eng", Type: "context", Description: "eng secret", Layer: "eng",
		IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	mapping, err := identity.ParseIdpGroupMapping("idp-eng=engineering")
	if err != nil {
		t.Fatalf("ParseIdpGroupMapping: %v", err)
	}
	verifier := identity.NewOIDCVerifier(idp.issuer(), opts.audience, 0)
	layerVerify := oidcJWTVerifier(verifier, "", mapping, opts.browserAuth)
	layerIdentity := layerIdentityResolver(layerVerify)
	layerCaller := layerCallerResolver(layerVerify)

	reg := core.New(st, tenant, layers)
	srv := server.New(reg, server.WithIdentityVerifier(layerVerify))
	mode := server.NewModeTracker()
	layerEndpoint := server.NewLayerEndpoint(st, tenant, mode).
		WithIdentityResolver(layerCaller)

	mux := http.NewServeMux()
	mux.Handle("/v1/layers", layerEndpoint.Handler())
	mux.Handle("/v1/layers/", layerEndpoint.Handler())
	if opts.browserAuth {
		ep := server.NewBrowserAuthEndpoint(server.BrowserAuthConfig{
			Flow: identity.AuthCodeFlow{
				AuthorizationEndpoint: stub.srv.URL + "/authorize",
				TokenURL:              stub.srv.URL + "/token",
				ClientID:              bfClientID,
				ClientSecret:          bfClientSecret,
				RedirectURI:           bfRedirectURI,
				Scopes:                opts.scopes,
				Audience:              opts.audience,
				Client:                &http.Client{Timeout: opts.exchangeTimeout},
			},
			TransactionTTL: opts.transactionTTL,
		})
		mux.Handle(server.PathWebUISignIn, ep.SignInHandler())
		mux.Handle(server.PathWebUICallback, ep.CallbackHandler())
		mux.Handle(server.PathWebUISignOut, ep.SignOutHandler())
	}
	mux.Handle(server.PathWebUISession, server.SessionPosture{
		IdentityProviderConfigured: true,
		BrowserAuthEnabled:         opts.browserAuth,
		Identity:                   layerIdentity,
	}.Handler())
	mux.Handle("/", srv.Handler())

	ts := httptest.NewServer(server.BrowserOriginGate(mux))
	t.Cleanup(ts.Close)
	return &browserStack{ts: ts, stub: stub, idp: idp, mode: mode}
}

// noFollow is a client that reports each redirect rather than following it, so
// every response's cookies and Location are observable.
func noFollow() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (b *browserStack) do(t *testing.T, method, path string, headers map[string]string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, b.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func responseCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func envelope(t *testing.T, resp *http.Response) server.ErrorResponse {
	t.Helper()
	var e server.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return e
}

// signInLeg runs sign-in and drives the IdP's authorization endpoint the way
// the browser would, returning the transaction cookie and the code the stub
// issued. It fails the test when the stub refuses the authorization request,
// which is what an omitted code_challenge_method produces.
func (b *browserStack) signInLeg(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	resp := b.do(t, http.MethodGet, server.PathWebUISignIn, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in status = %d, want 302", resp.StatusCode)
	}
	tx := responseCookie(resp, server.CookieAuthTransaction)
	if tx == nil {
		t.Fatal("sign-in set no transaction cookie")
	}
	authResp, err := noFollow().Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("authorization request: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(authResp.Body)
		t.Fatalf("the identity provider refused the authorization request: %d %s", authResp.StatusCode, body)
	}
	var issued struct{ Code, State string }
	if err := json.NewDecoder(authResp.Body).Decode(&issued); err != nil {
		t.Fatalf("decode authorization response: %v", err)
	}
	return tx, issued.Code
}

// Spec: §6.3.4 — the sign-in redirect carries the authorization-request
// parameters from the browser flow's own configuration, including the
// registry's resolved audience and code_challenge_method=S256, and the
// exchange completes into a session cookie the installed verifier resolves.
//
// Spec: §7.3.4
func TestBrowserFlow_RoutesCompleteTheExchange(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true, transactionTTL: 90 * time.Second})

	tx, code := b.signInLeg(t)
	authReq := b.stub.lastAuthorize
	if authReq.Get("client_id") != bfClientID || authReq.Get("redirect_uri") != bfRedirectURI {
		t.Errorf("authorization request = %v, want the configured client and redirect URI", authReq)
	}
	if authReq.Get("audience") != gwAudience {
		t.Errorf("audience = %q, want the registry's resolved audience", authReq.Get("audience"))
	}
	if authReq.Get("scope") != "openid "+bfGroupScope {
		t.Errorf("scope = %q, want the configured set", authReq.Get("scope"))
	}
	if authReq.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", authReq.Get("code_challenge_method"))
	}

	state, _, _ := strings.Cut(tx.Value, ".")
	resp := b.do(t, http.MethodGet,
		server.PathWebUICallback+"?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil, tx)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", resp.StatusCode)
	}
	session := responseCookie(resp, server.CookieSession)
	if session == nil {
		t.Fatal("the callback set no session cookie")
	}
	// The verifier the exchange sent is the one the transaction minted.
	if b.stub.lastExchange.Get("code_verifier") == "" {
		t.Error("the exchange sent no code_verifier")
	}
	if b.stub.lastExchange.Get("client_secret") != bfClientSecret {
		t.Error("the exchange sent no client credential")
	}

	// The token in the cookie is the access token the exchange returned: it
	// carries the registry's audience and resolves the stub's subject with
	// the group the configured scope put on it. An implementation that put
	// the ID token in the cookie, or that sent no audience, resolves nothing.
	sess := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, session)
	defer sess.Body.Close()
	if sess.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sess.Body)
		t.Fatalf("session read = %d, want 200 (the group-scoped layer)\nbody: %s", sess.StatusCode, body)
	}
}

// Spec: §6.3.4 — the authorization request asks the IdP for the registry's
// resolved audience. A flow that sends none receives the IdP's default one
// and every subsequent request is refused, which the fixture reproduces by
// minting the token for the client identifier instead.
func TestBrowserFlow_AudienceDrivesTheSessionToken(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	tx, code := b.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	resp := b.do(t, http.MethodGet,
		server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
	defer resp.Body.Close()
	session := responseCookie(resp, server.CookieSession)
	if session == nil {
		t.Fatal("no session cookie")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(session.Value, claims); err != nil {
		t.Fatalf("parse session token: %v", err)
	}
	if claims["aud"] != gwAudience {
		t.Errorf("session token aud = %v, want the registry's resolved audience", claims["aud"])
	}
	if claims["sub"] == "id-token-subject" {
		t.Error("the session cookie carries the ID token; it carries the access token")
	}
}

// Spec: §6.3.4 — the scope set is what puts the group claim on the token, so
// a session established under a set omitting it resolves the same subject
// with fewer layers than the CLI sees.
func TestBrowserFlow_ScopeSetDrivesGroupResolution(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true, scopes: []string{"openid"}})
	tx, code := b.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	resp := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
	defer resp.Body.Close()
	session := responseCookie(resp, server.CookieSession)
	if session == nil {
		t.Fatal("no session cookie")
	}
	read := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, session)
	defer read.Body.Close()
	if read.StatusCode != http.StatusNotFound {
		t.Errorf("under-scoped session read = %d, want 404; the group claim rides on the configured scope", read.StatusCode)
	}
}

// Spec: §6.3.4 — an exchange the IdP answers and refuses is 502
// auth.exchange_failed; one it never answers ends on the configured deadline
// with 500 registry.unavailable.
//
// Matrix: §6.10 (auth.exchange_failed)
func TestBrowserFlow_ExchangeFailureArms(t *testing.T) {
	t.Parallel()

	t.Run("oauth refusal", func(t *testing.T) {
		t.Parallel()
		b := newBrowserStack(t, stackOpts{browserAuth: true})
		tx, code := b.signInLeg(t)
		b.stub.mu.Lock()
		b.stub.refuseExchange = func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}
		b.stub.mu.Unlock()
		state, _, _ := strings.Cut(tx.Value, ".")
		resp := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
		e := envelope(t, resp)
		if e.Code != "auth.exchange_failed" || e.Retryable || e.SuggestedAction == "" {
			t.Errorf("envelope = %+v, want auth.exchange_failed, retryable false, a remediation", e)
		}
		if responseCookie(resp, server.CookieSession) != nil {
			t.Error("a refused exchange emitted a session Set-Cookie")
		}
	})

	t.Run("unanswered exchange", func(t *testing.T) {
		t.Parallel()
		b := newBrowserStack(t, stackOpts{browserAuth: true, exchangeTimeout: 200 * time.Millisecond})
		tx, code := b.signInLeg(t)
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })
		b.stub.mu.Lock()
		b.stub.blockExchange = block
		b.stub.mu.Unlock()
		state, _, _ := strings.Cut(tx.Value, ".")
		resp := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
		if e := envelope(t, resp); e.Code != "registry.unavailable" || !e.Retryable {
			t.Errorf("envelope = %+v, want a retryable registry.unavailable", e)
		}
	})
}

// Spec: §6.3.4 — the callback is outside the browser-origin gate, so a
// redirect back from the identity provider, which a browser sends with
// Sec-Fetch-Site: cross-site, completes and replaces a session cookie the
// browser already holds rather than being refused.
//
// Spec: §7.3.4
func TestBrowserFlow_CallbackOutsideTheGateReplacesTheSession(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	tx, code := b.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	resp := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code,
		map[string]string{"Sec-Fetch-Site": "cross-site"},
		tx, &http.Cookie{Name: server.CookieSession, Value: "an-earlier-session"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302; the callback is outside the gate", resp.StatusCode)
	}
	c := responseCookie(resp, server.CookieSession)
	if c == nil || c.Value == "an-earlier-session" {
		t.Errorf("session cookie = %+v, want the newly exchanged token", c)
	}
}

// Spec: §6.3.4 — sign-in is outside the gate as well, so re-running sign-in
// stays available whatever browser-origin evidence the navigation carries.
func TestBrowserFlow_SignInOutsideTheGate(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	for _, headers := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Origin": "https://elsewhere.example"},
		{"Sec-Fetch-Site": "none"},
	} {
		resp := b.do(t, http.MethodGet, server.PathWebUISignIn, headers)
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusFound {
				t.Errorf("%v: sign-in status = %d, want 302", headers, resp.StatusCode)
			}
			if responseCookie(resp, server.CookieAuthTransaction) == nil {
				t.Errorf("%v: sign-in set no fresh transaction cookie", headers)
			}
		}()
	}
}

// Spec: §6.3.4 — a forged cross-origin sign-out is refused before the handler
// runs, so it clears no cookie and the session still authenticates the
// browser on a subsequent request.
//
// Matrix: §6.10 (auth.csrf_invalid)
func TestBrowserFlow_ForgedSignOutClearsNothing(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	tx, code := b.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	cb := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
	cb.Body.Close()
	session := responseCookie(cb, server.CookieSession)
	if session == nil {
		t.Fatal("no session cookie")
	}

	forged := b.do(t, http.MethodPost, server.PathWebUISignOut,
		map[string]string{"Sec-Fetch-Site": "cross-site"}, session)
	defer forged.Body.Close()
	if forged.StatusCode != http.StatusForbidden {
		t.Fatalf("forged sign-out status = %d, want 403", forged.StatusCode)
	}
	if e := envelope(t, forged); e.Code != "auth.csrf_invalid" {
		t.Errorf("code = %q, want auth.csrf_invalid", e.Code)
	}
	if len(forged.Cookies()) != 0 {
		t.Errorf("the refused sign-out emitted %d Set-Cookie headers, want none", len(forged.Cookies()))
	}
	// The session still authenticates the browser.
	read := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, session)
	defer read.Body.Close()
	if read.StatusCode != http.StatusOK {
		t.Errorf("post-forgery read = %d, want 200; the forged sign-out signed the operator out", read.StatusCode)
	}

	// A same-origin sign-out clears both cookies.
	out := b.do(t, http.MethodPost, server.PathWebUISignOut,
		map[string]string{"Sec-Fetch-Site": "same-origin"}, session)
	defer out.Body.Close()
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("sign-out status = %d, want 204", out.StatusCode)
	}
	for _, name := range []string{server.CookieSession, server.CookieAuthTransaction} {
		if c := responseCookie(out, name); c == nil || c.MaxAge >= 0 {
			t.Errorf("%s = %+v, want a clearing Set-Cookie", name, c)
		}
	}
}

// Spec: §6.3.4 — the gate wraps the boot mux, so a cross-site layer write is
// refused before the layer endpoint runs, whatever credential authenticated
// it, and a write carrying no browser-origin evidence is admitted. The gate
// is not conditional on the browser flow.
func TestBrowserFlow_CSRFCoversLayerWrites(t *testing.T) {
	t.Parallel()
	enabled := newBrowserStack(t, stackOpts{browserAuth: true})
	disabled := newBrowserStack(t, stackOpts{browserAuth: false})

	refused := []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": "https://evil.example"},
	}
	for _, b := range []*browserStack{enabled, disabled} {
		for _, headers := range refused {
			resp := b.do(t, http.MethodDelete, "/v1/layers/some-layer", headers)
			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("%v: layer write = %d, want 403", headers, resp.StatusCode)
					return
				}
				if e := envelope(t, resp); e.Code != "auth.csrf_invalid" {
					t.Errorf("%v: code = %q, want auth.csrf_invalid", headers, e.Code)
				}
			}()
		}
	}

	// A write carrying no browser-origin evidence reaches the handler, which
	// is what every CLI and SDK writer sends. It answers the route's own
	// outcome rather than the gate's refusal.
	resp := enabled.do(t, http.MethodDelete, "/v1/layers/some-layer", nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		if e := envelope(t, resp); e.Code == "auth.csrf_invalid" {
			t.Error("a write carrying no browser-origin evidence was refused by the gate")
		}
	}
}

// Spec: §7.3.4 — a request carrying a session cookie past the token's exp
// reports differently on each surface, and the rule is what a panel built on
// a single expiry signal fails against. The meta-tool route reports the
// expiry, the layer read answers unfiltered, and the posture read answers 200
// with no subject.
func TestBrowserFlow_ExpiredSessionAcrossSurfaces(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	claims := gwClaims(b.idp.issuer(), "alice@acme.com", nil)
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	expired := &http.Cookie{Name: server.CookieSession, Value: b.idp.sign(t, claims)}

	metaTool := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, expired)
	defer metaTool.Body.Close()
	if metaTool.StatusCode != http.StatusUnauthorized {
		t.Fatalf("meta-tool route = %d, want 401", metaTool.StatusCode)
	}
	if e := envelope(t, metaTool); e.Code != "auth.token_expired" {
		t.Errorf("meta-tool code = %q, want auth.token_expired", e.Code)
	}

	layers := b.do(t, http.MethodGet, "/v1/layers", nil, expired)
	defer layers.Body.Close()
	if layers.StatusCode != http.StatusOK {
		t.Errorf("layer read = %d, want 200; it reports nothing about the verification failure", layers.StatusCode)
	}

	postureResp := b.do(t, http.MethodGet, server.PathWebUISession, nil, expired)
	defer postureResp.Body.Close()
	if postureResp.StatusCode != http.StatusOK {
		t.Fatalf("posture read = %d, want 200", postureResp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(postureResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode posture: %v", err)
	}
	if _, ok := body["subject"]; ok {
		t.Error("the posture read reported a subject for an unverifiable session")
	}
}

// Spec: §7.3.4 — the posture read reports the paths the mux registers and the
// caller's own subject, and it requires no credential.
func TestBrowserFlow_PostureRead(t *testing.T) {
	t.Parallel()
	enabled := newBrowserStack(t, stackOpts{browserAuth: true})
	disabled := newBrowserStack(t, stackOpts{browserAuth: false})

	read := func(b *browserStack, cookies ...*http.Cookie) map[string]any {
		resp := b.do(t, http.MethodGet, server.PathWebUISession, nil, cookies...)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("posture read = %d, want 200", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode posture: %v", err)
		}
		return body
	}

	off := read(disabled)
	auth, _ := off["browser_auth"].(map[string]any)
	if auth["enabled"] != false || len(auth) != 1 {
		t.Errorf("browser_auth = %v, want enabled false and no path fields", auth)
	}

	on := read(enabled)
	auth, _ = on["browser_auth"].(map[string]any)
	if auth["sign_in_path"] != server.PathWebUISignIn || auth["sign_out_path"] != server.PathWebUISignOut {
		t.Errorf("browser_auth = %v, want the paths the mux registers", auth)
	}
	// The registered paths answer, which is what keeps the page from spelling
	// a path the mux does not serve.
	resp := enabled.do(t, http.MethodGet, auth["sign_in_path"].(string), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("the reported sign_in_path answered %d", resp.StatusCode)
	}

	tx, code := enabled.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	cb := enabled.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
	cb.Body.Close()
	session := responseCookie(cb, server.CookieSession)
	if got := read(enabled, session)["subject"]; got != "alice@acme.com" {
		t.Errorf("subject = %v, want the session's verified subject", got)
	}
}

// Spec: §7.3.4 — sign-in, the callback, sign-out, and the posture read mutate
// no registry state, so a read-only registry serves each of them unchanged
// and none of them returns registry.read_only.
func TestBrowserFlow_ReadOnlyServesEveryRoute(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: true})
	b.mode.Set(server.ModeReadOnly)

	tx, code := b.signInLeg(t)
	state, _, _ := strings.Cut(tx.Value, ".")
	cb := b.do(t, http.MethodGet, server.PathWebUICallback+"?state="+state+"&code="+code, nil, tx)
	defer cb.Body.Close()
	if cb.StatusCode != http.StatusFound {
		t.Fatalf("callback in read-only mode = %d, want 302", cb.StatusCode)
	}
	session := responseCookie(cb, server.CookieSession)
	if session == nil {
		t.Fatal("no session cookie in read-only mode")
	}
	// An established session keeps reading while the registry serves from the
	// replica.
	read := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, session)
	defer read.Body.Close()
	if read.StatusCode != http.StatusOK {
		t.Errorf("read-only catalog read = %d, want 200", read.StatusCode)
	}
	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, server.PathWebUISession},
		{http.MethodPost, server.PathWebUISignOut},
	} {
		resp := b.do(t, probe.method, probe.path, map[string]string{"Sec-Fetch-Site": "same-origin"}, session)
		func() {
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				t.Errorf("%s %s in read-only mode = %d", probe.method, probe.path, resp.StatusCode)
			}
		}()
	}
}

// Spec: §7.3.4 — a registry that boots with the flow disabled registers none
// of the authentication routes, and a stale session cookie resolves anonymous
// there.
func TestBrowserFlow_DisabledRegistersNoRoutes(t *testing.T) {
	t.Parallel()
	b := newBrowserStack(t, stackOpts{browserAuth: false})
	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, server.PathWebUISignIn},
		{http.MethodGet, server.PathWebUICallback},
		{http.MethodPost, server.PathWebUISignOut},
	} {
		resp := b.do(t, probe.method, probe.path, nil)
		func() {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusNoContent {
				t.Errorf("%s %s answered %d on a registry with the flow disabled", probe.method, probe.path, resp.StatusCode)
			}
		}()
	}
	stale := &http.Cookie{Name: server.CookieSession, Value: b.idp.sign(t, gwClaims(b.idp.issuer(), "alice@acme.com", []string{"idp-eng"}))}
	read := b.do(t, http.MethodGet, "/v1/load_artifact?id=eng/secret", nil, stale)
	defer read.Body.Close()
	if read.StatusCode != http.StatusNotFound {
		t.Errorf("stale cookie read = %d, want 404; a disabled registry reads no cookie", read.StatusCode)
	}
}
