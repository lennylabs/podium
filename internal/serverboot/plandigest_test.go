package serverboot

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// goldenPlan is a fixed header and a fixed plan: two covered rows listed out
// of canonical order, and a migrated row the digest excludes.
func goldenPlan() (planHeader, []rehashRow) {
	h := planHeader{
		includeUnsigned: true,
		signingKey:      "0123456789abcdef",
		verifyKeys:      []string{"aaaa000000000000", "ffff000000000000"},
	}
	rows := []rehashRow{
		{
			rec:   store.ManifestRecord{TenantID: "acme", ArtifactID: "beta", Version: "2.0.0", ContentHash: "sha256:" + strings.Repeat("3", 64)},
			class: classBodyUnavailable,
			note:  "resource ref.md: connection reset",
		},
		{
			rec:   store.ManifestRecord{TenantID: "acme", ArtifactID: "gamma", Version: "1.0.0", ContentHash: "sha256:" + strings.Repeat("4", 64)},
			class: classMigrated, newHash: "sha256:" + strings.Repeat("4", 64),
		},
		{
			rec:      store.ManifestRecord{TenantID: "acme", ArtifactID: "alpha", Version: "1.0.0", ContentHash: "sha256:" + strings.Repeat("1", 64), Signature: "envelope"},
			class:    classUnmigrated,
			newHash:  "sha256:" + strings.Repeat("2", 64),
			sign:     true,
			signedBy: "0123456789abcdef",
		},
	}
	return h, rows
}

// Spec: §13.4 — the golden digest was computed outside Go from the framing
// rules: the tag, the header values, then each covered row's eight values in
// canonical order, each as a big-endian uint64 length and the bytes. It pins
// the tag, every literal encoding, the order, and the migrated-row exclusion.
func TestPlanDigest_Golden(t *testing.T) {
	h, rows := goldenPlan()
	got, covered := planDigest(h, canonicalPlan(rows))
	const want = "sha256:19d03e5aac065fcc807a6ff764803aa27755e38ed34767724a612409f94495bf"
	if got != want || covered != 2 {
		t.Errorf("planDigest = %s over %d, want %s over 2", got, covered, want)
	}
	if !planDigestPattern.MatchString(got) {
		t.Errorf("digest %s does not match the --plan-digest form", got)
	}
}

// Spec: §13.4 — the digest does not depend on the order in which the store
// lists rows: every permutation of a plan has one digest, and it differs from
// a digest computed over the listing order.
func TestPlanDigest_IndependentOfListingOrder(t *testing.T) {
	h, rows := goldenPlan()
	want, _ := planDigest(h, canonicalPlan(rows))
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)
	if got, _ := planDigest(h, canonicalPlan(reversed)); got != want {
		t.Errorf("reversed plan digest = %s, want %s", got, want)
	}
	if listing, _ := planDigest(h, rows); listing == want {
		t.Error("a digest over the listing order equals the canonical one; the test plan is already canonical")
	}
}

// Spec: §13.4 — every covered value enters the digest: changing any one of
// them changes it.
func TestPlanDigest_ChangesWithEveryCoveredValue(t *testing.T) {
	baseH, baseRows := goldenPlan()
	base, _ := planDigest(baseH, canonicalPlan(baseRows))
	alpha := func(rows []rehashRow) *rehashRow {
		for i := range rows {
			if rows[i].rec.ArtifactID == "alpha" {
				return &rows[i]
			}
		}
		t.Fatal("no alpha row")
		return nil
	}
	cases := map[string]func(h *planHeader, rows []rehashRow){
		"tenant":             func(_ *planHeader, r []rehashRow) { alpha(r).rec.TenantID = "globex" },
		"artifact id":        func(_ *planHeader, r []rehashRow) { alpha(r).rec.ArtifactID = "alphb" },
		"version":            func(_ *planHeader, r []rehashRow) { alpha(r).rec.Version = "1.0.1" },
		"stored hash":        func(_ *planHeader, r []rehashRow) { alpha(r).rec.ContentHash = "sha256:" + strings.Repeat("5", 64) },
		"class":              func(_ *planHeader, r []rehashRow) { alpha(r).class = classUnreproducible },
		"target":             func(_ *planHeader, r []rehashRow) { alpha(r).newHash = "sha256:" + strings.Repeat("6", 64) },
		"sign":               func(_ *planHeader, r []rehashRow) { alpha(r).sign = false },
		"signature state":    func(_ *planHeader, r []rehashRow) { alpha(r).signedBy = "aaaa000000000000" },
		"mode":               func(h *planHeader, _ []rehashRow) { h.recordPresent = true },
		"include-unsigned":   func(h *planHeader, _ []rehashRow) { h.includeUnsigned = false },
		"signing key":        func(h *planHeader, _ []rehashRow) { h.signingKey = "fedcba9876543210" },
		"verify key added":   func(h *planHeader, _ []rehashRow) { h.verifyKeys = append(h.verifyKeys, "ffff111111111111") },
		"verify key removed": func(h *planHeader, _ []rehashRow) { h.verifyKeys = h.verifyKeys[:1] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, rows := goldenPlan()
			mutate(&h, rows)
			if got, _ := planDigest(h, canonicalPlan(rows)); got == base {
				t.Errorf("digest unchanged after changing the %s", name)
			}
		})
	}
}

