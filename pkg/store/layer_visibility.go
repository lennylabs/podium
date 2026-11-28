package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// ErrVisibilityChanged reports a conditional visibility write whose row no
// longer holds the members the writer read.
var ErrVisibilityChanged = errors.New("store: the layer's visibility members changed")

// VisibilityState is the visibility members of a layer row: the visibility
// fields, visibility_source, and visibility_pending.
type VisibilityState struct {
	Visibility LayerVisibility
	Source     string
	Pending    *LayerVisibility
}

// VisibilityState returns the visibility members of the row.
func (c LayerConfig) VisibilityState() VisibilityState {
	return VisibilityState{Visibility: c.Visibility(), Source: c.VisibilitySource, Pending: clonePending(c.VisibilityPending)}
}

// visibilityStateColumns returns the stored form of s: the groups, users,
// and pending columns, which every backend compares.
func visibilityStateColumns(layerID string, s VisibilityState) (groups, users, pending string, err error) {
	return visibilityColumns(layerID, VisibilityWrite{Visibility: s.Visibility, Pending: s.Pending})
}

// sameStoredVisibility reports whether a row's members equal s in their
// stored form, as the SQL stores compare them.
func sameStoredVisibility(cfg LayerConfig, s VisibilityState) (bool, error) {
	g1, u1, p1, err := visibilityStateColumns(cfg.ID, cfg.VisibilityState())
	if err != nil {
		return false, err
	}
	g2, u2, p2, err := visibilityStateColumns(cfg.ID, s)
	if err != nil {
		return false, err
	}
	return cfg.Public == s.Visibility.Public && cfg.Organization == s.Visibility.Organization &&
		g1 == g2 && u1 == u2 && p1 == p2 && cfg.VisibilitySource == s.Source, nil
}

