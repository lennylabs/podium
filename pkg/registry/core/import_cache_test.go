package core

import (
	"reflect"
	"testing"
)

// spec: §12 — "Glob expansion is cached server-side per artifact-version
// snapshot." A repeat expansion over the same snapshot returns the memoized
// slice (same backing array), and the result matches an uncached resolve.
func TestImportCache_MemoizesPerSnapshot(t *testing.T) {
	c := newImportCache()
	ids := []string{"finance/ap/pay", "finance/ar/bill", "ops/runbook"}
	include := []string{"finance/**"}

	first := c.resolve(include, nil, ids)
	want := []string{"finance/ap/pay", "finance/ar/bill"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first resolve = %v, want %v", first, want)
	}
	second := c.resolve(include, nil, ids)
	// Same snapshot + patterns: the cache returns the identical slice header.
	if &first[0] != &second[0] {
		t.Errorf("second resolve did not reuse the cached slice")
	}
}

// spec: §12 — "cache invalidation is keyed on ingest events." A snapshot
// that gains an artifact yields a different fingerprint, so the expansion
// is recomputed against the new ID set rather than served stale.
func TestImportCache_InvalidatesWhenSnapshotChanges(t *testing.T) {
	c := newImportCache()
	include := []string{"finance/**"}

	before := c.resolve(include, nil, []string{"finance/ap/pay"})
	if !reflect.DeepEqual(before, []string{"finance/ap/pay"}) {
		t.Fatalf("before = %v", before)
	}
	// A new ingest adds finance/ar/bill: the snapshot fingerprint changes
	// and the cached entry is not reused.
	after := c.resolve(include, nil, []string{"finance/ap/pay", "finance/ar/bill"})
	if !reflect.DeepEqual(after, []string{"finance/ap/pay", "finance/ar/bill"}) {
		t.Errorf("after snapshot change = %v, want both artifacts", after)
	}
}

// spec: §4.5.2 — exclude is applied after include, and the cache key folds
// the exclude set in so two calls differing only in exclude do not collide.
func TestImportCache_ExcludeIsPartOfKey(t *testing.T) {
	c := newImportCache()
	ids := []string{"finance/ap/pay", "finance/ap/secret"}
	withAll := c.resolve([]string{"finance/**"}, nil, ids)
	if len(withAll) != 2 {
		t.Fatalf("include-only = %v, want 2", withAll)
	}
	withExclude := c.resolve([]string{"finance/**"}, []string{"finance/ap/secret"}, ids)
	if !reflect.DeepEqual(withExclude, []string{"finance/ap/pay"}) {
		t.Errorf("with exclude = %v, want [finance/ap/pay]", withExclude)
	}
}

// An empty include set bypasses the cache and yields no imports.
func TestImportCache_EmptyIncludeNoImports(t *testing.T) {
	c := newImportCache()
	got := c.resolve(nil, []string{"x/**"}, []string{"x/a", "y/b"})
	if len(got) != 0 {
		t.Errorf("empty include = %v, want none", got)
	}
}

// The cap drops the map wholesale once exceeded; correctness still holds
// (a re-resolve after the drop recomputes the same answer).
func TestImportCache_BoundedByCap(t *testing.T) {
	c := newImportCache()
	ids := []string{"finance/ap/pay"}
	// Fill past the cap with distinct snapshots so the map resets.
	for i := 0; i < importCacheCap+5; i++ {
		snap := []string{"finance/ap/pay", string(rune('a'+i%26)) + "/x"}
		c.resolve([]string{"finance/**"}, nil, snap)
	}
	if len(c.entries) > importCacheCap {
		t.Errorf("cache grew past cap: %d", len(c.entries))
	}
	got := c.resolve([]string{"finance/**"}, nil, ids)
	if !reflect.DeepEqual(got, []string{"finance/ap/pay"}) {
		t.Errorf("post-cap resolve = %v", got)
	}
}
