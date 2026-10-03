package sigstoreharness

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"time"
)

// EnvOpt adjusts one envelope built by Envelope. Unless an option says
// otherwise, the leaf and signature it produces are bound in the
// hashedrekord body, proven in the log, and timestamped as usual, so only
// the condition the option targets fails.
type EnvOpt func(*envConfig)

// WithSAN replaces the leaf's email subject alternative name.
func WithSAN(email string) EnvOpt { return func(c *envConfig) { c.leaf.email = email } }

// WithURISAN adds a URI subject alternative name beside the email SAN.
func WithURISAN(uri string) EnvOpt {
	return func(c *envConfig) { c.leaf.uris = append(c.leaf.uris, uri) }
}

// WithOtherNameSANOnly issues a leaf whose only SAN is a Fulcio username
// otherName, so it carries no email or URI SAN.
func WithOtherNameSANOnly() EnvOpt { return func(c *envConfig) { c.leaf.otherNameOnly = true } }

// WithIssuerExt sets the leaf extension oid to raw, replacing the default
// 1.3.6.1.4.1.57264.1.8 issuer when oid names it. IssuerValue encodes a
// well-formed .1.8 value.
func WithIssuerExt(oid asn1.ObjectIdentifier, raw []byte) EnvOpt {
	return func(c *envConfig) { c.leaf.setIssuerExt(oid, raw) }
}

// WithoutIssuer issues a leaf with neither Fulcio issuer extension.
func WithoutIssuer() EnvOpt { return func(c *envConfig) { c.leaf.issuerExts = nil } }

// WithoutCodeSigning issues the leaf with the server-authentication
// extended key usage only.
func WithoutCodeSigning() EnvOpt {
	return func(c *envConfig) { c.leaf.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} }
}

// WithLeafWithoutEKU issues the leaf with no extended-key-usage extension.
func WithLeafWithoutEKU() EnvOpt { return func(c *envConfig) { c.leaf.eku = nil } }

// WithLeafAnyUsage issues the leaf with the any-purpose usage as its only
// extended key usage.
func WithLeafAnyUsage() EnvOpt {
	return func(c *envConfig) { c.leaf.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageAny} }
}

// WithEd25519Leaf issues the leaf over an Ed25519 key and signs the digest
// with ed25519.Sign.
func WithEd25519Leaf() EnvOpt { return func(c *envConfig) { c.ed25519Leaf = true } }

// WithForeignSignature signs the digest of different content with the
// leaf key and writes those signature bytes into the envelope, the entry
// body, and the timestamp imprint, while the body's digest stays the
// verified content hash.
func WithForeignSignature() EnvOpt { return func(c *envConfig) { c.foreignSig = true } }

// WithLeafIssuedByTSARoot issues the code-signing leaf from the
// timestamp-authority root. The envelope cert carries the leaf alone.
func WithLeafIssuedByTSARoot() EnvOpt { return func(c *envConfig) { c.leafByTSARoot = true } }

// WithoutTLog omits the envelope's tlog object.
func WithoutTLog() EnvOpt { return func(c *envConfig) { c.noTLog = true } }

// WithEntryDigest binds the digest of contentHash ("alg:hex") in the entry
// body in place of the envelope's digest.
func WithEntryDigest(contentHash string) EnvOpt {
	return func(c *envConfig) { c.entryDigest = contentHash }
}

// WithEntrySignature binds sig in the entry body in place of the
// envelope's signature.
func WithEntrySignature(sig []byte) EnvOpt { return func(c *envConfig) { c.entrySig = sig } }

// WithEntryCert binds der in the entry body in place of the leaf.
func WithEntryCert(der []byte) EnvOpt { return func(c *envConfig) { c.entryCert = der } }

// WithEntryKind sets the entry body's kind and apiVersion.
func WithEntryKind(kind, apiVersion string) EnvOpt {
	return func(c *envConfig) { c.kind, c.apiVersion = kind, apiVersion }
}

// WithoutInclusionProof omits the tlog hashes and checkpoint.
func WithoutInclusionProof() EnvOpt { return func(c *envConfig) { c.proof.omit = true } }

// WithFlippedProofHash flips one bit of the first audit-path hash.
func WithFlippedProofHash() EnvOpt { return func(c *envConfig) { c.proof.flip = true } }

