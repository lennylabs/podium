package webhook_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/webhook"
)

// newPolicy builds a URLPolicy from allowlist entries and fails the test
// on a malformed allowlist.
func newPolicy(t *testing.T, allowed ...string) *webhook.URLPolicy {
	t.Helper()
	p, err := webhook.NewURLPolicy(allowed)
	if err != nil {
		t.Fatalf("NewURLPolicy(%v): %v", allowed, err)
	}
	return p
}

// staticResolver returns a resolveHost function that maps a fixed host to
// fixed addresses. webhook.SetResolver wires it into the policy.
func staticResolver(host string, ips ...string) func(context.Context, string) ([]net.IP, error) {
	parsed := make([]net.IP, 0, len(ips))
	for _, s := range ips {
		parsed = append(parsed, net.ParseIP(s))
	}
	return func(_ context.Context, h string) ([]net.IP, error) {
		if h == host {
			return parsed, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
	}
}

// Spec: §7.3.2 — the SSRF policy requires the https scheme and rejects a
// plain-http receiver URL.
func TestURLPolicy_RejectsNonHTTPSScheme(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	err := p.Validate(context.Background(), "http://hooks.acme.com/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("want ErrDisallowedTarget, got %v", err)
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error should name the scheme requirement, got %q", err)
	}
	var dte *webhook.DisallowedTargetError
	if !errors.As(err, &dte) {
		t.Fatalf("want *DisallowedTargetError, got %T", err)
	}
	if dte.Host != "hooks.acme.com" {
		t.Fatalf("error should name the host, got %q", dte.Host)
	}
}

// Spec: §7.3.2 — a public host that resolves to a public address passes.
func TestURLPolicy_AllowsPublicAddress(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	webhook.SetResolver(p, staticResolver("hooks.acme.com", "93.184.216.34"))
	if err := p.Validate(context.Background(), "https://hooks.acme.com/ci"); err != nil {
		t.Fatalf("public address should pass, got %v", err)
	}
}

// Spec: §7.3.2 — the policy rejects loopback, link-local, and private
// targets by default, naming the disallowed host.
func TestURLPolicy_RejectsBlockedRanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		ip     string
		reason string
	}{
		{"ipv4 loopback", "127.0.0.1", "loopback"},
		{"ipv6 loopback", "::1", "loopback"},
		{"ipv4 loopback range", "127.5.6.7", "loopback"},
		{"ipv4 link-local", "169.254.1.1", "link-local"},
		{"ipv6 link-local", "fe80::1", "link-local"},
		{"rfc1918 ten", "10.0.0.5", "private"},
		{"rfc1918 172", "172.16.4.9", "private"},
		{"rfc1918 192", "192.168.1.1", "private"},
		{"ipv6 unique-local", "fc00::1", "private"},
		{"unspecified", "0.0.0.0", "unspecified"},
		{"ipv4 multicast", "239.1.2.3", "multicast"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPolicy(t)
			webhook.SetResolver(p, staticResolver("internal.acme.com", tc.ip))
			err := p.Validate(context.Background(), "https://internal.acme.com/ci")
			if !errors.Is(err, webhook.ErrDisallowedTarget) {
				t.Fatalf("%s: want ErrDisallowedTarget, got %v", tc.ip, err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("%s: error should name %q range, got %q", tc.ip, tc.reason, err)
			}
			if !strings.Contains(err.Error(), "internal.acme.com") {
				t.Fatalf("%s: error should name the host, got %q", tc.ip, err)
			}
		})
	}
}

// Spec: §7.3.2 — with no resolver injected, Validate falls back to the
// default DNS resolver. localhost resolves to loopback without an
// external network, so the default path rejects it.
func TestURLPolicy_DefaultResolverFallback(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	err := p.Validate(context.Background(), "https://localhost/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("localhost should resolve to loopback and be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error should name the loopback range, got %q", err)
	}
}

// Spec: §7.3.2 — an IP-literal host in a blocked range is rejected
// without a DNS lookup.
func TestURLPolicy_RejectsIPLiteral(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	err := p.Validate(context.Background(), "https://10.0.0.1/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("want ErrDisallowedTarget for private IP literal, got %v", err)
	}
}

