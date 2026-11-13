package audit

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/auditharness"
)

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
	ca := auditharness.NewCA(t)
	certPEM, keyPEM := ca.Issue(t)
	_, otherKeyPEM := ca.Issue(t)
	cert := auditharness.WriteFile(t, dir, "client.crt", certPEM)
	key := auditharness.WriteFile(t, dir, "client.key", keyPEM)
	otherKey := auditharness.WriteFile(t, dir, "other.key", otherKeyPEM)
	noPEM := auditharness.WriteFile(t, dir, "nopem.crt", []byte("not a certificate\n"))
	missing := filepath.Join(dir, "missing")

	var rows []transportRow
	headerRow := func(name, line string, want ...string) {
		// Number the files, because a case-insensitive file system folds
		// names that differ only in the header name's case.
		file := fmt.Sprintf("%d.headers", len(rows))
		path := auditharness.WriteFile(t, dir, file, []byte("# audit headers\nX-Audit-Token: zq7k\n"+line+"\n"))
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
