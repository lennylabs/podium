package core

import (
	"sync"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
)

// projectionFields holds the name and when_to_use the §4.7 projection reads
// for one stored row.
type projectionFields struct {
	name      string
	whenToUse []string
}

// projectionFieldCache caches projectionFields by content hash. A row's
// manifest bytes are fixed for its content hash, so an entry never goes
// stale, and the cache holds at most one entry per stored version.
type projectionFieldCache struct {
	m sync.Map // content hash -> projectionFields
}

// withProjectionFields returns rec with the Name and WhenToUse that BM25 and
// the §4.7 projection read. The SQL stores persist neither field, so a row
// they return carries both empty, while ingest computed both from the same
// bytes. When rec carries neither, the two are resolved from the row's
// manifest bytes the way ingest resolves them: the §4.3.4 when_to_use
// precedence for a skill, and the SKILL.md name over the ARTIFACT.md one. A
// row whose bytes do not resolve keeps its fields.
//
// Spec: §4.7, §4.3.4
func (r *Registry) withProjectionFields(rec store.ManifestRecord) store.ManifestRecord {
	if rec.Name != "" || len(rec.WhenToUse) > 0 {
		return rec
	}
	var cache *projectionFieldCache
	if r != nil {
		cache = r.projection
	}
	if cache != nil && rec.ContentHash != "" {
		if f, ok := cache.m.Load(rec.ContentHash); ok {
			return applyProjectionFields(rec, f.(projectionFields))
		}
	}
	f, ok := resolveProjectionFields(rec)
	if !ok {
		return rec
	}
	if cache != nil && rec.ContentHash != "" {
		cache.m.Store(rec.ContentHash, f)
	}
	return applyProjectionFields(rec, f)
}

// applyProjectionFields copies f into rec. The list is copied, because the
// cached entry is shared by every caller.
func applyProjectionFields(rec store.ManifestRecord, f projectionFields) store.ManifestRecord {
	rec.Name = f.name
	rec.WhenToUse = append([]string(nil), f.whenToUse...)
	return rec
}

// resolveProjectionFields reads name and when_to_use from a row's ARTIFACT.md
// and SKILL.md bytes. A skill resolves through manifest.ResolveStoredSkill,
// so a top-level or metadata when_to_use counts, and its SKILL.md name wins
// as it does at ingest. Every other row reads its ARTIFACT.md.
func resolveProjectionFields(rec store.ManifestRecord) (projectionFields, bool) {
	if a, s, _, ok := manifest.ResolveStoredSkill(rec.Frontmatter, rec.SkillRaw); ok {
		name := a.Name
		if s.Name != "" {
			name = s.Name
		}
		return projectionFields{name: name, whenToUse: a.WhenToUse}, true
	}
	a, err := manifest.ParseArtifact(rec.Frontmatter)
	if err != nil {
		return projectionFields{}, false
	}
	return projectionFields{name: a.Name, whenToUse: a.WhenToUse}, true
}