// Spec: §7.3.2 — a host on the allowlist overrides the rejection of a
// resolved private address without a lookup.
func TestURLPolicy_AllowlistHostOverrides(t *testing.T) {
	t.Parallel()
	p := newPolicy(t, "relay.internal")
	// No resolver is wired; the host allowlist short-circuits resolution.
	if err := p.Validate(context.Background(), "https://relay.internal/ci"); err != nil {
		t.Fatalf("allowlisted host should pass, got %v", err)
	}
}

// Spec: §7.3.2 — an allowlisted host is exempt from the default policy,
// including the https requirement, because the operator has explicitly opted
// that host out. An in-cluster relay reached over plain http inside a trusted
// network is the case this serves, so an allowlisted host on an http URL passes
// while a non-allowlisted http host is still rejected.
func TestURLPolicy_AllowlistHostBypassesScheme(t *testing.T) {
	t.Parallel()
	p := newPolicy(t, "relay.internal", "127.0.0.1")
	// An allowlisted DNS host over plain http passes.
	if err := p.Validate(context.Background(), "http://relay.internal:8080/ci"); err != nil {
		t.Fatalf("allowlisted http host should pass, got %v", err)
	}
	// An allowlisted IP literal (a loopback) over plain http passes; this is the
	// loopback httptest receiver the standalone e2e webhook tests deliver to.
	if err := p.Validate(context.Background(), "http://127.0.0.1:9000/hook"); err != nil {
		t.Fatalf("allowlisted loopback http literal should pass, got %v", err)
	}
	// A host that is NOT allowlisted is still rejected over plain http.
	err := p.Validate(context.Background(), "http://hooks.acme.com/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("non-allowlisted http host should be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("rejection should name the scheme requirement, got %q", err)
	}
}

// Spec: §7.3.2 — a CIDR allowlist entry overrides the rejection of a
// resolved private address.
func TestURLPolicy_AllowlistCIDROverrides(t *testing.T) {
	t.Parallel()
	p := newPolicy(t, "10.0.0.0/8")
	webhook.SetResolver(p, staticResolver("relay.acme.com", "10.0.0.42"))
	if err := p.Validate(context.Background(), "https://relay.acme.com/ci"); err != nil {
		t.Fatalf("allowlisted CIDR should pass, got %v", err)
	}
}

// Spec: §7.3.2 — a CIDR allowlist that does not cover the resolved
// address does not override the rejection.
func TestURLPolicy_AllowlistCIDRDoesNotCover(t *testing.T) {
	t.Parallel()
	p := newPolicy(t, "10.0.0.0/8")
	webhook.SetResolver(p, staticResolver("relay.acme.com", "192.168.1.1"))
	err := p.Validate(context.Background(), "https://relay.acme.com/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("address outside the allowlisted CIDR should be rejected, got %v", err)
	}
}

// Spec: §7.3.2 — registration and delivery share Validate, so a host
// that resolves to a mix of public and private addresses is rejected on
// the private one.
func TestURLPolicy_RejectsMixedResolution(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	webhook.SetResolver(p, staticResolver("hooks.acme.com", "93.184.216.34", "127.0.0.1"))
	err := p.Validate(context.Background(), "https://hooks.acme.com/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("a private address in the resolution set should reject, got %v", err)
	}
}

// Spec: §7.3.2 — an unresolvable host is rejected with a host-naming
// error rather than passing.
func TestURLPolicy_RejectsUnresolvableHost(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	webhook.SetResolver(p, staticResolver("known.acme.com", "93.184.216.34"))
	err := p.Validate(context.Background(), "https://missing.acme.com/ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("unresolvable host should be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing.acme.com") {
		t.Fatalf("error should name the host, got %q", err)
	}
}

// Spec: §7.3.2 — a URL with no host is rejected.
func TestURLPolicy_RejectsEmptyHost(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	err := p.Validate(context.Background(), "https:///ci")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("URL with no host should be rejected, got %v", err)
	}
}

// Spec: §7.3.2 — a malformed receiver URL is rejected.
func TestURLPolicy_RejectsMalformedURL(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	err := p.Validate(context.Background(), "https://exa mple.com/\x7f")
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("malformed URL should be rejected, got %v", err)
	}
}

