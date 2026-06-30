package webhook

// SSRF policy for receiver URLs (§7.3.2). The registry originates the
// outbound request to a receiver URL, so an unrestricted URL lets a
// caller who can register a receiver point the registry at an internal
// endpoint it would not otherwise reach. URLPolicy validates a receiver
// URL at registration and re-checks it at delivery so both agree: it
// requires the https scheme, resolves the host, and rejects any address
// in a loopback, link-local, or private range unless an allowlist entry
// overrides the rejection.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ErrDisallowedTarget is the sentinel that every SSRF rejection wraps.
// Callers test for it with errors.Is and surface registry.invalid_argument.
var ErrDisallowedTarget = errors.New("webhook: disallowed target")

// DisallowedTargetError names the host a URLPolicy rejected and the
// reason. It wraps ErrDisallowedTarget so errors.Is(err, ErrDisallowedTarget)
// holds, and its message names the host so the handler can surface it in
// the registry.invalid_argument response.
type DisallowedTargetError struct {
	// Host is the receiver URL host that failed validation.
	Host string
	// Reason describes why the host was rejected (bad scheme,
	// unresolvable host, or a resolved address in a blocked range).
	Reason string
}

func (e *DisallowedTargetError) Error() string {
	if e.Host == "" {
		return fmt.Sprintf("webhook: disallowed target: %s", e.Reason)
	}
	return fmt.Sprintf("webhook: disallowed target %q: %s", e.Host, e.Reason)
}

func (e *DisallowedTargetError) Unwrap() error { return ErrDisallowedTarget }

// URLPolicy decides whether a receiver URL is an allowed delivery
// target. The zero value rejects loopback, link-local, and private
// addresses and requires https. AllowedTargets overrides the rejection
// for the deployments that run a legitimately internal receiver.
type URLPolicy struct {
	// AllowedTargets is the parsed allowlist sourced from
	// PODIUM_WEBHOOK_ALLOWED_TARGETS (§13.12). An entry is either a
	// bare host (matched case-insensitively against the URL host) or a
	// CIDR (matched against every resolved address). A URL that matches
	// any entry is allowed even when it resolves to a blocked range.
	// Build the policy with NewURLPolicy so malformed entries are
	// rejected before the policy is used.
	allowed []allowEntry

	// resolveHost resolves a host to its IP addresses. Production leaves
	// it nil and the policy uses net.DefaultResolver. Tests override it
	// to drive resolution without DNS.
	resolveHost func(ctx context.Context, host string) ([]net.IP, error)
}

// allowEntry is one parsed allowlist rule: exactly one of host or cidr
// is set.
type allowEntry struct {
	host string     // bare host, lowercased; empty when cidr is set
	cidr *net.IPNet // CIDR network; nil when host is set
}

// NewURLPolicy parses the allowlist entries and returns a URLPolicy.
// Each entry is a bare host or a CIDR. An entry that is neither, or a
// CIDR that does not parse, is a malformed allowlist and returns an
// error so a deployment fails closed on a bad PODIUM_WEBHOOK_ALLOWED_TARGETS
// rather than silently widening or narrowing the policy.
func NewURLPolicy(allowedTargets []string) (*URLPolicy, error) {
	p := &URLPolicy{}
	for _, raw := range allowedTargets {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		parsed, err := parseAllowEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("webhook: allowlist entry %q: %w", entry, err)
		}
		p.allowed = append(p.allowed, parsed)
	}
	return p, nil
}

// AllowedTargets returns the parsed allowlist entries as their canonical
// strings: a bare host is returned lowercased, and a CIDR is returned in
// its normalized network form. The order matches the parse order. Boot
// logs the result to report the policy it wired, and a caller confirms an
// empty slice means the strict default (https plus private-target
// rejection with no overrides).
func (p *URLPolicy) AllowedTargets() []string {
	if len(p.allowed) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.allowed))
	for _, e := range p.allowed {
		if e.cidr != nil {
			out = append(out, e.cidr.String())
			continue
		}
		out = append(out, e.host)
	}
	return out
}

// parseAllowEntry classifies one allowlist token as a CIDR or a bare
// host. A token containing "/" is a CIDR and must parse; otherwise it is
// a bare host and must not be empty or contain a scheme, port, or path,
// which would make matching against the URL host ambiguous.
func parseAllowEntry(entry string) (allowEntry, error) {
	if strings.Contains(entry, "/") {
		_, cidr, err := net.ParseCIDR(entry)
		if err != nil {
			return allowEntry{}, fmt.Errorf("malformed CIDR: %w", err)
		}
		return allowEntry{cidr: cidr}, nil
	}
	if strings.ContainsAny(entry, ":@? ") {
		// A bare host carries no scheme, userinfo, port, or query. A
		// bracketed IPv6 literal arrives without brackets in this form,
		// so reject the ambiguous tokens rather than guess.
		return allowEntry{}, errors.New("malformed host: must be a bare host or a CIDR")
	}
	return allowEntry{host: strings.ToLower(entry)}, nil
}

