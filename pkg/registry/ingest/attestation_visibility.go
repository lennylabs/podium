package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
)

// The causes of a layer.config_changed that a visibility rule of a
// path-bootstrapped layer records.
const (
	VisibilityCauseStart = "start"
	VisibilityCauseCycle = "cycle"
)

// visibilityState is the visibility members of a layer row: the visibility
// in force, visibility_source, and visibility_pending.
type visibilityState struct {
	vis     layer.Visibility
	source  string
	pending *layer.Visibility
}

// VisibilityStateOf returns the visibility members of a stored layer row.
func visibilityStateOf(lc store.LayerConfig) visibilityState {
	s := visibilityState{vis: FromStoreVisibility(lc.Visibility()), source: lc.VisibilitySource}
	if lc.VisibilityPending != nil {
		p := FromStoreVisibility(*lc.VisibilityPending)
		s.pending = &p
	}
	return s
}

// same reports whether two states hold the same members.
func (s visibilityState) same(o visibilityState) bool {
	if s.source != o.source || !layer.SameVisibility(s.vis, o.vis) || (s.pending == nil) != (o.pending == nil) {
		return false
	}
	return s.pending == nil || layer.SameVisibility(*s.pending, *o.pending)
}

// FromStoreVisibility converts a stored visibility to the evaluator's form.
func FromStoreVisibility(v store.LayerVisibility) layer.Visibility {
	return layer.Visibility{Public: v.Public, Organization: v.Organization, Groups: v.Groups, Users: v.Users}
}

// ToStoreVisibility converts the evaluator's form to the stored one.
func ToStoreVisibility(v layer.Visibility) store.LayerVisibility {
	return store.LayerVisibility{Public: v.Public, Organization: v.Organization, Groups: v.Groups, Users: v.Users}
}

// PendingFor returns the visibility_pending of a row whose visibility in
// force is applied after a step that read the directory visibility dir: dir
// when the two differ, and nil otherwise.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Pending change)
func PendingFor(applied, dir layer.Visibility) *layer.Visibility {
	if layer.SameVisibility(applied, dir) {
		return nil
	}
	d := dir
	return &d
}

// SourceFor returns the visibility_source that a directory visibility sets:
// declared for a file that declares a visibility, default otherwise.
func SourceFor(declared bool) string {
	if declared {
		return store.VisibilitySourceDeclared
	}
	return store.VisibilitySourceDefault
}

// Baseline returns the baseline of a stored row: the deployment default def
// when its visibility_source is default, and its stored visibility otherwise,
// which covers a row an earlier release stored with no visibility_source.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer)
func Baseline(lc store.LayerConfig, def layer.Visibility) layer.Visibility {
	if lc.VisibilitySource == store.VisibilitySourceDefault {
		return def
	}
	return FromStoreVisibility(lc.Visibility())
}

// visibilityStep is the visibility rule of one cycle of a path-bootstrapped
// layer under warn or require: the directory visibility of its snapshot, the
// baseline of the row it claimed, and the members the row holds after the
// cycle's last write.
type visibilityStep struct {
	dir      layer.Visibility
	declared bool
	baseline layer.Visibility
	current  visibilityState
}

// AtStart marks the cycle as the startup ingest of a path-bootstrapped layer.
// rowCreated reports that the start write created the row, and emit, when
// not nil, receives the layer.config_changed event of the cycle's visibility
// change, because a start has no request to carry an audit emitter.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Row created by a start)
func (a *AttestationCycle) AtStart(rowCreated bool, emit AuditEmitterFunc) *AttestationCycle {
	if a != nil {
		a.rowCreated, a.visAudit = rowCreated, emit
	}
	return a
}

// readVisibility reads the directory visibility of the snapshot files for a
// cycle of a path-bootstrapped layer, which a row marks by its
// visibility_source. It reads the bytes the layer step digests, through the
// cycle's single read of each path. A cycle of any other layer reads nothing.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer, Snapshot reads)
func (a *AttestationCycle) readVisibility(files fs.FS) error {
	if a.layer.VisibilitySource == "" {
		return nil
	}
	d, err := filesystem.ReadDirectoryVisibility(files)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	dir := d.Visibility
	if !d.Declared {
		dir = a.cfg.DefaultVisibility
	}
	a.vis = &visibilityStep{
		dir:      dir,
		declared: d.Declared,
		baseline: Baseline(a.layer, a.cfg.DefaultVisibility),
		current:  visibilityStateOf(a.layer),
	}
	return nil
}

// recordVisibility is the visibility the cycle's attestation record write
// carries: the meet of the baseline and the directory visibility, which is
// within the baseline, so neither an unsigned file nor content the cycle is
// about to store is served more widely than the row allowed. The source is
// kept, apart from the failed startup ingest that follows the start write
// which created the row: that row's baseline was the deployment default, and
// fixing the source from the file keeps a later unsigned file from widening
// the layer beyond this meet.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step, Failed layer step, Row created by a start)
func (a *AttestationCycle) recordVisibility(passed bool) *store.VisibilityWrite {
	if a.vis == nil {
		return nil
	}
	meet := layer.Meet(a.vis.baseline, a.vis.dir)
	w := &store.VisibilityWrite{Visibility: ToStoreVisibility(meet), Pending: storePending(PendingFor(meet, a.vis.dir))}
	if !passed && a.rowCreated {
		w.Source = SourceFor(a.vis.declared)
	}
	return w
}