// PutLayerConfigKeepingVisibility writes cfg and keeps the visibility
// members of a row the store holds, under the store lock.
func (s *Memory) PutLayerConfigKeepingVisibility(_ context.Context, cfg LayerConfig) error {
	cfg, err := storedLayerForm(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, ok := s.layers[layerKey(cfg.TenantID, cfg.ID)]; ok {
		cfg.Public, cfg.Organization, cfg.Groups, cfg.Users = held.Public, held.Organization, held.Groups, held.Users
		cfg.VisibilitySource, cfg.VisibilityPending = held.VisibilitySource, held.VisibilityPending
	}
	s.putLayerLocked(cfg)
	return nil
}

// SetLayerVisibilityIf sets the visibility members of the row while they
// equal expect, under the store lock.
func (s *Memory) SetLayerVisibilityIf(_ context.Context, tenantID, layerID string, expect, next VisibilityState) error {
	if _, _, _, err := visibilityStateColumns(layerID, next); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := layerKey(tenantID, layerID)
	cfg, ok := s.layers[key]
	if !ok {
		return ErrNotFound
	}
	same, err := sameStoredVisibility(cfg, expect)
	if err != nil {
		return err
	}
	if !same {
		return ErrVisibilityChanged
	}
	// The SQL stores set the source as given, an empty one included.
	cfg = VisibilityWrite(next).apply(cfg)
	cfg.VisibilitySource = next.Source
	s.layers[key] = cfg
	return nil
}

// SetLayerVisibilityIf sets the visibility members of the row with one
// update conditional on the members expect holds.
func (s *SQLite) SetLayerVisibilityIf(ctx context.Context, tenantID, layerID string, expect, next VisibilityState) error {
	ng, nu, np, err := visibilityStateColumns(layerID, next)
	if err != nil {
		return err
	}
	eg, eu, ep, err := visibilityStateColumns(layerID, expect)
	if err != nil {
		return err
	}
	written, err := rowsChanged(s.db.ExecContext(ctx, `
		UPDATE layer_configs SET public = ?, organization = ?, groups = ?, users = ?, visibility_source = ?, visibility_pending = ?
		WHERE tenant_id = ? AND id = ? AND public = ? AND organization = ? AND groups = ? AND users = ?
			AND visibility_source = ? AND visibility_pending = ?`,
		boolToInt(next.Visibility.Public), boolToInt(next.Visibility.Organization), ng, nu, next.Source, np,
		tenantID, layerID, boolToInt(expect.Visibility.Public), boolToInt(expect.Visibility.Organization), eg, eu, expect.Source, ep))
	if err != nil {
		return err
	}
	if written {
		return nil
	}
	var one int
	switch err := s.db.QueryRowContext(ctx, `SELECT 1 FROM layer_configs WHERE tenant_id = ? AND id = ?`, tenantID, layerID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	return ErrVisibilityChanged
}

// SetLayerVisibilityIf sets the visibility members of the row with one
// update conditional on the members expect holds.
func (p *Postgres) SetLayerVisibilityIf(ctx context.Context, tenantID, layerID string, expect, next VisibilityState) error {
	ng, nu, np, err := visibilityStateColumns(layerID, next)
	if err != nil {
		return err
	}
	eg, eu, ep, err := visibilityStateColumns(layerID, expect)
	if err != nil {
		return err
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	written, err := rowsChanged(conn.ExecContext(ctx, `
		UPDATE layer_configs SET public = $1, organization = $2, groups = $3, users = $4, visibility_source = $5, visibility_pending = $6
		WHERE tenant_id = $7 AND id = $8 AND public = $9 AND organization = $10 AND groups = $11 AND users = $12
			AND visibility_source = $13 AND visibility_pending = $14`,
		next.Visibility.Public, next.Visibility.Organization, ng, nu, next.Source, np,
		tenantID, layerID, expect.Visibility.Public, expect.Visibility.Organization, eg, eu, expect.Source, ep))
	if err != nil {
		return err
	}
	if written {
		return nil
	}
	var one int
	switch err := conn.QueryRowContext(ctx, `SELECT 1 FROM layer_configs WHERE tenant_id = $1 AND id = $2`, tenantID, layerID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	return ErrVisibilityChanged
}

// PutLayerConfigIf writes cfg while the held row's visibility members equal
// expect, under the store lock.
func (s *Memory) PutLayerConfigIf(_ context.Context, cfg LayerConfig, expect VisibilityState) error {
	cfg, err := storedLayerForm(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.layers[layerKey(cfg.TenantID, cfg.ID)]
	if !ok {
		return ErrNotFound
	}
	same, err := sameStoredVisibility(held, expect)
	if err != nil {
		return err
	}
	if !same {
		return ErrVisibilityChanged
	}
	s.putLayerLocked(cfg)
	return nil
}

// heldVisibilityMatches compares the visibility columns a row holds, read
// in a transaction that holds the row, with expect in its stored form.
func heldVisibilityMatches(row *sql.Row, layerID string, expect VisibilityState) error {
	var public, org bool
	var groups, users, source, pending string
	switch err := row.Scan(&public, &org, &groups, &users, &source, &pending); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	eg, eu, ep, err := visibilityStateColumns(layerID, expect)
	if err != nil {
		return err
	}
	if public != expect.Visibility.Public || org != expect.Visibility.Organization || groups != eg || users != eu || source != expect.Source || pending != ep {
		return ErrVisibilityChanged
	}
	return nil
}

// PutLayerConfigIf writes cfg in one transaction that takes the write lock
// before it reads the held visibility columns.
func (s *SQLite) PutLayerConfigIf(ctx context.Context, cfg LayerConfig, expect VisibilityState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE layer_configs SET ingest_claim = ingest_claim WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID); err != nil {
		return err
	}
	if err := heldVisibilityMatches(tx.QueryRowContext(ctx, `
		SELECT public != 0, organization != 0, groups, users, visibility_source, visibility_pending
		FROM layer_configs WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID), cfg.ID, expect); err != nil {
		return err
	}
	if err := sqlitePutLayer(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

// PutLayerConfigIf writes cfg in one transaction with the row locked.
func (p *Postgres) PutLayerConfigIf(ctx context.Context, cfg LayerConfig, expect VisibilityState) error {
	conn, release, err := p.org(ctx, cfg.TenantID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := heldVisibilityMatches(tx.QueryRowContext(ctx, `
		SELECT public, organization, groups, users, visibility_source, visibility_pending
		FROM layer_configs WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, cfg.TenantID, cfg.ID), cfg.ID, expect); err != nil {
		return err
	}
	if err := pgPutLayer(ctx, tx, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrTrustRequirementsChanged reports a conditional layer write whose row no
// longer holds the trust requirements the writer read.
var ErrTrustRequirementsChanged = errors.New("store: the layer's trust requirements changed")

// PutLayerConfigIfTrust writes cfg while the held row's trust requirements,
// and with vis set its visibility members, equal the expected ones, under
// the store lock.
func (s *Memory) PutLayerConfigIfTrust(_ context.Context, cfg LayerConfig, expect []TrustRequirement, vis *VisibilityState) error {
	cfg, err := storedLayerForm(cfg)
	if err != nil {
		return err
	}
	want, err := trustRequirementsColumn(expect)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.layers[layerKey(cfg.TenantID, cfg.ID)]
	if !ok {
		return ErrNotFound
	}
	got, err := trustRequirementsColumn(held.TrustRequirements)
	if err != nil {
		return err
	}
	if got != want {
		return ErrTrustRequirementsChanged
	}
	if vis == nil {
		cfg.Public, cfg.Organization, cfg.Groups, cfg.Users = held.Public, held.Organization, held.Groups, held.Users
		cfg.VisibilitySource, cfg.VisibilityPending = held.VisibilitySource, held.VisibilityPending
	} else {
		same, err := sameStoredVisibility(held, *vis)
		if err != nil {
			return err
		}
		if !same {
			return ErrVisibilityChanged
		}
	}
	s.putLayerLocked(cfg)
	return nil
}

// heldTrustMatches compares the trust_requirements column a row holds, read
// in a transaction that holds the row, with expect in its stored form.
func heldTrustMatches(row *sql.Row, expect []TrustRequirement) error {
	var held string
	switch err := row.Scan(&held); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	want, err := trustRequirementsColumn(expect)
	if err != nil {
		return err
	}
	if held != want {
		return ErrTrustRequirementsChanged
	}
	return nil
}

// PutLayerConfigIfTrust writes cfg in one transaction that takes the write
// lock before it reads the held trust requirements and visibility columns.
func (s *SQLite) PutLayerConfigIfTrust(ctx context.Context, cfg LayerConfig, expect []TrustRequirement, vis *VisibilityState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE layer_configs SET ingest_claim = ingest_claim WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID); err != nil {
		return err
	}
	if err := heldTrustMatches(tx.QueryRowContext(ctx, `SELECT trust_requirements FROM layer_configs WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID), expect); err != nil {
		return err
	}
	assignments := layerKeepVisibilityAssignments
	if vis != nil {
		if err := heldVisibilityMatches(tx.QueryRowContext(ctx, `
			SELECT public != 0, organization != 0, groups, users, visibility_source, visibility_pending
			FROM layer_configs WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID), cfg.ID, *vis); err != nil {
			return err
		}
		assignments = layerWriteAssignments
	}
	if err := sqliteUpsertLayer(ctx, tx, cfg, assignments); err != nil {
		return err
	}
	return tx.Commit()
}

// PutLayerConfigIfTrust writes cfg in one transaction with the row locked.
func (p *Postgres) PutLayerConfigIfTrust(ctx context.Context, cfg LayerConfig, expect []TrustRequirement, vis *VisibilityState) error {
	conn, release, err := p.org(ctx, cfg.TenantID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := heldTrustMatches(tx.QueryRowContext(ctx, `
		SELECT trust_requirements FROM layer_configs WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, cfg.TenantID, cfg.ID), expect); err != nil {
		return err
	}
	assignments := layerKeepVisibilityAssignments
	if vis != nil {
		if err := heldVisibilityMatches(tx.QueryRowContext(ctx, `
			SELECT public, organization, groups, users, visibility_source, visibility_pending
			FROM layer_configs WHERE tenant_id = $1 AND id = $2`, cfg.TenantID, cfg.ID), cfg.ID, *vis); err != nil {
			return err
		}
		assignments = layerWriteAssignments
	}
	if err := pgUpsertLayer(ctx, tx, cfg, assignments); err != nil {
		return err
	}
	return tx.Commit()
}
