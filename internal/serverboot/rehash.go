package serverboot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// keyedSigner is what the §13.4 rewrite needs from the §4.7.9 registry-managed
// provider: Sign and Verify, plus the key_id of the key that verified a stored
// envelope, the key_id Sign embeds, and the key_ids of the verification-only
// keys. The §9.1 SignatureProvider stays unchanged, so the interface lives here
// at its one consumer; sign.RegistryManagedKey satisfies it.
type keyedSigner interface {
	sign.Provider
	VerifiedKeyID(ctx context.Context, contentHash, signature string) (string, error)
	CurrentKeyID() string
	VerifyKeyIDs() []string
}

// rehashDeps carries the serving process's own collaborators into the §13.4
// stored-value rewrite. Every field is a value so a test can drive the pass
// against an in-memory store, an in-process object store, a generated key, and
// a recording sink without touching the environment or the filesystem.
type rehashDeps struct {
	// Store is the metadata store the process serves from.
	Store store.Store
	// Objects is the §7.2 object store, or nil when none is configured or
	// the open failed. A nil store leaves every externally held body
	// unreadable rather than failing the start.
	Objects objectstore.Provider
	// ReadTimeout bounds each object-store read
	// (PODIUM_MIGRATION_OBJECT_READ_TIMEOUT, §13.12).
	ReadTimeout time.Duration
	// Signer is the §4.7.9 registry-managed provider, or nil when signing
	// is off. The caller assigns it only when signing is on, because the
	// zero sign.RegistryManagedKey in this field would never be nil.
	Signer keyedSigner
	// MintUnsigned is the first-start rewrite's policy for a row that
	// carries no signature: true signs it when a signer is configured.
	// rehashPolicy replaces it with the effective value once the
	// completion record is read.
	MintUnsigned bool
	// AttestUnsigned is the operator's --include-unsigned: every unsigned
	// row is signed whether or not the rewrite has completed.
	AttestUnsigned bool
	// ReviewedPlan is the §13.4 plan digest the operator passed with
	// --plan-digest, or empty. The boot leaves it empty, and no comparison
	// runs then.
	ReviewedPlan string
	// Sink is the §8.3 audit sink, or nil. Scrubber applies the §8.2
	// query-text scrubbing and tolerates a nil receiver.
	Sink     audit.Sink
	Scrubber *audit.PIIScrubber
	// Summary receives the summary lines, or nil to log them. The boot logs
	// them; sign-stored-rows writes them to stdout, where the operator reads
	// the command's result, and keeps the per-row lines in the log.
	Summary io.Writer
}

// summarize writes one summary line to d.Summary, or logs it when none is set.
func (d rehashDeps) summarize(format string, args ...any) {
	if d.Summary == nil {
		log.Printf(format, args...)
		return
	}
	_, _ = fmt.Fprintf(d.Summary, format+"\n", args...)
}

// rehashPolicy reads the completion record once and returns a copy of d whose
// MintUnsigned is the effective unsigned-row policy, together with whether the
// record is present. Past the first run only the operator's attestation signs
// an unsigned row, because an unsigned row carries no evidence of who stored
// it. Spec: §13.4.
func rehashPolicy(ctx context.Context, d rehashDeps) (rehashDeps, bool, error) {
	applied, err := d.Store.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil {
		return d, false, fmt.Errorf("rehash: read data-migration marker: %w", err)
	}
	d.MintUnsigned = d.AttestUnsigned || (!applied && d.MintUnsigned)
	return d, applied, nil
}

// mintUnsignedOnFirstRun is the first-start rewrite's unsigned-row policy for
// the configured store and key path. The rewrite mints a first envelope for an
// unsigned row only when the store is the SQLite store in the key file's
// directory: there the key and the rows share one fate on disk, so an unsigned
// row was written by a process that could reach the key. A standard deployment
// attests its unsigned rows with a reviewed sign-stored-rows --include-unsigned
// run instead.
// A path that does not resolve returns an error rather than false, so the
// caller refuses before the rewrite instead of treating the key as absent.
// Spec: §13.4, §13.12.
func mintUnsignedOnFirstRun(cfg *Config, keyPathEnv string) (bool, error) {
	keyPath, err := sign.KeyFilePath(keyPathEnv)
	if err != nil {
		return false, fmt.Errorf("serverboot: resolve signing key path: %w", err)
	}
	return keyCoLocatedWithStore(cfg, keyPath), nil
}

// rehashClass is what the plan decided about one stored row.
type rehashClass int

