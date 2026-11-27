package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Snapshot reads) — a file that changes between two reads of
// one cycle reads as its first bytes, and so does a listing and a stat; a
// directory opened through the view lists its first entries in batches.
func TestReadOnce_RepeatsTheFirstRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "f.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := readOnce(os.DirFS(dir))
	if readOnce(fsys) != fsys {
		t.Fatal("a second wrap made a new view")
	}
	first, _ := fs.ReadFile(fsys, "a/f.md")
	first[0] = 'X' // a caller that edits its copy changes no later read
	if err := os.WriteFile(p, []byte("second, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "g.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	again, _ := fs.ReadFile(fsys, "a/f.md")
	info, _ := fs.Stat(fsys, "a/f.md")
	entries, _ := fs.ReadDir(fsys, "a")
	if string(again) != "first" || len(entries) != 2 {
		// The listing was taken after g.md was written; a stat taken
		// before would not see it. Both reads of f.md return its first bytes.
		t.Fatalf("read %q, %d entries", again, len(entries))
	}
	f, err := fsys.Open("a/f.md")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(f)
	st, _ := f.Stat()
	if string(body) != "first" || st.Size() != 5 || info.Size() != 14 || f.Close() != nil {
		t.Fatalf("open read %q size %d, stat size %d", body, st.Size(), info.Size())
	}
	d, err := fsys.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	rd := d.(fs.ReadDirFile)
	one, _ := rd.ReadDir(1)
	rest, _ := rd.ReadDir(5)
	_, end := rd.ReadDir(1)
	if _, rerr := d.Read(nil); len(one) != 1 || len(rest) != 1 || !errors.Is(end, io.EOF) || rerr == nil {
		t.Fatalf("batches %d, %d, end %v, read %v", len(one), len(rest), end, rerr)
	}
	if _, err := fsys.Open("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := fs.ReadFile(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing read: %v", err)
	}
	if all, _ := fs.ReadDir(fsys, "a"); len(all) != 2 {
		t.Fatalf("second listing %d entries", len(all))
	}
}

// verified returns an accepted verification whose statement lists subjects
// as name=hex pairs.
func verified(pairs ...string) *sign.VerifiedAttestation {
	v := &sign.VerifiedAttestation{Instance: "github"}
	for _, p := range pairs {
		name, hex, _ := strings.Cut(p, "=")
		v.Statement.Subjects = append(v.Statement.Subjects, sign.AttestationSubject{Name: name, Digest: map[string]string{"sha256": hex}})
	}
	return v
}

// record returns an artifact record with a package digest of 64 times c.
func record(id, version string, c byte) filesystem.ArtifactRecord {
	return filesystem.ArtifactRecord{ID: id, AuthoredVersion: version, PackageDigest: "sha256:" + strings.Repeat(string(c), 64)}
}

// Spec: §4.7.11 (Artifact step) — for each requirement an artifact meets it
// through a subject with its name and digest, and fails it as a digest or a
// version mismatch, or as missing with the scope artifact. Under require the
// failing artifact is dropped with its code; under warn it is kept with the
// outcome failed and the position of the requirement.
func TestArtifactStep_PerRequirement(t *testing.T) {
	t.Parallel()
	hex := func(c byte) string { return strings.Repeat(string(c), 64) }
	bundles := []*snapshotBundle{{accepted: []*sign.VerifiedAttestation{
		verified("a/x@1.0.0="+hex('1'), "a/y@1.0.0="+hex('2'), "a/z@1.0.0="+hex('3'), "a/w@1.0.0="+hex('4')),
		verified("a/x@1.0.0="+hex('1'), "a/y@1.0.0="+hex('9'), "a/z@2.0.0="+hex('3')),
	}}}
	kept := []filesystem.ArtifactRecord{record("a/x", "1.0.0", '1'), record("a/y", "1.0.0", '2'), record("a/z", "1.0.0", '3'), record("a/w", "1.0.0", '4')}
	reqs := []store.TrustRequirement{{}, {}}
	for _, mode := range []string{"require", "warn"} {
		a := &AttestationCycle{mode: mode, layer: store.LayerConfig{TrustRequirements: reqs}}
		drops, outcomes := a.artifactStep(bundles, kept)
		got := fmt.Sprintf("%v %v %v %v", outcomes["a/x"], outcomes["a/y"], outcomes["a/z"], outcomes["a/w"])
		want := "{verified    0} {failed ingest.attestation_mismatch artifact digest 1} {failed ingest.attestation_mismatch artifact version 1} {failed ingest.attestation_missing artifact  1}"
		if got != want {
			t.Errorf("%s outcomes:\n%s\nwant\n%s", mode, got, want)
		}
		if mode == "require" && (len(drops) != 3 || drops["a/w"].Details["scope"] != "artifact") || mode == "warn" && len(drops) != 0 {
			t.Errorf("%s drops %v", mode, drops)
		}
		v := &attestationVerdict{drops: drops}
		if rej, ok := v.drop("a/y"); mode == "require" && (!ok || rej.Code != CodeAttestationMismatch || rej.Details["constraint"] != "digest") {
			t.Errorf("drop = %+v %v", rej, ok)
		}
	}
	var none *attestationVerdict
	if _, ok := none.drop("a/x"); ok || none.report(nil) != nil {
		t.Error("a nil verdict drops or reports")
	}
}

