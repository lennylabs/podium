package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
)

// The §7.3.4 browser authentication route paths and the web UI's posture
// read. They are exported because the boot mux registers them, the §6.3.4
// browser-origin gate excludes two of them by name, and the posture read
// reports the registered paths rather than a literal the bundle spells.
const (
	PathWebUISignIn   = "/v1/ui/auth/sign-in"
	PathWebUICallback = "/v1/ui/auth/callback"
	PathWebUISignOut  = "/v1/ui/auth/sign-out"
	PathWebUISession  = "/v1/ui/session"
)

// The §7.3.4 cookie names. Both carry the __Host- prefix, which forbids a
// Domain attribute and forces Secure and Path=/, so no sibling host can plant
// either and neither needs a server-side signing key.
const (
	// CookieSession carries the access token the callback obtained. Its
	// lifetime is bounded server-side by the token's own exp, so it carries
	// no Max-Age.
	CookieSession = "__Host-podium_session"
	// CookieAuthTransaction carries the single-use pre-authorization
	// transaction: the state and the PKCE code_verifier the sign-in route
	// minted. Its Max-Age is the configured transaction TTL.
	CookieAuthTransaction = "__Host-podium_auth"
)

// webUIRoot is where the callback returns the browser on success and on a
// declined consent prompt (§7.3.4).
const webUIRoot = "/ui/"

// BrowserAuthConfig configures the §6.3.4 browser acquisition routes.
type BrowserAuthConfig struct {
	// Flow is the OAuth protocol client. It owns every parameter name the
	// flow puts on the wire; this package spells none of its own.
	Flow identity.AuthCodeFlow
	// TransactionTTL is the sign-in window, carried as
	// __Host-podium_auth's Max-Age (§7.3.4).
	TransactionTTL time.Duration
}

// BrowserAuthEndpoint serves the §7.3.4 sign-in, callback, and sign-out
// routes. It owns the cookies, the callback ordering, and the §6.10 status
// mapping, and keeps no session state: the pre-authorization transaction
// travels with the browser, so any replica serves the callback.
type BrowserAuthEndpoint struct {
	cfg BrowserAuthConfig
}

// NewBrowserAuthEndpoint returns the endpoint serving cfg.
func NewBrowserAuthEndpoint(cfg BrowserAuthConfig) *BrowserAuthEndpoint {
	return &BrowserAuthEndpoint{cfg: cfg}
}

// SignInHandler serves GET /v1/ui/auth/sign-in. It mints one transaction,
// returns it in __Host-podium_auth, and redirects the browser to the
// configured authorization endpoint (§7.3.4).
func (e *BrowserAuthEndpoint) SignInHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !methodIs(w, r, http.MethodGet) {
			return
		}
		tx, err := identity.NewAuthTransaction()
		if err != nil {
			// Reachable only from a crypto/rand failure, which no supported
			// platform produces, so no test drives this arm.
			writeError(w, http.StatusInternalServerError, "registry.unavailable",
				"The sign-in transaction could not be minted.")
			return
		}
		redirect, err := e.cfg.Flow.AuthorizationRequest(tx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable",
				"The authorization request could not be built.")
			return
		}
		http.SetCookie(w, hostCookie(CookieAuthTransaction, encodeTransaction(tx),
			transactionMaxAge(e.cfg.TransactionTTL)))
		http.Redirect(w, r, redirect, http.StatusFound)
	})
}