// Spec: §13.4 — a note, the stored signature bytes with the signature state
// held, and a migrated row do not enter the digest.
func TestPlanDigest_ExcludesNotesSignatureBytesAndMigratedRows(t *testing.T) {
	baseH, baseRows := goldenPlan()
	base, baseCovered := planDigest(baseH, canonicalPlan(baseRows))
	cases := map[string]func(rows []rehashRow) []rehashRow{
		"note": func(r []rehashRow) []rehashRow {
			r[0].note = "resource ref.md: i/o timeout"
			return r
		},
		"signature bytes": func(r []rehashRow) []rehashRow {
			r[2].rec.Signature = "another-envelope"
			return r
		},
		"migrated row": func(r []rehashRow) []rehashRow {
			return append(r, rehashRow{
				rec:   store.ManifestRecord{TenantID: "acme", ArtifactID: "delta", Version: "1.0.0", ContentHash: "sha256:" + strings.Repeat("7", 64)},
				class: classMigrated, newHash: "sha256:" + strings.Repeat("7", 64),
			})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, rows := goldenPlan()
			got, covered := planDigest(h, canonicalPlan(mutate(rows)))
			if got != base || covered != baseCovered {
				t.Errorf("digest %s over %d, want %s over %d", got, covered, base, baseCovered)
			}
		})
	}
}

// Spec: §13.4 — the dry run and the changed-plan listing come from one
// renderer, so the two differ only in their prefix, and stored= ends the row
// line.
func TestWritePlan_ListingsDifferOnlyInPrefix(t *testing.T) {
	h, rows := goldenPlan()
	rows = canonicalPlan(rows)
	render := func(prefix string) string {
		var b bytes.Buffer
		writePlanRows(&b, prefix, h, rows)
		writePlanHeader(&b, prefix, h)
		return b.String()
	}
	dry, plan := render("dry-run"), render("plan")
	strip := func(s, prefix string) string { return strings.ReplaceAll(s, prefix+": ", "") }
	if strip(dry, "dry-run") != strip(plan, "plan") {
		t.Errorf("listings differ beyond the prefix:\n%s\n%s", dry, plan)
	}
	for _, want := range []string{
		"dry-run: acme/alpha@1.0.0 class=unmigrated target=sha256:" + strings.Repeat("2", 64) + " write=true sign=true signed_by=0123456789abcdef stored=sha256:" + strings.Repeat("1", 64) + "\n",
		"dry-run: acme/beta@2.0.0 class=body_unavailable target=- write=false sign=false signed_by=unsigned stored=sha256:" + strings.Repeat("3", 64) + "\n",
		"dry-run: plan mode=record-absent include_unsigned=true signing_key=0123456789abcdef verify_keys=aaaa000000000000,ffff000000000000\n",
	} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry-run listing lacks %q:\n%s", want, dry)
		}
	}
	if !strings.HasPrefix(dry, "dry-run: acme/alpha@") {
		t.Errorf("rows are not in canonical order:\n%s", dry)
	}
}

