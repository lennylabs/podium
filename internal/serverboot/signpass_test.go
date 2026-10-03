package serverboot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// signPassFixture is the configuration a sign-stored-rows run reads: the boot
// fixture's isolated home and SQLite store, a key file in the store's
// directory, and a filesystem object store, all through the environment.
type signPassFixture struct {
	*bootFixture
	objRoot string
}

func newSignPassFixture(t *testing.T, key sign.RegistryManagedKey) *signPassFixture {
	t.Helper()
	f := &signPassFixture{bootFixture: newBootFixture(t)}
	f.objRoot = filepath.Join(f.home, "objects")
	t.Setenv("PODIUM_SIGN", "registry-key")
	t.Setenv("PODIUM_SIGN_KEY_PATH", f.keyPath)
	t.Setenv("PODIUM_FILESYSTEM_ROOT", f.objRoot)
	t.Setenv("PODIUM_OBJECT_STORE", "filesystem")
	t.Setenv("PODIUM_MIGRATION_OBJECT_READ_TIMEOUT", "")
	writeRegistryKeyFile(t, f.keyPath, key)
	return f
}

// writeRegistryKeyFile writes key's signing keypair and its Trusted keys as
// verify: lines.
func writeRegistryKeyFile(t *testing.T, path string, key sign.RegistryManagedKey) {
	t.Helper()
	kf := sign.KeyFile{Private: key.PrivateKey, Public: key.PublicKey, Verify: key.Trusted}
	if err := sign.WriteKeyFile(path, kf); err != nil {
		t.Fatalf("write key file: %v", err)
	}
}

// withStore opens the fixture's SQLite store and object store outside the
// command, runs fn, and closes the store, so the command never shares a
// connection with the test.
func (f *signPassFixture) withStore(t *testing.T, fn func(store.Store, objectstore.Provider)) {
	t.Helper()
	st, err := store.OpenSQLite(f.sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = st.Close() }()
	objs, err := objectstore.Open(f.objRoot)
	if err != nil {
		t.Fatalf("open object store: %v", err)
	}
	fn(st, objs)
}

// seed writes the rows and sets or clears the completion record.
func (f *signPassFixture) seed(t *testing.T, recordPresent bool, seeds ...rowSeed) {
	t.Helper()
	f.withStore(t, func(st store.Store, objs objectstore.Provider) {
		for _, s := range seeds {
			seedRow(t, st, objs, s)
		}
		if err := st.SetDataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming, recordPresent); err != nil {
			t.Fatalf("set record: %v", err)
		}
	})
}

func (f *signPassFixture) row(t *testing.T, s rowSeed) (rec store.ManifestRecord) {
	t.Helper()
	f.withStore(t, func(st store.Store, _ objectstore.Provider) { rec = readRow(t, st, s) })
	return rec
}

func (f *signPassFixture) recordSet(t *testing.T) (set bool) {
	t.Helper()
	f.withStore(t, func(st store.Store, _ objectstore.Provider) { set = markerSet(t, st) })
	return set
}

// runCommand runs sign-stored-rows with the log captured and returns its
// stdout and error.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	captureLog(t)
	var out bytes.Buffer
	err := runSignStoredRows(context.Background(), args, &out)
	return out.String(), err
}

// planDigestOf returns the digest on the last dry-run: plan digest line of a
// dry run's stdout and fails the test when the line is absent.
func planDigestOf(t *testing.T, stdout string) string {
	t.Helper()
	const prefix = "dry-run: plan digest "
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, prefix) {
		t.Fatalf("dry-run output does not end with a plan digest line:\n%s", stdout)
	}
	return strings.Fields(strings.TrimPrefix(last, prefix))[0]
}

// reviewedRun runs a dry run with args, then a run with args and the dry run's
// plan digest, and returns the run's stdout and error.
func reviewedRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	dry, err := runCommand(t, append([]string{"--dry-run"}, args...)...)
	if err != nil {
		t.Fatalf("dry run %v: %v", args, err)
	}
	return runCommand(t, append(args, "--plan-digest="+planDigestOf(t, dry))...)
}

func stillSignedLine(keyID string, n int) string {
	return fmt.Sprintf("rehash: verify key %s: %d row(s) still signed under it", keyID, n)
}

// verifiedBy returns the key_id that verifies the row's stored envelope under
// the provider's key set.
func verifiedBy(t *testing.T, key sign.RegistryManagedKey, rec store.ManifestRecord) string {
	t.Helper()
	id, err := key.VerifiedKeyID(context.Background(), rec.ContentHash, rec.Signature)
	if err != nil {
		t.Fatalf("%s envelope does not verify: %v", rec.ArtifactID, err)
	}
	return id
}

