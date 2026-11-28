package attest_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/version"
)

// wantFailure asserts that err is a Failure with code and constraint.
func wantFailure(t *testing.T, err error, code, constraint string) {
	t.Helper()
	f, ok := attest.FailureOf(err)
	if !ok {
		t.Fatalf("err = %v, want a Failure %s (%s)", err, code, constraint)
	}
	if f.Code != code || f.Constraint != constraint {
		t.Fatalf("failure = %s (%s), want %s (%s): %v", f.Code, f.Constraint, code, constraint, err)
	}
}

// Spec: §4.7.11 (Well-formed requests and records) — a requested ID and a
// resource path are refused, under every mode, for a .. segment, an empty
// segment, a . segment, a leading /, a backslash, a control character, and
// a segment that is not a portable name; an ID is also refused for a
// dot-prefixed segment.
// Matrix: §6.10 (materialize.request_mismatch)
func TestCheckRequestedID_WellFormedRule(t *testing.T) {
	t.Parallel()
	refused := []string{
		"", "/a", "a/", "a//b", "a/./b", "a/../b", "..", ".", "a\\b", "a\x00b", "a\x1fb", "a\u0085b", "a\u007fb",
		"a/.hidden", ".podium/x", "\xff",
		"a/close-books.", "a/close-books ", "CLOSE-~1", "GIT~1", "PODIUM~1", "con", "nul.txt", "COM¹",
		"conin$", "a:b", "a?b", "a/b@c", "finance/close-books@1.2.0", "\u200c.git", "\ufeffx", "CON .txt", "nul  .md", "ABCDEF~1.TXT",
	}
	for _, id := range refused {
		wantFailure(t, attest.CheckRequestedID(id), attest.CodeRequestMismatch, attest.ConstraintIDForm)
	}
	for _, id := range []string{"finance/close-books", "a", "release~2", "a/ABCDEF~1.TEXT", "a~1.b.c", "x/file.txt.~1~"} {
		if err := attest.CheckRequestedID(id); err != nil {
			t.Errorf("CheckRequestedID(%q) = %v, want nil", id, err)
		}
	}
}

// Spec: §4.7.11 (Well-formed requests and records) — a resource path may
// hold a dot-prefixed segment and is refused for every other breach of the
// rule, naming the constraint resource_path.
func TestCheckResourcePath_WellFormedRule(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "/etc/passwd", "a/../b", "a//b", "./a", "a\\b", "a\tb", "GIT~1/config", "x/CON", "\u200c.git/config"} {
		wantFailure(t, attest.CheckResourcePath(p), attest.CodeRequestMismatch, attest.ConstraintResourcePath)
	}
	for _, p := range []string{".github/workflow.yml", "scripts/run.sh", "a/.env"} {
		if err := attest.CheckResourcePath(p); err != nil {
			t.Errorf("CheckResourcePath(%q) = %v", p, err)
		}
	}
	rec := version.DeliveryRecord{Resources: map[string]string{"ok.txt": "", "GIT~1/config": ""}}
	wantFailure(t, attest.CheckRecordPaths(rec), attest.CodeRequestMismatch, attest.ConstraintResourcePath)
	if err := attest.CheckRecordPaths(version.DeliveryRecord{Resources: map[string]string{"a/b": ""}}); err != nil {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (Request binding) — the served ID equals the requested ID
// byte for byte, and a pin is read as the registry reads it: numbers by
// value, a sign before a number, no answer for a negative number, a
// registry-assigned pin byte for byte, a range pin by its numbers, and a
// content pin by the lowercase digest.
func TestBindRequest(t *testing.T) {
	t.Parallel()
	const hash = "sha256:00000000000000000000000000000000000000000000000000000000000000ab"
	cases := []struct {
		name, pin, served, contentHash, constraint string
	}{
		{"latest", "", "9.9.9", "", ""},
		{"exact", "1.2.3", "1.2.3", "", ""},
		{"exact leading zero served", "1.2.3", "1.02.3", "", ""},
		{"exact leading zero pin", "01.2.3", "1.2.3", "", ""},
		{"exact other", "1.2.3", "1.2.4", "", attest.ConstraintVersion},
		{"exact plus sign", "+1.2.3", "1.2.3", "", ""},
		{"exact minus zero", "-0.1.2", "0.1.2", "", ""},
		{"exact negative", "-1.2.3", "1.2.3", "", attest.ConstraintVersion},
		{"exact served not semver", "1.2.3", "0.0.0-auto.3", "", attest.ConstraintVersion},
		{"exact overflow", "9223372036854775808.0.0", "9223372036854775808.0.0", "", attest.ConstraintVersion},
		{"exact large served", "1.2.3", "1.2.0000000000000000000000003", "", ""},
		{"auto", "0.0.0-auto.4", "0.0.0-auto.4", "", ""},
		{"auto other", "0.0.0-auto.4", "0.0.0-auto.5", "", attest.ConstraintVersion},
		{"minor range", "1.2.x", "1.2.9", "", ""},
		{"minor range leading zero", "1.2.x", "1.02.9", "", ""},
		{"minor range outside", "1.2.x", "1.3.0", "", attest.ConstraintVersion},
		{"major range", "1.x", "1.7.0", "", ""},
		{"major range outside", "1.x", "2.0.0", "", attest.ConstraintVersion},
		{"major range auto", "0.x", "0.0.0-auto.1", "", attest.ConstraintVersion},
		{"major range negative", "-1.x", "1.0.0", "", attest.ConstraintVersion},
		{"content", "sha256:00000000000000000000000000000000000000000000000000000000000000AB", "1.0.0", hash, ""},
		{"content other", "sha256:00000000000000000000000000000000000000000000000000000000000000ac", "1.0.0", hash, attest.ConstraintContentHash},
		{"invalid pin", "1.2", "1.2.0", "", attest.ConstraintVersion},
		{"invalid suffix", "1.2.3-rc.1", "1.2.3-rc.1", "", attest.ConstraintVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := version.DeliveryRecord{ID: "a/b", Version: c.served, ContentHash: c.contentHash}
			err := attest.BindRequest(attest.Request{ID: "a/b", Pin: c.pin}, rec)
			if c.constraint == "" {
				if err != nil {
					t.Fatalf("BindRequest = %v, want nil", err)
				}
				return
			}
			wantFailure(t, err, attest.CodeRequestMismatch, c.constraint)
		})
	}
	err := attest.BindRequest(attest.Request{ID: "finance/close-books"}, version.DeliveryRecord{ID: "personal/x", Version: "1.0.0"})
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintID)
}

