package audit

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// The §13.12 audit transport variables. Each names a file, so a secret
// never sits in the environment or in a log line.
const (
	envHeadersFile = "PODIUM_AUDIT_HEADERS_FILE"
	envCAFile      = "PODIUM_AUDIT_TLS_CA_FILE"
	envCertFile    = "PODIUM_AUDIT_TLS_CERT_FILE"
	envKeyFile     = "PODIUM_AUDIT_TLS_KEY_FILE"
)

// maxResponseBody bounds how much of a response body a destination reads.
// Reading it lets the connection be reused, and the OTLP destination
// decodes its partial-success count from it, which an
// ExportLogsServiceResponse holds in far fewer bytes.
const maxResponseBody = 64 << 10

// reservedHeaders are the header names a headers file may not set, because
// the request itself defines them (§13.12).
var reservedHeaders = []string{"Host", "Content-Length", "Content-Type"}

// transport is the one HTTP client and header set every HTTP and OTLP
// destination of a stream sends through.
type transport struct {
	client  *http.Client
	headers http.Header
}

// loadTransport reads the §13.12 transport files that opts names and
// builds the stream's client. Every error wraps ErrConfig, names the
// variable of the file it concerns, and includes no content of the file.
//
// The client verifies the receiver's certificate against the CA file, or
// the system trust store when it is unset, and never skips verification
// (fail closed). CheckRedirect is an http.Client field, so this client is
// the only one a destination may use: a client built around the same
// transport would follow redirects and copy the custom credential headers
// to the redirect target. The client sets no Timeout, because the
// forwarder's requestContext hook is the one request deadline.
//
// Spec: §8.3, §13.12
func loadTransport(opts OpenOptions) (*transport, error) {
	headers, err := loadHeaders(opts.HeadersFile)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := loadTLS(opts)
	if err != nil {
		return nil, err
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = tlsConfig
	return &transport{
		client: &http.Client{
			Transport:     base,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		headers: headers,
	}, nil
}

// loadHeaders reads the headers file at path, or returns no headers when
// path is empty.
func loadHeaders(path string) (http.Header, error) {
	headers := http.Header{}
	if path == "" {
		return headers, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: read %s: %w", ErrConfig, envHeadersFile, path, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if err := parseHeaderLine(headers, line); err != nil {
			return nil, fmt.Errorf("%w: %s: %s:%d: %s", ErrConfig, envHeadersFile, path, i+1, err)
		}
	}
	return headers, nil
}

// parseHeaderLine adds one `Name: value` line to headers. A blank line and
// a line that begins with # add nothing. A refusal never quotes the line,
// which can hold a credential.
//
// Spec: §13.12
func parseHeaderLine(headers http.Header, line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return errors.New("the line has no colon")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("the header name is empty")
	}
	for _, reserved := range reservedHeaders {
		if strings.EqualFold(name, reserved) {
			return fmt.Errorf("the header %s cannot be set", reserved)
		}
	}
	headers.Add(name, strings.TrimSpace(value))
	return nil
}

// loadTLS builds the client TLS configuration from the CA file and the
// client certificate pair. InsecureSkipVerify stays false.
func loadTLS(opts OpenOptions) (*tls.Config, error) {
	cfg := &tls.Config{}
	if opts.CAFile != "" {
		pool, err := loadCAPool(opts.CAFile)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	if (opts.CertFile == "") != (opts.KeyFile == "") {
		return nil, fmt.Errorf("%w: %s and %s must be set together", ErrConfig, envCertFile, envKeyFile)
	}
	if opts.CertFile != "" {
		pair, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("%w: %s=%s, %s=%s: load client certificate: %w",
				ErrConfig, envCertFile, opts.CertFile, envKeyFile, opts.KeyFile, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// loadCAPool reads the PEM CA bundle at path, which replaces the system
// trust store.
func loadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: read %s: %w", ErrConfig, envCAFile, path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%w: %s: %s holds no PEM certificate", ErrConfig, envCAFile, path)
	}
	return pool, nil
}

// do sends req through the stream's client and returns the status, the
// Retry-After value, and at most maxResponseBody bytes of the body, which
// it closes.
func (t *transport) do(req *http.Request) (sendResult, error) {
	resp, err := t.client.Do(req)
	if err != nil {
		return sendResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A failed read only costs connection reuse and the bytes after the
	// failure; the status already decides the outcome.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	return sendResult{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: body}, nil
}
