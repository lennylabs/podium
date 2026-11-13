// Package auditharness holds the fixtures the §8.3 audit destination tests
// share across packages: an in-process receiver that records what the HTTP
// and OTLP destinations send, and a throwaway certificate authority for the
// transport fixtures. It imports nothing from pkg/audit, so the audit
// package's own tests can use it without an import cycle.
package auditharness

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Request is one request a Receiver answered.
type Request struct {
	Path   string
	Header http.Header
	Body   []byte
	// Status is the status the receiver answered with.
	Status int
}

// Receiver is an httptest server standing in for an audit receiver. It
// records every request, answers with the status SetResponse last set (200
// by default), and holds requests while Hold is in effect.
type Receiver struct {
	srv *httptest.Server

	// mu guards every field below.
	mu         sync.Mutex
	reqs       []Request
	status     int
	retryAfter string
	// gate is non-nil while Hold is in effect; a request waits for it to
	// close before it answers.
	gate chan struct{}
	// held counts the requests waiting on gate.
	held int
}

// NewReceiver starts a plain-HTTP receiver that answers 200 until
// SetResponse changes it. The test's cleanup releases any held request and
// closes the server.
func NewReceiver(t testing.TB) *Receiver {
	t.Helper()
	r := &Receiver{status: http.StatusOK}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.close)
	return r
}

// NewTLSReceiver starts a receiver that serves cfg. It discards the server's
// handshake error log, because a case that withholds the client certificate
// provokes one by design.
func NewTLSReceiver(t testing.TB, cfg *tls.Config) *Receiver {
	t.Helper()
	r := &Receiver{status: http.StatusOK}
	r.srv = httptest.NewUnstartedServer(http.HandlerFunc(r.serve))
	r.srv.TLS = cfg
	r.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	r.srv.StartTLS()
	t.Cleanup(r.close)
	return r
}

// URL is the receiver's base URL.
func (r *Receiver) URL() string { return r.srv.URL }

// close releases any held request before the server closes, because
// httptest.Server.Close waits for every active handler.
func (r *Receiver) close() {
	r.Release()
	r.srv.Close()
}

// SetResponse makes every later request answer status, with a Retry-After
// header when retryAfter is non-empty.
func (r *Receiver) SetResponse(status int, retryAfter string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.retryAfter = status, retryAfter
}

// Hold makes every later request wait inside the handler until Release.
func (r *Receiver) Hold() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gate == nil {
		r.gate = make(chan struct{})
	}
}

// Release lets every held request answer and ends Hold.
func (r *Receiver) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gate != nil {
		close(r.gate)
		r.gate = nil
	}
}

// AwaitHeld waits until at least one request is held and fails the test
// when none arrives within the deadline.
func (r *Receiver) AwaitHeld(t testing.TB, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		held := r.held
		r.mu.Unlock()
		if held > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no request reached the held receiver within %s", within)
}

func (r *Receiver) serve(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	gate := r.gate
	if gate != nil {
		r.held++
	}
	r.mu.Unlock()
	if gate != nil {
		<-gate
		r.mu.Lock()
		r.held--
		r.mu.Unlock()
	}
	r.mu.Lock()
	status, retryAfter := r.status, r.retryAfter
	r.reqs = append(r.reqs, Request{Path: req.URL.Path, Header: req.Header.Clone(), Body: body, Status: status})
	r.mu.Unlock()
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
}

// Requests returns a copy of every request the receiver has answered.
func (r *Receiver) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.reqs...)
}

// Lines returns every non-empty NDJSON line of the requests the receiver
// accepted with a 2xx status, in arrival order. A refused request delivered
// nothing, so its lines are left out.
func (r *Receiver) Lines() []string {
	var out []string
	for _, req := range r.Requests() {
		if req.Status/100 != 2 {
			continue
		}
		for _, line := range strings.Split(string(req.Body), "\n") {
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// Records decodes Lines into the fields every §8.1 record carries that the
// destination tests read.
func (r *Receiver) Records(t testing.TB) []Record {
	t.Helper()
	var out []Record
	for _, line := range r.Lines() {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode received line: %v\nline: %s", err, line)
		}
		rec.Line = line
		out = append(out, rec)
	}
	return out
}

// Record is the part of one §8.1 audit record the destination tests read,
// with the line it was decoded from.
type Record struct {
	Type          string            `json:"type"`
	Hash          string            `json:"hash"`
	PrevHash      string            `json:"prev_hash"`
	SchemaVersion int               `json:"schema_version"`
	Context       map[string]string `json:"context"`
	Line          string            `json:"-"`
}

// WaitForRecord polls until an accepted record satisfies match and returns
// it, or returns false when none does within the deadline.
func (r *Receiver) WaitForRecord(t testing.TB, within time.Duration, match func(Record) bool) (Record, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, rec := range r.Records(t) {
			if match(rec) {
				return rec, true
			}
		}
		if time.Now().After(deadline) {
			return Record{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// OfType returns a WaitForRecord predicate that matches the record type typ.
func OfType(typ string) func(Record) bool {
	return func(rec Record) bool { return rec.Type == typ }
}
