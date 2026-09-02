package server

import (
	"net/http"

	"github.com/lennylabs/podium/pkg/layer"
)

// SessionPosture serves the §7.3.4 posture read, GET /v1/ui/session. It
// reports the deployment's identity posture, the caller's own resolved
// subject, and what that caller may do on the §7.3.1 layer operations, and
// nothing else: no issuer, client identifier, endpoint, filesystem path, or
// other configuration value, and no artifact, layer, tenant, or other
// caller's subject or authorization. The read requires no credential and
// refuses no request for lack of one; a request that carries one has it
// verified so the response can report `subject` and evaluate
// `layer_capabilities`, and for no other purpose.
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
	// Capabilities evaluates the caller's §7.3.1 layer capabilities. Boot
	// passes the layer endpoint's own evaluator, so the value a client
	// renders on and the gate the registry applies are one expression. A nil
	// seam reports every member false: the layer endpoint's constructor
	// installs an admitting admin arm by default, and a reporting surface
	// defaults the other way.
	Capabilities func(*http.Request) LayerCapabilities
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
		// §7.3.4: the object and its member are always present, so the
		// closed default is written here once rather than at each caller.
		var caps LayerCapabilities
		if p.Capabilities != nil {
			caps = p.Capabilities(r)
		}
		body["layer_capabilities"] = caps
		if p.Identity != nil {
			if id := p.Identity(r); id.IsAuthenticated && id.Sub != "" {
				body["subject"] = id.Sub
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
}
