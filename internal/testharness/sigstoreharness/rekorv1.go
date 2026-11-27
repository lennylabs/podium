package sigstoreharness

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"
)

// rekorV1Origin is the checkpoint origin of a Rekor v1 shard: the host and
// the decimal tree ID. sigstore-go reads a checkpoint whose origin ends in
// " - <digits>" as a Rekor v1 signed tree head.
const (
	rekorV1Host   = "rekor.acme.test"
	rekorV1Origin = rekorV1Host + " - 1"
)

// dsseEntry is the canonicalized dsse v0.0.1 entry body Rekor v1 stores.
// The members are declared in lexical order, so encoding/json writes the
// sorted-key form.
type dsseEntry struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Spec       dsseEntrySpec `json:"spec"`
}

type dsseEntrySpec struct {
	EnvelopeHash hashDoc         `json:"envelopeHash"`
	PayloadHash  hashDoc         `json:"payloadHash"`
	Signatures   []dsseEntrySign `json:"signatures"`
}

type hashDoc struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type dsseEntrySign struct {
	Signature string `json:"signature"`
	// Verifier is the PEM leaf; encoding/json writes []byte as base64,
	// which is the strfmt.Base64 form Rekor stores.
	Verifier []byte `json:"verifier"`
}

// setPayload is the Rekor v1 signed entry timestamp payload, with its
// members in the RFC 8785 order.
type setPayload struct {
	Body           string `json:"body"`
	IntegratedTime int64  `json:"integratedTime"`
	LogID          string `json:"logID"`
	LogIndex       int64  `json:"logIndex"`
}

// RekorOpt adjusts the Rekor v1 entry of one bundle.
type RekorOpt func(*rekorConfig)

// rekorConfig describes one Rekor v1 entry.
type rekorConfig struct {
	integrated time.Time
	index      int64
	noSET      bool
	badSET     bool
	noProof    bool
	foreignKey bool
}

// WithoutSET omits the entry's inclusion promise, so its integrated time
// is not signed.
func WithoutSET() RekorOpt { return func(c *rekorConfig) { c.noSET = true } }

// WithBadSET writes an inclusion promise that does not verify.
func WithBadSET() RekorOpt { return func(c *rekorConfig) { c.badSET = true } }

// WithoutRekorProof omits the entry's inclusion proof and checkpoint.
func WithoutRekorProof() RekorOpt { return func(c *rekorConfig) { c.noProof = true } }

// WithIntegratedTime sets the entry's integrated time.
func WithIntegratedTime(t time.Time) RekorOpt { return func(c *rekorConfig) { c.integrated = t } }

// WithRekorIndex sets the entry's log index, so two entries of one bundle
// differ.
func WithRekorIndex(i int64) RekorOpt { return func(c *rekorConfig) { c.index = i } }

// WithForeignLogKey signs the entry's promise and checkpoint with a key no
// trusted root lists.
func WithForeignLogKey() RekorOpt { return func(c *rekorConfig) { c.foreignKey = true } }

// rekorV1Entry builds the dsse v0.0.1 entry for env, proves it at the
// entry's index of a tree twice that size, and signs the checkpoint and
// the signed entry timestamp with the P-256 log key.
//
// Spec: §4.7.11.
func (h *Harness) rekorV1Entry(cfg rekorConfig, env dsseJSON, leafDER []byte) (tlogEntryJSON, error) {
	body, err := dsseEntryBody(env, leafDER)
	if err != nil {
		return tlogEntryJSON{}, err
	}
	key := h.logKey
	if cfg.foreignKey {
		if key, err = newECKey(); err != nil {
			return tlogEntryJSON{}, err
		}
	}
	// The log ID names the trusted key even for a foreign signer, so the
	// verifier finds the trusted key and the signatures fail under it.
	keyID, err := ctLogID(&h.logKey.PublicKey)
	if err != nil {
		return tlogEntryJSON{}, err
	}
	entry := tlogEntryJSON{
		LogIndex:          cfg.index,
		LogID:             logIDDoc{KeyID: keyID},
		KindVersion:       kindVersionJSON{Kind: "dsse", Version: "0.0.1"},
		IntegratedTime:    cfg.integrated.Unix(),
		CanonicalizedBody: body,
	}
	if !cfg.noSET {
		set, err := signSET(setPayload{
			Body: base64.StdEncoding.EncodeToString(body), IntegratedTime: entry.IntegratedTime,
			LogID: hex.EncodeToString(keyID), LogIndex: cfg.index,
		}, key)
		if err != nil {
			return tlogEntryJSON{}, err
		}
		if cfg.badSET {
			set[len(set)-1] ^= 0x01
		}
		entry.InclusionPromise = &promiseJSON{SignedEntryTimestamp: set}
	}
	if !cfg.noProof {
		if entry.InclusionProof, err = rekorV1Proof(body, cfg.index, key); err != nil {
			return tlogEntryJSON{}, err
		}
	}
	return entry, nil
}

// dsseEntryBody encodes the canonicalized dsse v0.0.1 body of env.
func dsseEntryBody(env dsseJSON, leafDER []byte) ([]byte, error) {
	envJSON, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	envHash := sha256.Sum256(envJSON)
	payloadHash := sha256.Sum256(env.Payload)
	var sigs []dsseEntrySign
	for _, s := range env.Signatures {
		sigs = append(sigs, dsseEntrySign{
			Signature: base64.StdEncoding.EncodeToString(s.Sig),
			Verifier:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		})
	}
	return json.Marshal(dsseEntry{
		APIVersion: "0.0.1",
		Kind:       "dsse",
		Spec: dsseEntrySpec{
			EnvelopeHash: hashDoc{Algorithm: "sha256", Value: hex.EncodeToString(envHash[:])},
			PayloadHash:  hashDoc{Algorithm: "sha256", Value: hex.EncodeToString(payloadHash[:])},
			Signatures:   sigs,
		},
	})
}

// signSET signs the canonical JSON of p, as Rekor v1 signs an entry.
func signSET(p setPayload, key *ecdsa.PrivateKey) ([]byte, error) {
	canonical, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return ecdsa.SignASN1(rand.Reader, key, digest[:])
}

// rekorV1Proof proves body at index of a tree of 2*index+2 leaves and
// signs a Rekor v1 checkpoint over the root.
func rekorV1Proof(body []byte, index int64, key *ecdsa.PrivateKey) (*proofJSON, error) {
	size := 2*index + 2
	inc, err := prove(body, int(size), int(index))
	if err != nil {
		return nil, err
	}
	note := fmt.Sprintf("%s\n%d\n%s\n", rekorV1Origin, size, base64.StdEncoding.EncodeToString(inc.root))
	checkpoint, err := signNoteAs(note, rekorV1Host, key)
	if err != nil {
		return nil, err
	}
	return &proofJSON{
		LogIndex:   index,
		RootHash:   inc.root,
		TreeSize:   size,
		Hashes:     inc.hashes,
		Checkpoint: checkpointJSON{Envelope: checkpoint},
	}, nil
}
