package serverboot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/pkg/version"
)

// planDigestTag is the framed leading value of every §13.4 plan digest. It
// separates a plan digest from a content hash and a delivery hash, and its
// version lets a later change to the covered values change every digest.
const planDigestTag = "podium/sign-stored-rows-plan/1"

// planDigestPattern is the only form --plan-digest accepts.
var planDigestPattern = regexp.MustCompile("^sha256:[0-9a-f]{64}$")

// planHeader is the plan-wide state a sign-stored-rows plan depends on beyond
// its rows: whether the completion record is present, the --include-unsigned
// setting, the signing key_id, and the verification-only key_ids. A key added
// to or removed from the verification set that verifies no stored row leaves
// every row value unchanged, so the key_ids enter the digest explicitly.
type planHeader struct {
	recordPresent   bool
	includeUnsigned bool
	// signingKey is the signing key's key_id, or empty with no signer. A
	// registry-managed key_id is never empty, so a signing-off header
	// cannot collide with a signing one.
	signingKey string
	// verifyKeys are the verification-only key_ids in ascending byte order.
	verifyKeys []string
}

// newPlanHeader builds the header of a plan read under the completion-record
// state applied. The dry run and the run both call it, so the two digests
// cannot drift apart. VerifyKeyIDs returns ids in key-file order, and the
// digest depends on the set, so the copy is sorted. Spec: §13.4.
func newPlanHeader(applied bool, d rehashDeps) planHeader {
	h := planHeader{recordPresent: applied, includeUnsigned: d.AttestUnsigned}
	if d.Signer == nil {
		return h
	}
	h.signingKey = d.Signer.CurrentKeyID()
	h.verifyKeys = slices.Clone(d.Signer.VerifyKeyIDs())
	slices.Sort(h.verifyKeys)
	return h
}

// mode names the completion-record state the header frames and prints.
func (h planHeader) mode() string {
	if h.recordPresent {
		return "record-present"
	}
	return "record-absent"
}

// canonicalPlan returns a copy of plan in ascending byte order of (tenant ID,
// artifact ID, version), the primary key in both SQL stores, so the order is
// total and independent of the store's listing order and collation.
func canonicalPlan(plan []rehashRow) []rehashRow {
	rows := slices.Clone(plan)
	slices.SortFunc(rows, func(a, b rehashRow) int {
		if c := strings.Compare(a.rec.TenantID, b.rec.TenantID); c != 0 {
			return c
		}
		if c := strings.Compare(a.rec.ArtifactID, b.rec.ArtifactID); c != 0 {
			return c
		}
		return strings.Compare(a.rec.Version, b.rec.Version)
	})
	return rows
}

// planDigest is the §13.4 plan digest over h and rows, which the caller passes
// in canonical order, and the number of rows it covers. Each value is framed
// with version.WriteFramed into one SHA-256 stream:
//
//   - the tag podium/sign-stored-rows-plan/1;
//   - the mode, record-present or record-absent;
//   - include-unsigned as strconv.FormatBool;
//   - the signing key_id, empty with no signer;
//   - the decimal count of verification-only key_ids, then each of them;
//   - for each row not of class migrated: tenant ID, artifact ID, version,
//     stored content hash, class name, target hash (empty when none), sign as
//     strconv.FormatBool, and the signature state.
//
// Each row frames a fixed number of values, so the stream needs no row count.
// A migrated row is excluded, because the run neither writes nor signs it and
// a signed ingest between the dry run and the run produces exactly such a row.
// Notes, signature bytes, and resource bodies are excluded. The golden test
// TestPlanDigest_Golden pins these encodings. Spec: §13.4.
func planDigest(h planHeader, rows []rehashRow) (string, int) {
	sum := sha256.New()
	frame := func(v string) { version.WriteFramed(sum, []byte(v)) }
	frame(planDigestTag)
	frame(h.mode())
	frame(strconv.FormatBool(h.includeUnsigned))
	frame(h.signingKey)
	frame(strconv.Itoa(len(h.verifyKeys)))
	for _, id := range h.verifyKeys {
		frame(id)
	}
	signing := h.signingKey != ""
	covered := 0
	for _, row := range rows {
		if row.class == classMigrated {
			continue
		}
		covered++
		frame(row.rec.TenantID)
		frame(row.rec.ArtifactID)
		frame(row.rec.Version)
		frame(row.rec.ContentHash)
		frame(row.class.String())
		frame(row.newHash)
		frame(strconv.FormatBool(row.writesSigned()))
		frame(signatureState(row, signing))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), covered
}