// Spec: §13.4, §4.7.9 — the command refuses with
// config.signature_provider_unavailable when it is invoked with
// --include-unsigned and signing is off, and, with signing on, when the key
// file is absent, when it carries no private: line, and when a verify: line
// does not decode. It generates no key and writes no row.
func TestRunSignStoredRows_RefusesWithoutAUsableKey(t *testing.T) {
	cases := []struct {
		name      string
		signMode  string
		keyText   string // written to the key path; empty leaves it absent
		namesPath bool
	}{
		{name: "signing off", signMode: "none"},
		{name: "absent key file", signMode: "registry-key", namesPath: true},
		{name: "no private line", signMode: "registry-key", keyText: "public: %[1]s\n", namesPath: true},
		{name: "undecodable verify line", signMode: "registry-key", keyText: "private: %[2]s\npublic: %[1]s\nverify: !!!\n", namesPath: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := testSigner(t)
			f := newSignPassFixture(t, key)
			if err := os.Remove(f.keyPath); err != nil {
				t.Fatal(err)
			}
			if tc.keyText != "" {
				text := fmt.Sprintf(tc.keyText, base64.StdEncoding.EncodeToString(key.PublicKey), base64.StdEncoding.EncodeToString(key.PrivateKey))
				if err := os.WriteFile(f.keyPath, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PODIUM_SIGN", tc.signMode)
			s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
			f.seed(t, false, s)
			before := f.row(t, s)

			var args []string
			if tc.signMode == "none" {
				args = []string{"--include-unsigned", "--dry-run"}
			}
			_, err := runCommand(t, args...)
			if !errors.Is(err, sign.ErrRegistryManagedUnavailable) || !strings.Contains(err.Error(), "config.signature_provider_unavailable") {
				t.Fatalf("err = %v, want config.signature_provider_unavailable wrapping the sentinel", err)
			}
			if errors.Is(err, ErrSignStoredRowsUsage) {
				t.Error("a refusal wraps the usage sentinel")
			}
			if tc.signMode == "none" && !strings.Contains(err.Error(), "--include-unsigned") {
				t.Errorf("err = %v, want it to name --include-unsigned", err)
			}
			if tc.namesPath && !strings.Contains(err.Error(), f.keyPath) {
				t.Errorf("err = %v, want it to name %s", err, f.keyPath)
			}
			if _, statErr := os.Stat(f.keyPath); tc.keyText == "" && statErr == nil {
				t.Error("the command generated a key file")
			}
			after := f.row(t, s)
			if after.ContentHash != before.ContentHash || after.Signature != before.Signature || f.recordSet(t) {
				t.Error("a refused command wrote the store")
			}
		})
	}
}

// Spec: §13.12, §13.4 — with PODIUM_SIGN_KEY_PATH unset and the default key
// file outside the SQLite store's directory, the command is refused as the
// registry start is, and no row's signature changes.
func TestRunSignStoredRows_RefusesAnUnpersistedKey(t *testing.T) {
	current, retired := rotatedSigner(t)
	f := newSignPassFixture(t, current)
	writeRegistryKeyFile(t, filepath.Join(f.home, ".podium", "standalone", "registry-signing.key"), current)
	t.Setenv("PODIUM_SIGN_KEY_PATH", "")
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired}
	f.seed(t, true, s)
	before := f.row(t, s)

	_, err := runCommand(t)
	if err == nil || !strings.Contains(err.Error(), "PODIUM_SIGN_KEY_PATH") {
		t.Fatalf("err = %v, want a refusal naming PODIUM_SIGN_KEY_PATH", err)
	}
	if after := f.row(t, s); after.Signature != before.Signature {
		t.Error("a refused command re-signed a row")
	}
}

// Spec: §13.4 — an unknown flag, a positional argument, --include-unsigned
// alone, --plan-digest with --dry-run, and a malformed digest are usage errors
// wrapping ErrSignStoredRowsUsage, refused before the store opens, which the
// dispatchers map to exit status 2; a changed plan maps to 3, every other
// error to 1, and success to 0.
func TestRunSignStoredRows_UsageErrors(t *testing.T) {
	f := newBootFixture(t)
	zeros := "sha256:" + strings.Repeat("0", 64)
	for _, args := range [][]string{
		{"--bogus"}, {"extra-arg"}, {"--dry-run", "extra-arg"},
		{"--include-unsigned"},
		{"--dry-run", "--plan-digest=" + zeros},
		{"--plan-digest=sha256:XYZ"},
		{"--plan-digest=sha256:" + strings.Repeat("A", 64)},
	} {
		_, err := runCommand(t, args...)
		if !errors.Is(err, ErrSignStoredRowsUsage) {
			t.Errorf("%v: err = %v, want the usage sentinel", args, err)
		}
		var stderr bytes.Buffer
		if code := SignStoredRowsExitCode(&stderr, err); code != 2 || !strings.Contains(stderr.String(), SignStoredRowsUsage) {
			t.Errorf("%v: exit %d, stderr %q; want 2 with the usage text", args, code, stderr.String())
		}
		if _, statErr := os.Stat(f.sqlitePath); statErr == nil {
			t.Errorf("%v: a usage error created the store", args)
		}
	}
	var changed bytes.Buffer
	if code := SignStoredRowsExitCode(&changed, fmt.Errorf("sign-stored-rows: %w", ErrSignStoredRowsPlanChanged)); code != 3 || strings.Contains(changed.String(), "usage:") {
		t.Errorf("changed plan: exit %d, stderr %q; want 3 without the usage text", code, changed.String())
	}
	var stderr bytes.Buffer
	refusal := fmt.Errorf("config.signature_provider_unavailable: %w", sign.ErrRegistryManagedUnavailable)
	if code := SignStoredRowsExitCode(&stderr, refusal); code != 1 || strings.Contains(stderr.String(), "usage:") {
		t.Errorf("refusal: exit %d, stderr %q; want 1 without the usage text", code, stderr.String())
	}
	if code := SignStoredRowsExitCode(&stderr, nil); code != 0 {
		t.Errorf("success exit = %d, want 0", code)
	}
}

