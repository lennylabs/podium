package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// The values of LayerConfig.VisibilitySource.
const (
	// VisibilitySourceDeclared records that the visibility of the row was
	// last set from a declaration: a .layer-config that declares one, or an
	// update of the visibility members.
	VisibilitySourceDeclared = "declared"
	// VisibilitySourceDefault records that it was last set from the
	// deployment default.
	VisibilitySourceDefault = "default"
)

// LayerVisibility is the §4.6 visibility of a layer as a value of its own,
// which visibility_pending holds.
type LayerVisibility struct {
	Public       bool     `json:"public"`
	Organization bool     `json:"organization"`
	Groups       []string `json:"groups"`
	Users        []string `json:"users"`
}

// Visibility returns the visibility fields of the row.
func (c LayerConfig) Visibility() LayerVisibility {
	return LayerVisibility{Public: c.Public, Organization: c.Organization, Groups: slices.Clone(c.Groups), Users: slices.Clone(c.Users)}
}

// SetVisibility sets the visibility fields of the row to v.
func (c *LayerConfig) SetVisibility(v LayerVisibility) {
	c.Public, c.Organization, c.Groups, c.Users = v.Public, v.Organization, slices.Clone(v.Groups), slices.Clone(v.Users)
}

// clonePending returns a copy of an optional visibility that shares no slice
// with it.
func clonePending(v *LayerVisibility) *LayerVisibility {
	if v == nil {
		return nil
	}
	c := LayerVisibility{Public: v.Public, Organization: v.Organization, Groups: slices.Clone(v.Groups), Users: slices.Clone(v.Users)}
	return &c
}

// VisibilityWrite is the visibility an ingest cycle writes to a layer that
// --layer-path bootstraps: the four visibility fields, visibility_source
// unless Source is empty, which keeps the stored value, and
// visibility_pending, which nil clears.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer)
type VisibilityWrite struct {
	Visibility LayerVisibility
	Source     string
	Pending    *LayerVisibility
}

// apply returns cfg with w applied.
func (w VisibilityWrite) apply(cfg LayerConfig) LayerConfig {
	cfg.SetVisibility(w.Visibility)
	if w.Source != "" {
		cfg.VisibilitySource = w.Source
	}
	cfg.VisibilityPending = clonePending(w.Pending)
	return cfg
}

// check applies the store's list rule to the written visibility, so a list
// the SQL stores cannot read back as written is refused before any write.
func (w VisibilityWrite) check(layerID string) error {
	return checkLayerLists(LayerConfig{ID: layerID, Groups: w.Visibility.Groups, Users: w.Visibility.Users})
}

// pendingColumn encodes visibility_pending for a text column: a JSON object,
// or the empty string when nothing is pending. The lists are JSON arrays, so
// an entry is read back as written whatever it holds.
func pendingColumn(v *LayerVisibility) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: encode visibility_pending: %w", err)
	}
	return string(b), nil
}

// parsePending decodes the visibility_pending column.
func parsePending(col string) (*LayerVisibility, error) {
	if col == "" {
		return nil, nil
	}
	var v LayerVisibility
	if err := json.Unmarshal([]byte(col), &v); err != nil {
		return nil, fmt.Errorf("store: decode visibility_pending: %w", err)
	}
	return &v, nil
}

// visibilityColumns returns the columns a visibility write sets: the groups
// and users lists and the pending object.
func visibilityColumns(layerID string, w VisibilityWrite) (groups, users, pending string, err error) {
	if err := w.check(layerID); err != nil {
		return "", "", "", err
	}
	cfg := LayerConfig{ID: layerID, Groups: w.Visibility.Groups, Users: w.Visibility.Users}
	if groups, users, err = layerListColumns(cfg); err != nil {
		return "", "", "", err
	}
	pending, err = pendingColumn(w.Pending)
	return groups, users, pending, err
}

// WriteLayerVisibility applies the second visibility write of a cycle that
// passed the layer step, under the store lock (§4.7.11).
func (s *Memory) WriteLayerVisibility(_ context.Context, tenantID, layerID, claim string, mark time.Time, commit string, w VisibilityWrite) error {
	if err := w.check(layerID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.holdsClaimLocked(tenantID, layerID, claim) {
		return ErrIngestSuperseded
	}
	key := layerKey(tenantID, layerID)
	cfg := s.layers[key]
	m := normalizeMark(mark)
	if !marksEqual(cfg.Mark, &m) || cfg.Commit != commit {
		return ErrIngestSuperseded
	}
	s.layers[key] = w.apply(cfg)
	return nil
}

// WriteLayerVisibility applies the second visibility write of a cycle that
// passed the layer step with one conditional update (§4.7.11).
func (s *SQLite) WriteLayerVisibility(ctx context.Context, tenantID, layerID, claim string, mark time.Time, commit string, w VisibilityWrite) error {
	groups, users, pending, err := visibilityColumns(layerID, w)
	if err != nil {
		return err
	}
	m := normalizeMark(mark)
	written, err := rowsChanged(s.db.ExecContext(ctx, `
		UPDATE layer_configs SET public = ?, organization = ?, groups = ?, users = ?, visibility_pending = ?,
			visibility_source = CASE WHEN ? = '' THEN visibility_source ELSE ? END
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?
			AND attestation_mark = ? AND attested_commit = ?`,
		boolToInt(w.Visibility.Public), boolToInt(w.Visibility.Organization), groups, users, pending, w.Source, w.Source,
		tenantID, layerID, claim, nullTimeText(&m), commit))
	if err != nil {
		return err
	}
	if !written {
		return ErrIngestSuperseded
	}
	return nil
}

// WriteLayerVisibility applies the second visibility write of a cycle that
// passed the layer step with one conditional update (§4.7.11).
func (p *Postgres) WriteLayerVisibility(ctx context.Context, tenantID, layerID, claim string, mark time.Time, commit string, w VisibilityWrite) error {
	groups, users, pending, err := visibilityColumns(layerID, w)
	if err != nil {
		return err
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	written, err := rowsChanged(conn.ExecContext(ctx, `
		UPDATE layer_configs SET public = $1, organization = $2, groups = $3, users = $4, visibility_pending = $5,
			visibility_source = CASE WHEN $6 = '' THEN visibility_source ELSE $6 END
		WHERE tenant_id = $7 AND id = $8 AND deleted_at IS NULL AND ingest_claim = $9
			AND attestation_mark = $10 AND attested_commit = $11`,
		w.Visibility.Public, w.Visibility.Organization, groups, users, pending, w.Source,
		tenantID, layerID, claim, normalizeMark(mark), commit))
	if err != nil {
		return err
	}
	if !written {
		return ErrIngestSuperseded
	}
	return nil
}
