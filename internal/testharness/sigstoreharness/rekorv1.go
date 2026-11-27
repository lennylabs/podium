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

// rekorConfig describes one transparency-log entry. The default is a
// Rekor v1 dsse entry under a Rekor v1 checkpoint.
type rekorConfig struct {
	integrated time.Time
	index      int64
	noSET      bool
	badSET     bool
	noProof    bool
	foreignKey bool
	// label, when set, replaces the entry's kindVersion, which sits
	// outside the signed body.
	label *kindVersionJSON
	// body selects the signed body, and checkpoint the form of the
	// checkpoint the inclusion proof leads to.
	body       entryBody
	checkpoint checkpointForm
	// origin, when set, is the origin line of a Rekor v2 checkpoint.
	origin string
	// v2Kind, when set, is the kind a Rekor v2 body names.
	v2Kind string
}

// entryBody is the signed body of a log entry.
type entryBody int

const (
	// bodyV1DSSE is a Rekor v1 dsse 0.0.1 body over the bundle.
	bodyV1DSSE entryBody = iota
	// bodyV2Bound is a Rekor v2 hashedrekord 0.0.2 body over the
	// bundle's PAE digest, signature, and leaf.
	bodyV2Bound
	// bodyV2Other is a Rekor v2 hashedrekord 0.0.2 body that records
	// another digest and another signature beside the bundle's leaf.
	bodyV2Other
)

// checkpointForm is the origin form of the checkpoint of a proof.
type checkpointForm int

const (
	// checkpointForBody uses the form that matches the body.
	checkpointForBody checkpointForm = iota
	// checkpointV1 is a Rekor v1 origin, "<host> - <tree ID>".
	checkpointV1
	// checkpointV2 is a Rekor v2 origin, the host of the log's baseUrl.
	checkpointV2
)

// WithKindVersionLabel sets the entry's kindVersion label, which is
// unsigned bundle metadata, without changing the body.
func WithKindVersionLabel(kind, version string) RekorOpt {
	return func(c *rekorConfig) { c.label = &kindVersionJSON{Kind: kind, Version: version} }
}

// WithRekorV2Body writes a Rekor v2 hashedrekord 0.0.2 body over the
// bundle's PAE digest, signature, and leaf, under a Rekor v2 checkpoint
// and with no signed entry timestamp, which Rekor v2 does not issue.
func WithRekorV2Body() RekorOpt {
	return func(c *rekorConfig) { c.body, c.noSET = bodyV2Bound, true }
}

// WithRekorV2BodyForOther writes a Rekor v2 hashedrekord 0.0.2 body that
// records another digest and another signature, with no signed entry
// timestamp. The trusted log key still proves it.
func WithRekorV2BodyForOther() RekorOpt {
	return func(c *rekorConfig) { c.body, c.noSET = bodyV2Other, true }
}

// WithV2BodyKind sets the kind member of a Rekor v2 body, which
// sigstore-go does not read when it parses the body.
func WithV2BodyKind(kind string) RekorOpt { return func(c *rekorConfig) { c.v2Kind = kind } }

// WithV1Checkpoint proves the entry under a Rekor v1 checkpoint whatever
// its body.
func WithV1Checkpoint() RekorOpt { return func(c *rekorConfig) { c.checkpoint = checkpointV1 } }

// WithV2Checkpoint proves the entry under a Rekor v2 checkpoint whatever
// its body.
func WithV2Checkpoint() RekorOpt { return func(c *rekorConfig) { c.checkpoint = checkpointV2 } }

// WithCheckpointOrigin sets the origin line of a Rekor v2 checkpoint.
func WithCheckpointOrigin(origin string) RekorOpt { return func(c *rekorConfig) { c.origin = origin } }

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

// rekorV1Entry builds the log entry cfg describes for env, by default a
// dsse v0.0.1 entry, proves it at the entry's index of a tree twice that
// size, and signs the checkpoint and the signed entry timestamp with the
// P-256 log key.
//
// Spec: §4.7.11.
func (h *Harness) rekorV1Entry(cfg rekorConfig, env dsseJSON, leafDER []byte) (tlogEntryJSON, error) {
	body, label, err := entryBodyFor(cfg, env, leafDER)
	if err != nil {
		return tlogEntryJSON{}, err
	}
	if cfg.label != nil {
		label = *cfg.label
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
		KindVersion:       label,
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
		if entry.InclusionProof, err = proofFor(cfg, body, key); err != nil {
			return tlogEntryJSON{}, err
		}
	}
	return entry, nil
}

// entryBodyFor encodes the body kind for env and returns its label.
func entryBodyFor(cfg rekorConfig, env dsseJSON, leafDER []byte) ([]byte, kindVersionJSON, error) {
	kind := cfg.body
	if kind == bodyV1DSSE {
		body, err := dsseEntryBody(env, leafDER)
		return body, kindVersionJSON{Kind: "dsse", Version: "0.0.1"}, err
	}
	digest := sha256.Sum256(PAE(env.PayloadType, env.Payload))
	fields := entryFields{
		kind: defaultEntryKind, apiVersion: defaultEntryAPIVersion,
		digest: digest[:], sig: env.Signatures[0].Sig, certDER: leafDER, keyDetails: keyDetailsP256,
	}
	if kind == bodyV2Other {
		other := sha256.Sum256([]byte("acme other statement"))
		fields.digest, fields.sig = other[:], []byte("acme other signature")
	}
	if cfg.v2Kind != "" {
		fields.kind = cfg.v2Kind
	}
	body, err := hashedRekordBody(fields)
	return body, kindVersionJSON{Kind: fields.kind, Version: defaultEntryAPIVersion}, err
}

// proofFor proves body under the checkpoint form cfg selects: a Rekor v1
// checkpoint for a v1 body and a Rekor v2 checkpoint, whose origin is the
// host of the log's baseUrl, for a v2 body, unless cfg forces a form.
func proofFor(cfg rekorConfig, body []byte, key *ecdsa.PrivateKey) (*proofJSON, error) {
	form := cfg.checkpoint
	if form == checkpointForBody {
		form = checkpointV1
		if cfg.body != bodyV1DSSE {
			form = checkpointV2
		}
	}
	if form == checkpointV1 {
		return rekorV1Proof(body, cfg.index, key)
	}
	origin := checkpointOrigin
	if cfg.origin != "" {
		origin = cfg.origin
	}
	size := 2*cfg.index + 2
	inc, err := prove(body, int(size), int(cfg.index))
	if err != nil {
		return nil, err
	}
	note := fmt.Sprintf("%s\n%d\n%s\n", origin, size, base64.StdEncoding.EncodeToString(inc.root))
	checkpoint, err := signNoteAs(note, origin, key)
	if err != nil {
		return nil, err
	}
	return &proofJSON{
		LogIndex: cfg.index, RootHash: inc.root, TreeSize: size, Hashes: inc.hashes,
		Checkpoint: checkpointJSON{Envelope: checkpoint},
	}, nil
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
