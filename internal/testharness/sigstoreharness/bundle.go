package sigstoreharness

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

// Bundle media types and the DSSE payload type of an attestation.
const (
	// BundleMediaTypeV03 is the version 0.3 bundle media type.
	BundleMediaTypeV03 = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// BundleMediaTypeV03Legacy is the older spelling of version 0.3.
	BundleMediaTypeV03Legacy = "application/vnd.dev.sigstore.bundle+json;version=0.3"
	// BundleMediaTypeV01 is the version 0.1 bundle media type.
	BundleMediaTypeV01 = "application/vnd.dev.sigstore.bundle+json;version=0.1"
	// InTotoPayloadType is the DSSE payload type of an in-toto statement.
	InTotoPayloadType = "application/vnd.in-toto+json"
)

// The bundle types below are Sigstore's bundle.proto with its protojson
// member names. They are the harness's own, independent of sigstore-go
// and pkg/sign, so a drift between the generator and the verifier shows
// up as a test failure. encoding/json writes []byte as standard base64
// and an int64 tagged ",string" as a decimal string, which is the
// protojson form of bytes and int64.
type bundleJSON struct {
	MediaType            string                   `json:"mediaType"`
	VerificationMaterial verificationMaterialJSON `json:"verificationMaterial"`
	DSSEEnvelope         *dsseJSON                `json:"dsseEnvelope,omitempty"`
	MessageSignature     *messageSignatureJSON    `json:"messageSignature,omitempty"`
}

type verificationMaterialJSON struct {
	Certificate               *rawCertDoc     `json:"certificate,omitempty"`
	TLogEntries               []tlogEntryJSON `json:"tlogEntries,omitempty"`
	TimestampVerificationData *tsDataJSON     `json:"timestampVerificationData,omitempty"`
}

type tsDataJSON struct {
	RFC3161Timestamps []signedTimestampJSON `json:"rfc3161Timestamps"`
}

type signedTimestampJSON struct {
	SignedTimestamp []byte `json:"signedTimestamp"`
}

type tlogEntryJSON struct {
	LogIndex          int64           `json:"logIndex,string"`
	LogID             logIDDoc        `json:"logId"`
	KindVersion       kindVersionJSON `json:"kindVersion"`
	IntegratedTime    int64           `json:"integratedTime,string"`
	InclusionPromise  *promiseJSON    `json:"inclusionPromise,omitempty"`
	InclusionProof    *proofJSON      `json:"inclusionProof,omitempty"`
	CanonicalizedBody []byte          `json:"canonicalizedBody"`
}

type kindVersionJSON struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

type promiseJSON struct {
	SignedEntryTimestamp []byte `json:"signedEntryTimestamp"`
}

type proofJSON struct {
	LogIndex   int64          `json:"logIndex,string"`
	RootHash   []byte         `json:"rootHash"`
	TreeSize   int64          `json:"treeSize,string"`
	Hashes     [][]byte       `json:"hashes"`
	Checkpoint checkpointJSON `json:"checkpoint"`
}

type checkpointJSON struct {
	Envelope string `json:"envelope"`
}

type dsseJSON struct {
	Payload     []byte          `json:"payload"`
	PayloadType string          `json:"payloadType"`
	Signatures  []dsseSignature `json:"signatures"`
}

type dsseSignature struct {
	Sig   []byte `json:"sig"`
	KeyID string `json:"keyid"`
}

type messageSignatureJSON struct {
	MessageDigest struct {
		Algorithm string `json:"algorithm"`
		Digest    []byte `json:"digest"`
	} `json:"messageDigest"`
	Signature []byte `json:"signature"`
}

// BundleOpt adjusts one bundle built by DSSEBundle.
type BundleOpt func(*bundleConfig)

// bundleConfig describes one attestation bundle.
type bundleConfig struct {
	mediaType   string
	payloadType string
	leaf        leafConfig
	sct         sctMode
	tlogs       []rekorConfig
	timestamps  []timestampSpec
	extraSig    bool
	flipSig     bool
	noCert      bool
	message     bool
	// signingKey, when set, signs the envelope in place of a fresh leaf
	// key; keyID is the envelope signature's keyid and hint the public key
	// hint of a bundle without a certificate (keysigned.go).
	signingKey *ecdsa.PrivateKey
	keyID      string
	hint       string
	// mediaTypeKey, when set, is the member name the media type is
	// written under in place of mediaType.
	mediaTypeKey string
}

// timestampSpec is one RFC 3161 token of a bundle.
type timestampSpec struct {
	authority TSA
	opts      []TSOpt
}

// WithMediaType sets the bundle's media type.
func WithMediaType(mt string) BundleOpt { return func(c *bundleConfig) { c.mediaType = mt } }