const (
	// classMigrated: the row already stores the framed digest and needs no
	// write.
	classMigrated rehashClass = iota
	// classUnmigrated: the row's stored bytes reproduce the pre-framing
	// digest, or the row is at the framed digest and needs an envelope
	// under the signing key (its first one, or one replacing an envelope a
	// verification-only key made). The apply step rewrites it, signing it
	// when the row's sign flag is set.
	classUnmigrated
	// classUnreproducible: the stored bytes reproduce neither digest.
	classUnreproducible
	// classSignatureUnverified: a signed row whose envelope does not verify
	// over its stored hash under any key of the verification key set.
	classSignatureUnverified
	// classBodyMissing: object storage answered, and a bundled resource's
	// object is not there.
	classBodyMissing
	// classBodyUnavailable: a bundled resource's bytes could not be read.
	classBodyUnavailable
)

func (c rehashClass) String() string {
	switch c {
	case classMigrated:
		return "migrated"
	case classUnmigrated:
		return "unmigrated"
	case classUnreproducible:
		return "unreproducible"
	case classSignatureUnverified:
		return "signature_unverified"
	case classBodyMissing:
		return "body_missing"
	case classBodyUnavailable:
		return "body_unavailable"
	}
	return "unknown"
}

// rehashRow is one planned row: the record as stored, its class, and the value
// the apply step writes.
type rehashRow struct {
	rec     store.ManifestRecord
	class   rehashClass
	newHash string
	// note names why an unreadable row could not be read, for the log line.
	note string
	// unread marks a row classified body_unavailable without an
	// object-store read, after an earlier read exceeded its deadline.
	unread bool
	// sign marks a classUnmigrated row the apply step signs under the
	// signing key; without it the row is written with an empty signature.
	sign bool
	// signedBy is the key_id of the key that verified the stored envelope,
	// or empty for an unsigned row, a failed verification, or no signer.
	// It is recorded before the bodies are read, so the per-key count
	// includes a row whose bytes could not be read.
	signedBy string
}

func (r rehashRow) key() string {
	return fmt.Sprintf("%s/%s@%s", r.rec.TenantID, r.rec.ArtifactID, r.rec.Version)
}

// rehashStoredHashes rewrites every stored §4.7.6 content hash from the bytes
// the registry holds, once per store. The first start of this version that
// binds its listen address runs it only where refuseUnmigratedStore does not
// refuse that start, which is the SQLite store in the key file's directory or
// a store with no manifest row; otherwise, in either signing mode,
// sign-stored-rows runs it. It runs before the bootstrap ingest and before
// anything is served, because an ingest that meets a row still at the previous
// digest reports a conflict for it, and the registry's §13.4 stored-row
// admission check refuses a load of a row still at the previous digest with
// materialize.content_hash_mismatch.
//
// Spec: §13.4 — "A release that changes how a stored value is computed rewrites
// the affected rows in place, from the bytes the registry holds, on the first
// start of the new version that binds its listen address, before that start
// ingests or serves, or, where the paragraph that begins "While no completion
// is recorded" has that start refuse, through `sign-stored-rows` run before
// it, and records that the rewrite completed so that later starts skip it."
//
// Given d.ReviewedPlan, which sign-stored-rows sets from --plan-digest, it
// compares the plan digest of the plan it builds with that value before any
// check or write, and refuses with ErrSignStoredRowsPlanChanged on a mismatch,
// so the run writes only the plan the operator reviewed. Spec: §13.4.
//
// It plans every row before it writes any, so the refusal that protects a
// stored signature (step 5 below) leaves the store as it was. The failure
// policy is: a row whose bytes could not be read, or whose signing or write
// failed, holds the marker back and is tried again on the next start only
// where the store is the SQLite store in the key file's directory (otherwise,
// in either signing mode, the next start is refused and the next
// sign-stored-rows run tries the row again); a row
// whose bytes or whose signature fail their checks is logged and does not,
// because no later pass could clear it and an unset marker would make every
// later start re-read every resource body in the store.
//
// skipIfMarked returns at once when the completion record is present, which is
// the boot's behavior; sign-stored-rows passes false to plan every row again.
// It returns the counts the summary reports and whether a row held the record
// back.
func rehashStoredHashes(ctx context.Context, d rehashDeps, skipIfMarked bool) (rehashCounts, bool, error) {
	d, applied, err := rehashPolicy(ctx, d)
	if err != nil {
		return rehashCounts{}, false, err
	}
	if applied && skipIfMarked {
		return rehashCounts{}, false, nil
	}

	plan, err := planRehash(ctx, d)
	if err != nil {
		return rehashCounts{}, false, err
	}
	if err := checkReviewedPlan(d, applied, plan); err != nil {
		return rehashCounts{}, false, err
	}
	if err := refuseStrandedSignature(plan, d.Signer); err != nil {
		return rehashCounts{}, false, err
	}
	counts, held := applyRehash(ctx, d, plan)
	return counts, held, nil
}