// Spec: §4.7.11 (Batch entries), §7.6.2 — an ok entry is matched by its ID;
// an entry for an ID the request does not list, and every entry of an ID
// that occurs more often than the request lists it, is a batch_entry
// refusal; other entries are bound under their ID's pin, and entries that
// are not ok are left alone.
func TestBindBatch(t *testing.T) {
	t.Parallel()
	ok := func(id, v string) version.BatchEntry {
		return version.BatchEntry{Status: "ok", Served: version.Served{Record: version.DeliveryRecord{ID: id, Version: v}}}
	}
	entries := []version.BatchEntry{
		ok("a", "1.0.0"), ok("b", "2.0.0"), ok("x", "1.0.0"), ok("c", "1.0.0"), ok("c", "1.0.0"),
		{Status: "error"}, ok("d", "1.0.0"), ok("d", "1.0.0"),
	}
	errs := attest.BindBatch([]string{"a", "b", "c", "d", "d"}, map[string]string{"b": "1.x"}, entries)
	want := []string{"", attest.ConstraintVersion, attest.ConstraintBatchEntry, attest.ConstraintBatchEntry, attest.ConstraintBatchEntry, "", "", ""}
	for i, w := range want {
		if w == "" {
			if errs[i] != nil {
				t.Errorf("entry %d: %v, want nil", i, errs[i])
			}
			continue
		}
		wantFailure(t, errs[i], attest.CodeRequestMismatch, w)
	}
}

// Spec: §4.7.11 (Attestation check) — the warning text carries the
// requested ID, the code, and the constraint, and nothing else.
func TestFailure_WarningText(t *testing.T) {
	t.Parallel()
	err := attest.CheckRequestedID("GIT~1")
	f, _ := attest.FailureOf(err)
	if got := f.WarningText("GIT~1"); got != "GIT~1: materialize.request_mismatch (constraint id_form)" {
		t.Fatalf("WarningText = %q", got)
	}
	if f.Error() == "" || f.Unwrap() != nil {
		t.Fatalf("Error = %q", f.Error())
	}
	plain := &attest.Failure{Code: attest.CodeAttestationMissing}
	if got := plain.WarningText("a"); got != "a: materialize.attestation_missing" {
		t.Fatalf("WarningText = %q", got)
	}
	if _, ok := attest.FailureOf(nil); ok {
		t.Fatal("FailureOf(nil) is a failure")
	}
}