// WithPayloadType sets the DSSE payload type.
func WithPayloadType(pt string) BundleOpt { return func(c *bundleConfig) { c.payloadType = pt } }

// WithLeaf adjusts the leaf certificate.
func WithLeaf(opts ...LeafOpt) BundleOpt {
	return func(c *bundleConfig) {
		for _, o := range opts {
			o(&c.leaf)
		}
	}
}

// WithSCT embeds an SCT the harness CT log key signs.
func WithSCT() BundleOpt { return func(c *bundleConfig) { c.sct = sctTrusted } }

// WithForeignSCT embeds an SCT signed by a key no trusted root lists.
func WithForeignSCT() BundleOpt { return func(c *bundleConfig) { c.sct = sctForeign } }

// WithRekorV1Entry adds a Rekor v1 dsse entry with an inclusion promise
// and an inclusion proof, integrated at the harness clock at index 5.
// Each further call adds another entry.
func WithRekorV1Entry(opts ...RekorOpt) BundleOpt {
	return func(c *bundleConfig) {
		r := rekorConfig{index: 5}
		for _, o := range opts {
			o(&r)
		}
		c.tlogs = append(c.tlogs, r)
	}
}

// WithTimestamp adds an RFC 3161 token from authority over the DSSE
// signature, at the harness clock unless an option sets another time. The
// token embeds the signer certificate, as an authority's response to a
// request that asks for it does.
// Each further call adds another token, in order.
func WithTimestamp(authority TSA, opts ...TSOpt) BundleOpt {
	return func(c *bundleConfig) { c.timestamps = append(c.timestamps, timestampSpec{authority, opts}) }
}

// WithSecondSignature writes the envelope's signature twice.
func WithSecondSignature() BundleOpt { return func(c *bundleConfig) { c.extraSig = true } }

// WithBadSignature flips a bit of the envelope signature. The log entry
// and the timestamps cover the flipped bytes, so only the signature check
// fails.
func WithBadSignature() BundleOpt { return func(c *bundleConfig) { c.flipSig = true } }

// WithoutCertificate writes a public key hint in place of the leaf.
func WithoutCertificate() BundleOpt { return func(c *bundleConfig) { c.noCert = true } }

// WithMessageSignature writes a message signature in place of the DSSE
// envelope.
func WithMessageSignature() BundleOpt { return func(c *bundleConfig) { c.message = true } }

// WithMediaTypeMember writes the media type under the member name key,
// such as the protobuf field name media_type, in place of mediaType.
func WithMediaTypeMember(key string) BundleOpt {
	return func(c *bundleConfig) { c.mediaTypeKey = key }
}

// defaultBundleConfig is a version 0.3 in-toto bundle under the default
// leaf, with no log entry and no timestamp.
func defaultBundleConfig() bundleConfig {
	return bundleConfig{
		mediaType:   BundleMediaTypeV03,
		payloadType: InTotoPayloadType,
		leaf:        defaultLeafConfig(),
		hint:        "acme-key",
	}
}

// DSSEBundle returns a Sigstore bundle whose DSSE envelope carries
// payload. The leaf's key signs the DSSE pre-authentication encoding of
// the payload with ECDSA P-256 and SHA-256, and the options add the log
// entries, the timestamps, and the SCT a Sigstore instance attaches.
//
// Spec: §4.7.11.
func (h *Harness) DSSEBundle(payload []byte, opts ...BundleOpt) []byte {
	h.tb.Helper()
	cfg := defaultBundleConfig()
	for _, o := range opts {
		o(&cfg)
	}
	out, err := h.buildBundle(payload, cfg)
	must(h.tb, err)
	return out
}