// Spec: §13.4 — -h and --help are help requests rather than usage errors:
// the error carries flag.ErrHelp without ErrSignStoredRowsUsage, and the
// dispatchers print the usage text and exit 0.
func TestRunSignStoredRows_HelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		_, err := runCommand(t, arg)
		if !errors.Is(err, flag.ErrHelp) || errors.Is(err, ErrSignStoredRowsUsage) {
			t.Errorf("%s: err = %v, want flag.ErrHelp without the usage sentinel", arg, err)
		}
		var stderr bytes.Buffer
		code := SignStoredRowsExitCode(&stderr, err)
		if code != 0 || stderr.String() != SignStoredRowsUsage {
			t.Errorf("%s: exit %d, stderr %q; want 0 with the usage text alone", arg, code, stderr.String())
		}
	}
}

// Spec: §13.4 — a dry run with the completion record absent prints every
// planned write and makes none, the record included, so the boot's rewrite
// still runs afterwards and moves both rows to the framed digest. It lists the
// rows in canonical order with their stored hashes, then the plan header, and
// ends with the plan digest.
func TestRunSignStoredRows_DryRunWithTheRecordAbsent(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	unsigned := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
	signed := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", signWith: key}
	f.seed(t, false, signed)
	f.seed(t, false, unsigned)
	before := map[string]store.ManifestRecord{"alpha": f.row(t, unsigned), "beta": f.row(t, signed)}
	states := map[string]string{"alpha": "unsigned", "beta": key.CurrentKeyID()}

	out, err := runCommand(t, "--dry-run", "--include-unsigned")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, s := range []rowSeed{unsigned, signed} {
		want := fmt.Sprintf("dry-run: acme/%s@1.0.0 class=unmigrated target=%s write=true sign=true signed_by=%s stored=%s\n", s.id, framedHashOf(s), states[s.id], before[s.id].ContentHash)
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
		if after := f.row(t, s); after.ContentHash != before[s.id].ContentHash || after.Signature != before[s.id].Signature {
			t.Errorf("%s changed under a dry run", s.id)
		}
	}
	if strings.Index(out, "acme/alpha@") > strings.Index(out, "acme/beta@") {
		t.Errorf("rows are not in canonical order:\n%s", out)
	}
	for _, want := range []string{
		"dry-run: plan mode=record-absent include_unsigned=true signing_key=" + key.CurrentKeyID() + " verify_keys=-\n",
		"dry-run: 2 row(s) planned, 2 would be written, 2 would be signed\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if digest := planDigestOf(t, out); !strings.HasSuffix(out, "dry-run: plan digest "+digest+" over 2 row(s)\n") {
		t.Errorf("output does not end with the digest over 2 rows:\n%s", out)
	}
	if f.recordSet(t) {
		t.Fatal("a dry run recorded completion")
	}

	f.withStore(t, func(st store.Store, objs objectstore.Provider) {
		d := deps(st, objs)
		d.Signer = key
		if _, _, err := rehashStoredHashes(context.Background(), d, true); err != nil {
			t.Fatalf("boot rewrite: %v", err)
		}
		for _, s := range []rowSeed{unsigned, signed} {
			if rec := readRow(t, st, s); rec.ContentHash != framedHashOf(s) {
				t.Errorf("%s content_hash = %s after the boot rewrite, want %s", s.id, rec.ContentHash, framedHashOf(s))
			}
		}
	})
}

// Spec: §13.4, §4.7.9 — a dry run with the record present changes nothing,
// lists an unsigned framed row as a write that signs only under
// --include-unsigned, and names each row's signature state, so the unsigned
// rows the attestation covers read apart from the signed ones. It prints the
// unsigned-left and per-verify-key totals a run would report.
func TestRunSignStoredRows_DryRunWithTheRecordPresent(t *testing.T) {
	current, retired := rotatedSigner(t)
	outsider := testSigner(t)
	f := newSignPassFixture(t, current)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true}
	byCurrent := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true, signWith: current}
	byRetired := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0", framed: true, signWith: retired}
	untrusted := rowSeed{tenant: "acme", id: "delta", version: "1.0.0", framed: true, signWith: outsider}
	f.seed(t, true, s, byCurrent, byRetired, untrusted)

	plain, err := runCommand(t, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	attested, err := runCommand(t, "--dry-run", "--include-unsigned")
	if err != nil {
		t.Fatalf("dry run --include-unsigned: %v", err)
	}
	for _, want := range []string{
		"acme/alpha@1.0.0 class=migrated target=" + framedHashOf(s) + " write=false sign=false signed_by=unsigned stored=" + framedHashOf(s) + "\n",
		"acme/beta@1.0.0 class=migrated target=" + framedHashOf(byCurrent) + " write=false sign=false signed_by=" + current.CurrentKeyID() + " stored=" + framedHashOf(byCurrent) + "\n",
		"acme/gamma@1.0.0 class=unmigrated target=" + framedHashOf(byRetired) + " write=true sign=true signed_by=" + retired.CurrentKeyID() + " stored=" + framedHashOf(byRetired) + "\n",
		"acme/delta@1.0.0 class=signature_unverified target=- write=false sign=false signed_by=unverified stored=" + framedHashOf(untrusted) + "\n",
		"dry-run: plan mode=record-present include_unsigned=false signing_key=" + current.CurrentKeyID() + " verify_keys=" + retired.CurrentKeyID() + "\n",
		"dry-run: 1 unsigned left",
		"dry-run: verify key " + retired.CurrentKeyID() + ": 0 row(s) still signed under it",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain dry run lacks %q:\n%s", want, plain)
		}
	}
	for _, want := range []string{
		"acme/alpha@1.0.0 class=unmigrated target=" + framedHashOf(s) + " write=true sign=true signed_by=unsigned stored=" + framedHashOf(s) + "\n",
		"dry-run: plan mode=record-present include_unsigned=true",
		"dry-run: 0 unsigned left",
	} {
		if !strings.Contains(attested, want) {
			t.Errorf("attested dry run lacks %q:\n%s", want, attested)
		}
	}
	// Migrated rows leave the digest: the plain plan covers gamma and delta,
	// and the attestation adds alpha.
	if !strings.HasSuffix(plain, " over 2 row(s)\n") || !strings.HasSuffix(attested, " over 3 row(s)\n") {
		t.Errorf("covered counts: plain ends %q, attested ends %q; want 2 and 3", lastLine(plain), lastLine(attested))
	}
	if planDigestOf(t, plain) == planDigestOf(t, attested) {
		t.Error("the --include-unsigned setting does not change the digest")
	}
	if rec := f.row(t, s); rec.Signature != "" || rec.ContentHash != framedHashOf(s) {
		t.Error("a dry run wrote the row")
	}
	if id := verifiedBy(t, current, f.row(t, byRetired)); id != retired.CurrentKeyID() {
		t.Errorf("a dry run re-signed the retired-key row: verified by %s", id)
	}
}