// WithLogIndex writes i as the envelope's log_index while the proof stays
// for the entry's real position.
func WithLogIndex(i int64) EnvOpt { return func(c *envConfig) { c.proof.logIndex = &i } }

// WithTreeSize places the entry at index i of an n-leaf tree and writes i
// as the log_index.
func WithTreeSize(n, i int) EnvOpt {
	return func(c *envConfig) { c.proof.size, c.proof.index = n, i }
}

// WithMalformedCheckpoint replaces the checkpoint with text that is not a
// signed note.
func WithMalformedCheckpoint() EnvOpt { return func(c *envConfig) { c.proof.malformed = true } }

// WithCheckpointSignedByForeignKey signs the checkpoint with a P-256 key
// no trusted root lists.
func WithCheckpointSignedByForeignKey() EnvOpt {
	return func(c *envConfig) { c.proof.foreignKey = true }
}

// WithEd25519Checkpoint signs the checkpoint with the Ed25519 log key,
// which a trusted root lists only under WithEd25519LogKey.
func WithEd25519Checkpoint() EnvOpt { return func(c *envConfig) { c.proof.ed25519 = true } }

// WithoutTimestamp omits the envelope's timestamp.
func WithoutTimestamp() EnvOpt { return func(c *envConfig) { c.noTimestamp = true } }

// WithTimestampTime stamps the token at t in place of the clock.
func WithTimestampTime(t time.Time) EnvOpt {
	return tsMod(func(_ *Harness, ts *tsConfig) error { ts.genTime = t; return nil })
}

// WithTimestampOver stamps SHA-256(sig) in place of SHA-256 of the
// envelope's signature.
func WithTimestampOver(sig []byte) EnvOpt {
	return tsMod(func(_ *Harness, ts *tsConfig) error {
		imprint := sha256.Sum256(sig)
		ts.imprint = imprint[:]
		return nil
	})
}

// WithFlippedTimestampSignature flips one bit of the token's signature.
func WithFlippedTimestampSignature() EnvOpt {
	return tsMod(func(_ *Harness, ts *tsConfig) error { ts.flipSignature = true; return nil })
}

// WithUntrustedTSA signs the token under a timestamp authority no trusted
// root lists, and embeds its signer.
func WithUntrustedTSA() EnvOpt {
	return tsMod(func(h *Harness, ts *tsConfig) error {
		_, leaf, err := newTimestampAuthority("untrusted-tsa", h.clock)
		ts.signer, ts.embed = leaf, []*x509.Certificate{leaf.cert}
		return err
	})
}

// WithTimestampSignedByFulcioCA signs the token with a certificate the
// Fulcio root issued with the critical time-stamping usage, embedded
// alone. The root rather than the intermediate issues it, because the
// intermediate's code-signing usage would refuse the time-stamping chain
// under any trust pool and the case would not distinguish the pools.
func WithTimestampSignedByFulcioCA() EnvOpt {
	return timestampSignerFrom(func(h *Harness) authority { return h.fulcioRoot }, tsaUsageCriticalTimeStamping)
}

// WithTSALeafWithoutTimeStamping signs the token with a second signer from
// the timestamp-authority root that carries no extended-key-usage
// extension, embedded alone.
func WithTSALeafWithoutTimeStamping() EnvOpt {
	return timestampSignerFrom(func(h *Harness) authority { return h.tsaRoot }, tsaUsageNone)
}

// WithTSALeafAnyUsage signs the token with a second signer from the
// timestamp-authority root whose only extended key usage is any-purpose.
func WithTSALeafAnyUsage() EnvOpt {
	return timestampSignerFrom(func(h *Harness) authority { return h.tsaRoot }, tsaUsageAny)
}

// timestampSignerFrom signs the token with a fresh signer that issuer
// issues with usage, and embeds that signer alone.
func timestampSignerFrom(issuer func(*Harness) authority, usage tsaUsage) EnvOpt {
	return tsMod(func(h *Harness, ts *tsConfig) error {
		signer, err := issuer(h).timestampSigner("acme-alternate-tsa-signer", h.clock, usage)
		ts.signer, ts.embed = signer, []*x509.Certificate{signer.cert}
		return err
	})
}

// tsMod wraps a token adjustment as an EnvOpt.
func tsMod(mod func(*Harness, *tsConfig) error) EnvOpt {
	return func(c *envConfig) { c.ts = append(c.ts, mod) }
}