// Spec: §4.7.11 (Layer step 5) — a commit longer than any commit name is
// not_in_history with no query, a long list is sent in queries of at most
// 64 commits, and a response with the wrong number of answers fails the
// cycle as ingest.source_unreachable.
func TestLineageAnswers_Batches(t *testing.T) {
	t.Parallel()
	candidate := func(commit string) *snapshotBundle {
		v := &sign.VerifiedAttestation{}
		v.Certificate.SourceRepositoryDigest = sign.CertField{Value: commit, Present: true}
		return &snapshotBundle{accepted: []*sign.VerifiedAttestation{v}}
	}
	var cands []*snapshotBundle
	for i := range 70 {
		cands = append(cands, candidate(fmt.Sprintf("%040d", i)))
	}
	cands = append(cands, candidate(strings.Repeat("f", 200)), candidate(fmt.Sprintf("%040d", 0)))
	var sizes []int
	a := &AttestationCycle{layer: store.LayerConfig{AttestationRecord: store.AttestationRecord{Commit: "base"}}}
	a.lineage = func(_ context.Context, base string, commits []string) ([]source.LineageAnswer, error) {
		sizes = append(sizes, len(commits))
		out := make([]source.LineageAnswer, len(commits))
		for i := range out {
			out[i] = source.InLineage
		}
		return out, nil
	}
	answers, err := a.lineageAnswers(context.Background(), cands)
	if err != nil || fmt.Sprint(sizes) != "[64 6]" || answers[strings.Repeat("f", 200)] != source.NotInHistory || len(answers) != 71 {
		t.Fatalf("err %v, sizes %v, %d answers", err, sizes, len(answers))
	}
	a.lineage = func(context.Context, string, []string) ([]source.LineageAnswer, error) { return nil, nil }
	if _, err := a.lineageAnswers(context.Background(), cands[:1]); !errors.Is(err, source.ErrSourceUnreachable) {
		t.Fatalf("short response: %v", err)
	}
}

// Spec: §4.7.11 (Policy that became invalid) — a stored policy that breaks a
// rule other than the two per-cycle ones fails closed with policy_rule.
func TestPolicyFailure_OtherRule(t *testing.T) {
	t.Parallel()
	a := &AttestationCycle{cfg: &AttestationConfig{Instances: map[string]sign.AttestationInstance{"public-good": {}}}, mode: "require", layer: store.LayerConfig{ID: "L", TrustMode: "require",
		TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{Issuer: "i"}}}}}}
	if f := a.policyFailure(); f == nil || f.Constraint() != "policy_rule" {
		t.Fatalf("failure %v", f)
	}
}

// failingRoot is a trusted root source that cannot produce a root.
type failingRoot struct{}

func (failingRoot) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, errors.New("root unreadable")
}

