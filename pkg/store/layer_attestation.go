package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AttestationRecord is the §4.7.11 attestation record of a layer: the
// attestation mark, the attested commit recorded beside it, and the result of
// the last cycle that ran the attestation steps. A layer row carries it, and
// the layer object reports it as read-only members.
//
// The record lies outside the value a write of the layer row carries:
// PutLayerConfig keeps the stored record whatever the caller passes, so a
// start, an update, a reorder, a re-registration, an unregister, and a restore
// leave it as it is. Only WriteLayerAttestation, ClaimLayerIngestWithReset,
// and MigrateLayerConfig set it.
//
// Spec: §4.7.11 (Attestation record), §7.3.1
type AttestationRecord struct {
	// Mark is the attested time of the layer subject the registry last
	// accepted. Nil when no cycle has accepted one, or after a mark reset.
	Mark *time.Time `json:"attestation_mark,omitempty"`
	// Commit is the attested commit of that layer subject, or the commit a
	// mark reset named. Empty when none is recorded.
	Commit string `json:"attested_commit,omitempty"`
	// Status is "ok", the §6.10 code of the last cycle's refusal or
	// layer-step failure, or ingest.source_unreachable after a fetch deadline.
	Status string `json:"attestation_status,omitempty"`
	// Details is the details object of that failure.
	Details map[string]any `json:"attestation_details,omitempty"`
	// Dropped counts the artifacts the last cycle's artifact step dropped.
	Dropped int `json:"attestation_dropped,omitempty"`
	// CheckedAt is the time of the last record write of a cycle. Nil when no
	// cycle of this store has written the record.
	CheckedAt *time.Time `json:"attestation_checked_at,omitempty"`
}

// layerWriteAssignments is the update arm of the SQL layer upsert: every
// column a layer write carries. The ingest claim and the attestation record
// are absent, so a write keeps them as stored.
//
// Spec: §7.3.1 (Removed artifacts), §4.7.11 (Attestation record)
const layerWriteAssignments = layerKeepVisibilityAssignments + `,
			public = excluded.public,
			organization = excluded.organization,
			groups = excluded.groups,
			users = excluded.users,
			visibility_source = excluded.visibility_source,
			visibility_pending = excluded.visibility_pending`

// layerKeepVisibilityAssignments is the update arm of a layer write that
// keeps the stored visibility members as well (PutLayerConfigKeepingVisibility).
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep the visibility)
const layerKeepVisibilityAssignments = `source_type = excluded.source_type,
			repo = excluded.repo,
			ref = excluded.ref,
			root = excluded.root,
			local_path = excluded.local_path,
			ord = excluded.ord,
			user_defined = excluded.user_defined,
			owner = excluded.owner,
			webhook_secret = excluded.webhook_secret,
			last_ingested_ref = excluded.last_ingested_ref,
			force_push_policy = excluded.force_push_policy,
			git_provider = excluded.git_provider,
			created_at = excluded.created_at,
			deleted_at = excluded.deleted_at,
			last_ingested_at = excluded.last_ingested_at,
			repo_userinfo = excluded.repo_userinfo,
			trust_mode = excluded.trust_mode,
			trust_requirements = excluded.trust_requirements,
			trust_layer_root = excluded.trust_layer_root`

// AttestationStatusOK is the status of a cycle that passed the layer step.
const AttestationStatusOK = "ok"

// AttestationWrite is one record write of an ingest cycle.
type AttestationWrite struct {
	// Status, Details, Dropped, and CheckedAt replace the stored members.
	Status    string
	Details   map[string]any
	Dropped   int
	CheckedAt time.Time
	// Advance sets the mark to Mark and, when Commit is not empty, the
	// recorded commit to Commit. A write that advances applies only while
	// the stored mark and commit equal ReadMark and ReadCommit, the values
	// the cycle read when it claimed the layer.
	Advance    bool
	Mark       time.Time
	Commit     string
	ReadMark   *time.Time
	ReadCommit string
	// Visibility, when not nil, is written in the same store write: the
	// visibility of a layer that --layer-path bootstraps, which the record
	// write of its cycle carries (§4.7.11).
	Visibility *VisibilityWrite
}

// AttestationCopy is what MigrateLayerConfig did with the attestation record
// of a layer.
type AttestationCopy string

// The outcomes of the §13.4 migration copy of an attestation record.
const (
	// AttestationCopyWritten: the target held no row for the layer, and the
	// copy wrote it with the source's mark and commit.
	AttestationCopyWritten AttestationCopy = "written"
	// AttestationCopyReplaced: no cycle of the target had written the record,
	// and the copy replaced its mark and commit with the source's later ones.
	AttestationCopyReplaced AttestationCopy = "replaced"
	// AttestationCopyLeft: the copy left the target's record as it was.
	AttestationCopyLeft AttestationCopy = "left"
)

// attestationMarkPrecision is the precision a mark and a check time are
// stored at. Postgres
// keeps microseconds, so every backend stores that precision and a mark the
// cycle reads back equals the one it compares in the conditional write.
const attestationMarkPrecision = time.Microsecond

