package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
)

// TrustFloor is the §4.7.11 tenant trust floor: the lowest trust mode a
// layer of the tenant resolves to, the artifact types every layer lists in
// trust_require_types, and the admissible signers. The zero value sets no
// floor. The JSON members are those of the single-tenant deployment setting
// (PODIUM_LAYER_TRUST_FLOOR_FILE); the §7.3.3 tenant object carries the
// same values under trust_floor_mode, trust_floor_require_types, and
// trust_floor_signers.
//
// Spec: §4.7.11 (Tenant trust floor)
type TrustFloor struct {
	Mode         string             `json:"mode,omitempty"`
	RequireTypes []string           `json:"require_types,omitempty"`
	Signers      []TrustFloorSigner `json:"signers,omitempty"`
}

// TrustFloorSigner is one entry of a floor's signer list. Issuer is
// required; an empty member is not named, and an empty Instance is
// public-good.
//
// Spec: §4.7.11 (Tenant trust floor)
type TrustFloorSigner struct {
	Issuer                  string `json:"issuer"`
	Instance                string `json:"instance,omitempty"`
	SourceRepositoryOwner   string `json:"source_repository_owner,omitempty"`
	SourceRepositoryOwnerID string `json:"source_repository_owner_id,omitempty"`
	SourceRepositoryRef     string `json:"source_repository_ref,omitempty"`
	Identity                string `json:"identity,omitempty"`
}

// The members a floor object and a floor entry hold.
var (
	trustFloorMembers       = []string{"mode", "require_types", "signers"}
	trustFloorSignerMembers = []string{"issuer", "instance", "source_repository_owner",
		"source_repository_owner_id", "source_repository_ref", "identity"}
)

// IsZero reports whether f sets no floor.
func (f TrustFloor) IsZero() bool {
	return f.Mode == "" && len(f.RequireTypes) == 0 && len(f.Signers) == 0
}

// Clone returns a copy of f that shares no slice with it, with an empty
// list as nil, so a stored floor and the caller's value stay apart.
func (f TrustFloor) Clone() TrustFloor {
	out := TrustFloor{Mode: f.Mode, RequireTypes: cloneRequireTypes(f.RequireTypes)}
	if len(f.Signers) > 0 {
		out.Signers = slices.Clone(f.Signers)
	}
	return out
}

// UnmarshalJSON decodes a floor object. A member whose name is not one of
// mode, require_types, and signers exactly, and a member given twice, are
// refused, so a misspelled or repeated member cannot drop a part of the
// floor without an error.
//
// Spec: §4.7.11 (Tenant trust floor)
func (f *TrustFloor) UnmarshalJSON(b []byte) error {
	if err := exactMembers(b, trustFloorMembers); err != nil {
		return fmt.Errorf("trust floor: %w", err)
	}
	type plain TrustFloor
	var p plain
	if err := decodeStrict(b, &p); err != nil {
		return fmt.Errorf("trust floor: %w", err)
	}
	*f = TrustFloor(p)
	return nil
}

// UnmarshalJSON decodes a floor entry and refuses a member of another name
// or a member given twice, so a misspelled owner or instance is an error
// rather than an entry that admits more signers.
//
// Spec: §4.7.11 (Tenant trust floor)
func (s *TrustFloorSigner) UnmarshalJSON(b []byte) error {
	if err := exactMembers(b, trustFloorSignerMembers); err != nil {
		return fmt.Errorf("trust floor signer: %w", err)
	}
	type plain TrustFloorSigner
	var p plain
	if err := decodeStrict(b, &p); err != nil {
		return fmt.Errorf("trust floor signer: %w", err)
	}
	*s = TrustFloorSigner(p)
	return nil
}

// exactMembers refuses a JSON object that holds a member whose name is not
// in allowed byte for byte, or one member twice. encoding/json matches a
// member name without regard to letter case and keeps the last of two
// members, which would let "Mode" or a second "mode" pass silently. A value
// that is not an object is left to the decode, which refuses it.
func exactMembers(b []byte, allowed []string) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("member %q is not one of %v", name, allowed)
		}
		if seen[name] {
			return fmt.Errorf("member %q is given twice", name)
		}
		seen[name] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}

// floorSignersColumn returns the trust_floor_signers column the SQL stores
// write: the JSON array, or the empty string for no entry. An entry holds
// strings alone, so the column has no encoding failure.
func floorSignersColumn(signers []TrustFloorSigner) string {
	if len(signers) == 0 {
		return ""
	}
	b, _ := json.Marshal(signers)
	return string(b)
}

// parseFloorSigners reads the trust_floor_signers column back. The empty
// string, which every row written before the column existed holds, is no
// entry. A column that does not decode is an error, so a floor the store
// cannot read is never served as no floor.
func parseFloorSigners(col string) ([]TrustFloorSigner, error) {
	if col == "" {
		return nil, nil
	}
	var signers []TrustFloorSigner
	if err := json.Unmarshal([]byte(col), &signers); err != nil {
		return nil, fmt.Errorf("store: decode trust floor signers: %w", err)
	}
	if len(signers) == 0 {
		return nil, nil
	}
	return signers, nil
}

// floorColumns returns the three tenant columns that hold f.
func floorColumns(f TrustFloor) (mode, types, signers string) {
	return f.Mode, requireTypesColumn(f.RequireTypes), floorSignersColumn(f.Signers)
}

// parseFloorColumns reads a floor back from its three tenant columns.
func parseFloorColumns(mode, types, signers string) (TrustFloor, error) {
	rt, err := parseRequireTypes(types)
	if err != nil {
		return TrustFloor{}, fmt.Errorf("store: decode trust floor require types: %w", err)
	}
	ss, err := parseFloorSigners(signers)
	if err != nil {
		return TrustFloor{}, err
	}
	return TrustFloor{Mode: mode, RequireTypes: rt, Signers: ss}, nil
}

// tenantColumns are the tenant columns scanTenant reads, in its order. Both
// SQL stores name the same columns.
const tenantColumns = `id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview, active,
		trust_floor_mode, trust_floor_require_types, trust_floor_signers`

// scanTenant reads one tenant row of tenantColumns. A floor column that does
// not decode fails the read.
func scanTenant(row rowScanner) (Tenant, error) {
	var t Tenant
	var exposeScopePreview sql.NullBool
	var fm, ft, fs string
	if err := row.Scan(&t.ID, &t.Name,
		&t.Quota.StorageBytes, &t.Quota.SearchQPS,
		&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay, &t.Quota.MaxUserLayers,
		&exposeScopePreview, &t.Active, &fm, &ft, &fs); err != nil {
		return Tenant{}, err
	}
	t.ExposeScopePreview = ptrFromNullBool(exposeScopePreview)
	floor, err := parseFloorColumns(fm, ft, fs)
	if err != nil {
		return Tenant{}, err
	}
	t.TrustFloor = floor
	return t, nil
}
