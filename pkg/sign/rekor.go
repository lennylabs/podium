package sign

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// rekorRawBytes is the protobuf-specs bytes wrapper Rekor v2 uses for a
// certificate.
type rekorRawBytes struct {
	RawBytes []byte `json:"rawBytes"`
}

// rekorSignature is the hashedrekord v0.0.2 signature: the signature
// bytes and the verifier that made them. The create-entry request and the
// stored entry body share it.
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

// rekorEntryResponse is the protojson TransparencyLogEntry Rekor v2
// returns. protojson writes int64 fields as decimal strings and omits
// zero values, so logIndex is a string and an absent value means 0.
type rekorEntryResponse struct {
	LogIndex          string `json:"logIndex"`
	CanonicalizedBody []byte `json:"canonicalizedBody"`
	InclusionProof    *struct {
		Hashes     [][]byte `json:"hashes"`
		Checkpoint *struct {
			Envelope string `json:"envelope"`
		} `json:"checkpoint"`
	} `json:"inclusionProof"`
}

// hashedRekordEntry is the canonicalized hashedrekord v0.0.2 entry body
// the log proves.
type hashedRekordEntry struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Spec       struct {
		HashedRekordV002 struct {
			Data struct {
				Algorithm string `json:"algorithm"`
				Digest    []byte `json:"digest"`
			} `json:"data"`
			Signature rekorSignature `json:"signature"`
		} `json:"hashedRekordV002"`
	} `json:"spec"`
}

// uploadRekor records the (digest, signature, leaf) entry in the Rekor v2
// log at RekorURL and returns the entry with its inclusion proof and
// checkpoint. A log that serves no /api/v2 path answers 404, which fails
// the signing.
//
// Spec: §4.7.9, §6.2.
func (s SigstoreKeyless) uploadRekor(ctx context.Context, digest, sig []byte, leaf *x509.Certificate) (*tlogEntry, error) {
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
	var resp rekorEntryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.tlogEntry()
}

// tlogEntry converts the response into the envelope's tlog object.
func (r rekorEntryResponse) tlogEntry() (*tlogEntry, error) {
	var index int64
	if r.LogIndex != "" {
		var err error
		if index, err = strconv.ParseInt(r.LogIndex, 10, 64); err != nil {
			return nil, fmt.Errorf("decode response: log index: %w", err)
		}
	}
	switch {
	case len(r.CanonicalizedBody) == 0:
		return nil, errors.New("response carries no canonicalized body")
	case r.InclusionProof == nil:
		return nil, errors.New("response carries no inclusion proof")
	case r.InclusionProof.Checkpoint == nil || r.InclusionProof.Checkpoint.Envelope == "":
		return nil, errors.New("response carries no checkpoint")
	}
	hashes := make([]string, len(r.InclusionProof.Hashes))
	for i, h := range r.InclusionProof.Hashes {
		hashes[i] = base64.StdEncoding.EncodeToString(h)
	}
	return &tlogEntry{
		LogIndex:   index,
		Body:       base64.StdEncoding.EncodeToString(r.CanonicalizedBody),
		Hashes:     hashes,
		Checkpoint: r.InclusionProof.Checkpoint.Envelope,
	}, nil
}

// bindEntry requires the proven entry body to record the digest being
// verified, the envelope's signature, and the envelope's leaf. Without
// it, a valid inclusion proof for an unrelated entry would pass.
//
// Spec: §4.7.9.
func bindEntry(body string, digest, sig []byte, leaf *x509.Certificate) error {
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return fmt.Errorf("log entry is not a hashedrekord v0.0.2 entry: %w", err)
	}
	var entry hashedRekordEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return fmt.Errorf("log entry is not a hashedrekord v0.0.2 entry: %w", err)
	}
	if entry.Kind != "hashedrekord" || entry.APIVersion != "0.0.2" {
		return errors.New("log entry is not a hashedrekord v0.0.2 entry")
	}
	v2 := entry.Spec.HashedRekordV002
	if v2.Data.Algorithm != "SHA2_256" || !bytes.Equal(v2.Data.Digest, digest) {
		return errors.New("log entry does not bind the digest")
	}
	if !bytes.Equal(v2.Signature.Content, sig) {
		return errors.New("log entry does not bind the signature")
	}
	if !bytes.Equal(v2.Signature.Verifier.X509Certificate.RawBytes, leaf.Raw) {
		return errors.New("log entry does not bind the certificate")
	}
	return nil
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