// planRehash enumerates every tenant's rows, including the soft-deleted ones a
// §8.4 restore brings back, and classifies each before anything is written.
func planRehash(ctx context.Context, d rehashDeps) ([]rehashRow, error) {
	tenants, err := d.Store.ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("rehash: list tenants: %w", err)
	}
	p := &rehashPlanner{deps: d}
	for _, t := range tenants {
		recs, err := d.Store.ListManifestsIncludingDeleted(ctx, t.ID)
		if err != nil {
			return nil, fmt.Errorf("rehash: list manifests for tenant %s: %w", t.ID, err)
		}
		for _, rec := range recs {
			p.rows = append(p.rows, p.classify(ctx, rec))
		}
	}
	p.applyWrongRootGuard()
	return p.rows, nil
}

// rehashPlanner carries the state one plan accumulates: the rows, whether a
// read has already exceeded its deadline, and whether any read returned a body.
type rehashPlanner struct {
	deps rehashDeps
	rows []rehashRow
	// deadlineSpent records that an object-store read exceeded its
	// deadline. After the first, no further Get is issued in this run, so a
	// stalled object store costs the plan one deadline rather than one per
	// row.
	deadlineSpent bool
	// bodiesRead counts the object-store reads that returned a body. The
	// wrong-root guard reads it: an object store opened at the wrong root
	// or bucket answers every Get with not-found, which no single row can
	// tell from one lost object.
	bodiesRead int
}

// classify decides one row. With a signer, the stored envelope is verified
// before the bodies are read, because the check needs no body bytes and the
// per-key count must include a row whose bytes could not be read; the class
// order is otherwise unchanged, so a body failure is reported first.
// Spec: §13.4, §4.7.9.
func (p *rehashPlanner) classify(ctx context.Context, rec store.ManifestRecord) rehashRow {
	row := rehashRow{rec: rec}
	var verifyErr error
	if p.deps.Signer != nil && rec.Signature != "" {
		row.signedBy, verifyErr = p.deps.Signer.VerifiedKeyID(ctx, rec.ContentHash, rec.Signature)
	}
	resources, fail := p.assemble(ctx, rec)
	if fail != nil {
		row.class, row.note, row.unread = fail.class, fail.note, fail.unread
		return row
	}

	// The envelope is the only evidence that the stored bytes are the bytes
	// the registry signed: an actor with store write access can alter a
	// row's bytes and recompute either digest over them, and cannot mint an
	// envelope. Re-signing such a row would turn a consumer's
	// materialize.signature_invalid into an accepted load. Spec: §4.7.9.
	if verifyErr != nil {
		row.class = classSignatureUnverified
		row.note = verifyErr.Error()
		return row
	}

	row.newHash = "sha256:" + version.CanonicalContentHash(rec.Frontmatter, rec.SkillRaw, resources)
	row.class, row.sign = p.decide(row, resources)
	return row
}

// decide classifies a row whose bodies are in hand and whose envelope, if any,
// verified. Reproduction is checked before signing, so a row whose bytes were
// altered in the store is never re-signed, whichever key verified it.
func (p *rehashPlanner) decide(row rehashRow, resources map[string][]byte) (rehashClass, bool) {
	rec := row.rec
	atNew := rec.ContentHash == row.newHash
	if !atNew && rec.ContentHash != "sha256:"+preFramingContentHash(rec.Frontmatter, rec.SkillRaw, resources) {
		return classUnreproducible, false
	}
	sign := p.signs(row, atNew)
	if atNew && !sign {
		return classMigrated, false
	}
	return classUnmigrated, sign
}

// signs reports whether the apply step signs a reproducible row: an unsigned
// row under the effective unsigned-row policy, and a verified row unless the
// signing key already signed it at the new digest. A row signed under a
// verification-only key is re-signed under the signing key. With no signer
// nothing is signed, so a signing-off rewrite never reaches Sign.
// Spec: §13.4, §4.7.9.
func (p *rehashPlanner) signs(row rehashRow, atNew bool) bool {
	if p.deps.Signer == nil {
		return false
	}
	if row.rec.Signature == "" {
		return p.deps.MintUnsigned
	}
	return !atNew || row.signedBy != p.deps.Signer.CurrentKeyID()
}

