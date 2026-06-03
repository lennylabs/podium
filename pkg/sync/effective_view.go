package sync

import "sort"

// EffectiveArtifact is one row in the §7.5.5 override checklist: an artifact the
// caller can see, annotated with whether the target currently materializes it
// and the layer it resolves from.
type EffectiveArtifact struct {
	ID           string
	Type         string
	Layer        string
	Materialized bool
}

// ResolveEffectiveView returns every artifact the caller can see, the input the
// §7.5.5 `podium sync override` checklist renders ("the resolved set +
// everything else the caller can see"). The registry source and overlay come
// from opts; the materialized set is read from the target's lock so each row
// carries its current on-disk state. Visibility is enforced by the registry
// source, so an artifact the caller cannot see never appears.
//
// When opts.RegistryPath is empty there is no source to enumerate, so the view
// is the lock's materialized set alone (the caller can deselect but not add).
// spec: §7.5.5.
func ResolveEffectiveView(opts Options) ([]EffectiveArtifact, error) {
	lock, _ := ReadLock(opts.Target)
	materialized := map[string]bool{}
	if lock != nil {
		for _, a := range lock.Artifacts {
			materialized[a.ID] = true
		}
	}

	byID := map[string]*EffectiveArtifact{}
	order := []string{}
	add := func(id, typ, layer string) {
		if _, ok := byID[id]; ok {
			return
		}
		byID[id] = &EffectiveArtifact{ID: id, Type: typ, Layer: layer, Materialized: materialized[id]}
		order = append(order, id)
	}

	if opts.RegistryPath != "" {
		all, err := resolveRecords(opts)
		if err != nil {
			return nil, err
		}
		for _, rec := range all {
			typ := ""
			if rec.Artifact != nil {
				typ = string(rec.Artifact.Type)
			}
			add(rec.ID, typ, rec.LayerID)
		}
	}
	// Fold in any materialized ID the source no longer exposes (e.g. a prior
	// override --add for an artifact since dropped) so the checklist can still
	// remove it.
	if lock != nil {
		for _, a := range lock.Artifacts {
			add(a.ID, "", a.Layer)
		}
	}

	sort.Strings(order)
	out := make([]EffectiveArtifact, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}
