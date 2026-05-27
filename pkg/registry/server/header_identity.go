// Header-based identity resolver for standalone deployments.
//
// HeaderIdentityResolver reads caller identity from a set of trusted
// HTTP request headers and returns the resulting layer.Identity.
// This is the resolver wired by serverboot.Run when the operator
// selects the §6.3 trusted-headers identity provider via
// PODIUM_IDENTITY_PROVIDER=trusted-headers.
//
// Trust model. The headers are advisory by themselves. Any client
// that can reach the registry over HTTP can claim any identity by
// setting these headers. Header mode is intended for deployments
// where a trusted upstream proxy strips and re-issues the headers
// from a verified token (a JWT verifier sidecar, an ingress that
// terminates OIDC, the per-user delegation bridge used in the
// ai-platform-demo, etc.). Production deployments that expose the
// registry directly to untrusted callers should run in public mode
// and add their own resolver via server.WithIdentityResolver.
//
// Headers consumed:
//
//   X-Podium-User-Sub      stable subject identifier (required for IsAuthenticated)
//   X-Podium-User-Email    email address
//   X-Podium-User-Org      organization identifier
//   X-Podium-User-Groups   comma-separated group memberships
//
// When the Sub and Email headers are both empty, the resolver
// returns an anonymous-public identity, matching the default
// resolver in server.New.

package server

import (
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
)

// Header names consumed by HeaderIdentityResolver. Exported so
// callers can document the contract or build clients that target
// the same names without copy-pasting strings.
const (
	HeaderUserSub    = "X-Podium-User-Sub"
	HeaderUserEmail  = "X-Podium-User-Email"
	HeaderUserOrg    = "X-Podium-User-Org"
	HeaderUserGroups = "X-Podium-User-Groups"
)

// HeaderIdentityResolver builds a layer.Identity from the trusted
// X-Podium-User-* headers on r. When neither Sub nor Email is set,
// the returned identity has IsPublic:true so visibility checks
// follow the public-mode bypass that matches the default resolver.
func HeaderIdentityResolver(r *http.Request) layer.Identity {
	sub := strings.TrimSpace(r.Header.Get(HeaderUserSub))
	email := strings.TrimSpace(r.Header.Get(HeaderUserEmail))
	if sub == "" && email == "" {
		return layer.Identity{IsPublic: true}
	}
	return layer.Identity{
		Sub:             sub,
		Email:           email,
		OrgID:           strings.TrimSpace(r.Header.Get(HeaderUserOrg)),
		Groups:          parseGroupsHeader(r.Header.Get(HeaderUserGroups)),
		IsAuthenticated: true,
	}
}

// parseGroupsHeader splits a comma-separated header value into a
// slice of trimmed, non-empty group names. Whitespace around
// commas is tolerated so callers can format the header for
// readability.
func parseGroupsHeader(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
