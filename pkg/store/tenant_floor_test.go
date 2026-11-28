package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Spec: §4.7.11 (Tenant trust floor) — a floor object and a floor entry
// refuse a member of another name, a member that differs in letter case,
// and a member given twice, so no part of a floor is dropped silently.
func TestTrustFloor_DecodeRefusesInexactMembers(t *testing.T) {
	t.Parallel()
	bad := []string{
		`{"Mode":"warn"}`,
		`{"mode":"warn","mode":"none"}`,
		`{"mod":"warn"}`,
		`{"signers":[{"issuer":"i","Identity":"x"}]}`,
		`{"signers":[{"issuer":"i","identity":"a","identity":"b"}]}`,
		`{"signers":[{"issuer":"i","source_repository":"x"}]}`,
		`{"mode":3}`,
		`{"mode":"warn"`,
	}
	for _, in := range bad {
		var f TrustFloor
		if err := json.Unmarshal([]byte(in), &f); err == nil {
			t.Errorf("decode %s = %+v, want an error", in, f)
		}
	}
	var f TrustFloor
	in := `{"mode":"require","require_types":["hook"],"signers":[{"issuer":"i","instance":"github","source_repository_owner":"o","source_repository_ref":"r","identity":"x"}]}`
	if err := json.Unmarshal([]byte(in), &f); err != nil {
		t.Fatalf("decode %s: %v", in, err)
	}
	want := TrustFloor{Mode: "require", RequireTypes: []string{"hook"}, Signers: []TrustFloorSigner{{Issuer: "i", Instance: "github", SourceRepositoryOwner: "o", SourceRepositoryRef: "r", Identity: "x"}}}
	if !reflect.DeepEqual(f, want) || f.IsZero() || !(TrustFloor{}).IsZero() {
		t.Errorf("decode = %+v, want %+v", f, want)
	}
	// A value that is not an object is the decode's to refuse.
	if err := exactMembers([]byte(`[]`), trustFloorMembers); err != nil {
		t.Errorf("exactMembers([]) = %v, want nil", err)
	}
}

// Spec: §4.7.11 (Tenant trust floor) — a floor column that does not decode
// fails the read rather than reading as no floor, and an empty list column
// reads as no entry.
func TestTrustFloor_ColumnsFailClosed(t *testing.T) {
	t.Parallel()
	if _, err := parseFloorColumns("warn", "not json", ""); err == nil || !strings.Contains(err.Error(), "require types") {
		t.Errorf("parseFloorColumns(bad types) = %v, want a require types error", err)
	}
	if _, err := parseFloorColumns("warn", "", `[{"issuer":"i","owner":"x"}]`); err == nil {
		t.Errorf("parseFloorColumns(bad signers) = nil, want an error")
	}
	f, err := parseFloorColumns("", "", "[]")
	if err != nil || !f.IsZero() {
		t.Errorf("parseFloorColumns(empty list) = %+v, %v, want the zero floor", f, err)
	}
	m, ty, s := floorColumns(TrustFloor{})
	if m != "" || ty != "" || s != "" {
		t.Errorf("floorColumns(zero) = %q %q %q, want empty columns", m, ty, s)
	}
}
