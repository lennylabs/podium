package identity

import (
	"context"
	"crypto/subtle"
	"strings"
)

// TrustedHeaders is the server-side trusted-headers provider (§6.3.3). It
// trusts identity headers injected by a fronting gateway without verifying
// them, resting on the operator's guarantee that every request arrived through
// the gateway. Identity is resolved from the request by the request-time
// verifier, so Resolve is inbound-only.
type TrustedHeaders struct{}

// ID returns "trusted-headers".
func (TrustedHeaders) ID() string { return "trusted-headers" }

// Resolve reports that trusted-headers resolves the caller from the inbound
// request rather than acquiring a token to present.
func (TrustedHeaders) Resolve(context.Context) (Identity, error) {
	return Identity{}, errServerSideProvider
}

// The fixed §6.3.3 trusted-headers request headers. They are part of the wire
// contract and carry no environment override.
const (
	// HeaderUserSub carries the caller's OIDC subject.
	HeaderUserSub = "X-Podium-User-Sub"
	// HeaderUserEmail carries the caller's email.
	HeaderUserEmail = "X-Podium-User-Email"
	// HeaderUserGroups carries the caller's groups, comma-separated.
	HeaderUserGroups = "X-Podium-User-Groups"
	// HeaderUserOrg carries the caller's organization (an org ID or org-name
	// alias, §4.7.1). Consulted only by a multi-tenant registry; a single-tenant
	// registry resolves every caller to its sole tenant (§6.3.3).
	HeaderUserOrg = "X-Podium-User-Org"
	// HeaderProxySecret carries the shared secret matched against
	// PODIUM_TRUSTED_PROXY_SECRET.
	HeaderProxySecret = "X-Podium-Proxy-Secret"
)

// IdentityFromTrustedHeaders builds the §6.3.3 caller Identity from
// gateway-injected headers.
//
// When proxySecret is non-empty, the identity headers are honored only on a
// request whose presentedSecret matches under a constant-time comparison; any
// other request is anonymous. When proxySecret is empty the presented secret is
// ignored and the identity headers are honored on every request.
//
// A missing X-Podium-User-Sub yields an anonymous (unauthenticated) Identity.
// trusted-headers raises no authentication error: a missing or distrusted
// identity is anonymous and sees public visibility only (§4.6), never a
// rejection. Groups come from X-Podium-User-Groups directly; SCIM and the
// IdpGroupMapping adapter are not consulted in this mode.
func IdentityFromTrustedHeaders(sub, email, groups, org, proxySecret, presentedSecret string) Identity {
	if proxySecret != "" &&
		subtle.ConstantTimeCompare([]byte(proxySecret), []byte(presentedSecret)) != 1 {
		return Identity{}
	}
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return Identity{}
	}
	id := Identity{
		Sub:             sub,
		Email:           strings.TrimSpace(email),
		OrgID:           strings.TrimSpace(org),
		IsAuthenticated: true,
	}
	for _, g := range strings.Split(groups, ",") {
		if g = strings.TrimSpace(g); g != "" {
			id.Groups = append(id.Groups, g)
		}
	}
	return id
}