// Spec: §13.4 — with no signer the header frames an empty signing key_id and
// no verification key_ids, prints both as "-", and differs from a signing
// header's digest; a signed row reads unchecked, which differs from the
// unverified state a signer would report.
func TestPlanHeader_WithoutASigner(t *testing.T) {
	h := newPlanHeader(true, rehashDeps{})
	if h.signingKey != "" || h.verifyKeys != nil || !h.recordPresent || h.includeUnsigned {
		t.Fatalf("header = %+v, want record-present with no signing or verify keys", h)
	}
	var b bytes.Buffer
	writePlanHeader(&b, "dry-run", h)
	if want := "dry-run: plan mode=record-present include_unsigned=false signing_key=- verify_keys=-\n"; b.String() != want {
		t.Errorf("header line = %q, want %q", b.String(), want)
	}

	signed := rehashRow{rec: store.ManifestRecord{TenantID: "acme", ArtifactID: "alpha", Version: "1.0.0", Signature: "envelope"}, class: classSignatureUnverified}
	if got := signatureState(signed, false); got != "unchecked" {
		t.Errorf("signatureState without a signer = %s, want unchecked", got)
	}
	if got := signatureState(signed, true); got != "unverified" {
		t.Errorf("signatureState with a signer = %s, want unverified", got)
	}

	plan := []rehashRow{signed}
	off, _ := planDigest(h, plan)
	on := h
	on.signingKey = "0123456789abcdef"
	if withSigner, _ := planDigest(on, plan); withSigner == off {
		t.Error("a signing-off plan has the digest of a signing plan")
	}
}

// Spec: §13.4 — the digest depends on the set of verification-only key_ids:
// two key files that list the same verify: keys in opposite orders yield one
// header and one digest, although VerifyKeyIDs returns key-file order.
func TestNewPlanHeader_SortsTheVerifyKeys(t *testing.T) {
	current, a, b := testSigner(t), testSigner(t), testSigner(t)
	ab, ba := current, current
	ab.Trusted = []ed25519.PublicKey{a.PublicKey, b.PublicKey}
	ba.Trusted = []ed25519.PublicKey{b.PublicKey, a.PublicKey}
	if slices.Equal(ab.VerifyKeyIDs(), ba.VerifyKeyIDs()) {
		t.Fatal("VerifyKeyIDs ignores key-file order; the case would not pin the sort")
	}
	hab := newPlanHeader(false, rehashDeps{Signer: ab})
	hba := newPlanHeader(false, rehashDeps{Signer: ba})
	if !slices.Equal(hab.verifyKeys, hba.verifyKeys) || !slices.IsSorted(hab.verifyKeys) || hab.signingKey != current.CurrentKeyID() {
		t.Errorf("headers = %+v and %+v, want equal sorted verify keys under the current key", hab, hba)
	}
	_, rows := goldenPlan()
	dab, _ := planDigest(hab, canonicalPlan(rows))
	dba, _ := planDigest(hba, canonicalPlan(rows))
	if dab != dba {
		t.Error("the digest depends on the order of the verify: lines")
	}
}

// Spec: §13.4 — the comparison runs only given a reviewed digest, passes a
// matching one, and on a mismatch lists the run's own plan and returns the
// sentinel. With no summary writer the listing goes to the log.
func TestCheckReviewedPlan(t *testing.T) {
	h, rows := goldenPlan()
	d := rehashDeps{AttestUnsigned: h.includeUnsigned}
	if err := checkReviewedPlan(d, false, rows); err != nil {
		t.Fatalf("no reviewed digest: %v", err)
	}
	d.ReviewedPlan, _ = planDigest(newPlanHeader(false, d), canonicalPlan(rows))
	if err := checkReviewedPlan(d, false, rows); err != nil {
		t.Fatalf("matching digest: %v", err)
	}

	logs := captureLog(t)
	err := checkReviewedPlan(d, true, rows)
	if !errors.Is(err, ErrSignStoredRowsPlanChanged) || !strings.Contains(err.Error(), d.ReviewedPlan) {
		t.Fatalf("err = %v, want the sentinel naming the reviewed digest", err)
	}
	for _, want := range []string{"plan: acme/alpha@1.0.0 class=unmigrated", "plan: plan mode=record-present", "plan: plan digest sha256:", " over 2 row(s)"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
}
