package audit

import "net/url"

// ClientHeader is the §7.2.2 request header that names the client software.
//
// Spec: §7.2.2
const ClientHeader = "X-Podium-Client"

// The §7.2.2 bounds. They are spec-fixed rather than operator-tunable, so a
// registry and its clients agree on them without configuration.
//
// Spec: §7.2.2
const (
	// maxClientHeaderBytes is the longest X-Podium-Client value the registry
	// reads. A longer value is ignored as a whole.
	maxClientHeaderBytes = 1024
	// maxClientMemberBytes is the length each member is truncated to after
	// sanitization.
	maxClientMemberBytes = 64
	// clientSanitizeByte replaces every byte outside the allowed set.
	clientSanitizeByte = '_'
	// clientKindOther is the kind recorded for an absent or unknown kind.
	clientKindOther = "other"
)

// The X-Podium-Client member keys (§7.2.2).
const (
	clientKeyKind        = "kind"
	clientKeyName        = "name"
	clientKeyVersion     = "version"
	clientKeyHost        = "host"
	clientKeyHostVersion = "host_version"
	clientKeyHarness     = "harness"
)

// knownClientKinds are the kind values §7.2.2 names, compared exactly.
var knownClientKinds = map[string]bool{"mcp": true, "sync": true, "sdk": true, "cli": true}

// CallerClient is the §8.1 caller.client attribute: the self-reported client
// software that sent a request. It is advisory and feeds no access decision
// (§7.2.2).
//
// Spec: §7.2.2, §8.1
type CallerClient struct {
	Kind        string
	Name        string
	Version     string
	Host        string
	HostVersion string
	Harness     string
}

// clientMember pairs an X-Podium-Client key with the CallerClient field it
// fills.
type clientMember struct {
	key string
	val *string
}

// members returns c's members paired with their keys, in the fixed §8.6
// hashing order. The codec, the sanitizer, and canonicalBody all walk this
// list, so the member set is defined once.
func (c *CallerClient) members() []clientMember {
	return []clientMember{
		{clientKeyKind, &c.Kind},
		{clientKeyName, &c.Name},
		{clientKeyVersion, &c.Version},
		{clientKeyHost, &c.Host},
		{clientKeyHostVersion, &c.HostVersion},
		{clientKeyHarness, &c.Harness},
	}
}

// Header encodes c as an X-Podium-Client value, omitting empty members.
// url.Values.Encode sorts the keys; §7.2.2 gives member order no meaning.
//
// Spec: §7.2.2
func (c CallerClient) Header() string {
	v := url.Values{}
	for _, m := range c.members() {
		if *m.val != "" {
			v.Set(m.key, *m.val)
		}
	}
	return v.Encode()
}

// ParseClientHeader parses and normalizes an X-Podium-Client value. It
// returns nil for an empty, oversized (over 1024 bytes), unparsable, or
// all-empty value, so an absent or ignored header never fails a request.
// A repeated member keeps its first value, and an unknown member is ignored.
//
// Spec: §7.2.2
func ParseClientHeader(v string) *CallerClient {
	if v == "" || len(v) > maxClientHeaderBytes {
		return nil
	}
	q, err := url.ParseQuery(v)
	if err != nil {
		return nil
	}
	var c CallerClient
	for _, m := range c.members() {
		*m.val = q.Get(m.key)
	}
	return NormalizeClient(c)
}

// NormalizeClient applies the §7.2.2 sanitization to c and returns nil when
// no member is left non-empty. Each member is sanitized and truncated first;
// the kind is mapped to "other" only after the all-empty check, so a value
// such as "kind=&name=" is treated as absent rather than recorded as
// {kind: other}. Normalization is idempotent.
//
// Spec: §7.2.2, §8.1
func NormalizeClient(c CallerClient) *CallerClient {
	empty := true
	for _, m := range c.members() {
		*m.val = sanitizeClientMember(*m.val)
		if *m.val != "" {
			empty = false
		}
	}
	if empty {
		return nil
	}
	if !knownClientKinds[c.Kind] {
		c.Kind = clientKindOther
	}
	return &c
}

// sanitizeClientMember replaces every byte outside the §7.2.2 character set
// with '_' and truncates the result to 64 bytes. The replacement keeps
// control bytes, NUL among them, out of log lines and out of the
// NUL-separated §8.6 hashed body. The replacement maps one byte to one byte,
// so truncating first yields the same result as the §7.2.2 order and bounds
// the work on an oversized member.
func sanitizeClientMember(s string) string {
	if len(s) > maxClientMemberBytes {
		s = s[:maxClientMemberBytes]
	}
	b := []byte(s)
	for i, ch := range b {
		if !clientMemberByteAllowed(ch) {
			b[i] = clientSanitizeByte
		}
	}
	return string(b)
}

// clientMemberByteAllowed reports whether ch is an ASCII letter, a digit, a
// space, or one of . _ - + / ( ).
func clientMemberByteAllowed(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		return true
	}
	switch ch {
	case ' ', '.', '_', '-', '+', '/', '(', ')':
		return true
	}
	return false
}

// normalizeEventClient normalizes e's CallerClient in place. Every sink's
// Append calls it before hashing, so the hashed body stays NUL-free whatever
// the caller passed (§8.6).
func normalizeEventClient(e *Event) {
	if e.CallerClient != nil {
		e.CallerClient = NormalizeClient(*e.CallerClient)
	}
}