// normalizeMark returns t in UTC at the stored precision.
func normalizeMark(t time.Time) time.Time {
	return t.UTC().Truncate(attestationMarkPrecision)
}

// marksEqual reports whether two optional marks are the same instant.
func marksEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// migrationReplaces applies the §13.4 copy rule to a target record that the
// target holds: the copy replaces the mark and the commit only when no cycle
// of the target has written the record, the target holds a mark, the
// source's mark is later, and the copy empties no commit.
//
// Spec: §13.4, §4.7.11 (Migration copy)
func migrationReplaces(target, src AttestationRecord) bool {
	switch {
	case target.CheckedAt != nil, target.Mark == nil, src.Mark == nil:
		return false
	case !src.Mark.After(*target.Mark):
		return false
	}
	return src.Commit != "" || target.Commit == ""
}

// copyAttestationRecord returns a copy of r that shares no map with it.
func copyAttestationRecord(r AttestationRecord) AttestationRecord {
	if r.Details != nil {
		d := make(map[string]any, len(r.Details))
		for k, v := range r.Details {
			d[k] = v
		}
		r.Details = d
	}
	return r
}

// attestationDetailsColumn encodes the details object for a text column. An
// empty object is the empty string.
func attestationDetailsColumn(d map[string]any) (string, error) {
	if len(d) == 0 {
		return "", nil
	}
	// The column stores <, >, and & as written; the HTML escaping of
	// json.Marshal would make a bounded object up to six times longer.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return "", fmt.Errorf("store: encode attestation details: %w", err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// parseAttestationDetails decodes the details column.
func parseAttestationDetails(col string) (map[string]any, error) {
	if col == "" {
		return nil, nil
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(col), &d); err != nil {
		return nil, fmt.Errorf("store: decode attestation details: %w", err)
	}
	return d, nil
}

// roundTripDetails gives the memory store the details a SQL store returns,
// so a number reads back as float64 in every backend.
func roundTripDetails(d map[string]any) (map[string]any, error) {
	col, err := attestationDetailsColumn(d)
	if err != nil {
		return nil, err
	}
	return parseAttestationDetails(col)
}

// WriteLayerAttestation writes the attestation record of the live layer that
// holds claim (§4.7.11), under the store lock.
func (s *Memory) WriteLayerAttestation(_ context.Context, tenantID, layerID, claim string, w AttestationWrite) error {
	details, err := roundTripDetails(w.Details)
	if err != nil {
		return err
	}
	if w.Visibility != nil {
		if err := w.Visibility.check(layerID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.holdsClaimLocked(tenantID, layerID, claim) {
		return ErrIngestSuperseded
	}
	key := layerKey(tenantID, layerID)
	cfg := s.layers[key]
	rec := cfg.AttestationRecord
	if w.Advance {
		if !marksEqual(rec.Mark, w.ReadMark) || rec.Commit != w.ReadCommit {
			return ErrIngestSuperseded
		}
		mark := normalizeMark(w.Mark)
		rec.Mark = &mark
		if w.Commit != "" {
			rec.Commit = w.Commit
		}
	}
	checked := normalizeMark(w.CheckedAt)
	rec.Status, rec.Details, rec.Dropped, rec.CheckedAt = w.Status, details, w.Dropped, &checked
	cfg.AttestationRecord = rec
	if w.Visibility != nil {
		cfg = w.Visibility.apply(cfg)
	}
	s.layers[key] = cfg
	return nil
}

// ClaimLayerIngestWithReset claims the live layer and applies the mark reset
// in one step under the store lock (§4.7.11 Mark reset).
func (s *Memory) ClaimLayerIngestWithReset(_ context.Context, tenantID, layerID, claim, toCommit string) (LayerConfig, string, AttestationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := layerKey(tenantID, layerID)
	cfg, ok := s.layers[key]
	if !ok || cfg.DeletedAt != nil {
		return LayerConfig{}, "", AttestationRecord{}, ErrNotFound
	}
	prior := copyAttestationRecord(cfg.AttestationRecord)
	previous := s.ingestClaims[key]
	s.ingestClaims[key] = claim
	cfg.Mark, cfg.Commit = nil, toCommit
	s.layers[key] = cfg
	return splitStoredLayer(cfg), previous, prior, nil
}

// MigrateLayerConfig writes cfg and applies the §13.4 copy rule to its
// attestation record under the store lock.
func (s *Memory) MigrateLayerConfig(_ context.Context, cfg LayerConfig) (AttestationCopy, error) {
	src := cfg.AttestationRecord
	cfg, err := storedLayerForm(cfg)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := layerKey(cfg.TenantID, cfg.ID)
	stored, held := s.layers[key]
	s.putLayerLocked(cfg)
	out := migrationOutcome(held, stored.AttestationRecord, src)
	if out != AttestationCopyLeft {
		row := s.layers[key]
		row.Mark, row.Commit = normalizedMarkPtr(src.Mark), src.Commit
		s.layers[key] = row
	}
	return out, nil
}

// normalizedMarkPtr returns a copy of an optional mark at the stored
// precision.
func normalizedMarkPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := normalizeMark(*t)
	return &v
}
