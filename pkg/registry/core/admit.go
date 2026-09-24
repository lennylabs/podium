package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// WithAdmission sets the inputs of the §13.4 stored-row admission check: the
// signer whose key verifies each stored signature, the object store that holds
// the bundled-resource bodies ingest moved out of the manifest record, and the
// deadline on each object read (PODIUM_MIGRATION_OBJECT_READ_TIMEOUT, §13.12).
// A nil signer admits on the content hash alone. A nil object store refuses a
// row with an object-held body as unavailable, because admission cannot read
// the bytes it would serve. A non-positive timeout takes
// objectstore.DefaultReadTimeout. A registry that never calls WithAdmission
// admits with no signer and no object store.
func (r *Registry) WithAdmission(signer sign.Provider, objects objectstore.Provider, readTimeout time.Duration) *Registry {
	r.admitSigner = signer
	r.admitObjects = objects
	r.admitReadTimeout = readTimeout
	return r
}

// admit checks one stored row before the registry serves content built from it
// and returns the row with its derived fields taken from the admitted bytes.
// The checks run in the §13.4 order: the bundled-resource paths are distinct,
// each body is assembled and bound to its ref's content hash and size, the
// §4.7.6 content hash is recomputed and compared, the parent pin is checked
// against the admitted extends: reference, and, under a signer, the stored
// signature is verified over the stored hash. Each refusal is logged with the
// row's coordinates, and the returned error wraps the refusal's sentinel.
//
// Spec: §13.4 — stored-row admission, checks (1) through (3).
func (r *Registry) admit(ctx context.Context, rec store.ManifestRecord) (store.ManifestRecord, error) {
	a, err := r.admitBytes(ctx, rec)
	if err == nil {
		err = r.admitSignature(ctx, rec)
	}
	if err != nil {
		log.Printf("admission: refused %s/%s@%s: %v", rec.TenantID, rec.ArtifactID, rec.Version, err)
		return store.ManifestRecord{}, err
	}
	return withAdmittedFields(rec, a), nil
}

// admitBytes runs the content checks, (1) and (2), and returns the parsed
// manifest the derived fields come from. The artifact is nil for a row whose
// manifest is empty or does not parse, which declares no extends:.
func (r *Registry) admitBytes(ctx context.Context, rec store.ManifestRecord) (*manifest.Artifact, error) {
	if err := distinctResourcePaths(rec.Resources); err != nil {
		return nil, err
	}
	bodies, err := r.admittedBodies(ctx, rec.Resources)
	if err != nil {
		return nil, err
	}
	if rec.ContentHash == "" {
		return nil, fmt.Errorf("%w: the stored content hash is empty", ErrContentHashMismatch)
	}
	if got := "sha256:" + version.CanonicalContentHash(rec.Frontmatter, rec.SkillRaw, bodies); got != rec.ContentHash {
		return nil, fmt.Errorf("%w: the stored bytes hash to %s, the row stores %s", ErrContentHashMismatch, got, rec.ContentHash)
	}
	a, perr := manifest.ParseArtifact(rec.Frontmatter)
	if perr != nil {
		a = nil
	}
	if err := checkExtendsPin(a, rec.ExtendsPin); err != nil {
		return nil, err
	}
	return a, nil
}

// distinctResourcePaths refuses a row with two refs under one path. The hash
// and the assembly key bodies by path, so a second ref would drop out of the
// recomputed hash while the serve paths still served it.
func distinctResourcePaths(refs []store.ResourceRef) error {
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.Path] {
			return fmt.Errorf("%w: two resources under %s", ErrContentHashMismatch, ref.Path)
		}
		seen[ref.Path] = true
	}
	return nil
}

// admittedBodies assembles every bundled-resource body as the first-start
// rewrite does, from Inline when it is set and otherwise from the object stored
// under the ref's content hash, and binds each body to its ref's stored content
// hash and size. The serve paths state both columns to a consumer, and the
// §4.7.6 digest covers neither, so the check here is what binds them.
func (r *Registry) admittedBodies(ctx context.Context, refs []store.ResourceRef) (map[string][]byte, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	bodies := make(map[string][]byte, len(refs))
	for _, ref := range refs {
		body := ref.Inline
		if body == nil {
			var err error
			if body, err = r.readObjectBody(ctx, ref); err != nil {
				return nil, err
			}
		}
		sum := sha256.Sum256(body)
		if "sha256:"+hex.EncodeToString(sum[:]) != ref.ContentHash || int64(len(body)) != ref.Size {
			return nil, fmt.Errorf("%w: resource %s does not match its stored content hash or size", ErrContentHashMismatch, ref.Path)
		}
		bodies[ref.Path] = body
	}
	return bodies, nil
}

