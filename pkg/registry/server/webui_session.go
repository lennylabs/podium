package server

import (
	"net/http"

	"github.com/lennylabs/podium/pkg/layer"
)

// SessionPosture serves the §7.3.4 posture read, GET /v1/ui/session. It
// reports the deployment's identity posture and the caller's own resolved
// subject, and nothing else: no issuer, client identifier, endpoint, or other
// configuration value, and no artifact, layer, tenant, or other caller's
// data. The read requires no credential and refuses no request for lack of
// one; a request that carries one has it verified only so the response can
// report `subject`.
//
// The browser can observe neither of the two things it reports: the session
// cookie is HttpOnly, and no other shipped response separates the postures.
// The UI's sign-in control and its rendering rules key on this read.
type SessionPosture struct {
	// IdentityProviderConfigured reports whether an identity provider is
	// configured. The body never names which one.
	IdentityProviderConfigured bool
	// PublicMode reports whether public mode is engaged.
	PublicMode bool
	// BrowserAuthEnabled reports whether the §6.3.4 browser flow is enabled
	// on this deployment. The path fields are reported only when it is,
	// because the flow's routes are registered only then.
	BrowserAuthEnabled bool
	// Identity resolves the requesting caller with the error-swallowing
	// resolver, so an unverifiable session resolves the anonymous caller and
	// the response omits `subject`, which is what §7.3.4 requires of a read
	// that refuses no request for lack of a credential. The §7.3.1 layer
	// endpoint refuses the same credential.
	Identity func(*http.Request) layer.Identity
}

// Handler serves the posture read.
func (p SessionPosture) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !methodIs(w, r, http.MethodGet) {
			return
		}
		browserAuth := map[string]any{"enabled": p.BrowserAuthEnabled}
		if p.BrowserAuthEnabled {
			browserAuth["sign_in_path"] = PathWebUISignIn
			browserAuth["sign_out_path"] = PathWebUISignOut
		}
		body := map[string]any{
			"identity_provider_configured": p.IdentityProviderConfigured,
			"public_mode":                  p.PublicMode,
			"browser_auth":                 browserAuth,
		}
		if p.Identity != nil {
			if id := p.Identity(r); id.IsAuthenticated && id.Sub != "" {
				body["subject"] = id.Sub
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
}
