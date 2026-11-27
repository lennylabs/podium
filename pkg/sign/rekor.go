package sign

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// keyDetailsP256 is the Sigstore keyDetails value of the ECDSA P-256 leaf
// key a keyless signature uses.
const keyDetailsP256 = "PKIX_ECDSA_P256_SHA_256"

// rekorRawBytes is the protobuf-specs bytes wrapper Rekor v2 uses for a
// certificate.
type rekorRawBytes struct {
	RawBytes []byte `json:"rawBytes"`
}

// rekorSignature is the hashedrekord v0.0.2 signature: the signature
// bytes and the verifier that made them.
type rekorSignature struct {
	Content  []byte `json:"content"`
	Verifier struct {
		X509Certificate rekorRawBytes `json:"x509Certificate"`
		KeyDetails      string        `json:"keyDetails"`
	} `json:"verifier"`
}

// rekorCreateRequest is the Rekor v2 create-entry request for a
// hashedrekord v0.0.2 entry.
type rekorCreateRequest struct {
	HashedRekordRequestV002 struct {
		Digest    []byte         `json:"digest"`
		Signature rekorSignature `json:"signature"`
	} `json:"hashedRekordRequestV002"`
}

// uploadRekor records the (digest, signature, leaf) entry in the Rekor v2
// log at RekorURL and returns the TransparencyLogEntry it answers with,
// which the bundle carries as it is. A log that serves no /api/v2 path
// answers 404, which fails the signing.
//
// Spec: §4.7.9, §6.2.
func (s SigstoreKeyless) uploadRekor(ctx context.Context, digest, sig []byte, leaf *x509.Certificate) (*protorekor.TransparencyLogEntry, error) {
	var create rekorCreateRequest
	create.HashedRekordRequestV002.Digest = digest
	create.HashedRekordRequestV002.Signature.Content = sig
	create.HashedRekordRequestV002.Signature.Verifier.X509Certificate.RawBytes = leaf.Raw
	create.HashedRekordRequestV002.Signature.Verifier.KeyDetails = keyDetailsP256
	body, err := json.Marshal(create)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	url := strings.TrimRight(s.RekorURL, "/") + "/api/v2/log/entries"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	raw, err := s.post(req)
	if err != nil {
		return nil, err
	}
	return parseRekorEntry(raw)
}

// parseRekorEntry decodes the protojson TransparencyLogEntry Rekor v2
// returns and refuses one that lacks a member the bundle needs. protojson
// omits a zero logIndex, so an absent value decodes as 0.
func parseRekorEntry(raw []byte) (*protorekor.TransparencyLogEntry, error) {
	entry := new(protorekor.TransparencyLogEntry)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, entry); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	switch {
	case len(entry.GetCanonicalizedBody()) == 0:
		return nil, errors.New("response carries no canonicalized body")
	case len(entry.GetLogId().GetKeyId()) == 0:
		return nil, errors.New("response carries no log ID")
	case entry.GetKindVersion().GetKind() == "" || entry.GetKindVersion().GetVersion() == "":
		return nil, errors.New("response carries no entry kind and version")
	case entry.GetInclusionProof() == nil:
		return nil, errors.New("response carries no inclusion proof")
	case entry.GetInclusionProof().GetCheckpoint().GetEnvelope() == "":
		return nil, errors.New("response carries no checkpoint")
	}
	return entry, nil
}

// sha256Digest returns the digest bytes of a "sha256:hex" content hash.
// A keyless signature covers a SHA-256 digest only, so another algorithm
// is refused.
//
// Spec: §4.7.9.
func sha256Digest(contentHash string) ([]byte, error) {
	alg, hexStr, err := splitContentHash(contentHash)
	if err != nil {
		return nil, fmt.Errorf("content hash: %w", err)
	}
	if alg != "sha256" {
		return nil, fmt.Errorf("content hash: algorithm %s is not sha256", alg)
	}
	// splitContentHash has already hex-decoded hexStr, so this cannot fail.
	return hex.DecodeString(hexStr)
}

// decodeContentHash parses an "alg:hex" string and returns the raw
// hash bytes.
func decodeContentHash(s string) ([]byte, error) {
	_, hexStr, err := splitContentHash(s)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(hexStr)
}

// splitContentHash splits "sha256:abc..." into ("sha256", "abc...").
func splitContentHash(contentHash string) (string, string, error) {
	i := strings.Index(contentHash, ":")
	if i <= 0 || i == len(contentHash)-1 {
		return "", "", fmt.Errorf("content hash %q must be alg:hex", contentHash)
	}
	alg := contentHash[:i]
	hexStr := contentHash[i+1:]
	if _, err := hex.DecodeString(hexStr); err != nil {
		return "", "", fmt.Errorf("content hash hex: %w", err)
	}
	return alg, hexStr, nil
}