// bodyFailure is why one row's bundled-resource bytes could not be assembled.
type bodyFailure struct {
	class  rehashClass
	note   string
	unread bool
}

// assemble collects the artifact's bundled-resource bodies, fetching the ones
// held in object storage because the digest covers a resource's full bytes
// (§4.7.6). A nil failure means every body is in hand.
func (p *rehashPlanner) assemble(ctx context.Context, rec store.ManifestRecord) (map[string][]byte, *bodyFailure) {
	if len(rec.Resources) == 0 {
		return nil, nil
	}
	resources := make(map[string][]byte, len(rec.Resources))
	for _, ref := range rec.Resources {
		if ref.Inline != nil {
			resources[ref.Path] = ref.Inline
			continue
		}
		key := strings.TrimPrefix(ref.ContentHash, "sha256:")
		if p.deps.Objects == nil {
			return nil, &bodyFailure{class: classBodyUnavailable, note: fmt.Sprintf("resource %s (%s): no object store is configured", ref.Path, key)}
		}
		if p.deadlineSpent {
			return nil, &bodyFailure{class: classBodyUnavailable, unread: true, note: fmt.Sprintf("resource %s (%s): not read, an earlier object-store read exceeded PODIUM_MIGRATION_OBJECT_READ_TIMEOUT", ref.Path, key)}
		}
		body, err := objectstore.GetWithDeadline(ctx, p.deps.Objects, key, p.deps.ReadTimeout)
		switch {
		case err == nil:
			p.bodiesRead++
			resources[ref.Path] = body
		case errors.Is(err, objectstore.ErrNotFound):
			return nil, &bodyFailure{class: classBodyMissing, note: fmt.Sprintf("resource %s (%s): %v", ref.Path, key, err)}
		default:
			if errors.Is(err, context.DeadlineExceeded) {
				p.deadlineSpent = true
			}
			return nil, &bodyFailure{class: classBodyUnavailable, note: fmt.Sprintf("resource %s (%s): %v", ref.Path, key, err)}
		}
	}
	return resources, nil
}

// applyWrongRootGuard treats every body_missing row as body_unavailable when
// no object-store read in this pass returned a body, so an object store opened
// at the wrong root or bucket holds the marker back and the rewrite runs again,
// by the next start only over the SQLite store in the key file's directory and
// otherwise by the next sign-stored-rows run, rather than the pass writing the
// store off as permanently short of bodies.
func (p *rehashPlanner) applyWrongRootGuard() {
	if p.bodiesRead > 0 {
		return
	}
	var missing int
	for i := range p.rows {
		if p.rows[i].class == classBodyMissing {
			p.rows[i].class = classBodyUnavailable
			p.rows[i].note += fmt.Sprintf("; no object-store read returned a body, so %s may be the wrong root or bucket", objectStoreLocation(p.deps.Objects))
			missing++
		}
	}
	if missing > 0 {
		log.Printf("rehash: %d row(s) report a missing object and no object-store read returned a body; check the object store at %s", missing, objectStoreLocation(p.deps.Objects))
	}
}

// refuseStrandedSignature is step 5: a signed row the pass would rewrite while
// no signer is configured would be left with an envelope over a hash the row no
// longer stores. The plan completes before any write, so the refusal leaves the
// store as it was and the marker unset. Spec: §13.4.
func refuseStrandedSignature(plan []rehashRow, signer keyedSigner) error {
	if signer != nil {
		return nil
	}
	var stranded []string
	for _, row := range plan {
		if row.class == classUnmigrated && row.rec.Signature != "" {
			stranded = append(stranded, row.key())
		}
	}
	if len(stranded) == 0 {
		return nil
	}
	return fmt.Errorf("rehash: %d stored row(s) carry a §4.7.9 signature and need their content hash rewritten while no signer is configured: %s; remove PODIUM_SIGN=none and point PODIUM_SIGN_KEY_PATH at the key that signed them", len(stranded), strings.Join(stranded, ", "))
}

