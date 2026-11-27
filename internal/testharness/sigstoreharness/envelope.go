package sigstoreharness

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The builders below return the errors of key generation, certificate and
// token encoding, and JSON marshalling, which depend on crypto/rand and the
// encoders rather than on the configuration, so no test input reaches
// those returns; KeylessBundle reports them through must. The errors a
// configuration causes are covered by TestKeylessBundle_ReportsSetupFailures
// and TestEnvelope_RejectsMalformedContentHash.

// envConfig collects the EnvOpt adjustments of one keyless bundle.
type envConfig struct {
	leaf          leafConfig
	ed25519Leaf   bool
	p384Leaf      bool
	foreignSig    bool
	leafByTSARoot bool
	sct           sctMode
	noTLog        bool
	noTimestamp   bool
	extraTS       []TSA
	entryDigest   string
	entrySig      []byte
	entryCert     []byte
	kind          string
	apiVersion    string
	foreignLogID  bool
	v1Body        bool
	v1Checkpoint  bool
	label         *kindVersionJSON
	mediaType     string
	digestAlg     string
	ts            []func(*Harness, *tsConfig) error
	proof         proofConfig
}

// proofConfig describes the inclusion proof and checkpoint of a bundle.
type proofConfig struct {
	size, index int
	logIndex    *int64
	omit        bool
	flip        bool
	malformed   bool
	foreignKey  bool
	ed25519     bool
}

// defaultEnvConfig places a default leaf's entry at index 5 of 7.
func defaultEnvConfig() envConfig {
	return envConfig{
		leaf:       defaultLeafConfig(),
		kind:       defaultEntryKind,
		apiVersion: defaultEntryAPIVersion,
		mediaType:  BundleMediaTypeV03,
		digestAlg:  "SHA2_256",
		proof:      proofConfig{size: 7, index: 5},
	}
}

// keylessParts are the values a keyless bundle carries, kept for the
// legacy envelope that WriteFiles derives from a bundle.
type keylessParts struct {
	leaf  signedLeaf
	entry *tlogEntryJSON
	tsr   []byte
}

// KeylessBundle returns a §4.7.9 keyless bundle over contentHash
// ("alg:hex"): a version 0.3 Sigstore bundle with a message signature. It
// issues a leaf under the Fulcio intermediate, signs the digest, binds the
// leaf and signature in a hashedrekord v0.0.2 body, proves the body at
// index 5 of a 7-leaf RFC 6962 tree under a checkpoint signed by the P-256
// log key, and timestamps SHA-256 of the signature at the clock. The
// bundle carries the leaf alone; the trusted root lists the intermediate.
//
// Spec: §4.7.9.
func (h *Harness) KeylessBundle(t testing.TB, contentHash string, opts ...EnvOpt) string {
	t.Helper()
	cfg := defaultEnvConfig()
	for _, o := range opts {
		o(&cfg)
	}
	out, _, err := h.buildKeyless(contentHash, cfg)
	must(t, err)
	return string(out)
}

// buildKeyless assembles the bundle JSON for cfg.
func (h *Harness) buildKeyless(contentHash string, cfg envConfig) ([]byte, keylessParts, error) {
	digest, err := decodeContentHash(contentHash)
	if err != nil {
		return nil, keylessParts{}, err
	}
	var parts keylessParts
	if parts.leaf, err = h.keylessLeaf(cfg, digest); err != nil {
		return nil, parts, err
	}
	b := bundleJSON{MediaType: cfg.mediaType}
	b.VerificationMaterial.Certificate = &rawCertDoc{RawBytes: parts.leaf.cert.Raw}
	if !cfg.noTLog {
		if parts.entry, err = h.keylessEntry(cfg, digest, parts.leaf); err != nil {
			return nil, parts, err
		}
		b.VerificationMaterial.TLogEntries = []tlogEntryJSON{*parts.entry}
	}
	if parts.tsr, err = h.keylessTimestamps(&b, cfg, parts.leaf.sig); err != nil {
		return nil, parts, err
	}
	b.MessageSignature = &messageSignatureJSON{Signature: parts.leaf.sig}
	b.MessageSignature.MessageDigest.Algorithm = cfg.digestAlg
	b.MessageSignature.MessageDigest.Digest = digest
	out, err := marshalBundle(b, false, "")
	return out, parts, err
}

