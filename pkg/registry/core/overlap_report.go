package core

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// OverlapReport is the GET /v1/overlap response. A client renders it
// directly: every overlap diagnostic carries its peers as structured entries,
// so no client parses a message.
//
// Spec: §7.3.1
type OverlapReport struct {
	SemanticOverlap OverlapState `json:"semantic_overlap"`
	// Diagnostics is ordered as `podium lint` orders it: by artifact
	// identifier, then code, then message.
	Diagnostics []OverlapDiagnostic `json:"diagnostics"`
	Counts      OverlapReportCounts `json:"counts"`
}

// OverlapState reports whether the read included lint.semantic_overlap, the
// threshold it applied, and the model identifier it filtered records by.
// Threshold and Model are null when the check is off.
type OverlapState struct {
	State     string   `json:"state"`
	Threshold *float64 `json:"threshold"`
	Model     *string  `json:"model"`
}

// OverlapDiagnostic is one entry of the report's diagnostics. Peers is set on
// lint.colliding_descriptions and lint.semantic_overlap and lists every peer
// the message names or abbreviates, in the message's order.
type OverlapDiagnostic struct {
	ArtifactID string        `json:"artifact_id"`
	Code       string        `json:"code"`
	Severity   string        `json:"severity"`
	Message    string        `json:"message"`
	Peers      []OverlapPeer `json:"peers,omitempty"`
}

// OverlapPeer is one peer of an overlap diagnostic. ComputedAt is the time the
// overlap record was computed, set on a lint.semantic_overlap peer only.
type OverlapPeer struct {
	ArtifactID string     `json:"artifact_id"`
	Similarity float64    `json:"similarity"`
	ComputedAt *time.Time `json:"computed_at,omitempty"`
}

// OverlapReportCounts counts the report's diagnostics by severity.
type OverlapReportCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// OverlapQuery holds the optional parameters of the catalog overlap report.
type OverlapQuery struct {
	// Threshold replaces PODIUM_LINT_SEMANTIC_SIMILARITY for this read. It
	// must lie from 0.5 to 1.0.
	Threshold *float64
	// ArtifactID, when set, restricts the response to the diagnostics whose
	// artifact_id it names.
	ArtifactID string
}

// CatalogOverlap builds the §7.3.1 catalog overlap report over the current
// served version (§4.7.6) of every artifact id can see (§4.6), across layers.
// It runs lint.thin_description and lint.colliding_descriptions over that set
// and, when the check is on, reports lint.semantic_overlap from the stored
// records whose two artifacts id can both see. It reads stored records and
// descriptions and issues no index query, so a hidden artifact is never named
// or counted. An out-of-range q.Threshold is ErrInvalidArgument.
//
// Spec: §7.3.1
func (r *Registry) CatalogOverlap(ctx context.Context, id layer.Identity, q OverlapQuery) (*OverlapReport, error) {
	threshold := r.overlap.semantic.Similarity
	if q.Threshold != nil {
		if !lint.ValidSimilarity(*q.Threshold) {
			return nil, fmt.Errorf("%w: threshold must be a number from 0.5 to 1.0", ErrInvalidArgument)
		}
		threshold = *q.Threshold
	}
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	served := dedupeLatest(visible)
	records := make([]filesystem.ArtifactRecord, 0, len(served))
	for _, m := range served {
		records = append(records, descriptionRecord(m))
	}
	rules := (&lint.Linter{Thresholds: r.overlap.thresholds}).DescriptionRules()
	diags := (&lint.Linter{Rules: rules}).Lint(ctx, nil, records)
	state := OverlapState{State: "off"}
	if r.overlap.semantic.Enabled {
		model := r.overlapModel()
		state = OverlapState{State: "on", Threshold: &threshold, Model: &model}
		semantic, err := r.storedSemanticDiagnostics(ctx, served, served, threshold)
		if err != nil {
			return nil, err
		}
		diags = append(diags, semantic...)
		lint.SortDiagnostics(diags)
	}
	return newOverlapReport(state, diags, q.ArtifactID), nil
}

// newOverlapReport renders diagnostics into the response, keeping only those
// of artifactID when it is set and counting the ones it keeps.
func newOverlapReport(state OverlapState, diags []lint.Diagnostic, artifactID string) *OverlapReport {
	report := &OverlapReport{SemanticOverlap: state, Diagnostics: []OverlapDiagnostic{}}
	for _, d := range diags {
		if artifactID != "" && d.ArtifactID != artifactID {
			continue
		}
		report.Diagnostics = append(report.Diagnostics, OverlapDiagnostic{
			ArtifactID: d.ArtifactID,
			Code:       d.Code,
			Severity:   string(d.Severity),
			Message:    d.Message,
			Peers:      overlapPeers(d.Peers),
		})
		switch d.Severity {
		case lint.SeverityError:
			report.Counts.Error++
		case lint.SeverityWarning:
			report.Counts.Warning++
		case lint.SeverityInfo:
			report.Counts.Info++
		}
	}
	return report
}