// rehashCounts is what the summary lines report.
type rehashCounts struct {
	rewritten           int
	migrated            int
	signatureUnverified int
	unreproducible      int
	bodyMissing         int
	bodyUnavailable     int
	unread              int
	conflicts           int
	errors              int
	eventsNotAppended   int
	// unsignedLeft counts planned rows the pass left without a signature.
	unsignedLeft int
	// stillSigned counts, per verifying key_id, planned rows the pass did
	// not rewrite.
	stillSigned map[string]int
	// recordErr is the failed write of the completion record. The boot logs
	// it and runs the pass again on the next start; outside the co-located
	// SQLite store, the boot also fails this start before its bootstrap
	// ingest through refuseUnrecordedIngest. sign-stored-rows fails on it,
	// because its success status tells the operator the rewrite's completion
	// is recorded.
	recordErr error
}

// rehashApplier carries the state one apply step accumulates.
type rehashApplier struct {
	deps   rehashDeps
	counts rehashCounts
	// appendEvents goes false after the first sink failure: an http(s)
	// audit destination is a synchronous POST with a ten-second timeout, so
	// retrying per row would cost that timeout for every re-signed row
	// while nothing answers the health probes.
	appendEvents bool
	// held records a row the next start must try again. Outside the
	// co-located SQLite store, the held row stays in the store, and the next
	// start is refused until sign-stored-rows records completion.
	held bool
}

// applyRehash writes every unmigrated row through the store's compare-and-swap
// on its stored hash and signature, so two replicas running the pass together
// never overwrite each other, signs each row the plan marks for signing, and
// appends one artifact.signed event (§8.1) per signed write. It returns the
// counts and whether a row held the completion record back.
func applyRehash(ctx context.Context, d rehashDeps, plan []rehashRow) (rehashCounts, bool) {
	a := &rehashApplier{
		deps:         d,
		counts:       rehashCounts{stillSigned: map[string]int{}},
		appendEvents: d.Sink != nil,
	}
	for _, row := range plan {
		a.counts.countSigningState(row, a.apply(ctx, row))
	}

	if !a.held {
		if err := d.Store.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, true); err != nil {
			log.Printf("rehash: recording the completed rewrite failed: %v", err)
			a.counts.recordErr = fmt.Errorf("record the completed rewrite: %w", err)
		}
	}
	c := a.counts
	d.summarize("rehash: %d rewritten, %d already migrated, %d signature_unverified, %d unreproducible, %d body_missing, %d body_unavailable (%d unread), %d in conflict, %d in error, %d event(s) not appended",
		c.rewritten, c.migrated, c.signatureUnverified, c.unreproducible, c.bodyMissing, c.bodyUnavailable, c.unread, c.conflicts, c.errors, c.eventsNotAppended)
	logSigningSummary(d, c, "rehash")
	return c, a.held
}

// countSigningState adds one planned row to the unsigned-left and per-key
// counts, given whether the pass wrote it. The dry run calls it with the write
// it would make, so its totals are the ones a run reports.
func (c *rehashCounts) countSigningState(row rehashRow, written bool) {
	if row.rec.Signature == "" && (!written || !row.sign) {
		c.unsignedLeft++
	}
	if row.signedBy != "" && !written {
		c.stillSigned[row.signedBy]++
	}
}

// logSigningSummary reports, on lines of their own after the summary line, how
// many rows were left unsigned and how many remain signed under each
// verification-only key, with a line for a key that verifies no row so a
// missing line never stands in for a zero. Each line starts with prefix, so
// the dry run's projected totals read apart from a run's. A nonzero
// unsigned-left count names the reviewed procedure that attests those rows,
// a --include-unsigned dry run and a run with its plan digest, because outside
// the co-located SQLite store the rewrite leaves them unsigned. With no signer
// it logs nothing. Spec: §13.4, §4.7.9.
func logSigningSummary(d rehashDeps, c rehashCounts, prefix string) {
	if d.Signer == nil {
		return
	}
	hint := ""
	if c.unsignedLeft > 0 {
		hint = "; run sign-stored-rows --include-unsigned --dry-run, review it, and pass its plan digest to sign them"
	}
	d.summarize("%s: %d unsigned left%s", prefix, c.unsignedLeft, hint)
	for _, id := range d.Signer.VerifyKeyIDs() {
		d.summarize("%s: verify key %s: %d row(s) still signed under it", prefix, id, c.stillSigned[id])
	}
}