// keylessLeaf issues the leaf and its signature over the digest, or over
// other content under WithForeignSignature. A P-256 leaf from the Fulcio
// intermediate can carry an SCT; the other leaf kinds carry none.
func (h *Harness) keylessLeaf(cfg envConfig, digest []byte) (signedLeaf, error) {
	msg := digest
	if cfg.foreignSig {
		foreign := sha256.Sum256([]byte("acme foreign content"))
		msg = foreign[:]
	}
	switch {
	case cfg.leafByTSARoot:
		return h.issueSignedLeaf(cfg.leaf, h.tsaRoot, cfg.ed25519Leaf, msg)
	case cfg.ed25519Leaf:
		return h.issueSignedLeaf(cfg.leaf, h.fulcioInter, true, msg)
	case cfg.p384Leaf:
		return h.p384Leaf(cfg.leaf, msg)
	}
	key, err := newECKey()
	if err != nil {
		return signedLeaf{}, err
	}
	der, err := h.issueLeafDER(cfg.leaf, key, cfg.sct)
	if err != nil {
		return signedLeaf{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return signedLeaf{}, fmt.Errorf("parse leaf: %w", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, key, msg)
	if err != nil {
		return signedLeaf{}, fmt.Errorf("sign digest: %w", err)
	}
	return signedLeaf{cert: cert, sig: sig, keyDetails: keyDetailsP256}, nil
}

// p384Leaf issues a leaf over an ECDSA P-384 key under the Fulcio
// intermediate and signs msg with it.
func (h *Harness) p384Leaf(cfg leafConfig, msg []byte) (signedLeaf, error) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return signedLeaf{}, err
	}
	spec, err := cfg.spec(h.clock)
	if err != nil {
		return signedLeaf{}, err
	}
	cert, err := h.fulcioInter.issue(spec, key.Public())
	if err != nil {
		return signedLeaf{}, err
	}
	sig, err := ecdsa.SignASN1(rand.Reader, key, msg)
	if err != nil {
		return signedLeaf{}, fmt.Errorf("sign digest: %w", err)
	}
	return signedLeaf{cert: cert, sig: sig, keyDetails: "PKIX_ECDSA_P384_SHA_384"}, nil
}

// keylessEntry builds the Rekor v2 entry: the hashedrekord body, its
// inclusion proof, and the signed checkpoint, under the log ID of the key
// that signs the checkpoint by default.
func (h *Harness) keylessEntry(cfg envConfig, digest []byte, leaf signedLeaf) (*tlogEntryJSON, error) {
	fields := entryFields{
		kind: cfg.kind, apiVersion: cfg.apiVersion,
		digest: digest, sig: leaf.sig, certDER: leaf.cert.Raw, keyDetails: leaf.keyDetails,
	}
	if cfg.entryDigest != "" {
		d, err := decodeContentHash(cfg.entryDigest)
		if err != nil {
			return nil, err
		}
		fields.digest = d
	}
	if cfg.entrySig != nil {
		fields.sig = cfg.entrySig
	}
	if cfg.entryCert != nil {
		fields.certDER = cfg.entryCert
	}
	encode := hashedRekordBody
	if cfg.v1Body {
		encode = hashedRekordV1Body
	}
	body, err := encode(fields)
	if err != nil {
		return nil, err
	}
	return h.proofFor(cfg, body)
}

// hashedRekordV1Entry is the canonicalized hashedrekord 0.0.1 body Rekor
// v1 stores, with its members in lexical order.
type hashedRekordV1Entry struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Data struct {
			Hash struct {
				Algorithm string `json:"algorithm"`
				Value     string `json:"value"`
			} `json:"hash"`
		} `json:"data"`
		Signature struct {
			Content   []byte `json:"content"`
			PublicKey struct {
				Content []byte `json:"content"`
			} `json:"publicKey"`
		} `json:"signature"`
	} `json:"spec"`
}

// hashedRekordV1Body encodes a Rekor v1 hashedrekord 0.0.1 body that
// binds the digest, the signature, and the PEM leaf.
func hashedRekordV1Body(f entryFields) ([]byte, error) {
	var e hashedRekordV1Entry
	e.APIVersion, e.Kind = "0.0.1", "hashedrekord"
	e.Spec.Data.Hash.Algorithm = "sha256"
	e.Spec.Data.Hash.Value = hex.EncodeToString(f.digest)
	e.Spec.Signature.Content = f.sig
	e.Spec.Signature.PublicKey.Content = []byte(pemCert(&x509.Certificate{Raw: f.certDER}))
	return json.Marshal(e)
}