// signedVisibility is the second write of a cycle that passed the layer
// step: the directory visibility of the signed snapshot itself.
func (a *AttestationCycle) signedVisibility() store.VisibilityWrite {
	return store.VisibilityWrite{Visibility: ToStoreVisibility(a.vis.dir), Source: SourceFor(a.vis.declared)}
}

// storePending converts an optional pending visibility to the stored form.
func storePending(p *layer.Visibility) *store.LayerVisibility {
	if p == nil {
		return nil
	}
	v := ToStoreVisibility(*p)
	return &v
}

// noteVisibility records a visibility write the store applied: the cycle's
// view of the row moves to it, and a change of the members records
// layer.config_changed.
func (a *AttestationCycle) noteVisibility(ctx context.Context, w *store.VisibilityWrite) {
	if a.vis == nil || w == nil {
		return
	}
	after := visibilityState{vis: FromStoreVisibility(w.Visibility), source: a.vis.current.source}
	if w.Source != "" {
		after.source = w.Source
	}
	if w.Pending != nil {
		p := FromStoreVisibility(*w.Pending)
		after.pending = &p
	}
	before := a.vis.current
	a.vis.current = after
	if before.same(after) {
		return
	}
	emit := a.visAudit
	if emit == nil {
		emit = a.audit
	}
	if emit != nil {
		emit(string(audit.EventLayerConfigChanged), a.layer.ID, VisibilityEventFields(VisibilityCauseCycle,
			before.vis, before.source, before.pending, after.vis, after.source, after.pending))
	}
	// Spec: §7.6 — the change reaches the change-event stream with the
	// visibility the layer held before it, so a subscriber the change
	// withdraws the layer from learns of it.
	if a.publish != nil {
		prior := before.vis
		a.publish(ctx, core.EventScope{TenantID: a.layer.TenantID, Layers: []string{a.layer.ID}, Prior: &prior},
			string(audit.EventLayerConfigChanged), map[string]any{
				"layer": a.layer.ID, "action": "visibility", "cause": VisibilityCauseCycle,
			})
	}
}

// ApplySignedVisibility makes the second visibility write of a cycle that
// passed the layer step, after the cycle stored its artifacts and applied
// its removals: the visibility of the signed snapshot itself. The store
// applies it only while the cycle's claim is the latest and the row holds
// the mark and the commit the cycle's record write stored, so a superseded
// cycle cannot widen a row a newer cycle narrowed. superseded reports that
// the write did not apply.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Passed layer step)
func (a *AttestationCycle) applySignedVisibility(ctx context.Context, st store.Store, v *attestationVerdict) (superseded bool, err error) {
	if a == nil || a.vis == nil || v == nil || v.failure != nil {
		return false, nil
	}
	commit := v.commit
	if commit == "" {
		commit = a.layer.Commit
	}
	w := a.signedVisibility()
	switch err := st.WriteLayerVisibility(ctx, a.layer.TenantID, a.layer.ID, a.claim, v.layerTime, commit, w); {
	case errors.Is(err, store.ErrIngestSuperseded):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("ingest: write the visibility of layer %s: %w", a.layer.ID, err)
	}
	a.noteVisibility(ctx, &w)
	return false, nil
}

// VisibilityEventFields returns the members of the layer.config_changed
// event that a visibility rule of a path-bootstrapped layer records: the
// action visibility, the cause, and the earlier and the new visibility,
// visibility_source, and visibility_pending, each visibility as a JSON
// object and a pending one that is absent as the empty string.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Event), §8.1
func VisibilityEventFields(cause string, beforeVis layer.Visibility, beforeSource string, beforePending *layer.Visibility,
	vis layer.Visibility, source string, pending *layer.Visibility) map[string]string {
	return map[string]string{
		"action":                    "visibility",
		"cause":                     cause,
		"user_defined":              "false",
		"visibility_before":         visibilityJSON(&beforeVis),
		"visibility":                visibilityJSON(&vis),
		"visibility_source_before":  beforeSource,
		"visibility_source":         source,
		"visibility_pending_before": visibilityJSON(beforePending),
		"visibility_pending":        visibilityJSON(pending),
	}
}

// visibilityJSON encodes a visibility for an event field, the empty string
// for none.
func visibilityJSON(v *layer.Visibility) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(ToStoreVisibility(*v))
	if err != nil {
		// A visibility holds booleans and strings alone, which always
		// encode; the guard keeps an impossible failure visible.
		return fmt.Sprintf("unencodable: %v", err)
	}
	return string(b)
}
