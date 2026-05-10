package sign

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fulcioCertRequest is the §4.7.9 Fulcio v2 signing-cert request body.
// The OIDC token authorizes the caller; proof_of_possession signs a
// hash of the OIDC subject with the ephemeral private key, proving
// the caller controls the key the cert will be issued for.
type fulcioCertRequest struct {
	Credentials      fulcioCredentials      `json:"credentials"`
	PublicKeyRequest fulcioPublicKeyRequest `json:"publicKeyRequest"`
}

type fulcioCredentials struct {
	OIDCIdentityToken string `json:"oidcIdentityToken"`
}

type fulcioPublicKeyRequest struct {
	PublicKey         fulcioPublicKey `json:"publicKey"`
	ProofOfPossession string          `json:"proofOfPossession"`
}

type fulcioPublicKey struct {
	Algorithm string `json:"algorithm"`
	Content   string `json:"content"`
}

// fulcioCertResponse is the v2 signing-cert response. The
// `signedCertificateEmbeddedSct` shape carries a chain of PEM-encoded
// certs; the leaf is index 0, intermediates follow.
type fulcioCertResponse struct {
	SignedCertificateEmbeddedSct *fulcioCertChain `json:"signedCertificateEmbeddedSct,omitempty"`
	SignedCertificateDetachedSct *fulcioCertChain `json:"signedCertificateDetachedSct,omitempty"`
}

type fulcioCertChain struct {
	Chain fulcioChain `json:"chain"`
}

type fulcioChain struct {
	Certificates []string `json:"certificates"`
}

// mintCert exchanges the configured OIDC token for a Sigstore-issued
// short-lived signing certificate. Returns the leaf certificate plus
// any intermediates the caller must include in the verification chain.
func (s SigstoreKeyless) mintCert(ctx context.Context, priv *ecdsa.PrivateKey) (*x509.Certificate, []*x509.Certificate, error) {
	subject, err := oidcSubject(s.OIDCToken)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc subject: %w", err)
	}

	pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal pubkey: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})

	subjectHash := sha256.Sum256([]byte(subject))
	popSig, err := ecdsa.SignASN1(rand.Reader, priv, subjectHash[:])
	if err != nil {
		return nil, nil, fmt.Errorf("proof of possession: %w", err)
	}

	body, err := json.Marshal(fulcioCertRequest{
		Credentials: fulcioCredentials{OIDCIdentityToken: s.OIDCToken},
		PublicKeyRequest: fulcioPublicKeyRequest{
			PublicKey: fulcioPublicKey{
				Algorithm: "ECDSA",
				Content:   string(pubPEM),
			},
			ProofOfPossession: base64.StdEncoding.EncodeToString(popSig),
		},
	})
	if err != nil {
		return nil, nil, err
	}

	url := strings.TrimRight(s.FulcioURL, "/") + "/api/v2/signingCert"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("fulcio: HTTP %d: %s", resp.StatusCode, string(buf))
	}

	var parsed fulcioCertResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, nil, fmt.Errorf("decode fulcio response: %w", err)
	}
	chain := parsed.SignedCertificateEmbeddedSct
	if chain == nil {
		chain = parsed.SignedCertificateDetachedSct
	}
	if chain == nil || len(chain.Chain.Certificates) == 0 {
		return nil, nil, fmt.Errorf("fulcio: empty cert chain")
	}
	return parseChainPEM(chain.Chain.Certificates)
}

// parseChainPEM parses a Fulcio cert chain. Each element is a single
// PEM-encoded cert; index 0 is the leaf, the rest are intermediates.
func parseChainPEM(elems []string) (*x509.Certificate, []*x509.Certificate, error) {
	if len(elems) == 0 {
		return nil, nil, fmt.Errorf("empty cert chain")
	}
	leaf, err := parseSinglePEMCert(elems[0])
	if err != nil {
		return nil, nil, fmt.Errorf("leaf cert: %w", err)
	}
	intermediates := make([]*x509.Certificate, 0, len(elems)-1)
	for i, raw := range elems[1:] {
		c, err := parseSinglePEMCert(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("intermediate %d: %w", i, err)
		}
		intermediates = append(intermediates, c)
	}
	return leaf, intermediates, nil
}

func parseSinglePEMCert(s string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

// oidcSubject decodes a JWT and returns the email or sub claim used
// as the cert subject. Fulcio binds the issued cert to this value.
//
// The JWT signature is not verified here — Fulcio verifies it against
// its configured issuers. We only decode the payload to identify the
// caller for proof-of-possession.
func oidcSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Fall back to standard base64 for tokens that didn't strip padding.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", fmt.Errorf("decode payload: %w", err)
		}
	}
	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse claims: %w", err)
	}
	if claims.Email != "" {
		return claims.Email, nil
	}
	if claims.Sub != "" {
		return claims.Sub, nil
	}
	return "", fmt.Errorf("no subject claim")
}