// readObjectBody reads one object-held body within the admission deadline. A
// body the store reports absent is a hash refusal; any other failure, and a
// registry with no object store, is an availability refusal.
func (r *Registry) readObjectBody(ctx context.Context, ref store.ResourceRef) ([]byte, error) {
	key := strings.TrimPrefix(ref.ContentHash, "sha256:")
	if r.admitObjects == nil {
		return nil, fmt.Errorf("%w: resource %s (%s) is held in object storage and no object store is configured", ErrUnavailable, ref.Path, key)
	}
	timeout := r.admitReadTimeout
	if timeout <= 0 {
		timeout = objectstore.DefaultReadTimeout
	}
	body, err := objectstore.GetWithDeadline(ctx, r.admitObjects, key, timeout)
	switch {
	case err == nil:
		return body, nil
	case errors.Is(err, objectstore.ErrNotFound):
		return nil, fmt.Errorf("%w: resource %s (%s) is absent from object storage", ErrContentHashMismatch, ref.Path, key)
	default:
		return nil, fmt.Errorf("%w: read resource %s (%s): %v", ErrUnavailable, ref.Path, key, err)
	}
}

// checkExtendsPin requires the stored parent pin to agree with the extends:
// reference the admitted manifest declares: empty exactly when it declares
// none, naming the referenced ID, and, for a reference other than a content
// hash, naming a version the reference accepts. A content-hash reference is
// compared with the parent row's admitted hash once admitChain has admitted
// that parent.
//
// Spec: §13.4 — stored-row admission, check (2).
func checkExtendsPin(a *manifest.Artifact, pin string) error {
	ref := ""
	if a != nil {
		ref = a.Extends
	}
	if ref == "" {
		if pin != "" {
			return fmt.Errorf("%w: the row stores a parent pin and its manifest declares no extends:", ErrContentHashMismatch)
		}
		return nil
	}
	if pin == "" {
		return fmt.Errorf("%w: the manifest declares extends: and the row stores no parent pin", ErrContentHashMismatch)
	}
	refID, refVersion := splitParentRef(ref)
	pinID, pinVersion := splitParentRef(pin)
	if refID != pinID {
		return fmt.Errorf("%w: the parent pin names another artifact than extends:", ErrContentHashMismatch)
	}
	p, err := version.ParsePin(refVersion)
	if err != nil {
		return fmt.Errorf("%w: the extends: reference does not parse: %v", ErrContentHashMismatch, err)
	}
	if p.Kind == version.PinContentHash {
		return nil
	}
	if _, err := version.Resolve(p, []string{pinVersion}); err != nil {
		return fmt.Errorf("%w: the parent pin's version does not satisfy extends:", ErrContentHashMismatch)
	}
	return nil
}

// admitSignature is check (3): under a configured signer, the stored signature
// must be present and verify over the stored content hash.
func (r *Registry) admitSignature(ctx context.Context, rec store.ManifestRecord) error {
	if r.admitSigner == nil {
		return nil
	}
	if rec.Signature == "" {
		return fmt.Errorf("%w: the row carries no signature", ErrStoredSignatureMissing)
	}
	if err := r.admitSigner.Verify(ctx, rec.ContentHash, rec.Signature); err != nil {
		return fmt.Errorf("%w: %v", ErrStoredSignatureInvalid, err)
	}
	return nil
}

// withAdmittedFields returns rec with every derived field taken from the
// admitted bytes. A row whose manifest is empty or does not parse keeps its
// stored values, because its bytes state none.
func withAdmittedFields(rec store.ManifestRecord, a *manifest.Artifact) store.ManifestRecord {
	if a == nil {
		return rec
	}
	rec.Body = []byte(admittedBody(a, rec.SkillRaw))
	setManifestFields(&rec, a)
	return rec
}

// admittedBody is the prose body ingest stores for a row: the SKILL.md body
// for a skill whose SKILL.md parses, the ARTIFACT.md body otherwise.
func admittedBody(a *manifest.Artifact, skillRaw []byte) string {
	if a.Type == manifest.TypeSkill {
		if s, err := manifest.ParseSkill(skillRaw); err == nil {
			return s.Body
		}
	}
	return a.Body
}

// setManifestFields writes the served fields a parsed manifest states onto a
// record. admit calls it with one row's manifest and mergeChain with the §4.6
// merge over a chain, so a chained and an unchained record take these fields
// by one rule.
func setManifestFields(rec *store.ManifestRecord, a *manifest.Artifact) {
	rec.Type = string(a.Type)
	rec.Sensitivity = string(a.Sensitivity)
	rec.Deprecated = a.Deprecated
	rec.ReplacedBy = a.ReplacedBy
	rec.AuditRedact = append([]string(nil), a.AuditRedact...)
}