// buildBundle assembles the bundle JSON for cfg.
func (h *Harness) buildBundle(payload []byte, cfg bundleConfig) ([]byte, error) {
	key := cfg.signingKey
	if key == nil {
		var err error
		if key, err = newECKey(); err != nil {
			return nil, err
		}
	}
	leafDER, err := h.issueLeafDER(cfg.leaf, key, cfg.sct)
	if err != nil {
		return nil, err
	}
	sig, err := signPAE(key, cfg.payloadType, payload)
	if err != nil {
		return nil, err
	}
	if cfg.flipSig {
		sig[len(sig)-1] ^= 0x01
	}
	env := dsseJSON{Payload: payload, PayloadType: cfg.payloadType, Signatures: []dsseSignature{{Sig: sig, KeyID: cfg.keyID}}}
	if cfg.extraSig {
		env.Signatures = append(env.Signatures, dsseSignature{Sig: sig})
	}
	b := bundleJSON{MediaType: cfg.mediaType}
	if cfg.noCert {
		b.VerificationMaterial.Certificate = nil
	} else {
		b.VerificationMaterial.Certificate = &rawCertDoc{RawBytes: leafDER}
	}
	for _, r := range cfg.tlogs {
		if r.integrated.IsZero() {
			r.integrated = h.clock
		}
		entry, err := h.rekorV1Entry(r, env, leafDER)
		if err != nil {
			return nil, err
		}
		b.VerificationMaterial.TLogEntries = append(b.VerificationMaterial.TLogEntries, entry)
	}
	if err := h.addTimestamps(&b, cfg.timestamps, sig); err != nil {
		return nil, err
	}
	if cfg.message {
		b.MessageSignature = &messageSignatureJSON{Signature: sig}
		b.MessageSignature.MessageDigest.Algorithm = "SHA2_256"
		digest := sha256.Sum256(payload)
		b.MessageSignature.MessageDigest.Digest = digest[:]
	} else {
		b.DSSEEnvelope = &env
	}
	hint := ""
	if cfg.noCert {
		hint = cfg.hint
	}
	return marshalBundle(b, hint, cfg.mediaTypeKey)
}

// addTimestamps adds one RFC 3161 token per spec over sig.
func (h *Harness) addTimestamps(b *bundleJSON, specs []timestampSpec, sig []byte) error {
	if len(specs) == 0 {
		return nil
	}
	data := &tsDataJSON{}
	for _, s := range specs {
		a, err := h.timestampAuthority(s.authority)
		if err != nil {
			return err
		}
		imprint := sha256.Sum256(sig)
		ts := h.defaultTSConfig(imprint[:])
		ts.signer, ts.embed = a.signer, []*x509.Certificate{a.signer.cert}
		ts.hash, ts.digestOID, ts.sigAlg = a.hash, digestOIDOf(a.hash), a.sigAlg
		for _, o := range s.opts {
			o(&ts)
		}
		if ts.embedChain {
			ts.embed = []*x509.Certificate{a.signer.cert, a.root.cert}
		}
		token, err := buildToken(ts)
		if err != nil {
			return err
		}
		// A bundle carries the DER TimeStampResp, status and token.
		resp, err := timeStampResp(0, token)
		if err != nil {
			return err
		}
		if ts.bare {
			resp = token
		}
		data.RFC3161Timestamps = append(data.RFC3161Timestamps, signedTimestampJSON{SignedTimestamp: resp})
	}
	b.VerificationMaterial.TimestampVerificationData = data
	return nil
}

// marshalBundle encodes b. A non-empty hint writes a public key hint in
// place of a certificate, which the bundle types above do not declare, and
// a non-empty mediaTypeKey renames the mediaType member.
func marshalBundle(b bundleJSON, hint, mediaTypeKey string) ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil || (hint == "" && mediaTypeKey == "") {
		return out, err
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	if hint != "" {
		vm := doc["verificationMaterial"].(map[string]any)
		vm["publicKey"] = map[string]any{"hint": hint}
	}
	if mediaTypeKey != "" {
		doc[mediaTypeKey] = doc["mediaType"]
		delete(doc, "mediaType")
	}
	return json.MarshalIndent(doc, "", "  ")
}

// signPAE signs the DSSE pre-authentication encoding of payload.
func signPAE(key *ecdsa.PrivateKey, payloadType string, payload []byte) ([]byte, error) {
	digest := sha256.Sum256(PAE(payloadType, payload))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sign envelope: %w", err)
	}
	return sig, nil
}

// PAE is the DSSE version 1 pre-authentication encoding.
func PAE(payloadType string, payload []byte) []byte {
	out := []byte("DSSEv1 " + strconv.Itoa(len(payloadType)) + " " + payloadType + " " + strconv.Itoa(len(payload)) + " ")
	return append(out, payload...)
}

// Subject is one in-toto statement subject with a sha256 digest member.
type Subject struct {
	Name   string
	SHA256 string
}

// statementJSON is an in-toto Statement v1.
type statementJSON struct {
	Type          string        `json:"_type"`
	Subject       []subjectJSON `json:"subject"`
	PredicateType string        `json:"predicateType"`
	Predicate     struct{}      `json:"predicate"`
}

type subjectJSON struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Statement returns an in-toto Statement v1 with predicateType and
// subjects, in the member order the signing tools write.
func Statement(t testing.TB, predicateType string, subjects ...Subject) []byte {
	t.Helper()
	st := statementJSON{Type: "https://in-toto.io/Statement/v1", PredicateType: predicateType}
	for _, s := range subjects {
		st.Subject = append(st.Subject, subjectJSON{Name: s.Name, Digest: map[string]string{"sha256": s.SHA256}})
	}
	out, err := json.Marshal(st)
	must(t, err)
	return out
}