// CallbackHandler serves GET /v1/ui/auth/callback. It compares the returned
// state against __Host-podium_auth before inspecting anything else in the
// query, then branches on the IdP's error parameter, and exchanges the code
// server-side otherwise. Every response it emits clears __Host-podium_auth,
// which is what makes the transaction single-use (§7.3.4).
func (e *BrowserAuthEndpoint) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The callback consumes the transaction on the request that delivers
		// it, so the clearing Set-Cookie is a property of the route rather
		// than of the outcome. Set it before any body is written and ahead of
		// every arm below, including the method refusal and a request that
		// delivered no cookie to consume. The §6.3.4 gate excludes this route
		// by name, so a cross-origin form POST reaches the method refusal, and
		// a refusal that skipped the clearing would leave the transaction
		// replayable for the rest of its Max-Age.
		http.SetCookie(w, clearedHostCookie(CookieAuthTransaction))
		if !methodIs(w, r, http.MethodGet) {
			return
		}

		cb := e.cfg.Flow.ParseCallback(r.URL.Query())
		tx, ok := readTransaction(r)
		if !ok || cb.State == "" || cb.State != tx.State {
			// An absent, expired, or non-matching transaction is refused
			// whatever else the query carries (§6.3.4, §7.3.4). The §6.3.4
			// gate excludes this route, so the message names the
			// pre-authorization transaction rather than the browser-origin
			// check the gate reports; the two refusals share the code and
			// stay distinguishable from the body.
			writeError(w, http.StatusForbidden, "auth.csrf_invalid",
				"The sign-in transaction was missing, expired, or did not match; re-run sign-in.")
			return
		}
		if cb.Error != "" {
			// A declined consent prompt or a refused authorization request.
			// The recovery is re-running sign-in, so it takes no error code
			// and establishes or replaces no session (§7.3.4).
			http.Redirect(w, r, webUIRoot, http.StatusFound)
			return
		}
		tokens, err := e.cfg.Flow.Exchange(r.Context(), cb.Code, tx.Verifier)
		if err != nil {
			var refused *identity.ExchangeRefusedError
			if errors.As(err, &refused) {
				writeError(w, http.StatusBadGateway, "auth.exchange_failed",
					"The identity provider refused the authorization code exchange.")
				return
			}
			writeError(w, http.StatusInternalServerError, "registry.unavailable",
				"The identity provider could not be reached for the authorization code exchange.")
			return
		}
		// The session cookie carries the access token, which is the
		// credential §6.3.3 already accepts. Its lifetime is the token's own
		// exp, so the cookie carries no Max-Age.
		http.SetCookie(w, hostCookie(CookieSession, tokens.AccessToken, 0))
		http.Redirect(w, r, webUIRoot, http.StatusFound)
	})
}

// SignOutHandler serves POST /v1/ui/auth/sign-out. It clears both cookies on
// every request it serves, so a sign-out mid-transaction leaves none behind
// (§7.3.4). A sign-out the §6.3.4 gate refuses never reaches this handler.
func (e *BrowserAuthEndpoint) SignOutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !methodIs(w, r, http.MethodPost) {
			return
		}
		http.SetCookie(w, clearedHostCookie(CookieSession))
		http.SetCookie(w, clearedHostCookie(CookieAuthTransaction))
		w.WriteHeader(http.StatusNoContent)
	})
}

// methodIs answers the §6.10 method refusal and reports whether the request
// carries the method the route answers on (§7.3.4 fixes one per route).
func methodIs(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
		"method not allowed: "+r.Method)
	return false
}

// transactionMaxAge renders the configured sign-in window as a cookie
// Max-Age, rounding up to at least one second. hostCookie reads a maxAge of 0
// as the session row's form, so a truncated sub-second window would emit
// __Host-podium_auth with no Max-Age at all and leave the transaction live for
// the whole browser session. A sub-second duration reaches this function from
// the --web-ui-auth-transaction-ttl flag, which the environment clamp in
// internal/serverboot does not see.
func transactionMaxAge(ttl time.Duration) int {
	seconds := (ttl + time.Second - 1) / time.Second
	if seconds < 1 {
		return 1
	}
	return int(seconds)
}

// hostCookie builds a __Host- cookie per the §7.3.4 cookie contract. A
// maxAge of 0 leaves the attribute off, which is the session row's form.
func hostCookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearedHostCookie expires a __Host- cookie. The attributes match the set
// form, because a browser matches a clearing cookie on name, path, and
// domain.
func clearedHostCookie(name string) *http.Cookie {
	return hostCookie(name, "", -1)
}

// encodeTransaction renders the pre-authorization transaction for the
// cookie. Both halves are base64.RawURLEncoding, which carries no ".", so one
// separator round-trips them. Neither value is signed or encrypted: each is
// compared against something the IdP returns, so tampering breaks only the
// tamperer's own flow (§6.3.4).
func encodeTransaction(tx identity.AuthTransaction) string {
	return tx.State + "." + tx.Verifier
}

// readTransaction recovers the transaction from the request's
// __Host-podium_auth cookie, reporting false when the cookie is absent or
// does not carry both halves. An expired cookie is one the browser does not
// send, so it reaches this function as an absent one.
func readTransaction(r *http.Request) (identity.AuthTransaction, bool) {
	c, err := r.Cookie(CookieAuthTransaction)
	if err != nil {
		return identity.AuthTransaction{}, false
	}
	state, verifier, ok := strings.Cut(c.Value, ".")
	if !ok || state == "" || verifier == "" {
		return identity.AuthTransaction{}, false
	}
	return identity.AuthTransaction{State: state, Verifier: verifier}, true
}
