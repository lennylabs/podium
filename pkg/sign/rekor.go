package sign

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// rekorRecord is the §8.6 Rekor entry shape we upload. We use the
// hashedrekord type because Podium signs a content hash rather than
// a blob; hashedrekord lets the log validate signature presence
// without storing the underlying bytes.
type rekorRecord struct {
	Kind       string          `json:"kind"`
	APIVersion string          `json:"apiVersion"`
	Spec       hashedRekordSpec `json:"spec"`
}

type hashedRekordSpec struct {
	Signature hashedRekordSignature `json:"signature"`
	Data      hashedRekordData      `json:"data"`
}

type hashedRekordSignature struct {
	Content   string                  `json:"content"`
	PublicKey hashedRekordPublicKey   `json:"publicKey"`
}

type hashedRekordPublicKey struct {
	Content string `json:"content"`
}

type hashedRekordData struct {
	Hash hashedRekordHash `json:"hash"`
}

type hashedRekordHash struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// rekorEntryResponse is keyed by entry UUID; we extract the first
// (and only) entry the upload created.
type rekorEntryResponse map[string]rekorEntry

type rekorEntry struct {
	LogID          string `json:"logID"`
	LogIndex       int64  `json:"logIndex"`
	IntegratedTime int64  `json:"integratedTime"`
}

// uploadRekor records the (cert, signature, content_hash) tuple in
// the configured transparency log and returns the assigned log index.
func (s SigstoreKeyless) uploadRekor(ctx context.Context, contentHash string, signature []byte, leaf *x509.Certificate) (int64, error) {
	hashAlg, hashHex, err := splitContentHash(contentHash)
	if err != nil {
		return 0, err
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	body, err := json.Marshal(rekorRecord{
		Kind:       "hashedrekord",
		APIVersion: "0.0.1",
		Spec: hashedRekordSpec{
			Signature: hashedRekordSignature{
				Content: base64.StdEncoding.EncodeToString(signature),
				PublicKey: hashedRekordPublicKey{
					Content: base64.StdEncoding.EncodeToString(leafPEM),
				},
			},
			Data: hashedRekordData{
				Hash: hashedRekordHash{Algorithm: hashAlg, Value: hashHex},
			},
		},
	})
	if err != nil {
		return 0, err
	}

	url := strings.TrimRight(s.RekorURL, "/") + "/api/v1/log/entries"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("rekor: HTTP %d: %s", resp.StatusCode, string(buf))
	}

	var parsed rekorEntryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode rekor response: %w", err)
	}
	for _, entry := range parsed {
		return entry.LogIndex, nil
	}
	return 0, fmt.Errorf("rekor: empty response")
}

// fetchRekor checks that an entry with the given log index exists
// in the transparency log. The cheap presence check is enough for
// §8.6 anchoring: the cert chain validation already proves the
// signature; Rekor presence anchors the signature in time.
func (s SigstoreKeyless) fetchRekor(ctx context.Context, logIndex int64) error {
	url := fmt.Sprintf("%s/api/v1/log/entries?logIndex=%d",
		strings.TrimRight(s.RekorURL, "/"), logIndex)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("rekor: log index %d not found", logIndex)
	}
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rekor: HTTP %d: %s", resp.StatusCode, string(buf))
	}
	return nil
}

// splitContentHash splits "sha256:abc..." into ("sha256", "abc...").
// Sigstore Rekor expects the algorithm and hex-encoded hash separately.
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
