package auditharness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CA is a throwaway certificate authority for the §13.12 audit transport
// fixtures.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// PEM is the CA certificate in PEM form, the content of a
	// PODIUM_AUDIT_TLS_CA_FILE.
	PEM []byte
}

// NewCA generates a CA valid for an hour either side of now.
func NewCA(t testing.TB) *CA {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "podium test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &CA{cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Issue returns a PEM certificate and key signed by the CA, valid for
// 127.0.0.1 as a server and as a client.
func (ca *CA) Issue(t testing.TB) (certPEM, keyPEM []byte) {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// ServerTLS returns a server configuration presenting a certificate from the
// CA.
func (ca *CA) ServerTLS(t testing.TB) *tls.Config {
	t.Helper()
	certPEM, keyPEM := ca.Issue(t)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}}
}

// MutualServerTLS is ServerTLS that also requires a client certificate the
// CA issued.
func (ca *CA) MutualServerTLS(t testing.TB) *tls.Config {
	t.Helper()
	cfg := ca.ServerTLS(t)
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.ClientCAs = ca.Pool()
	return cfg
}

// Pool returns a certificate pool holding only the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// TransportFiles are the files the §13.12 audit transport variables name.
type TransportFiles struct {
	Headers, CA, Cert, Key string
}

// WriteTransportFiles writes a headers file holding headerLines, the CA
// certificate, and a client certificate and key the CA issued, all under
// dir.
func (ca *CA) WriteTransportFiles(t testing.TB, dir, headerLines string) TransportFiles {
	t.Helper()
	certPEM, keyPEM := ca.Issue(t)
	return TransportFiles{
		Headers: WriteFile(t, dir, "audit-headers", []byte(headerLines)),
		CA:      WriteFile(t, dir, "audit-ca.pem", ca.PEM),
		Cert:    WriteFile(t, dir, "audit-client.pem", certPEM),
		Key:     WriteFile(t, dir, "audit-client.key", keyPEM),
	}
}

// Setenv sets the four transport variables to f for the rest of the test.
func (f TransportFiles) Setenv(t testing.TB) {
	t.Helper()
	t.Setenv("PODIUM_AUDIT_HEADERS_FILE", f.Headers)
	t.Setenv("PODIUM_AUDIT_TLS_CA_FILE", f.CA)
	t.Setenv("PODIUM_AUDIT_TLS_CERT_FILE", f.Cert)
	t.Setenv("PODIUM_AUDIT_TLS_KEY_FILE", f.Key)
}

// WriteFile writes data to dir/name with mode 0o600 and returns the path.
func WriteFile(t testing.TB, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
