package sigstoreharness

import (
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
)

// envelopeJSON is the §4.7.9 keyless envelope. The types are the
// harness's own, independent of pkg/sign, so a drift between the
// generator and sign.SigstoreKeyless shows up as a test failure.
type envelopeJSON struct {
	Cert      string    `json:"cert"`
	Signature string    `json:"signature"`
	TLog      *tlogJSON `json:"tlog,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

// tlogJSON is the Rekor v2 entry and inclusion proof an envelope carries.
type tlogJSON struct {
	LogIndex   int64    `json:"log_index"`
	Body       string   `json:"body"`
	Hashes     []string `json:"hashes,omitempty"`
	Checkpoint string   `json:"checkpoint,omitempty"`
}

// envConfig collects the EnvOpt adjustments of one envelope.
type envConfig struct {
	leaf          leafConfig
	ed25519Leaf   bool
	foreignSig    bool
	leafByTSARoot bool
	noTLog        bool
	noTimestamp   bool
	entryDigest   string
	entrySig      []byte
	entryCert     []byte
	kind          string
	apiVersion    string
	ts            []func(*Harness, *tsConfig) error
	proof         proofConfig
}

// proofConfig describes the inclusion proof and checkpoint of an envelope.
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
		proof:      proofConfig{size: 7, index: 5},
	}
}

// Envelope returns a keyless envelope over contentHash ("alg:hex"). It
// issues a leaf under the Fulcio intermediate, signs the digest, binds the
// leaf and signature in a hashedrekord v0.0.2 body, proves the body at
// index 5 of a 7-leaf RFC 6962 tree under a checkpoint signed by the
// P-256 log key, and timestamps SHA-256 of the signature at the clock.
//
// Spec: §4.7.9.
func (h *Harness) Envelope(t testing.TB, contentHash string, opts ...EnvOpt) string {
	t.Helper()
	cfg := defaultEnvConfig()
	for _, o := range opts {
		o(&cfg)
	}
	out, err := h.buildEnvelope(contentHash, cfg)
	must(t, err)
	return out
}

// buildEnvelope assembles the envelope JSON for cfg.
func (h *Harness) buildEnvelope(contentHash string, cfg envConfig) (string, error) {
	digest, err := decodeContentHash(contentHash)
	if err != nil {
		return "", err
	}
	leaf, err := h.envelopeLeaf(cfg, digest)
	if err != nil {
		return "", err
	}
	env := envelopeJSON{
		Cert:      pemChain(append([]*x509.Certificate{leaf.cert}, leaf.intermediates...)),
		Signature: base64.StdEncoding.EncodeToString(leaf.sig),
	}
	if !cfg.noTLog {
		if env.TLog, err = h.envelopeTLog(cfg, digest, leaf); err != nil {
			return "", err
		}
	}
	if !cfg.noTimestamp {
		token, err := h.envelopeTimestamp(cfg, leaf.sig)
		if err != nil {
			return "", err
		}
		env.Timestamp = base64.StdEncoding.EncodeToString(token)
	}
	out, err := json.Marshal(env)
	return string(out), err
}

// envelopeLeaf issues the leaf and its signature for cfg.
func (h *Harness) envelopeLeaf(cfg envConfig, digest []byte) (signedLeaf, error) {
	issuer, intermediates := h.fulcioInter, []*x509.Certificate{h.fulcioInter.cert}
	if cfg.leafByTSARoot {
		issuer, intermediates = h.tsaRoot, nil
	}
	msg := digest
	if cfg.foreignSig {
		foreign := sha256.Sum256([]byte("acme foreign content"))
		msg = foreign[:]
	}
	leaf, err := h.issueSignedLeaf(cfg.leaf, issuer, cfg.ed25519Leaf, msg)
	leaf.intermediates = intermediates
	return leaf, err
}

// envelopeTLog builds the entry body, proves it, and signs the checkpoint.
func (h *Harness) envelopeTLog(cfg envConfig, digest []byte, leaf signedLeaf) (*tlogJSON, error) {
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
	body, err := hashedRekordBody(fields)
	if err != nil {
		return nil, err
	}
	return h.proofFor(cfg.proof, body)
}

// proofFor proves body as cfg describes and returns the envelope tlog.
func (h *Harness) proofFor(p proofConfig, body []byte) (*tlogJSON, error) {
	key, err := h.checkpointKey(p)
	if err != nil {
		return nil, err
	}
	inc, err := signedInclusion(body, p.size, p.index, key)
	if err != nil {
		return nil, err
	}
	out := &tlogJSON{
		LogIndex:   int64(p.index),
		Body:       base64.StdEncoding.EncodeToString(body),
		Hashes:     encodeHashes(inc.hashes),
		Checkpoint: inc.checkpoint,
	}
	if p.logIndex != nil {
		out.LogIndex = *p.logIndex
	}
	if p.flip && len(inc.hashes) > 0 {
		flipped := append([]byte(nil), inc.hashes[0]...)
		flipped[0] ^= 0x01
		out.Hashes[0] = base64.StdEncoding.EncodeToString(flipped)
	}
	if p.malformed {
		out.Checkpoint = "acme checkpoint without a tree size\n"
	}
	if p.omit {
		out.Hashes, out.Checkpoint = nil, ""
	}
	return out, nil
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

// envelopeTimestamp issues the RFC 3161 token over SHA-256(sig).
func (h *Harness) envelopeTimestamp(cfg envConfig, sig []byte) ([]byte, error) {
	imprint := sha256.Sum256(sig)
	ts := h.defaultTSConfig(imprint[:])
	for _, mod := range cfg.ts {
		if err := mod(h, &ts); err != nil {
			return nil, err
		}
	}
	return buildToken(ts)
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

// encodeHashes base64-encodes an audit path.
func encodeHashes(hashes [][]byte) []string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		out[i] = base64.StdEncoding.EncodeToString(h)
	}
	return out
}

// pemChain concatenates certificates as PEM, leaf first.
func pemChain(chain []*x509.Certificate) string {
	var out []byte
	for _, c := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return string(out)
}