// writesSigned reports whether the run signs the row: only an unmigrated row
// is written, and only a written row is signed.
func (r rehashRow) writesSigned() bool {
	return r.class == classUnmigrated && r.sign
}

// writePlanRows writes one line per row, prefixed with prefix. The dry run and
// the changed-plan refusal both use it, so the two listings differ only in
// their prefix. stored= stays last, so readers of the earlier fields keep
// their field order. Spec: §13.4.
func writePlanRows(w io.Writer, prefix string, h planHeader, rows []rehashRow) {
	signing := h.signingKey != ""
	for _, row := range rows {
		target := row.newHash
		if target == "" {
			target = "-"
		}
		_, _ = fmt.Fprintf(w, "%s: %s class=%s target=%s write=%t sign=%t signed_by=%s stored=%s\n",
			prefix, row.key(), row.class, target, row.class == classUnmigrated, row.writesSigned(), signatureState(row, signing), row.rec.ContentHash)
	}
}

// writePlanHeader writes the header line, prefixed with prefix. An empty
// signing key and an empty verification set print as "-". Spec: §13.4.
func writePlanHeader(w io.Writer, prefix string, h planHeader) {
	signingKey, verifyKeys := h.signingKey, strings.Join(h.verifyKeys, ",")
	if signingKey == "" {
		signingKey = "-"
	}
	if verifyKeys == "" {
		verifyKeys = "-"
	}
	_, _ = fmt.Fprintf(w, "%s: plan mode=%s include_unsigned=%t signing_key=%s verify_keys=%s\n",
		prefix, h.mode(), h.includeUnsigned, signingKey, verifyKeys)
}

// signatureState names a planned row's stored envelope: the key_id of the
// trusted key that verifies it, "unsigned" for a row with no envelope,
// "unverified" for an envelope no trusted key verifies, or, when signing is
// false, "unchecked" for any envelope, because with no signer no envelope is
// verified and every signed row would otherwise read as unverified.
// Spec: §13.4.
func signatureState(row rehashRow, signing bool) string {
	switch {
	case row.rec.Signature == "":
		return "unsigned"
	case !signing:
		return "unchecked"
	case row.signedBy == "":
		return "unverified"
	}
	return row.signedBy
}

// checkReviewedPlan compares the plan a run built with the digest the operator
// reviewed. It returns nil when no digest was given, which is the boot path,
// and when the digests match. On a mismatch it prints the run's own plan as
// plan: lines in the dry-run format, so the operator can diff it against the
// reviewed dry run, and returns ErrSignStoredRowsPlanChanged before any write.
// Spec: §13.4.
func checkReviewedPlan(d rehashDeps, applied bool, plan []rehashRow) error {
	if d.ReviewedPlan == "" {
		return nil
	}
	h := newPlanHeader(applied, d)
	rows := canonicalPlan(plan)
	got, covered := planDigest(h, rows)
	if got == d.ReviewedPlan {
		return nil
	}
	var out strings.Builder
	writePlanRows(&out, "plan", h, rows)
	writePlanHeader(&out, "plan", h)
	_, _ = fmt.Fprintf(&out, "plan: plan digest %s over %d row(s)\n", got, covered)
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		d.summarize("%s", line)
	}
	return fmt.Errorf("%w: this run's plan digest is %s, the reviewed digest is %s; no row was written; rerun with --dry-run, review it, and pass its digest", ErrSignStoredRowsPlanChanged, got, d.ReviewedPlan)
}
