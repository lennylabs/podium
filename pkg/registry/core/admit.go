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

// admit checks the requested stored row before the registry serves content
// built from it and returns the row with its derived fields taken from the
// admitted bytes. The checks run in the §13.4 order: the bundled-resource
// paths are distinct, each body is assembled and bound to its ref's content
// hash and size, the §4.7.6 content hash is recomputed and compared, and,
// under a signer, the stored signature is verified over the stored hash. Each
// refusal is logged with the row's coordinates and the underlying error, and
// the returned error is built from the refusal's sentinel and the artifact ID
// alone, so no stored detail reaches a caller. Manifests do not inherit
// (§4.6), so the served content comes from this one row.
//
// Spec: §13.4 — stored-row admission, checks (1) and (2).
func (r *Registry) admit(ctx context.Context, rec store.ManifestRecord) (store.ManifestRecord, error) {
	a, err := r.admitBytes(ctx, rec)
	if err == nil {
		err = r.admitSignature(ctx, rec)
	}
	if err != nil {
		log.Printf("admission: refused %s/%s@%s: %v", rec.TenantID, rec.ArtifactID, rec.Version, err)
		return store.ManifestRecord{}, fmt.Errorf("%w: %s", sentinelOf(err), rec.ArtifactID)
	}
	return withAdmittedFields(rec, a), nil
}

// admitBytes runs the content check, (1), and returns the parsed manifest the
// derived fields come from. A plain-skill row yields the value the filesystem
// loader builds for a skill with no ARTIFACT.md: type skill and the SKILL.md
// description. The artifact is nil for any other row whose manifest is empty
// or does not parse.
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
	if err := bindResourceRefs(rec.Resources, bodies); err != nil {
		return nil, err
	}
	return admittedArtifact(rec), nil
}

// admittedArtifact parses the row's admitted manifest. A plain-skill row has
// no manifest, so its artifact is built from its admitted SKILL.md rather
// than left nil, which would serve the row's unhashed columns.
//
// Spec: §13.4 — a row whose manifest is empty and whose SKILL.md parses is a
// skill with no ARTIFACT.md.
func admittedArtifact(rec store.ManifestRecord) *manifest.Artifact {
	if a, err := manifest.ParseArtifact(rec.Frontmatter); err == nil {
		return a
	}
	s, ok := plainSkillOf(rec)
	if !ok {
		return nil
	}
	return &manifest.Artifact{Type: manifest.TypeSkill, Description: s.Description}
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
// under the ref's content hash. It binds no ref here: bindResourceRefs runs
// after the content-hash recompute, so every read is attempted before any ref
// is compared and a read failure is answered as unavailable whatever the order
// of the refs.
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
		bodies[ref.Path] = body
	}
	return bodies, nil
}

// bindResourceRefs binds each ref's stored content hash and size to its
// assembled body. The serve paths state both columns to a consumer, and the
// §4.7.6 digest covers neither, so this check is what binds them.
//
// Spec: §13.4 — stored-row admission, check (1).
func bindResourceRefs(refs []store.ResourceRef, bodies map[string][]byte) error {
	for _, ref := range refs {
		body := bodies[ref.Path]
		sum := sha256.Sum256(body)
		if "sha256:"+hex.EncodeToString(sum[:]) != ref.ContentHash || int64(len(body)) != ref.Size {
			return fmt.Errorf("%w: resource %s does not match its stored content hash or size", ErrContentHashMismatch, ref.Path)
		}
	}
	return nil
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

// admitSignature is check (2): under a configured signer, the stored signature
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
// admitted bytes. A plain-skill row also takes its description and tags from
// them, because parsedArtifact reads both columns for a row with no manifest,
// so an edited column never reaches the served result or the revalidation key
// set. Any other row whose manifest is empty or does not parse keeps its
// stored values, because its bytes state none.
//
// Spec: §13.4
func withAdmittedFields(rec store.ManifestRecord, a *manifest.Artifact) store.ManifestRecord {
	if a == nil {
		return rec
	}
	rec.Body = []byte(admittedBody(a, rec.SkillRaw))
	setManifestFields(&rec, a)
	if plainSkillRow(rec) {
		rec.Description = a.Description
		rec.Tags = nil
	}
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
// record.
func setManifestFields(rec *store.ManifestRecord, a *manifest.Artifact) {
	rec.Type = string(a.Type)
	rec.Sensitivity = string(a.Sensitivity)
	rec.Deprecated = a.Deprecated
	rec.ReplacedBy = a.ReplacedBy
	rec.AuditRedact = append([]string(nil), a.AuditRedact...)
}

// admissionSentinels are the errors admit reports to a caller, in the order
// sentinelOf tests them.
var admissionSentinels = []error{
	ErrContentHashMismatch,
	ErrStoredSignatureMissing,
	ErrStoredSignatureInvalid,
	ErrUnavailable,
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
// rec would record, for the read event of a revalidation answered without
// admission. It is the row's own directive: no key for a plain-skill row, as
// its admitted SKILL.md states none, and the stored column for a row whose
// manifest is empty or does not parse, the fallback admission applies. It
// reads no store and no object.
//
// Spec: §8.2, §13.4.
func (r *Registry) revalidationRedactKeys(rec store.ManifestRecord) []string {
	if plainSkillRow(rec) {
		return nil
	}
	return append([]string(nil), parsedArtifact(rec).AuditRedact...)
}