// Spec: §13.4, §4.7.9 — a dry run that would leave a row under a
// verification-only key counts it on that key's line.
func TestDryRunSignStoredRows_CountsRowsLeftUnderAVerifyKey(t *testing.T) {
	captureLog(t)
	current, retired := rotatedSigner(t)
	st := store.NewMemory()
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired, resource: &seedResource{path: "big.md", body: []byte("BIG"), external: true}}
	seedRow(t, st, nil, s)
	if err := st.SetDataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming, true); err != nil {
		t.Fatalf("set record: %v", err)
	}
	d := deps(st, nil)
	d.Signer = current
	var out bytes.Buffer
	if err := dryRunSignStoredRows(context.Background(), d, &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{
		"class=body_unavailable target=- write=false sign=false signed_by=" + retired.CurrentKeyID(),
		"dry-run: verify key " + retired.CurrentKeyID() + ": 1 row(s) still signed under it",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}
}

// Spec: §13.4, §4.7.9 — with the record present the command re-signs a row a
// verification-only key verifies, leaves a row the signing key signed and a
// row no trusted key verifies untouched, and signs an unsigned row only under
// --include-unsigned.
func TestRunSignStoredRows_ResignsVerifyKeyRows(t *testing.T) {
	current, retired := rotatedSigner(t)
	outsider := testSigner(t)
	f := newSignPassFixture(t, current)
	byRetired := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired}
	byCurrent := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true, signWith: current}
	untrusted := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0", framed: true, signWith: outsider}
	unsigned := rowSeed{tenant: "acme", id: "delta", version: "1.0.0", framed: true}
	f.seed(t, true, byRetired, byCurrent, untrusted, unsigned)
	before := map[string]string{}
	for _, s := range []rowSeed{byCurrent, untrusted} {
		before[s.id] = f.row(t, s).Signature
	}

	out, err := runCommand(t)
	if err != nil {
		t.Fatalf("sign-stored-rows: %v", err)
	}
	if id := verifiedBy(t, current, f.row(t, byRetired)); id != current.CurrentKeyID() {
		t.Errorf("retired-key row verified by %s, want the signing key", id)
	}
	for _, s := range []rowSeed{byCurrent, untrusted} {
		if f.row(t, s).Signature != before[s.id] {
			t.Errorf("%s was rewritten", s.id)
		}
	}
	if f.row(t, unsigned).Signature != "" {
		t.Error("an unsigned row was signed without --include-unsigned")
	}
	for _, want := range []string{"rehash: 1 unsigned left", stillSignedLine(retired.CurrentKeyID(), 0), "1 signature_unverified"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}

	if _, err := reviewedRun(t, "--include-unsigned"); err != nil {
		t.Fatalf("sign-stored-rows --include-unsigned: %v", err)
	}
	if id := verifiedBy(t, current, f.row(t, unsigned)); id != current.CurrentKeyID() {
		t.Errorf("unsigned row signed by %s, want the signing key", id)
	}
}

// Spec: §13.4 — with the record absent the command performs the rewrite in
// place of the first start: it moves a signed row to the framed digest,
// signs the unsigned rows that start would sign without --include-unsigned,
// and records completion.
func TestRunSignStoredRows_PerformsTheRewriteWhenTheRecordIsAbsent(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	signed := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: key}
	unsignedFramed := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true}
	unsignedPre := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0"}
	f.seed(t, false, signed, unsignedFramed, unsignedPre)

	out, err := runCommand(t)
	if err != nil {
		t.Fatalf("sign-stored-rows: %v", err)
	}
	for _, s := range []rowSeed{signed, unsignedFramed, unsignedPre} {
		rec := f.row(t, s)
		if rec.ContentHash != framedHashOf(s) {
			t.Errorf("%s content_hash = %s, want %s", s.id, rec.ContentHash, framedHashOf(s))
		}
		if id := verifiedBy(t, key, rec); id != key.CurrentKeyID() {
			t.Errorf("%s signed by %s, want the signing key", s.id, id)
		}
	}
	if !strings.Contains(out, "rehash: 0 unsigned left") {
		t.Errorf("stdout lacks the unsigned-left line:\n%s", out)
	}
	if !f.recordSet(t) {
		t.Error("completion not recorded")
	}
}