// apply handles one planned row and reports whether the pass wrote it.
func (a *rehashApplier) apply(ctx context.Context, row rehashRow) bool {
	if !a.tally(row) {
		return false
	}
	signature := ""
	if row.sign {
		env, err := a.deps.Signer.Sign(ctx, row.newHash)
		if err != nil {
			a.counts.errors++
			a.held = true
			log.Printf("rehash: %s left at its stored content hash: sign: %v", row.key(), err)
			return false
		}
		signature = env
	}
	if !a.write(ctx, row, signature) {
		return false
	}
	a.recordSigned(ctx, row, signature)
	return true
}

// tally counts and logs a row the pass does not write, and reports whether the
// row is one to write.
func (a *rehashApplier) tally(row rehashRow) bool {
	switch row.class {
	case classUnmigrated:
		return true
	case classMigrated:
		a.counts.migrated++
		return false
	case classSignatureUnverified:
		a.counts.signatureUnverified++
		log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
	case classUnreproducible:
		a.counts.unreproducible++
		log.Printf("rehash: %s left at its stored content hash: %s", row.key(), row.class)
	case classBodyMissing:
		a.counts.bodyMissing++
		log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
	case classBodyUnavailable:
		a.counts.bodyUnavailable++
		if row.unread {
			a.counts.unread++
		}
		a.held = true
		log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
	}
	return false
}

// write replaces the row through the store's compare-and-swap on the stored
// hash and signature the plan read, and reports whether it landed.
func (a *rehashApplier) write(ctx context.Context, row rehashRow, signature string) bool {
	err := a.deps.Store.RehashManifest(ctx, row.rec.TenantID, row.rec.ArtifactID, row.rec.Version, row.rec.ContentHash, row.rec.Signature, row.newHash, signature)
	switch {
	case err == nil:
		a.counts.rewritten++
		return true
	case errors.Is(err, store.ErrImmutableViolation), errors.Is(err, store.ErrNotFound):
		// A peer replica rewrote the row, a concurrent ingest moved it,
		// or a §8.4 purge removed it. None of those is a failed write, so
		// none holds the marker back.
		a.counts.conflicts++
		log.Printf("rehash: %s left as another writer holds it: %v", row.key(), err)
	default:
		a.counts.errors++
		a.held = true
		log.Printf("rehash: %s left at its stored content hash: rehash: %v", row.key(), err)
	}
	return false
}

// recordSigned appends the artifact.signed event for a signed write while the
// sink accepts events, and counts the events it could not append.
func (a *rehashApplier) recordSigned(ctx context.Context, row rehashRow, signature string) {
	if signature == "" || a.deps.Sink == nil {
		return
	}
	if !a.appendEvents {
		a.counts.eventsNotAppended++
		return
	}
	if err := appendSignedEvent(ctx, a.deps, row); err != nil {
		a.appendEvents = false
		a.counts.eventsNotAppended++
		log.Printf("rehash: audit sink refused an artifact.signed event, appending no more in this run: %v", err)
	}
}

// appendSignedEvent emits the §8.1 artifact.signed event for one re-signed row,
// through the §8.2 manifest-declared redaction ingest applies to that event and
// through the §8.2 query-text scrubber. The event records the tenant that owns
// the artifact (§8.1).
func appendSignedEvent(ctx context.Context, d rehashDeps, row rehashRow) error {
	redact := ingest.ArtifactEventRedactor(row.rec)
	ev := audit.Event{
		Type:   audit.EventArtifactSigned,
		Caller: "system",
		Target: row.rec.ArtifactID,
		Tenant: row.rec.TenantID,
		Context: redact(map[string]string{
			"version":      row.rec.Version,
			"content_hash": row.newHash,
		}),
	}
	return d.Sink.Append(ctx, d.Scrubber.ScrubEvent(ev))
}

// objectStoreLocation names where the object store is configured to read from,
// so the wrong-root guard's log line points the operator at the root or bucket
// that answered nothing.
func objectStoreLocation(p objectstore.Provider) string {
	switch s := p.(type) {
	case nil:
		return "(no object store)"
	case *objectstore.Filesystem:
		return "root " + s.Root
	case *objectstore.S3:
		return "bucket " + s.Bucket
	}
	return p.ID()
}