// Validate reports whether rawURL is an allowed delivery target. By
// default it requires the https scheme, resolves the host, and rejects
// the URL when any resolved address falls in a loopback, link-local, or
// private range. A host on the allowlist is exempt from the default
// policy: the operator has explicitly opted that host out, so it is
// permitted without resolving it and without the https requirement, which
// is what an in-cluster relay reached over plain http inside a trusted
// network needs. A rejection is a *DisallowedTargetError wrapping
// ErrDisallowedTarget. Registration and delivery both call Validate so
// the two agree.
func (p *URLPolicy) Validate(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return &DisallowedTargetError{Reason: fmt.Sprintf("malformed URL: %v", err)}
	}
	host := u.Hostname()

	// A host on the allowlist is permitted without resolving it. The check
	// runs before the scheme requirement so an explicitly allowlisted
	// internal target reached over plain http is permitted; the default
	// policy (https plus private-range rejection) applies only to hosts the
	// operator did not allowlist.
	if host != "" && p.hostAllowed(host) {
		return nil
	}

	if u.Scheme != "https" {
		return &DisallowedTargetError{
			Host:   host,
			Reason: fmt.Sprintf("scheme %q is not https", u.Scheme),
		}
	}
	if host == "" {
		return &DisallowedTargetError{Reason: "URL has no host"}
	}

	ips, err := p.resolve(ctx, host)
	if err != nil {
		return &DisallowedTargetError{
			Host:   host,
			Reason: fmt.Sprintf("host does not resolve: %v", err),
		}
	}
	if len(ips) == 0 {
		return &DisallowedTargetError{Host: host, Reason: "host resolves to no addresses"}
	}
	for _, ip := range ips {
		if p.ipAllowed(ip) {
			continue
		}
		if blocked, why := blockedAddress(ip); blocked {
			return &DisallowedTargetError{
				Host:   host,
				Reason: fmt.Sprintf("resolves to %s %s", why, ip),
			}
		}
	}
	return nil
}

// hostAllowed reports whether host matches a bare-host allowlist entry,
// case-insensitively.
func (p *URLPolicy) hostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, e := range p.allowed {
		if e.cidr == nil && e.host == host {
			return true
		}
	}
	return false
}

// ipAllowed reports whether ip falls in a CIDR allowlist entry.
func (p *URLPolicy) ipAllowed(ip net.IP) bool {
	for _, e := range p.allowed {
		if e.cidr != nil && e.cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// resolve returns the IP addresses for host. A host that is itself an IP
// literal resolves to that single address without a lookup.
func (p *URLPolicy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	resolve := p.resolveHost
	if resolve == nil {
		resolve = defaultResolveHost
	}
	return resolve(ctx, host)
}

// defaultResolveHost looks host up through net.DefaultResolver.
func defaultResolveHost(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// blockedAddress reports whether ip is in a range the SSRF policy
// rejects by default and names the range. The ranges are loopback
// (127.0.0.0/8, ::1), link-local (169.254.0.0/16, fe80::/10), and
// private (RFC 1918 10/8, 172.16/12, 192.168/16, fc00::/7). It also
// rejects the unspecified address and the IPv4-broadcast and multicast
// ranges, which are not valid receiver targets.
func blockedAddress(ip net.IP) (bool, string) {
	switch {
	case ip.IsLoopback():
		return true, "loopback address"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return true, "link-local address"
	case ip.IsUnspecified():
		return true, "unspecified address"
	case ip.IsMulticast():
		return true, "multicast address"
	case ip.IsPrivate():
		// IsPrivate covers the RFC 1918 IPv4 ranges and the IPv6
		// unique-local range fc00::/7.
		return true, "private address"
	}
	return false, ""
}

// NoRedirect is the http.Client.CheckRedirect hook that disables
// redirect following for webhook delivery. A receiver that 30x-redirects
// to an internal target would bypass the registration-time SSRF check,
// so delivery refuses to follow any redirect. Wire it as
// client.CheckRedirect = webhook.NoRedirect.
func NoRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// CheckRedirect is the http.Client.CheckRedirect hook that follows a
// redirect only when its target passes the SSRF policy (§7.3.2). A
// receiver that 30x-redirects to a loopback, link-local, or private
// address is refused so the redirect cannot reach a target the policy
// blocks, while a redirect to an allowed target is followed. Wire it as
// client.CheckRedirect = policy.CheckRedirect.
func (p *URLPolicy) CheckRedirect(req *http.Request, _ []*http.Request) error {
	if err := p.Validate(req.Context(), req.URL.String()); err != nil {
		return err
	}
	return nil
}