// Spec: §13.4 — a row that holds the completion record back makes the
// command fail, where the boot's rewrite returns nil in the same state.
func TestSignStoredRows_HeldBackRowFails(t *testing.T) {
	key := testSigner(t)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: key}
	newDeps := func() rehashDeps {
		backing := store.NewMemory()
		seedRow(t, backing, nil, s)
		d := deps(&countingStore{Store: backing, rehashErr: errors.New("disk full")}, nil)
		d.Signer = key
		d.Summary = &bytes.Buffer{}
		return d
	}
	captureLog(t)
	if err := signStoredRows(context.Background(), newDeps()); err == nil {
		t.Error("the command succeeded with a held-back row")
	}
	if _, _, err := rehashStoredHashes(context.Background(), newDeps(), true); err != nil {
		t.Errorf("the boot rewrite returned %v, want nil", err)
	}
}

// Spec: §13.4 — a failed write of the completion record makes the command
// fail, because its success status says the rewrite standing in for the first
// start is recorded. The boot's rewrite logs the failure and returns nil, and
// the next start runs the pass again.
func TestSignStoredRows_CompletionRecordWriteFailureFails(t *testing.T) {
	key := testSigner(t)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: key}
	newDeps := func() rehashDeps {
		backing := store.NewMemory()
		seedRow(t, backing, nil, s)
		d := deps(&countingStore{Store: backing, setMarkErr: errors.New("record write refused")}, nil)
		d.Signer = key
		d.Summary = &bytes.Buffer{}
		return d
	}
	captureLog(t)
	err := signStoredRows(context.Background(), newDeps())
	if err == nil || !strings.Contains(err.Error(), "record write refused") {
		t.Errorf("command err = %v, want the record write's error", err)
	}
	if _, _, err := rehashStoredHashes(context.Background(), newDeps(), true); err != nil {
		t.Errorf("the boot rewrite returned %v, want nil", err)
	}
}

// Spec: §13.4 — with object storage turned off, an externally held body is
// unreadable, which holds the record back and fails the command.
func TestRunSignStoredRows_MissingObjectStoreFails(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: key,
		resource: &seedResource{path: "ref.md", body: []byte("reference"), external: true}}
	f.seed(t, false, s)
	t.Setenv("PODIUM_OBJECT_STORE", "none")

	if _, err := runCommand(t); err == nil {
		t.Error("the command succeeded with an unreadable body")
	}
	if f.recordSet(t) {
		t.Error("completion recorded over an unreadable body")
	}
}

// Spec: §13.4, §4.7.9 — the per-key line goes from the rows a
// verification-only key signed to 0 across one run, a second run still prints
// the 0 line, and a verify: key that signed no row prints a 0 line as well.
func TestRunSignStoredRows_PerKeyCounts(t *testing.T) {
	current, retired := rotatedSigner(t)
	spare := testSigner(t)
	current.Trusted = append(current.Trusted, spare.PublicKey)
	f := newSignPassFixture(t, current)
	a := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired}
	b := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", signWith: retired}
	f.seed(t, true, a, b)
	for _, s := range []rowSeed{a, b} {
		if id := verifiedBy(t, current, f.row(t, s)); id != retired.CurrentKeyID() {
			t.Fatalf("%s seeded under %s, want the retired key", s.id, id)
		}
	}

	for run := 1; run <= 2; run++ {
		out, err := runCommand(t)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, want := range []string{stillSignedLine(retired.CurrentKeyID(), 0), stillSignedLine(spare.CurrentKeyID(), 0)} {
			if !strings.Contains(out, want) {
				t.Errorf("run %d stdout lacks %q:\n%s", run, want, out)
			}
		}
	}
}

