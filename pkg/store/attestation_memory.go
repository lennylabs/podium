package store

import (
	"context"
	"sort"
)

// memoryAttestations holds the §4.7.11 attestation tables of the memory
// store. The store lock guards it.
type memoryAttestations struct {
	// bundles is keyed by tenant and digest, links by tenant and version
	// (mkey) and then by link key, and roots by instance.
	bundles map[[2]string]StoredAttestation
	links   map[string]map[LinkKey]AttestationLink
	roots   map[string]RootRecord
}

// attestationTables returns the attestation tables, creating them on first
// use, under the store lock.
func (s *Memory) attestationTables() *memoryAttestations {
	if s.att == nil {
		s.att = &memoryAttestations{
			bundles: map[[2]string]StoredAttestation{},
			links:   map[string]map[LinkKey]AttestationLink{},
			roots:   map[string]RootRecord{},
		}
	}
	return s.att
}

// WriteAttestations applies w under the store lock (§4.7.11 Attestation
// storage).
func (s *Memory) WriteAttestations(_ context.Context, tenantID, layerID, claim string, w AttestationWrites) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim != "" && !s.holdsClaimLocked(tenantID, layerID, claim) {
		return ErrIngestSuperseded
	}
	t := s.attestationTables()
	for _, b := range w.Bundles {
		key := [2]string{tenantID, b.Digest}
		if _, ok := t.bundles[key]; !ok {
			b.TenantID = tenantID
			t.bundles[key] = normalizeStoredAttestation(b)
		}
	}
	for _, v := range w.Versions {
		s.writeVersionLocked(tenantID, v)
	}
	return nil
}

// writeVersionLocked applies the write of one version.
func (s *Memory) writeVersionLocked(tenantID string, v VersionAttestations) {
	t := s.attestationTables()
	vk := mkey(tenantID, v.ArtifactID, v.Version)
	links := t.links[vk]
	if links == nil || v.ReplaceLinks {
		links = map[LinkKey]AttestationLink{}
	}
	for _, l := range v.Links {
		l.ArtifactID, l.Version = v.ArtifactID, v.Version
		links[l.Key()] = normalizeLink(l)
	}
	for _, l := range v.Updates {
		if cur, ok := links[l.Key()]; ok && cur.Digest == l.Digest {
			l.ArtifactID, l.Version = v.ArtifactID, v.Version
			links[l.Key()] = normalizeLink(l)
		}
	}
	if len(links) == 0 {
		delete(t.links, vk)
	} else {
		t.links[vk] = links
	}
	if v.Outcome == nil {
		return
	}
	rec, ok := s.manifests[vk]
	if !ok || v.OutcomeIf != "" && rec.AttestationOutcome != v.OutcomeIf {
		return
	}
	o := normalizedOutcome(*v.Outcome)
	rec.AttestationOutcome, rec.AttestationFailure, rec.AttestationEvaluatedAt = o.Outcome, o.Failure, o.EvaluatedAt
	s.manifests[vk] = rec
}

// GetAttestation returns the stored bundle of digest.
func (s *Memory) GetAttestation(_ context.Context, tenantID, digest string) (StoredAttestation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attestationTables().bundles[[2]string{tenantID, digest}]
	if !ok {
		return StoredAttestation{}, ErrNotFound
	}
	return normalizeStoredAttestation(a), nil
}

// ListAttestations returns the tenant's stored bundles by digest.
func (s *Memory) ListAttestations(_ context.Context, tenantID string) ([]StoredAttestation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []StoredAttestation
	for k, a := range s.attestationTables().bundles {
		if k[0] == tenantID {
			out = append(out, normalizeStoredAttestation(a))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out, nil
}

// ListAttestationLinks returns the tenant's links that f selects.
func (s *Memory) ListAttestationLinks(_ context.Context, tenantID string, f LinkFilter) ([]AttestationLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AttestationLink
	for vk, links := range s.attestationTables().links {
		for _, l := range links {
			if mkey(tenantID, l.ArtifactID, l.Version) != vk || !s.linkMatchesLocked(tenantID, l, f) {
				continue
			}
			l.AttestedAt = s.attestationTables().bundles[[2]string{tenantID, l.Digest}].AttestedAt
			out = append(out, l)
		}
	}
	sortLinks(out)
	return out, nil
}

// linkMatchesLocked applies f to a link of tenantID.
func (s *Memory) linkMatchesLocked(tenantID string, l AttestationLink, f LinkFilter) bool {
	switch {
	case f.ArtifactID != "" && l.ArtifactID != f.ArtifactID,
		f.Version != "" && l.Version != f.Version,
		f.Digest != "" && l.Digest != f.Digest:
		return false
	case f.LayerID == "":
		return true
	}
	rec, ok := s.manifests[mkey(tenantID, l.ArtifactID, l.Version)]
	return ok && rec.Layer == f.LayerID
}

// ListRootRecords returns every root record by instance.
func (s *Memory) ListRootRecords(_ context.Context) ([]RootRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []RootRecord
	for _, r := range s.attestationTables().roots {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}

// ReplaceRootRecord writes next when the stored record equals prev.
func (s *Memory) ReplaceRootRecord(_ context.Context, prev *RootRecord, next RootRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.attestationTables()
	var stored *RootRecord
	if r, ok := t.roots[next.Instance]; ok {
		stored = &r
	}
	if !rootRecordEqual(stored, normalizedRootPtr(prev)) {
		return false, nil
	}
	next.RootTime = normalizeMark(next.RootTime)
	t.roots[next.Instance] = next
	return true, nil
}

// normalizedRootPtr returns prev with its time at the stored precision.
func normalizedRootPtr(prev *RootRecord) *RootRecord {
	if prev == nil {
		return nil
	}
	r := *prev
	r.RootTime = normalizeMark(r.RootTime)
	return &r
}

// sweepAttestationsLocked applies the §8.4 retention of a purge: it deletes
// the links of every version the store no longer holds and then every bundle
// that no version links.
//
// Spec: §4.7.11 (Attestation storage), §8.4
func (s *Memory) sweepAttestationsLocked() {
	t := s.attestationTables()
	linked := map[[2]string]bool{}
	for vk, links := range t.links {
		if _, ok := s.manifests[vk]; !ok {
			delete(t.links, vk)
			continue
		}
		for _, l := range links {
			linked[[2]string{s.manifests[vk].TenantID, l.Digest}] = true
		}
	}
	for k := range t.bundles {
		if !linked[k] {
			delete(t.bundles, k)
		}
	}
}