// proofFor proves body as cfg describes and returns the bundle entry.
func (h *Harness) proofFor(cfg envConfig, body []byte) (*tlogEntryJSON, error) {
	p := cfg.proof
	key, err := h.checkpointKey(p)
	if err != nil {
		return nil, err
	}
	inc, err := signedInclusion(body, p.size, p.index, key)
	if err != nil {
		return nil, err
	}
	logKey := crypto.PublicKey(h.logKey.Public())
	if p.ed25519 {
		logKey = h.edLogKey.Public()
	}
	if cfg.foreignLogID {
		other, err := newECKey()
		if err != nil {
			return nil, err
		}
		logKey = other.Public()
	}
	logID, err := LogID(logKey)
	if err != nil {
		return nil, err
	}
	index := int64(p.index)
	if p.logIndex != nil {
		index = *p.logIndex
	}
	out := &tlogEntryJSON{
		LogIndex:          index,
		LogID:             logIDDoc{KeyID: logID},
		KindVersion:       kindVersionJSON{Kind: cfg.kind, Version: cfg.apiVersion},
		CanonicalizedBody: body,
		InclusionProof: &proofJSON{
			LogIndex: index, RootHash: inc.root, TreeSize: int64(p.size),
			Hashes: inc.hashes, Checkpoint: checkpointJSON{Envelope: inc.checkpoint},
		},
	}
	if p.flip && len(inc.hashes) > 0 {
		flipped := append([]byte(nil), inc.hashes[0]...)
		flipped[0] ^= 0x01
		out.InclusionProof.Hashes = append([][]byte{flipped}, inc.hashes[1:]...)
	}
	if p.malformed {
		out.InclusionProof.Checkpoint.Envelope = "acme checkpoint without a tree size\n"
	}
	if cfg.v1Checkpoint {
		if out.InclusionProof, err = rekorV1Proof(body, index, h.logKey); err != nil {
			return nil, err
		}
	}
	if cfg.label != nil {
		out.KindVersion = *cfg.label
	}
	if p.omit {
		out.InclusionProof = nil
	}
	return out, nil
}

// LogID returns the Sigstore log ID of a log key: SHA-256 of its PKIX
// encoding, which is the logId.keyId a trusted root lists for the key.
func LogID(pub crypto.PublicKey) ([]byte, error) {
	raw, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("log key: %w", err)
	}
	id := sha256.Sum256(raw)
	return id[:], nil
}

// checkpointKey returns the key that signs the checkpoint.
func (h *Harness) checkpointKey(p proofConfig) (crypto.Signer, error) {
	switch {
	case p.foreignKey:
		return newECKey()
	case p.ed25519:
		return h.edLogKey, nil
	default:
		return h.logKey, nil
	}
}

// keylessTimestamps adds the RFC 3161 responses of the bundle: the
// default token over SHA-256(sig), adjusted by the timestamp options,
// then one token from each authority WithExtraTimestamp names. It returns
// the first response, the one a legacy envelope carries.
func (h *Harness) keylessTimestamps(b *bundleJSON, cfg envConfig, sig []byte) ([]byte, error) {
	if cfg.noTimestamp {
		return nil, nil
	}
	imprint := sha256.Sum256(sig)
	ts := h.defaultTSConfig(imprint[:])
	for _, mod := range cfg.ts {
		if err := mod(h, &ts); err != nil {
			return nil, err
		}
	}
	token, err := buildToken(ts)
	if err != nil {
		return nil, err
	}
	first, err := timeStampResp(0, token)
	if err != nil {
		return nil, err
	}
	data := &tsDataJSON{RFC3161Timestamps: []signedTimestampJSON{{SignedTimestamp: first}}}
	b.VerificationMaterial.TimestampVerificationData = data
	if len(cfg.extraTS) == 0 {
		return first, nil
	}
	extra := bundleJSON{}
	specs := make([]timestampSpec, len(cfg.extraTS))
	for i, a := range cfg.extraTS {
		specs[i] = timestampSpec{authority: a}
	}
	if err := h.addTimestamps(&extra, specs, sig); err != nil {
		return nil, err
	}
	data.RFC3161Timestamps = append(data.RFC3161Timestamps, extra.VerificationMaterial.TimestampVerificationData.RFC3161Timestamps...)
	return first, nil
}

// decodeContentHash returns the digest bytes of an "alg:hex" content hash.
func decodeContentHash(contentHash string) ([]byte, error) {
	_, hexStr, ok := strings.Cut(contentHash, ":")
	if !ok {
		return nil, fmt.Errorf("content hash %q must be alg:hex", contentHash)
	}
	digest, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("content hash %q: %w", contentHash, err)
	}
	return digest, nil
}
