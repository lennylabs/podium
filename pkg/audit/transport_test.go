package audit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA is a throwaway certificate authority for the TLS fixtures.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
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
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// issue returns a PEM certificate and key signed by the CA, valid for
// 127.0.0.1 as a server and as a client.
func (ca *testCA) issue(t *testing.T) (certPEM, keyPEM []byte) {
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

// serverTLS returns a server configuration presenting a certificate from
// the CA.
func (ca *testCA) serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	certPEM, keyPEM := ca.issue(t)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}}
}

func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

func writeFixture(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// transportRow is one transport refusal of TestOpen_RefusesUnusableConfiguration.
type transportRow struct {
	name string
	opts OpenOptions
	want []string
}

// transportRefusals builds the §13.12 transport refusal rows over fixture
// files in dir. Every header value is zq7k, which the table's absence check
// covers, and each refused header line is the third line of its file.
func transportRefusals(t *testing.T, dir string) []transportRow {
	t.Helper()
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issue(t)
	_, otherKeyPEM := ca.issue(t)
	cert := writeFixture(t, dir, "client.crt", certPEM)
	key := writeFixture(t, dir, "client.key", keyPEM)
	otherKey := writeFixture(t, dir, "other.key", otherKeyPEM)
	noPEM := writeFixture(t, dir, "nopem.crt", []byte("not a certificate\n"))
	missing := filepath.Join(dir, "missing")

	var rows []transportRow
	headerRow := func(name, line string, want ...string) {
		// Number the files, because a case-insensitive file system folds
		// names that differ only in the header name's case.
		file := fmt.Sprintf("%d.headers", len(rows))
		path := writeFixture(t, dir, file, []byte("# audit headers\nX-Audit-Token: zq7k\n"+line+"\n"))
		rows = append(rows, transportRow{name, OpenOptions{HeadersFile: path}, append([]string{envHeadersFile, path + ":3"}, want...)})
	}
	for _, line := range []string{"Host: zq7k", "hOST: zq7k", "Content-Length: zq7k", "content-length: zq7k", "Content-Type: zq7k", "CONTENT-TYPE: zq7k"} {
		headerRow("header "+strings.SplitN(line, ":", 2)[0], line, "cannot be set")
	}
	headerRow("line without a colon", "zq7k", "no colon")
	headerRow("empty header name", ": zq7k", "name is empty")
	both := []string{envCertFile, envKeyFile}
	rows = append(rows,
		transportRow{"unreadable headers file", OpenOptions{HeadersFile: missing}, []string{envHeadersFile, missing}},
		transportRow{"unreadable CA file", OpenOptions{CAFile: missing}, []string{envCAFile, missing}},
		transportRow{"CA file with no certificate", OpenOptions{CAFile: noPEM}, []string{envCAFile, noPEM, "no PEM certificate"}},
		transportRow{"only the certificate set", OpenOptions{CertFile: cert}, both},
		transportRow{"only the key set", OpenOptions{KeyFile: key}, both},
		transportRow{"certificate with no PEM block", OpenOptions{CertFile: noPEM, KeyFile: key}, append([]string{noPEM}, both...)},
		transportRow{"mismatched certificate and key", OpenOptions{CertFile: cert, KeyFile: otherKey}, append([]string{cert}, both...)},
	)
	return rows
}

// The stream's client refuses redirects and leaves certificate
// verification on.
//
// Spec: §8.3, §13.12
func TestLoadTransport_ClientRefusesRedirectsAndVerifies(t *testing.T) {
	t.Parallel()
	tr, err := loadTransport(OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.client.Timeout != 0 {
		t.Errorf("client Timeout = %v, want none; requestContext is the deadline", tr.client.Timeout)
	}
	if err := tr.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}
	base := tr.client.Transport.(*http.Transport)
	if base == http.DefaultTransport || base.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("transport shares DefaultTransport or skips verification")
	}
}