// Spec: §4.7.11 (Layer step) — a trusted root that fails for a reason other
// than its age fails the cycle outside the attestation steps, and an entry
// with the bundle suffix that is a directory, and an attestation directory
// path that is a file, fail step 1 with the constraint unreadable.
func TestLayerStep_RootErrorAndDirectoryEntries(t *testing.T) {
	t.Parallel()
	a := &AttestationCycle{cfg: &AttestationConfig{Instances: map[string]sign.AttestationInstance{
		"public-good": {Name: "public-good", Root: failingRoot{}},
	}}, layer: store.LayerConfig{TrustRequirements: []store.TrustRequirement{{Signers: []store.TrustSigner{{}}}}}}
	if f, err := a.checkRoots(context.Background()); f != nil || err == nil {
		t.Fatalf("failure %v, err %v", f, err)
	}
	for name, files := range map[string]fstest.MapFS{
		"directory entry": {".podium/attestations/x.sigstore.json/inner": {Data: []byte("{}")}},
		"file path":       {".podium/attestations": {Data: []byte("a file")}},
	} {
		if bundles, f, err := a.readBundles(files); len(bundles) != 0 || err != nil || f == nil || f.Constraint() != "unreadable" {
			t.Errorf("%s: bundles %d, failure %v, err %v", name, len(bundles), f, err)
		}
	}
}

// Spec: §4.7.11 (Reported strings) — a details object whose lists encode to
// 32 KiB or more, with <, >, and & escaped, loses list entries from its
// longest list until it fits, and is marked truncated; a small one is kept.
func TestBoundDetails(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("<", 256)
	var ids []string
	for range 40 {
		ids = append(ids, long)
	}
	d := boundDetails(map[string]any{"constraint": "x", "ids": ids, "other": []string{"a"}})
	b, _ := json.Marshal(d)
	if len(b) >= maxDetailsBytes || d["truncated"] != true || len(d["other"].([]string)) != 1 {
		t.Fatalf("bounded to %d bytes, details %v", len(b), d["truncated"])
	}
	if small := boundDetails(map[string]any{"constraint": "x"}); small["truncated"] != nil {
		t.Fatalf("small details marked truncated")
	}
	if n := listLen(1); n != 0 || dropLast(1) != 1 || listLen([]map[string]string{{}}) != 1 || len(dropLast([]map[string]string{{}}).([]map[string]string)) != 0 {
		t.Fatal("list helpers")
	}
}

// Spec: §4.7.11 (Attestation record) — only a Git call that reached its
// fetch deadline is recorded; another source failure records nothing.
func TestRecordSourceFailure_OnlyTheDeadline(t *testing.T) {
	t.Parallel()
	a := &AttestationCycle{}
	if recorded, err := a.RecordSourceFailure(context.Background(), nil, source.ErrSourceUnreachable, nil); recorded || err != nil {
		t.Fatalf("recorded %v, err %v", recorded, err)
	}
}

// Spec: §8.1 — the audit event reads the log index of a bundle's entry, and
// a bundle with none, or with an index that is no number, reports -1.
func TestLogIndex(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]int64{
		`{"verificationMaterial":{"tlogEntries":[{"logIndex":"42"}]}}`: 42,
		`{"verificationMaterial":{"tlogEntries":[{"logIndex":"x"}]}}`:  -1,
		`{"verificationMaterial":{}}`:                                  -1,
		`not json`:                                                     -1,
	} {
		if got := logIndex([]byte(raw)); got != want {
			t.Errorf("logIndex(%s) = %d, want %d", raw, got, want)
		}
	}
}

// Spec: §4.7.11 (Attestation record) — a store error on the record write
// fails the cycle, and a second write of one cycle does nothing.
func TestWrite_StoreErrorAndOnce(t *testing.T) {
	t.Parallel()
	st := failingRecordStore{Store: store.NewMemory()}
	a := &AttestationCycle{cfg: &AttestationConfig{}, clock: fixedClock{}, layer: store.LayerConfig{ID: "L"}}
	if _, err := a.write(context.Background(), st, store.AttestationWrite{}, nil); err == nil || !strings.Contains(err.Error(), "write the attestation record") {
		t.Fatalf("err %v", err)
	}
	if superseded, err := a.write(context.Background(), st, store.AttestationWrite{}, nil); superseded || err != nil {
		t.Fatalf("second write: %v %v", superseded, err)
	}
}

type failingRecordStore struct{ store.Store }

func (failingRecordStore) WriteLayerAttestation(context.Context, string, string, string, store.AttestationWrite) error {
	return errors.New("disk full")
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC) }
