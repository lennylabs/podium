package sign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func selfSignedPEM(t *testing.T, subject string) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: subject},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestParseChainPEM_EmptyErrors(t *testing.T) {
	t.Parallel()
	if _, _, err := parseChainPEM(nil); err == nil {
		t.Errorf("expected error for empty chain")
	}
}

func TestParseChainPEM_BadLeafErrors(t *testing.T) {
	t.Parallel()
	if _, _, err := parseChainPEM([]string{"not pem"}); err == nil {
		t.Errorf("expected error for bad leaf")
	}
}

func TestParseChainPEM_BadIntermediateErrors(t *testing.T) {
	t.Parallel()
	leaf := selfSignedPEM(t, "leaf")
	if _, _, err := parseChainPEM([]string{leaf, "not pem"}); err == nil ||
		!strings.Contains(err.Error(), "intermediate") {
		t.Errorf("expected intermediate error, got %v", err)
	}
}

func TestParseChainPEM_HappyPath(t *testing.T) {
	t.Parallel()
	leaf := selfSignedPEM(t, "leaf")
	inter := selfSignedPEM(t, "intermediate")
	gotLeaf, intermediates, err := parseChainPEM([]string{leaf, inter})
	if err != nil {
		t.Fatalf("parseChainPEM: %v", err)
	}
	if gotLeaf == nil {
		t.Errorf("nil leaf")
	}
	if len(intermediates) != 1 {
		t.Errorf("intermediates = %d, want 1", len(intermediates))
	}
}

func TestParseSinglePEMCert_BadInput(t *testing.T) {
	t.Parallel()
	if _, err := parseSinglePEMCert("not pem"); err == nil {
		t.Errorf("expected error")
	}
	// Valid PEM block but bogus DER.
	bogus := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")}))
	if _, err := parseSinglePEMCert(bogus); err == nil {
		t.Errorf("expected DER parse error")
	}
}

func TestOIDCSubject_MalformedJWT(t *testing.T) {
	t.Parallel()
	if _, err := oidcSubject("not.a.token"); err == nil {
		t.Errorf("expected error for malformed JWT")
	}
	if _, err := oidcSubject("only-one-part"); err == nil {
		t.Errorf("expected error for one-part token")
	}
}

func TestOIDCSubject_ExtractsEmailOrSub(t *testing.T) {
	t.Parallel()
	encode := func(payload string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(payload))
	}
	emailPayload, _ := json.Marshal(map[string]string{"email": "user@example.com"})
	subPayload, _ := json.Marshal(map[string]string{"sub": "abc-123"})
	header := encode("{}")
	signature := encode("sig")

	got, err := oidcSubject(header + "." + encode(string(emailPayload)) + "." + signature)
	if err != nil {
		t.Fatalf("email: %v", err)
	}
	if got != "user@example.com" {
		t.Errorf("email got %q", got)
	}

	got, err = oidcSubject(header + "." + encode(string(subPayload)) + "." + signature)
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if got != "abc-123" {
		t.Errorf("sub got %q", got)
	}
}