// admitChain admits the requested row and, for a row that declares extends:,
// each row of its chain, and returns the admitted rows parent first, the order
// mergeChain takes. It follows a row's pin only after admit has passed that
// row, so a pin the admitted bytes do not declare is refused before anything
// reads it. Every error it returns is built from its sentinel and the
// requested ID alone; the failing chain member and the underlying error are
// logged, so no ancestor's ID or pin reaches a caller.
//
// Spec: §13.4 — stored-row admission, check (4) and the chain walk.
func (r *Registry) admitChain(ctx context.Context, rec store.ManifestRecord, requestedID string) ([]store.ManifestRecord, error) {
	chain, err := r.walkAdmitted(ctx, rec)
	if err != nil {
		log.Printf("admission: refused load of %s/%s: %v", r.tenantFor(ctx), requestedID, err)
		return nil, fmt.Errorf("%w: %s", sentinelOf(err), requestedID)
	}
	return chain, nil
}

// walkAdmitted is admitChain's walk from the requested row toward the root.
func (r *Registry) walkAdmitted(ctx context.Context, rec store.ManifestRecord) ([]store.ManifestRecord, error) {
	seen := map[string]bool{rec.ArtifactID + "@" + rec.Version: true}
	current, err := r.admit(ctx, rec)
	if err != nil {
		return nil, err
	}
	chain := []store.ManifestRecord{current}
	for current.ExtendsPin != "" {
		if seen[current.ExtendsPin] {
			return nil, fmt.Errorf("%w: extends cycle at %s", ErrInvalidArgument, current.ExtendsPin)
		}
		seen[current.ExtendsPin] = true
		parentID, parentVer := splitParentRef(current.ExtendsPin)
		stored, err := r.store.GetManifest(ctx, r.tenantFor(ctx), parentID, parentVer)
		if err != nil {
			return nil, fmt.Errorf("%w: parent %s: %v", ErrNotFound, current.ExtendsPin, err)
		}
		parent, err := r.admit(ctx, stored)
		if err != nil {
			return nil, fmt.Errorf("parent %s: %w", current.ExtendsPin, err)
		}
		if err := checkContentHashReference(current, parent); err != nil {
			return nil, err
		}
		chain = append([]store.ManifestRecord{parent}, chain...)
		current = parent
	}
	return chain, nil
}

// checkContentHashReference requires the admitted parent of a child that
// declares a content-hash extends: reference to carry the hash it names. The
// child passed checkExtendsPin, which parsed its manifest and its reference,
// so the parse failures here cannot occur and return nil defensively.
func checkContentHashReference(child, parent store.ManifestRecord) error {
	a, err := manifest.ParseArtifact(child.Frontmatter)
	if err != nil {
		return nil
	}
	_, refVersion := splitParentRef(a.Extends)
	p, err := version.ParsePin(refVersion)
	if err != nil || p.Kind != version.PinContentHash {
		return nil
	}
	if parent.ContentHash != "sha256:"+p.Hash {
		return fmt.Errorf("%w: parent %s does not carry the content hash extends: names", ErrContentHashMismatch, child.ExtendsPin)
	}
	return nil
}

// admissionSentinels are the errors admitChain and revalidationRedactKeys
// report to a caller, in the order sentinelOf tests them.
var admissionSentinels = []error{
	ErrContentHashMismatch,
	ErrStoredSignatureMissing,
	ErrStoredSignatureInvalid,
	ErrUnavailable,
	ErrNotFound,
	ErrInvalidArgument,
}

// sentinelOf returns the sentinel err wraps. Admission builds every error from
// one of them, so the unavailable fallback is defensive: it keeps an
// unforeseen error on the retryable registry.unavailable code.
func sentinelOf(err error) error {
	for _, s := range admissionSentinels {
		if errors.Is(err, s) {
			return s
		}
	}
	return ErrUnavailable
}

// revalidationRedactKeys returns the §8.2 audit_redact key set a full load of
// rec would record, for the read event of a revalidation that is answered
// without admission. It folds each chain row's directive child-wins over the
// unadmitted walk, as manifest.MergeExtends folds audit_redact, and reads no
// object. A row whose manifest is empty or does not parse contributes its
// stored column, the fallback admission applies.
//
// Spec: §8.2, §13.4.
func (r *Registry) revalidationRedactKeys(ctx context.Context, rec store.ManifestRecord, requestedID string) ([]string, error) {
	chain, err := r.resolveExtendsChain(ctx, rec, map[string]bool{})
	if err != nil {
		log.Printf("admission: revalidation of %s/%s: %v", r.tenantFor(ctx), requestedID, err)
		return nil, fmt.Errorf("%w: %s", sentinelOf(err), requestedID)
	}
	var keys []string
	for _, row := range chain {
		own := row.AuditRedact
		if a, perr := manifest.ParseArtifact(row.Frontmatter); perr == nil {
			own = a.AuditRedact
		}
		if len(own) > 0 {
			keys = own
		}
	}
	return append([]string(nil), keys...), nil
}
