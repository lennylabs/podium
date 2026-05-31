package core

import (
	"hash/fnv"
	"strings"
	"sync"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
)

// importCacheCap bounds the number of memoized glob expansions an
// importCache holds. When the map exceeds the cap it is dropped wholesale
// and the next expansion repopulates it. The cap keeps memory bounded
// across many distinct (pattern-set, snapshot) combinations without a
// per-entry eviction policy.
const importCacheCap = 4096

// importCache memoizes §4.5.2 DOMAIN.md include/exclude glob expansion so
// repeated load_domain calls over an unchanged artifact snapshot reuse the
// resolved import set instead of rescanning every artifact ID. This is the
// §12 "Recursive globs in DOMAIN.md are expensive" mitigation: "Glob
// expansion is cached server-side per artifact-version snapshot; cache
// invalidation is keyed on ingest events." The cache key folds the
// include/exclude patterns together with a fingerprint of the visible
// artifact-ID snapshot, so any ingest that adds or removes an artifact
// changes the fingerprint and the stale entry is never read again. A
// content change that leaves the ID set unchanged does not change the
// resolved import set (ResolveImports depends only on the IDs), so reusing
// the entry is correct.
type importCache struct {
	mu      sync.Mutex
	entries map[importKey][]string
}

// importKey identifies one memoized expansion: the include/exclude pattern
// set joined into a single string, plus a fingerprint of the sorted
// visible artifact-ID snapshot the expansion ran over.
type importKey struct {
	patterns string
	snapshot uint64
}

func newImportCache() *importCache {
	return &importCache{entries: map[importKey][]string{}}
}

// resolve returns domainpkg.ResolveImports(include, exclude, ids), serving
// a memoized result when the same patterns were already expanded over the
// same artifact-ID snapshot. ids must be the sorted visible artifact-ID set
// (manifestIDs already sorts it); the fingerprint is order-sensitive, so an
// unsorted input would defeat the cache without affecting correctness.
func (c *importCache) resolve(include, exclude, ids []string) []string {
	// No include patterns yield no imports; skip the cache entirely so the
	// common no-import domain never allocates a key.
	if len(include) == 0 {
		return domainpkg.ResolveImports(include, exclude, ids)
	}
	key := importKey{patterns: patternKey(include, exclude), snapshot: fingerprintIDs(ids)}

	c.mu.Lock()
	if got, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return got
	}
	c.mu.Unlock()

	resolved := domainpkg.ResolveImports(include, exclude, ids)

	c.mu.Lock()
	if len(c.entries) >= importCacheCap {
		c.entries = map[importKey][]string{}
	}
	c.entries[key] = resolved
	c.mu.Unlock()
	return resolved
}

// patternKey joins an include and exclude pattern set into a single key
// string. The 0x00 separator cannot appear in a glob pattern, and the 0x01
// divider keeps include patterns from colliding with exclude patterns.
func patternKey(include, exclude []string) string {
	var b strings.Builder
	for _, p := range include {
		b.WriteString(p)
		b.WriteByte(0)
	}
	b.WriteByte(1)
	for _, p := range exclude {
		b.WriteString(p)
		b.WriteByte(0)
	}
	return b.String()
}

// fingerprintIDs returns an order-sensitive FNV-64a hash of the
// artifact-ID snapshot. manifestIDs returns a sorted slice, so the same
// visible set yields the same fingerprint and any added or removed ID
// changes it, which is the "invalidation keyed on ingest events" property.
func fingerprintIDs(ids []string) uint64 {
	h := fnv.New64a()
	for _, id := range ids {
		_, _ = h.Write([]byte(id))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}