// preFramingContentHash is the digest the previous release computed over the
// same parts: the SHA-256 over their unframed concatenation, in the same slot
// order. It is the tree's only remaining use of that composition and lives
// outside pkg/version so no caller can produce an unframed digest for anything
// but this comparison. It is deleted with the pass.
func preFramingContentHash(artifactBytes, skillBytes []byte, resources map[string][]byte) string {
	h := sha256.New()
	_, _ = h.Write(artifactBytes)
	_, _ = h.Write(skillBytes)
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write(resources[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// refuseGeneratedSigningKey refuses a start that would generate a signing key
// while the §13.4 rewrite has not completed and a stored row carries a
// signature. A key generated at this start signed no stored row and its file
// carries no verify: line, so no stored envelope verifies under its §4.7.9
// verification key set. Every signed row would classify signature_unverified,
// and the pass would set the marker and leave those rows at the previous
// digest even after the operator restored the key that signed them.
//
// It runs ahead of the signing-key loader whatever the bind outcome, so a
// refused start generates and writes no key and the next start with the same
// configuration is refused the same way. Spec: §13.4, §4.7.9.
func refuseGeneratedSigningKey(ctx context.Context, st store.Store, signMode string) error {
	if !registrySigningEnabled(signMode) {
		return nil
	}
	path, err := registrySigningKeyPath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	// os.Stat follows a symbolic link, as readOrCreateKeyFile's read does,
	// so a link to an absent target counts as absent for both.
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		// A key file that exists, or a stat error other than absence:
		// the loader generates nothing, and it reports a read error
		// itself.
		return nil
	}
	applied, err := st.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	if applied {
		return nil
	}
	rec, found, err := firstStoredRow(ctx, st, func(rec store.ManifestRecord) bool { return rec.Signature != "" })
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	if !found {
		return nil
	}
	return fmt.Errorf("PODIUM_SIGN_KEY_PATH names no file (%s) and generating a key now would strand the §4.7.9 signature on %s/%s@%s, because the §13.4 content-hash rewrite has not completed: point PODIUM_SIGN_KEY_PATH at the key that signed the stored rows",
		path, rec.TenantID, rec.ArtifactID, rec.Version)
}

// firstStoredRow returns the first manifest row, soft-deleted ones included,
// that match accepts, walking every tenant. It returns a listing error
// unwrapped, so each caller prefixes it with the refusal it belongs to.
func firstStoredRow(ctx context.Context, st store.Store, match func(store.ManifestRecord) bool) (store.ManifestRecord, bool, error) {
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		return store.ManifestRecord{}, false, err
	}
	for _, t := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, t.ID)
		if err != nil {
			return store.ManifestRecord{}, false, err
		}
		for _, rec := range recs {
			if match(rec) {
				return rec, true, nil
			}
		}
	}
	return store.ManifestRecord{}, false, nil
}

// refuseUnmigratedStore refuses a start over a store the §13.4 rewrite has not
// completed on, outside the SQLite store in the directory of the registry
// signing key location, when the store holds a manifest row. sign-stored-rows
// performs the rewrite in place of such a start, behind its dry run and, with
// signing on, its plan digest, so the rows a start would rewrite are the rows
// an operator reviewed.
//
// Two stores are exempt. The co-located SQLite store keeps the automatic
// rewrite, because the key and the rows it signs share one fate on disk. A
// store with no manifest row has nothing to review, and its first start
// records completion before it ingests (refuseUnrecordedIngest), so the rows
// it then stores do not refuse the next start. The refusal applies in both
// signing modes: a signing-off rewrite signs nothing but still moves every
// stored row to a new digest without review.
//
// It runs after refuseUnpersistedSigningKey and refuseGeneratedSigningKey,
// whose refusals take precedence, and before the signing-key loader whatever
// the bind outcome, so a refused start generates no key, rewrites no row, and
// records no completion. A start that finds a row reads the record again and
// refuses only when it is still absent, so a replica that starts beside a
// peer's first start over an empty store proceeds once the peer has recorded
// completion. Every store read error fails the start, so a start that cannot
// confirm the record or tell whether the store holds a row runs no rewrite.
// Spec: §13.4.
func refuseUnmigratedStore(ctx context.Context, st store.Store, cfg *Config) error {
	keyPath, governed, err := unmigratedStoreGoverned(cfg)
	if err != nil || !governed {
		return err
	}
	applied, err := st.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil {
		return fmt.Errorf("registry start: %w", err)
	}
	if applied {
		return nil
	}
	rec, found, err := firstStoredRow(ctx, st, func(store.ManifestRecord) bool { return true })
	if err != nil {
		return fmt.Errorf("registry start: %w", err)
	}
	if !found {
		return nil
	}
	if applied, err = st.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming); err != nil {
		return fmt.Errorf("registry start: %w", err)
	}
	if applied {
		return nil
	}
	return unmigratedStoreError(cfg, rec, keyPath)
}

