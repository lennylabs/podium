package core

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
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
// threshold it applied, the model identifier it filtered records by, and how
// many of the artifacts the caller can see have a current computation.
// Computed counts the visible artifacts whose last successful computation is
// for their current served version and the configured model, and Total counts
// the visible artifacts, so a Computed below Total tells a client the report
// can miss overlap of artifacts not computed yet or whose computation failed.
// Threshold, Model, Computed, and Total are null when the check is off.
//
// Spec: §7.3.1
type OverlapState struct {
	State     string   `json:"state"`
	Threshold *float64 `json:"threshold"`
	Model     *string  `json:"model"`
	Computed  *int     `json:"computed"`
	Total     *int     `json:"total"`
}

// OverlapArtifact names one artifact of the report so a client can show it
// without loading it: its identifier, its description as the catalog
// advisories read it, its artifact type, and the identifier of the layer that
// serves its current version. A diagnostic and each of its peers carry it.
//
// Spec: §7.3.1
type OverlapArtifact struct {
	ArtifactID  string `json:"artifact_id"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Layer       string `json:"layer"`
}

// OverlapDiagnostic is one entry of the report's diagnostics, naming its
// artifact. Peers is set on lint.colliding_descriptions and
// lint.semantic_overlap and lists every peer the message names or
// abbreviates, in the message's order.
type OverlapDiagnostic struct {
	OverlapArtifact
	Code     string        `json:"code"`
	Severity string        `json:"severity"`
	Message  string        `json:"message"`
	Peers    []OverlapPeer `json:"peers,omitempty"`
}

// OverlapPeer is one peer of an overlap diagnostic, naming the peer artifact.
// ComputedAt is the time the overlap record was computed, set on a
// lint.semantic_overlap peer only.
type OverlapPeer struct {
	OverlapArtifact
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
	threshold := r.semanticThreshold()
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
		computed, err := r.computedCount(ctx, served, model)
		if err != nil {
			return nil, err
		}
		total := len(served)
		state = OverlapState{State: "on", Threshold: &threshold, Model: &model, Computed: &computed, Total: &total}
		semantic, err := r.storedSemanticDiagnostics(ctx, served, served, threshold)
		if err != nil {
			return nil, err
		}
		diags = append(diags, semantic...)
		lint.SortDiagnostics(diags)
	}
	return newOverlapReport(state, diags, q.ArtifactID, overlapArtifacts(served)), nil
}

// computedCount returns how many of served, the current served versions the
// caller can see, have a last successful computation for that version and
// model.
//
// Spec: §7.3.1
func (r *Registry) computedCount(ctx context.Context, served []store.ManifestRecord, model string) (int, error) {
	runs, err := r.store.ListOverlapComputations(ctx, r.TenantFor(ctx))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	entries := make(map[string]store.OverlapComputation, len(runs))
	for _, c := range runs {
		entries[c.ArtifactID] = c
	}
	n := 0
	for _, m := range served {
		if c, ok := entries[m.ArtifactID]; ok && c.Version == m.Version && c.ModelID == model {
			n++
		}
	}
	return n, nil
}

// overlapArtifacts maps the identifier of each served record the caller can
// see to the artifact fields the report gives a diagnostic or a peer. The
// report names only those artifacts, so the map holds every artifact it
// names and no other.
//
// Spec: §7.3.1
func overlapArtifacts(served []store.ManifestRecord) map[string]OverlapArtifact {
	out := make(map[string]OverlapArtifact, len(served))
	for _, m := range served {
		out[m.ArtifactID] = OverlapArtifact{
			ArtifactID:  m.ArtifactID,
			Description: advisoryDescription(m),
			Type:        m.Type,
			Layer:       m.Layer,
		}
	}
	return out
}

// overlapArtifactOf returns the report fields of id. An identifier missing
// from artifacts, which a diagnostic over the caller's served set never
// names, carries its identifier alone.
func overlapArtifactOf(artifacts map[string]OverlapArtifact, id string) OverlapArtifact {
	if a, ok := artifacts[id]; ok {
		return a
	}
	return OverlapArtifact{ArtifactID: id}
}

// newOverlapReport renders diagnostics into the response, keeping only those
// of artifactID when it is set and counting the ones it keeps. artifacts
// gives each diagnostic and each peer its description, type, and layer.
func newOverlapReport(state OverlapState, diags []lint.Diagnostic, artifactID string, artifacts map[string]OverlapArtifact) *OverlapReport {
	report := &OverlapReport{SemanticOverlap: state, Diagnostics: []OverlapDiagnostic{}}
	for _, d := range diags {
		if artifactID != "" && d.ArtifactID != artifactID {
			continue
		}
		report.Diagnostics = append(report.Diagnostics, OverlapDiagnostic{
			OverlapArtifact: overlapArtifactOf(artifacts, d.ArtifactID),
			Code:            d.Code,
			Severity:        string(d.Severity),
			Message:         d.Message,
			Peers:           overlapPeers(d.Peers, artifacts),
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

// overlapPeers renders lint peers with their artifact fields, carrying
// computed_at only when the peer came from a stored record.
func overlapPeers(peers []lint.Peer, artifacts map[string]OverlapArtifact) []OverlapPeer {
	if len(peers) == 0 {
		return nil
	}
	out := make([]OverlapPeer, 0, len(peers))
	for _, p := range peers {
		op := OverlapPeer{OverlapArtifact: overlapArtifactOf(artifacts, p.ArtifactID), Similarity: p.Similarity}
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

// reportablePairs keeps the records whose owner and peer are in nameable at
// the served versions nameable holds and whose model is model, and returns
// one pair per two artifacts. When both artifacts own a record naming the
// other, the more recently computed record gives the pair its similarity and
// its computed time; a tie goes to the record whose owner sorts first, so the
// read is deterministic.
//
// Spec: §7.3.1
func reportablePairs(records []store.OverlapRecord, nameable map[string]store.ManifestRecord, model string) []lint.SemanticPair {
	at := func(id, version string) bool {
		m, ok := nameable[id]
		return ok && m.Version == version
	}
	byPair := map[[2]string]store.OverlapRecord{}
	for _, rec := range records {
		if rec.ModelID != model || !at(rec.Owner, rec.OwnerVersion) || !at(rec.Peer, rec.PeerVersion) {
			continue
		}
		key := [2]string{min(rec.Owner, rec.Peer), max(rec.Owner, rec.Peer)}
		if prev, seen := byPair[key]; seen && !supersedes(rec, prev) {
			continue
		}
		byPair[key] = rec
	}
	keys := make([][2]string, 0, len(byPair))
	for k := range byPair {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := make([]lint.SemanticPair, 0, len(keys))
	for _, k := range keys {
		rec := byPair[k]
		out = append(out, lint.SemanticPair{A: k[0], B: k[1], Similarity: rec.Similarity, ComputedAt: rec.ComputedAt})
	}
	return out
}

// supersedes reports whether rec, a record of the same two artifacts as prev
// owned by the other side, is the one a read reports.
func supersedes(rec, prev store.OverlapRecord) bool {
	if !rec.ComputedAt.Equal(prev.ComputedAt) {
		return rec.ComputedAt.After(prev.ComputedAt)
	}
	return rec.Owner < prev.Owner
}

// IngestSemanticOverlap computes the overlap records of each row an ingest
// cycle stored as a new version, after the cycle's writes, and returns
// lint.semantic_overlap for each artifact it computed. A diagnostic names a
// peer only when the peer is in the ingested layer or in a layer whose
// visibility includes public or organization, so a reingest response never
// names an artifact of a layer restricted to groups or users; a cycle a
// source webhook triggered (ctx carries ContextWithWebhookTrigger) names only
// peers in the ingested layer. A failed computation produces no diagnostic
// for that artifact, and nothing here fails the cycle: a store read that
// fails is logged and reports nothing.
//
// Spec: §7.3.1
func (r *Registry) IngestSemanticOverlap(ctx context.Context, layerID string, stored []vector.RowKey) []lint.Diagnostic {
	if len(stored) == 0 {
		return nil
	}
	out, served, err := r.computeOverlapKeys(ctx, stored, overlapBatchOptions{})
	if err != nil {
		log.Printf("%s: layer %s: %v", lint.SemanticOverlapCode, layerID, err)
		return nil
	}
	computed := out.computed
	if len(computed) == 0 {
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
	diags, err := r.storedSemanticDiagnostics(ctx, subjects, nameable, r.semanticThreshold())
	if err != nil {
		log.Printf("%s: layer %s: %v", lint.SemanticOverlapCode, layerID, err)
		return nil
	}
	return diags
}

// webhookTriggerKey marks a context whose ingest cycle a source webhook
// triggered.
type webhookTriggerKey struct{}

// ContextWithWebhookTrigger marks ctx as carrying an ingest cycle that a
// source webhook triggered. The git host records the webhook response, so
// IngestSemanticOverlap then names only peers in the ingested layer. The
// inbound webhook handler sets it on the request context it passes to the
// ingest runner.
//
// Spec: §7.3.1
func ContextWithWebhookTrigger(ctx context.Context) context.Context {
	return context.WithValue(ctx, webhookTriggerKey{}, true)
}

// webhookTriggered reports whether ctx carries ContextWithWebhookTrigger.
func webhookTriggered(ctx context.Context) bool {
	v, _ := ctx.Value(webhookTriggerKey{}).(bool)
	return v
}

// ingestPeerLayers returns the layers whose artifacts an ingest diagnostic may
// name: the ingested layer, and, unless a source webhook triggered the cycle,
// every layer of the tenant whose visibility includes public or organization
// (§4.6).
//
// Spec: §7.3.1
func (r *Registry) ingestPeerLayers(ctx context.Context, layerID string) map[string]bool {
	allowed := map[string]bool{layerID: true}
	if webhookTriggered(ctx) {
		return allowed
	}
	for _, l := range r.resolveLayers(ctx) {
		if l.Visibility.Public || l.Visibility.Organization {
			allowed[l.ID] = true
		}
	}
	return allowed
}

// descriptionRecord projects a stored record onto the lint record the
// description checks read: its identifier, its lint_suppress, and its
// description as advisoryDescription defines it.
func descriptionRecord(m store.ManifestRecord) filesystem.ArtifactRecord {
	a := parsedArtifact(m)
	a.Description = advisoryDescription(m)
	return filesystem.ArtifactRecord{ID: m.ArtifactID, Artifact: a}
}

// advisoryDescription returns m's description as the catalog advisories
// define it: the ARTIFACT.md description, or the SKILL.md description when
// ARTIFACT.md states none. The stored description of a skill is its SKILL.md
// description when SKILL.md states one (§4.7 embedding projection), so the
// ARTIFACT.md description is read from the stored frontmatter first; `podium
// lint` applies the same order to a checkout.
//
// Spec: §7.3.1
func advisoryDescription(m store.ManifestRecord) string {
	if d := parsedArtifact(m).Description; strings.TrimSpace(d) != "" {
		return d
	}
	return m.Description
}