// DisallowedTargetError.Error names the host when one is known and omits
// it when the failure is host-independent.
func TestDisallowedTargetError_Message(t *testing.T) {
	t.Parallel()
	withHost := &webhook.DisallowedTargetError{Host: "internal.acme.com", Reason: "loopback address 127.0.0.1"}
	if !strings.Contains(withHost.Error(), "internal.acme.com") || !strings.Contains(withHost.Error(), "loopback") {
		t.Fatalf("error with host should name host and reason, got %q", withHost.Error())
	}
	noHost := &webhook.DisallowedTargetError{Reason: "URL has no host"}
	if !strings.Contains(noHost.Error(), "URL has no host") {
		t.Fatalf("error without host should carry the reason, got %q", noHost.Error())
	}
	if strings.Contains(noHost.Error(), "\"\"") {
		t.Fatalf("error without host should not print an empty host, got %q", noHost.Error())
	}
}

// DefaultResolveHost resolves a host through net.DefaultResolver.
// localhost resolves without an external network, so this covers the
// production resolver path that the injected resolver replaces in the
// other tests.
func TestDefaultResolveHost_ResolvesLocalhost(t *testing.T) {
	t.Parallel()
	ips, err := webhook.DefaultResolveHost(context.Background(), "localhost")
	if err != nil {
		t.Fatalf("resolving localhost should succeed, got %v", err)
	}
	if len(ips) == 0 {
		t.Fatalf("localhost should resolve to at least one address")
	}
}

// A malformed allowlist entry fails NewURLPolicy so a bad
// PODIUM_WEBHOOK_ALLOWED_TARGETS fails closed.
func TestNewURLPolicy_RejectsMalformedAllowlist(t *testing.T) {
	t.Parallel()
	cases := []string{
		"10.0.0.0/99",        // CIDR out of range
		"not a cidr/24",      // CIDR token that does not parse
		"https://relay.acme", // scheme in a bare host
		"relay.acme:8443",    // port in a bare host
		"user@relay.acme",    // userinfo in a bare host
	}
	for _, entry := range cases {
		entry := entry
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			if _, err := webhook.NewURLPolicy([]string{entry}); err == nil {
				t.Fatalf("entry %q should be rejected", entry)
			}
		})
	}
}

// NewURLPolicy skips blank and whitespace-only entries so a trailing
// comma in PODIUM_WEBHOOK_ALLOWED_TARGETS does not fail the policy.
func TestNewURLPolicy_SkipsBlankEntries(t *testing.T) {
	t.Parallel()
	p, err := webhook.NewURLPolicy([]string{"", "  ", "relay.internal"})
	if err != nil {
		t.Fatalf("blank entries should be skipped, got %v", err)
	}
	if err := p.Validate(context.Background(), "https://relay.internal/ci"); err != nil {
		t.Fatalf("allowlisted host should pass, got %v", err)
	}
}

// Spec: §7.3.2 — NoRedirect disables redirect following so a 30x to an
// internal target cannot bypass the registration-time check.
func TestNoRedirect_StopsAtFirstResponse(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodGet, "https://hooks.acme.com/ci", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := webhook.NoRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("NoRedirect should return http.ErrUseLastResponse, got %v", err)
	}
}

// Spec: §7.3.2 — the policy-aware CheckRedirect refuses a redirect whose
// target the SSRF policy disallows, so a 30x to an internal address is not
// followed.
func TestURLPolicy_CheckRedirectRejectsDisallowedTarget(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	webhook.SetResolver(p, staticResolver("internal.acme.com", "10.1.2.3"))
	req, err := http.NewRequest(http.MethodGet, "https://internal.acme.com/secret", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	err = p.CheckRedirect(req, nil)
	if !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Fatalf("CheckRedirect should refuse a private target, got %v", err)
	}
}

// Spec: §7.3.2 — the policy-aware CheckRedirect follows a redirect to an
// allowed (public) target.
func TestURLPolicy_CheckRedirectAllowsPublicTarget(t *testing.T) {
	t.Parallel()
	p := newPolicy(t)
	webhook.SetResolver(p, staticResolver("hooks.acme.com", "93.184.216.34"))
	req, err := http.NewRequest(http.MethodGet, "https://hooks.acme.com/ci", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := p.CheckRedirect(req, nil); err != nil {
		t.Fatalf("CheckRedirect should allow a public target, got %v", err)
	}
}