// unmigratedStoreError is refuseUnmigratedStore's refusal. With signing on it
// names the dry run, the --include-unsigned setting the run repeats because
// the plan header frames it, and --plan-digest; when no key file exists it
// first names podium admin signing-key generate, because sign-stored-rows
// never generates a key. With signing off it names PODIUM_SIGN=none for the
// command's environment, because podium serve --sign none sets the mode only
// inside its own process, and names neither --include-unsigned, which the
// command refuses with signing off, nor --plan-digest, which is optional
// without it.
func unmigratedStoreError(cfg *Config, rec store.ManifestRecord, keyPath string) error {
	row := fmt.Sprintf("%s/%s@%s", rec.TenantID, rec.ArtifactID, rec.Version)
	if !registrySigningEnabled(cfg.signMode) {
		return fmt.Errorf("registry start: the store is %s and holds manifest rows, such as %s, with no record that the §13.4 content-hash rewrite completed, and outside the SQLite store in the directory of the registry signing key location %s a start does not run that rewrite, with signing off as with signing on; with no registry process on the previous release serving the store, and with PODIUM_SIGN=none in the command's environment, run sign-stored-rows --dry-run, review its report, run sign-stored-rows, and start the registry again",
			storeLabel(cfg), row, keyLocationLabel(keyPath))
	}
	steps := "run sign-stored-rows --dry-run"
	// A stat error other than absence keeps the first message, because the
	// command's key loader reports that error itself.
	if _, err := os.Stat(keyPath); errors.Is(err, fs.ErrNotExist) {
		steps = fmt.Sprintf("create the signing key file, which sign-stored-rows never generates, with podium admin signing-key generate --key-file %s and keep it in the deployment's backup, then run sign-stored-rows --dry-run", keyPath)
	}
	return fmt.Errorf("registry start: the store is %s and holds manifest rows, such as %s, with no record that the §13.4 content-hash rewrite completed, and outside the SQLite store in the directory of the signing key file %s a start does not run that rewrite; with no registry process on the previous release serving the store, %s, adding --include-unsigned to attest unsigned rows, review its report, run sign-stored-rows with the same --include-unsigned setting and --plan-digest set to the digest on the report's last line, and start the registry again",
		storeLabel(cfg), row, keyPath, steps)
}

// keyLocationLabel names the key location in a refusal. An empty keyPath,
// which only a signing-off start whose default key location cannot be
// resolved returns, is named by the variable that sets it.
func keyLocationLabel(keyPath string) string {
	if keyPath == "" {
		return "PODIUM_SIGN_KEY_PATH (unset, and the default location cannot be resolved)"
	}
	return keyPath
}

// refuseUnrecordedIngest fails a start that unmigratedStoreGoverned covers
// when the §13.4 completion record is absent once the rewrite would have run,
// before the bootstrap ingest. It closes three paths: a listener that did not
// bind, so the rewrite never ran; a row the rewrite held back; and a failed
// write of the record. Ingesting without the record would store rows that
// refuseUnmigratedStore then refuses at every later start of every replica.
//
// A failed governed start therefore stores no manifest row. Over a store that
// was empty, the next start after a failed bind or a failed record write finds
// no row and runs the empty rewrite, which records completion. A row the
// rewrite held back stays in the store, so the next start is refused until
// sign-stored-rows records completion.
//
// It reads the completion record, which only applyRehash sets,
// migrate-to-standard clears, and Postgres.ResetForTest truncates in tests.
// A present record, whether this start's rewrite, an earlier start, or a peer
// set it, lets the start proceed whatever the bind outcome, and a read error
// fails the start. A bind failure over an absent record is returned as the
// error run() reports for it, so the operator reads the address in use.
// Spec: §13.4.
func refuseUnrecordedIngest(ctx context.Context, st store.Store, cfg *Config, bindErr error, configuredBind string) error {
	keyPath, governed, err := unmigratedStoreGoverned(cfg)
	if err != nil || !governed {
		return err
	}
	applied, err := st.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil {
		return fmt.Errorf("registry start: %w", err)
	}
	if applied {
		return nil
	}
	if bindErr != nil {
		return fmt.Errorf("serve: bind %s: %w", configuredBind, bindErr)
	}
	return fmt.Errorf("registry start: the §13.4 content-hash rewrite did not record its completion on the store %s, and outside the SQLite store in the directory of the signing key file %s a start does not ingest before that record exists; the rehash: lines above name the row that held it back or the failed record write; fix that cause and start the registry again",
		storeLabel(cfg), keyLocationLabel(keyPath))
}
