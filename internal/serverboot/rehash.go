package serverboot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	// is off. The pass needs Verify as well as Sign, so it takes the whole
	// provider rather than ingest's SignerFunc.
	Signer sign.Provider
	// Sink is the §8.3 audit sink, or nil. Scrubber applies the §8.2
	// query-text scrubbing and tolerates a nil receiver.
	Sink     audit.Sink
	Scrubber *audit.PIIScrubber
}

// rehashClass is what the plan decided about one stored row.
type rehashClass int

const (
	// classMigrated: the row already stores the framed digest and needs no
	// write.
	classMigrated rehashClass = iota
	// classUnmigrated: the row's stored bytes reproduce the pre-framing
	// digest, or the row is at the framed digest and needs its first
	// envelope. The apply step rewrites it.
	classUnmigrated
	// classUnreproducible: the stored bytes reproduce neither digest.
	classUnreproducible
	// classSignatureUnverified: a signed row whose envelope does not verify
	// over its stored hash under the configured key.
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
}

func (r rehashRow) key() string {
	return fmt.Sprintf("%s/%s@%s", r.rec.TenantID, r.rec.ArtifactID, r.rec.Version)
}

// rehashStoredHashes rewrites every stored §4.7.6 content hash from the bytes
// the registry holds, once per store, on the first start of this version that
// binds its listen address. It runs before the bootstrap ingest and before
// anything is served, because an ingest that meets a row still at the previous
// digest reports a conflict for it and a served row at the previous digest
// fails a consumer's §6.6 step-2 check with materialize.content_hash_mismatch.
//
// Spec: §13.4 — "A release that changes how a stored value is computed rewrites
// the affected rows in place, from the bytes the registry holds, on the first
// start of the new version that binds its listen address, before that start
// ingests or serves, and records that the rewrite completed so that later
// starts skip it."
//
// It plans every row before it writes any, so the refusal that protects a
// stored signature (step 5 below) leaves the store as it was. The failure
// policy is: a row whose bytes could not be read, or whose signing or write
// failed, holds the marker back and is tried again on the next start; a row
// whose bytes or whose signature fail their checks is logged and does not,
// because no later pass could clear it and an unset marker would make every
// later start re-read every resource body in the store.
func rehashStoredHashes(ctx context.Context, d rehashDeps) error {
	applied, err := d.Store.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
	if err != nil {
		return fmt.Errorf("rehash: read data-migration marker: %w", err)
	}
	if applied {
		return nil
	}

	plan, err := planRehash(ctx, d)
	if err != nil {
		return err
	}
	if err := refuseStrandedSignature(plan, d.Signer); err != nil {
		return err
	}
	applyRehash(ctx, d, plan)
	return nil
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

func (p *rehashPlanner) classify(ctx context.Context, rec store.ManifestRecord) rehashRow {
	row := rehashRow{rec: rec}
	resources, fail := p.assemble(ctx, rec)
	if fail != nil {
		row.class, row.note, row.unread = fail.class, fail.note, fail.unread
		return row
	}

	row.newHash = "sha256:" + version.CanonicalContentHash(rec.Frontmatter, rec.SkillRaw, resources)

	// The envelope is the only evidence that the stored bytes are the bytes
	// the registry signed: an actor with store write access can alter a
	// row's bytes and recompute either digest over them, and cannot mint an
	// envelope. Re-signing such a row would turn a consumer's
	// materialize.signature_invalid into an accepted load. Spec: §4.7.9.
	if p.deps.Signer != nil && rec.Signature != "" {
		if err := p.deps.Signer.Verify(ctx, rec.ContentHash, rec.Signature); err != nil {
			row.class = classSignatureUnverified
			row.note = err.Error()
			return row
		}
	}

	if rec.ContentHash == row.newHash {
		if rec.Signature != "" || p.deps.Signer == nil {
			row.class = classMigrated
			return row
		}
		// A row at the framed digest carrying no envelope, with a
		// signer configured: the apply step mints its first one.
		row.class = classUnmigrated
		return row
	}
	if rec.ContentHash == "sha256:"+preFramingContentHash(rec.Frontmatter, rec.SkillRaw, resources) {
		row.class = classUnmigrated
		return row
	}
	row.class = classUnreproducible
	return row
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
		body, err := getWithDeadline(ctx, p.deps.Objects, key, p.deps.ReadTimeout)
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
// at the wrong root or bucket holds the marker back and the next start retries
// rather than the pass writing the store off as permanently short of bodies.
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
func refuseStrandedSignature(plan []rehashRow, signer sign.Provider) error {
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

// rehashCounts is what the summary line reports.
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
}

// applyRehash writes every unmigrated row through the store's compare-and-swap,
// so two replicas running the pass together never overwrite each other, signs
// each row it rewrites when a signer is configured, and appends one
// artifact.signed event (§8.1) per re-signed row.
func applyRehash(ctx context.Context, d rehashDeps, plan []rehashRow) {
	var c rehashCounts
	// appendEvents goes false after the first sink failure: an http(s)
	// audit destination is a synchronous POST with a ten-second timeout, so
	// retrying per row would cost that timeout for every re-signed row
	// while nothing answers the health probes.
	appendEvents := d.Sink != nil
	// holdMarker records a row the next start must try again.
	holdMarker := false

	for _, row := range plan {
		switch row.class {
		case classMigrated:
			c.migrated++
			continue
		case classSignatureUnverified:
			c.signatureUnverified++
			log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
			continue
		case classUnreproducible:
			c.unreproducible++
			log.Printf("rehash: %s left at its stored content hash: %s", row.key(), row.class)
			continue
		case classBodyMissing:
			c.bodyMissing++
			log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
			continue
		case classBodyUnavailable:
			c.bodyUnavailable++
			if row.unread {
				c.unread++
			}
			holdMarker = true
			log.Printf("rehash: %s left at its stored content hash: %s (%s)", row.key(), row.class, row.note)
			continue
		}

		signature := ""
		if d.Signer != nil {
			env, err := d.Signer.Sign(ctx, row.newHash)
			if err != nil {
				c.errors++
				holdMarker = true
				log.Printf("rehash: %s left at its stored content hash: sign: %v", row.key(), err)
				continue
			}
			signature = env
		}
		err := d.Store.RehashManifest(ctx, row.rec.TenantID, row.rec.ArtifactID, row.rec.Version, row.rec.ContentHash, row.newHash, signature)
		switch {
		case err == nil:
			c.rewritten++
		case errors.Is(err, store.ErrImmutableViolation), errors.Is(err, store.ErrNotFound):
			// A peer replica rewrote the row, a concurrent ingest
			// moved it, or a §8.4 purge removed it. None of those is
			// a failed write, so none holds the marker back.
			c.conflicts++
			log.Printf("rehash: %s left as another writer holds it: %v", row.key(), err)
			continue
		default:
			c.errors++
			holdMarker = true
			log.Printf("rehash: %s left at its stored content hash: rehash: %v", row.key(), err)
			continue
		}

		if signature == "" || !appendEvents {
			if signature != "" && d.Sink != nil {
				c.eventsNotAppended++
			}
			continue
		}
		if err := appendSignedEvent(ctx, d, row); err != nil {
			appendEvents = false
			c.eventsNotAppended++
			log.Printf("rehash: audit sink refused an artifact.signed event, appending no more in this run: %v", err)
		}
	}

	if !holdMarker {
		if err := d.Store.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, true); err != nil {
			log.Printf("rehash: recording the completed rewrite failed, the next start runs it again: %v", err)
		}
	}
	log.Printf("rehash: %d rewritten, %d already migrated, %d signature_unverified, %d unreproducible, %d body_missing, %d body_unavailable (%d unread), %d in conflict, %d in error, %d event(s) not appended",
		c.rewritten, c.migrated, c.signatureUnverified, c.unreproducible, c.bodyMissing, c.bodyUnavailable, c.unread, c.conflicts, c.errors, c.eventsNotAppended)
}

// appendSignedEvent emits the §8.1 artifact.signed event for one re-signed row,
// through the §8.2 manifest-declared redaction ingest applies to that event and
// through the §8.2 query-text scrubber.
func appendSignedEvent(ctx context.Context, d rehashDeps, row rehashRow) error {
	redact := ingest.ArtifactEventRedactor(row.rec)
	ev := audit.Event{
		Type:   audit.EventArtifactSigned,
		Caller: "system",
		Target: row.rec.ArtifactID,
		Context: redact(map[string]string{
			"version":      row.rec.Version,
			"content_hash": row.newHash,
		}),
	}
	return d.Sink.Append(ctx, d.Scrubber.ScrubEvent(ev))
}

// getWithDeadline reads one object under a deadline the provider does not have
// to honor. Filesystem.Get discards its context and calls os.ReadFile, and
// filesystem is the default object store, so a hung ReadWriteMany mount would
// otherwise block the plan for the life of the process. The read runs in a
// goroutine that sends on a buffered channel, so the goroutine exits whenever
// the provider returns even though nothing reads its result.
func getWithDeadline(ctx context.Context, p objectstore.Provider, key string, timeout time.Duration) ([]byte, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		body, err := p.Get(dctx, key)
		done <- result{body: body, err: err}
	}()
	select {
	case r := <-done:
		return r.body, r.err
	case <-dctx.Done():
		return nil, dctx.Err()
	}
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
// signature. A key generated at this start signed no stored row, and
// sign.RegistryManagedKey.Verify rejects a differing key_id, so every signed row
// would classify signature_unverified and the pass would set the marker and
// leave those rows at the previous digest even after the operator restored the
// key that signed them.
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
	// os.Stat follows a symbolic link, as readOrCreateEd25519's own stat
	// does, so a link to an absent target counts as absent for both.
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
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	for _, t := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("registry signing key: %w", err)
		}
		for _, rec := range recs {
			if rec.Signature == "" {
				continue
			}
			return fmt.Errorf("PODIUM_SIGN_KEY_PATH names no file (%s) and generating a key now would strand the §4.7.9 signature on %s/%s@%s, because the §13.4 content-hash rewrite has not completed: point PODIUM_SIGN_KEY_PATH at the key that signed the stored rows",
				path, rec.TenantID, rec.ArtifactID, rec.Version)
		}
	}
	return nil
}
