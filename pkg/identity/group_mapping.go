package identity

import (
	"fmt"
	"sort"
	"strings"
)

// IdpGroupMapping is the §6.3.1 adapter that rewrites the OIDC group
// claims carried in a token into the group names a layer's `groups:`
// filter declares. It exists for IdPs without SCIM: where SCIM pushes a
// (user_id → groups) directory registry-side, the group-mapping adapter
// instead reads the token's group claim and maps each value through a
// registry-side table (for example translating an Okta group OID like
// "00g1a2b3c4d5" to the friendly name "finance").
//
// Mapping is configured registry-side and applied to the verified
// identity before visibility evaluation. A group claim value that has no
// table entry passes through unchanged, so a deployment whose IdP already
// emits the layer group names keeps working without configuration (the
// direct pass-through matching the registry has always done).
type IdpGroupMapping struct {
	table map[string]string
}

// NewIdpGroupMapping returns a mapping backed by table (token group value
// → layer group name). A nil or empty table yields a mapping that passes
// every group through unchanged.
func NewIdpGroupMapping(table map[string]string) *IdpGroupMapping {
	t := make(map[string]string, len(table))
	for k, v := range table {
		if k == "" || v == "" {
			continue
		}
		t[k] = v
	}
	return &IdpGroupMapping{table: t}
}

// ParseIdpGroupMapping parses a "k=v,k2=v2" specification (the
// PODIUM_IDP_GROUP_MAPPING env-var form) into a mapping. Whitespace
// around keys and values is trimmed. An empty spec yields an empty
// (pass-through) mapping. A malformed entry (no "=") is an error so a
// misconfiguration surfaces at startup rather than silently dropping a
// mapping.
func ParseIdpGroupMapping(spec string) (*IdpGroupMapping, error) {
	table := map[string]string{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		k, v, ok := strings.Cut(entry, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("idp group mapping: malformed entry %q (want claim=group)", entry)
		}
		table[k] = v
	}
	return NewIdpGroupMapping(table), nil
}

// Empty reports whether the mapping has no entries, in which case Map is
// the identity transform.
func (m *IdpGroupMapping) Empty() bool { return m == nil || len(m.table) == 0 }

// Len returns the number of configured (claim → group) entries.
func (m *IdpGroupMapping) Len() int {
	if m == nil {
		return 0
	}
	return len(m.table)
}

// Map rewrites raw token group values through the table. Each value that
// has a table entry becomes the mapped layer group name; a value with no
// entry passes through unchanged. The result is deduplicated and ordered
// deterministically so visibility evaluation and audit records are stable.
func (m *IdpGroupMapping) Map(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, g := range raw {
		name := g
		if m != nil {
			if mapped, ok := m.table[g]; ok {
				name = mapped
			}
		}
		if name == "" {
			continue
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