// signedEvents returns the content_hash of every artifact.signed event in the
// audit file, keyed by target.
func signedEvents(t *testing.T, path string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out
	}
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer func() { _ = file.Close() }()
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var ev struct {
			Type    string            `json:"type"`
			Target  string            `json:"target"`
			Context map[string]string `json:"context"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("parse audit line: %v", err)
		}
		if ev.Type == "artifact.signed" {
			out[ev.Target] = append(out[ev.Target], ev.Context["content_hash"])
		}
	}
	return out
}

// Spec: §8.1, §13.4 — the command appends one artifact.signed event per row
// it signs through the audit sink PODIUM_AUDIT_LOG_PATH names, and none for a
// row it moves without signing.
func TestRunSignStoredRows_AuditsEachSignedRow(t *testing.T) {
	current, retired := rotatedSigner(t)
	f := newSignPassFixture(t, current)
	byRetired := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired}
	unsigned := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true}
	f.seed(t, true, byRetired, unsigned)

	if _, err := runCommand(t); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := reviewedRun(t, "--include-unsigned"); err != nil {
		t.Fatalf("run --include-unsigned: %v", err)
	}
	events := signedEvents(t, f.auditPath)
	for _, s := range []rowSeed{byRetired, unsigned} {
		if got := events[s.id]; len(got) != 1 || got[0] != framedHashOf(s) {
			t.Errorf("%s events = %v, want one carrying %s", s.id, got, framedHashOf(s))
		}
	}

	moved := newSignPassFixture(t, current)
	pre := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0"}
	moved.seed(t, true, pre)
	if _, err := runCommand(t); err != nil {
		t.Fatalf("run over an unsigned pre-framing row: %v", err)
	}
	if rec := moved.row(t, pre); rec.ContentHash != framedHashOf(pre) || rec.Signature != "" {
		t.Errorf("unsigned pre-framing row = %s / %q, want the framed hash unsigned", rec.ContentHash, rec.Signature)
	}
	if n := len(signedEvents(t, moved.auditPath)); n != 0 {
		t.Errorf("an unsigned move appended %d artifact.signed event(s), want 0", n)
	}
}

// Spec: §13.4, §4.7.9 — a verification-only key's row whose object is absent
// from the object store stays counted under that key.
func TestRunSignStoredRows_CountsARowWithAMissingBody(t *testing.T) {
	current, retired := rotatedSigner(t)
	f := newSignPassFixture(t, current)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired,
		resource: &seedResource{path: "ref.md", body: []byte("reference"), external: true, withhold: true}}
	f.seed(t, true, s)

	out, _ := runCommand(t)
	if want := stillSignedLine(retired.CurrentKeyID(), 1); !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
}

// Spec: §13.12, §13.4, §4.7.9 — a verification-only key's row whose object
// read exceeds the deadline stays counted under that key, and the command
// fails because the unread row holds the record back.
func TestSignStoredRows_CountsARowWhoseReadTimesOut(t *testing.T) {
	current, retired := rotatedSigner(t)
	backing := store.NewMemory()
	objs, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired,
		resource: &seedResource{path: "ref.md", body: []byte("reference"), external: true}}
	seedRow(t, backing, objs, s)
	if err := backing.SetDataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming, true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d := deps(backing, &blockingObjects{Provider: objs, honorContext: true})
	d.ReadTimeout = 20 * time.Millisecond
	d.Signer = current
	d.Summary = &out
	captureLog(t)

	if err := signStoredRows(context.Background(), d); err == nil {
		t.Error("the command succeeded with a timed-out read")
	}
	if want := stillSignedLine(retired.CurrentKeyID(), 1); !strings.Contains(out.String(), want) {
		t.Errorf("summary lacks %q:\n%s", want, out.String())
	}
}

// Spec: §13.12, §13.4 — with PODIUM_MIGRATION_OBJECT_READ_TIMEOUT unset the
// command reads object-held bodies under the 30-second default, so a
// verification-only key's row with an external resource is re-signed and the
// command succeeds.
func TestRunSignStoredRows_ReadsBodiesWithinTheDefaultDeadline(t *testing.T) {
	current, retired := rotatedSigner(t)
	f := newSignPassFixture(t, current)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired,
		resource: &seedResource{path: "ref.md", body: []byte("reference"), external: true}}
	f.seed(t, true, s)

	out, err := runCommand(t)
	if err != nil {
		t.Fatalf("sign-stored-rows: %v\n%s", err, out)
	}
	if id := verifiedBy(t, current, f.row(t, s)); id != current.CurrentKeyID() {
		t.Errorf("row signed by %s, want the signing key", id)
	}
	if want := stillSignedLine(retired.CurrentKeyID(), 0); !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
}

// Spec: §13.4 — a store that cannot report the completion record or list its
// rows fails the command and the dry run with the store's error.
func TestSignStoredRows_StoreErrorsFailTheCommand(t *testing.T) {
	captureLog(t)
	cases := map[string]*countingStore{
		"record read": {Store: store.NewMemory(), markerErr: errors.New("record unreadable")},
		"tenant list": {Store: store.NewMemory(), tenantsErr: errors.New("tenants unreadable")},
	}
	for name, st := range cases {
		d := deps(st, nil)
		d.Signer = testSigner(t)
		if err := signStoredRows(context.Background(), d); err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Errorf("%s: command err = %v, want the store's error", name, err)
		}
		if err := dryRunSignStoredRows(context.Background(), d, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Errorf("%s: dry run err = %v, want the store's error", name, err)
		}
	}
}

// Spec: §13.4, §13.12 — with the record absent and the key file outside the
// store's directory, the command standing in for the first start follows the
// first start's policy: it leaves an unsigned framed row unsigned, names the
// reviewed --include-unsigned procedure on the unsigned-left line, and records
// completion. A reviewed --include-unsigned run then signs the row.
func TestRunSignStoredRows_RecordAbsentMintsUnsignedRowsOnlyBesideTheKey(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	elsewhere := filepath.Join(t.TempDir(), "registry-signing.key")
	writeRegistryKeyFile(t, elsewhere, key)
	t.Setenv("PODIUM_SIGN_KEY_PATH", elsewhere)
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true}
	f.seed(t, false, s)

	out, err := runCommand(t)
	if err != nil {
		t.Fatalf("sign-stored-rows: %v", err)
	}
	if rec := f.row(t, s); rec.Signature != "" {
		t.Error("the row was signed without --include-unsigned outside the key's directory")
	}
	if want := "rehash: 1 unsigned left; run sign-stored-rows --include-unsigned --dry-run, review it, and pass its plan digest to sign them"; !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
	if !f.recordSet(t) {
		t.Error("completion not recorded")
	}

	if _, err := reviewedRun(t, "--include-unsigned"); err != nil {
		t.Fatalf("sign-stored-rows --include-unsigned: %v", err)
	}
	if id := verifiedBy(t, key, f.row(t, s)); id != key.CurrentKeyID() {
		t.Errorf("row signed by %s, want the signing key", id)
	}
}

// lastLine returns the last non-empty line of out.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

// rowLines returns the row lines of a dry-run or plan listing with the prefix
// stripped, so a refused run's listing compares with the reviewed dry run's.
func rowLines(out, prefix string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix+": ") && strings.Contains(line, " class=") {
			rows = append(rows, strings.TrimPrefix(line, prefix+": "))
		}
	}
	return rows
}

// Spec: §13.4 — a reviewed --include-unsigned run signs the unsigned rows
// its dry run listed. An unsigned row stored between the dry run and the run
// changes the plan: the run writes no row, no audit event, and no completion
// record, prints its own plan as plan: lines, and exits 3.
func TestRunSignStoredRows_RefusesARowStoredAfterTheDryRun(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	alpha := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true}
	planted := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true}
	f.seed(t, true, alpha)
	dry, err := runCommand(t, "--dry-run", "--include-unsigned")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	f.seed(t, true, planted)
	before := map[string]store.ManifestRecord{"alpha": f.row(t, alpha), "beta": f.row(t, planted)}

	out, err := runCommand(t, "--include-unsigned", "--plan-digest="+planDigestOf(t, dry))
	if !errors.Is(err, ErrSignStoredRowsPlanChanged) {
		t.Fatalf("err = %v, want ErrSignStoredRowsPlanChanged", err)
	}
	var stderr bytes.Buffer
	if code := SignStoredRowsExitCode(&stderr, fmt.Errorf("sign-stored-rows: %w", err)); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	if n := strings.Count(stderr.String(), "sign-stored-rows: plan changed"); n != 1 {
		t.Errorf("stderr names the command prefix %d times, want once: %s", n, stderr.String())
	}
	for _, s := range []rowSeed{alpha, planted} {
		if after := f.row(t, s); after.ContentHash != before[s.id].ContentHash || after.Signature != before[s.id].Signature {
			t.Errorf("%s was written by a refused run", s.id)
		}
	}
	if !f.recordSet(t) {
		t.Error("a refused run changed the completion record")
	}
	if n := len(signedEvents(t, f.auditPath)); n != 0 {
		t.Errorf("a refused run appended %d artifact.signed event(s)", n)
	}
	reviewed, got := rowLines(dry, "dry-run"), rowLines(out, "plan")
	plantedLine := "acme/beta@1.0.0 class=unmigrated target=" + framedHashOf(planted) + " write=true sign=true signed_by=unsigned stored=" + framedHashOf(planted)
	if want := append(slices.Clone(reviewed), plantedLine); !reflect.DeepEqual(got, want) {
		t.Errorf("plan: rows = %q, want the reviewed rows plus the planted row %q", got, want)
	}
	if !strings.Contains(out, "plan: plan mode=record-present include_unsigned=true") || !strings.Contains(out, " over 2 row(s)") {
		t.Errorf("plan listing lacks its header or digest line:\n%s", out)
	}

	if _, err := reviewedRun(t, "--include-unsigned"); err != nil {
		t.Fatalf("a fresh reviewed run: %v", err)
	}
	for _, s := range []rowSeed{alpha, planted} {
		if id := verifiedBy(t, key, f.row(t, s)); id != key.CurrentKeyID() {
			t.Errorf("%s signed by %s, want the signing key", s.id, id)
		}
	}
}

// Spec: §13.4 — the run refuses with exit 3 when the plan's state changes
// between the dry run and the run: the completion record is set, a verify:
// line is added to the key file, or an object body is deleted. A row a signing
// registry stores at the current hash under the current key is migrated,
// outside the digest, and causes no refusal.
func TestRunSignStoredRows_ComparesThePlanItBuilds(t *testing.T) {
	cases := []struct {
		name    string
		record  bool
		between func(t *testing.T, f *signPassFixture, key sign.RegistryManagedKey)
		refused bool
	}{
		{name: "record set", between: func(t *testing.T, f *signPassFixture, _ sign.RegistryManagedKey) {
			f.seed(t, true)
		}, refused: true},
		{name: "verify key added", record: true, between: func(t *testing.T, f *signPassFixture, key sign.RegistryManagedKey) {
			key.Trusted = append(key.Trusted, testSigner(t).PublicKey)
			writeRegistryKeyFile(t, f.keyPath, key)
		}, refused: true},
		{name: "body deleted", record: true, between: func(t *testing.T, f *signPassFixture, _ sign.RegistryManagedKey) {
			f.withStore(t, func(_ store.Store, objs objectstore.Provider) {
				body := strings.TrimPrefix("sha256:"+version.CanonicalContentHash([]byte("reference"), nil, nil), "sha256:")
				if err := objs.Delete(context.Background(), body); err != nil {
					t.Fatal(err)
				}
			})
		}, refused: true},
		{name: "signed ingest", record: true, between: func(t *testing.T, f *signPassFixture, key sign.RegistryManagedKey) {
			f.seed(t, true, rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true, signWith: key})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := testSigner(t)
			f := newSignPassFixture(t, key)
			s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: tc.record, signWith: key,
				resource: &seedResource{path: "ref.md", body: []byte("reference"), external: true}}
			f.seed(t, tc.record, s)
			dry, err := runCommand(t, "--dry-run")
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			tc.between(t, f, key)
			before := f.row(t, s)

			_, err = runCommand(t, "--plan-digest="+planDigestOf(t, dry))
			if got := errors.Is(err, ErrSignStoredRowsPlanChanged); got != tc.refused {
				t.Fatalf("err = %v, want refused=%t", err, tc.refused)
			}
			if after := f.row(t, s); tc.refused && (after.ContentHash != before.ContentHash || after.Signature != before.Signature) {
				t.Error("a refused run wrote the row")
			}
		})
	}
}

// Spec: §13.4, §4.7.9 — a rotation run takes --plan-digest without
// --include-unsigned, the invocation a chart run Job with includeUnsigned=false
// makes: a wrong digest refuses with exit 3 and writes nothing, and the dry
// run's own digest re-signs the verify-key row.
func TestRunSignStoredRows_RotationRunWithADigest(t *testing.T) {
	current, retired := rotatedSigner(t)
	f := newSignPassFixture(t, current)
	byRetired := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", framed: true, signWith: retired}
	f.seed(t, true, byRetired)
	before := f.row(t, byRetired)

	_, err := runCommand(t, "--plan-digest=sha256:"+strings.Repeat("0", 64))
	if !errors.Is(err, ErrSignStoredRowsPlanChanged) || SignStoredRowsExitCode(&bytes.Buffer{}, err) != 3 {
		t.Fatalf("err = %v, want ErrSignStoredRowsPlanChanged and exit 3", err)
	}
	if after := f.row(t, byRetired); after.ContentHash != before.ContentHash || after.Signature != before.Signature {
		t.Error("a refused rotation run wrote the row")
	}

	if _, err := reviewedRun(t); err != nil {
		t.Fatalf("reviewed rotation run: %v", err)
	}
	if id := verifiedBy(t, current, f.row(t, byRetired)); id != current.CurrentKeyID() {
		t.Errorf("row verified by %s, want the signing key", id)
	}
	if !f.recordSet(t) {
		t.Error("the completion record was cleared")
	}
}

// Spec: §13.4 — with signing off the command runs the rewrite with no signer:
// the dry run reports a signed row as unchecked, a header with no keys, no
// unsigned-left line, and a digest over the one row it writes, and the run
// moves the unsigned pre-framing row to the new digest unsigned, records
// completion, appends no artifact.signed event, and generates no key.
func TestRunSignStoredRows_SigningOffRewritesWithoutSigning(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	if err := os.Remove(f.keyPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODIUM_SIGN", "none")
	pre := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0"}
	signed := rowSeed{tenant: "acme", id: "beta", version: "1.0.0", framed: true, signWith: key}
	framed := rowSeed{tenant: "acme", id: "gamma", version: "1.0.0", framed: true}
	f.seed(t, false, pre, signed, framed)

	dry, err := runCommand(t, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{
		"dry-run: acme/alpha@1.0.0 class=unmigrated target=" + framedHashOf(pre) + " write=true sign=false signed_by=unsigned",
		"dry-run: acme/beta@1.0.0 class=migrated target=" + framedHashOf(signed) + " write=false sign=false signed_by=unchecked",
		"dry-run: plan mode=record-absent include_unsigned=false signing_key=- verify_keys=-\n",
		" over 1 row(s)\n",
	} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry run lacks %q:\n%s", want, dry)
		}
	}
	if strings.Contains(dry, "unsigned left") {
		t.Errorf("a signing-off dry run printed an unsigned-left line:\n%s", dry)
	}
	if _, err := runCommand(t, "--plan-digest="+planDigestOf(t, dry)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec := f.row(t, pre); rec.ContentHash != framedHashOf(pre) || rec.Signature != "" {
		t.Errorf("pre-framing row = %s / %q, want the framed hash unsigned", rec.ContentHash, rec.Signature)
	}
	if !f.recordSet(t) {
		t.Error("completion not recorded")
	}
	if n := len(signedEvents(t, f.auditPath)); n != 0 {
		t.Errorf("a signing-off run appended %d artifact.signed event(s)", n)
	}
	if _, err := os.Stat(f.keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a signing-off run left a key file: %v", err)
	}
}

// Spec: §13.4 — with signing off the dry run and the run both refuse a plan
// that would strand a stored signature, writing nothing, and the dry run
// prints no digest for it.
func TestRunSignStoredRows_SigningOffRefusesAStrandedSignature(t *testing.T) {
	key := testSigner(t)
	f := newSignPassFixture(t, key)
	t.Setenv("PODIUM_SIGN", "none")
	s := rowSeed{tenant: "acme", id: "alpha", version: "1.0.0", signWith: key}
	f.seed(t, false, s)
	before := f.row(t, s)

	for _, args := range [][]string{{"--dry-run"}, nil} {
		out, err := runCommand(t, args...)
		if err == nil || !strings.Contains(err.Error(), "acme/alpha@1.0.0") || !strings.Contains(err.Error(), "PODIUM_SIGN=none") {
			t.Errorf("%v: err = %v, want a refusal naming the row and PODIUM_SIGN=none", args, err)
		}
		if strings.Contains(out, "plan digest") {
			t.Errorf("%v: printed a plan digest for a refused plan:\n%s", args, out)
		}
	}
	if after := f.row(t, s); after.ContentHash != before.ContentHash || after.Signature != before.Signature || f.recordSet(t) {
		t.Error("a refused command wrote the store")
	}
}

// Spec: §8.3, §13.12 — sign-stored-rows never anchors, so a file audit sink
// that cannot be opened is logged and the pass opens its collaborators
// without a sink.
func TestOpenSignPassDeps_UnopenableSinkLogsAndContinues(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_AUDIT_LOG_PATH", f.home)
	cfg, err := loadBootConfig()
	if err != nil {
		t.Fatalf("loadBootConfig: %v", err)
	}
	logs := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logs)
	defer log.SetOutput(prev)
	d, closeStore, err := openSignPassDeps(cfg, signStoredRowsOptions{dryRun: true})
	if err != nil {
		t.Fatalf("openSignPassDeps: %v", err)
	}
	defer closeStore()
	if d.Sink != nil {
		t.Errorf("sink = %v, want nil for an unopenable file sink", d.Sink)
	}
	if !strings.Contains(logs.String(), "warning: audit sink disabled: audit: open "+f.home) {
		t.Errorf("logs do not report the disabled sink:\n%s", logs.String())
	}
}
