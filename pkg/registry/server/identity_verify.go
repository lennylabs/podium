package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
)

// verifiedIDKey is the context key under which the identity-verification
// middleware stores the verified caller Identity (§6.3.2) so handlers and
// the audit emitter recover one verified identity per request.
type verifiedIDKey struct{}

// withVerifiedIdentity returns ctx carrying the verified caller identity.
func withVerifiedIdentity(ctx context.Context, id layer.Identity) context.Context {
	return context.WithValue(ctx, verifiedIDKey{}, id)
}

// verifiedIdentityFrom recovers the verified identity, reporting false when
// no verifier ran for the request (the anonymous resolveID path).
func verifiedIdentityFrom(ctx context.Context) (layer.Identity, bool) {
	id, ok := ctx.Value(verifiedIDKey{}).(layer.Identity)
	return id, ok
}

// withIdentityVerification enforces the §6.3.2 "verify the signature on
// every call" contract. When a verifier is installed, every request to a
// route that carries caller identity is verified before the handler runs:
// a failure is rejected with the §6.10 auth.* envelope, and a success
// carries the Identity to the handler via the request context. Operational
// and federation routes (health, readiness, SCIM push, the data-plane
// object store) are exempt because they do not run on a caller session
// token. With no verifier the middleware is a pass-through, so standalone,
// public-mode, and oauth-device-code deployments are unaffected.
func (s *Server) withIdentityVerification(next http.Handler) http.Handler {
	if s.idVerifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !pathRequiresIdentity(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := s.idVerifier(r)
		if err != nil {
			s.writeIdentityError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withVerifiedIdentity(r.Context(), id)))
	})
}

// pathRequiresIdentity reports whether a route carries caller identity and
// must therefore be verified in injected-session-token mode. Health and
// readiness probes, the SCIM 2.0 receiver (authenticated by the IdP's own
// bearer token), and the data-plane object store (presigned-URL access)
// run without a caller session token and are exempt.
func pathRequiresIdentity(p string) bool {
	switch {
	case p == "/healthz", p == "/readyz":
		return false
	case strings.HasPrefix(p, "/scim/"):
		return false
	case strings.HasPrefix(p, "/objects/"):
		return false
	}
	return true
}

// writeIdentityError maps a verification failure to the §6.10 envelope.
// An expired token reports auth.token_expired; every other failure reports
// auth.untrusted_runtime with details.runtime_iss naming the offending
// issuer when the token carried one (matching the §6.10 canonical example).
func (s *Server) writeIdentityError(w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrTokenExpired) {
		writeError(w, http.StatusUnauthorized, "auth.token_expired",
			"the session token has expired; the runtime is responsible for refresh")
		return
	}
	var ute *identity.UntrustedRuntimeError
	if errors.As(err, &ute) && ute.Issuer != "" {
		writeErrorDetails(w, http.StatusUnauthorized, "auth.untrusted_runtime",
			"runtime '"+ute.Issuer+"' is not registered with the registry",
			map[string]any{"runtime_iss": ute.Issuer})
		return
	}
	writeError(w, http.StatusUnauthorized, "auth.untrusted_runtime",
		"the session token could not be verified against a registered runtime key")
}