// overlapPeers renders lint peers, carrying computed_at only when the peer
// came from a stored record.
func overlapPeers(peers []lint.Peer) []OverlapPeer {
	if len(peers) == 0 {
		return nil
	}
	out := make([]OverlapPeer, 0, len(peers))
	for _, p := range peers {
		op := OverlapPeer{ArtifactID: p.ArtifactID, Similarity: p.Similarity}
		if !p.ComputedAt.IsZero() {
			at := p.ComputedAt.UTC()
			op.ComputedAt = &at
		}
		out = append(out, op)
	}
	return out
}

// storedSemanticDiagnostics reads the tenant's overlap records and builds
// lint.semantic_overlap for subjects. A record is reported only when both of
// its artifacts are in nameable at their served versions, its model is the
// configured model, and its similarity is at or above threshold.
//
// Spec: §7.3.1
func (r *Registry) storedSemanticDiagnostics(ctx context.Context, subjects, nameable []store.ManifestRecord, threshold float64) ([]lint.Diagnostic, error) {
	records, err := r.store.ListOverlapRecords(ctx, r.TenantFor(ctx))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	byID := make(map[string]store.ManifestRecord, len(nameable))
	for _, m := range nameable {
		byID[m.ArtifactID] = m
	}
	pairs := reportablePairs(records, byID, r.overlapModel())
	subs := make([]lint.SemanticSubject, 0, len(subjects))
	for _, m := range subjects {
		subs = append(subs, lint.SemanticSubject{
			ArtifactID: m.ArtifactID,
			// Only ARTIFACT.md carries lint_suppress (§4.3.4).
			Suppressed: parsedArtifact(m).Suppresses(lint.SemanticOverlapCode),
		})
	}
	return lint.SemanticDiagnostics(subs, pairs, threshold), nil
}

// reportablePairs keeps the records whose two artifacts are in nameable at
// the served versions nameable holds and whose model is model.
func reportablePairs(records []store.OverlapRecord, nameable map[string]store.ManifestRecord, model string) []lint.SemanticPair {
	at := func(id, version string) bool {
		m, ok := nameable[id]
		return ok && m.Version == version
	}
	var out []lint.SemanticPair
	for _, rec := range records {
		if rec.ModelID != model || !at(rec.ArtifactA, rec.VersionA) || !at(rec.ArtifactB, rec.VersionB) {
			continue
		}
		out = append(out, lint.SemanticPair{A: rec.ArtifactA, B: rec.ArtifactB, Similarity: rec.Similarity, ComputedAt: rec.ComputedAt})
	}
	return out
}

// IngestSemanticOverlap computes the overlap records of each row an ingest
// cycle stored as a new version, after the cycle's writes, and returns
// lint.semantic_overlap for each artifact it computed. A diagnostic names a
// peer only when the peer is in the ingested layer or in a layer whose
// visibility includes public or organization, so a reingest response never
// names an artifact of a layer restricted to groups or users. A failed
// computation produces no diagnostic for that artifact, and nothing here
// fails the cycle: a store read that fails is logged and reports nothing.
//
// Spec: §7.3.1
func (r *Registry) IngestSemanticOverlap(ctx context.Context, layerID string, stored []vector.RowKey) []lint.Diagnostic {
	if len(stored) == 0 {
		return nil
	}
	computed, _, err := r.computeOverlapKeys(ctx, stored)
	if err != nil {
		log.Printf("%s: layer %s: %v", lint.SemanticOverlapCode, layerID, err)
		return nil
	}
	if len(computed) == 0 {
		return nil
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		log.Printf("%s: layer %s: %v", lint.SemanticOverlapCode, layerID, err)
		return nil
	}
	eligible := r.ingestPeerLayers(ctx, layerID)
	var nameable, subjects []store.ManifestRecord
	for _, m := range served {
		if eligible[m.Layer] {
			nameable = append(nameable, m)
		}
	}
	for _, id := range computed {
		subjects = append(subjects, served[id])
	}
	diags, err := r.storedSemanticDiagnostics(ctx, subjects, nameable, r.overlap.semantic.Similarity)
	if err != nil {
		log.Printf("%s: layer %s: %v", lint.SemanticOverlapCode, layerID, err)
		return nil
	}
	return diags
}

// ingestPeerLayers returns the layers whose artifacts an ingest diagnostic may
// name: the ingested layer, and every layer of the tenant whose visibility
// includes public or organization (§4.6).
func (r *Registry) ingestPeerLayers(ctx context.Context, layerID string) map[string]bool {
	allowed := map[string]bool{layerID: true}
	for _, l := range r.resolveLayers(ctx) {
		if l.Visibility.Public || l.Visibility.Organization {
			allowed[l.ID] = true
		}
	}
	return allowed
}

// descriptionRecord projects a stored record onto the lint record the
// description checks read: its identifier, its lint_suppress, and the
// description the registry serves for it, which for a skill is the SKILL.md
// description (§4.7 embedding projection).
func descriptionRecord(m store.ManifestRecord) filesystem.ArtifactRecord {
	a := parsedArtifact(m)
	a.Description = m.Description
	return filesystem.ArtifactRecord{ID: m.ArtifactID, Artifact: a}
}
