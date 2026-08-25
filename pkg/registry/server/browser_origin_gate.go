package server

import (
	"net/http"
	"net/url"
	"strings"
)

// BrowserOriginGate refuses a state-changing request carrying cross-site
// browser-origin evidence with 403 auth.csrf_invalid before the handler runs,
// whatever credential authenticated it (§6.3.4).
//
// The gate is scoped by the evidence the request carries rather than by which
// credential authenticated it, because the session cookie is not the only
// credential a browser attaches by itself: where a gateway fronts the
// registry, the gateway converts its own ambient session into the configured
// token header on every request the browser can be induced to make, including
// a cross-origin form POST. It is likewise not conditional on the browser
// flow or on the web UI, so the gateway-fronted deployment is covered as
// well, and a non-browser client carries no such evidence either way.
//
// It wraps the boot mux rather than joining the Server middleware chain,
// which the boot mux serves at the catch-all alone: installed there the gate
// would miss every layer write, every webhook ingest, and the erase endpoint,
// which are the set it exists to protect. It reads the request's method and
// its Sec-Fetch-Site and Origin headers against its own Host, and reads and
// writes no other state.
func BrowserOriginGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !stateChanging(r.Method) || gateExcluded(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if crossSiteEvidence(r) {
			writeError(w, http.StatusForbidden, "auth.csrf_invalid",
				"The request was refused because it did not pass the browser-origin check.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// stateChanging reports whether the method is other than GET, HEAD, or
// OPTIONS. The predicate is the method rather than the handler's effect,
// because the gate runs before the handler and has nothing else to read.
func stateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// gateExcluded reports whether the path is one §6.3.4 excludes by name. Both
// answer on GET, so the method predicate already leaves them outside the
// gate; the exclusion is stated by name as well so that widening the method
// predicate does not pull them in. A browser that already holds a session
// cookie sends it on both, and refusing them would leave an expired session
// with no recovery.
func gateExcluded(path string) bool {
	return path == PathWebUISignIn || path == PathWebUICallback
}

// crossSiteEvidence reports whether the request carries browser-origin
// evidence the gate reads as cross-site: a Sec-Fetch-Site header whose value
// is other than same-origin or none, or an Origin header whose host and port
// differ from the host and port the request's own Host header names.
//
// The scheme is not compared. An HTTP Host header carries no scheme, and a
// registry behind a TLS-terminating gateway cannot observe the browser-facing
// one, so a scheme comparison would refuse every panel write on a gateway-
// fronted deployment. Omitting the scheme term admits a downgrade origin,
// which costs the gate nothing, because §13.10 requires the flow's redirect
// URI to be an https URL or a loopback http URL and a browser therefore holds
// no session credential to present on such an origin.
//
// A request carrying neither header carries no such evidence and is admitted,
// which is what a CLI, an SDK, or any other non-browser client sends.
func crossSiteEvidence(r *http.Request) bool {
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "":
	case "same-origin", "none":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// An opaque origin ("null") and an unparsable one prove nothing
		// about same-origin, so the gate fails closed on them.
		return true
	}
	return !strings.EqualFold(u.Host, r.Host)
}
