package auditharness

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, url, body string) int {
	t.Helper()
	resp, err := http.Post(url, "application/x-ndjson", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The receiver keeps only the lines of accepted requests, so a refused
// batch never reads as delivered.
func TestReceiver_LinesOmitRefusedRequests(t *testing.T) {
	r := NewReceiver(t)
	r.SetResponse(http.StatusServiceUnavailable, "60")
	if got := post(t, r.URL(), `{"type":"refused"}`+"\n"); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
	r.SetResponse(http.StatusOK, "")
	post(t, r.URL(), `{"type":"a","hash":"h1"}`+"\n\n"+`{"type":"b","context":{"k":"v"}}`+"\n")
	if n := len(r.Requests()); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
	if reqs := r.Requests(); reqs[0].Status != http.StatusServiceUnavailable {
		t.Errorf("first status = %d, want 503", reqs[0].Status)
	}
	recs := r.Records(t)
	if len(recs) != 2 || recs[0].Hash != "h1" || recs[1].Context["k"] != "v" {
		t.Errorf("records = %+v, want the two accepted lines", recs)
	}
	if _, ok := r.WaitForRecord(t, 0, OfType("refused")); ok {
		t.Error("a refused record matched")
	}
	if rec, ok := r.WaitForRecord(t, time.Second, OfType("b")); !ok || rec.Line == "" {
		t.Errorf("WaitForRecord(b) = (%+v, %v), want the record and its line", rec, ok)
	}
}

// A held request answers only after Release.
func TestReceiver_HoldAndRelease(t *testing.T) {
	r := NewReceiver(t)
	r.Hold()
	r.Hold()
	done := make(chan int, 1)
	go func() {
		resp, err := http.Post(r.URL(), "application/x-ndjson", strings.NewReader(`{"type":"held"}`+"\n"))
		if err != nil {
			done <- 0
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	r.AwaitHeld(t, 5*time.Second)
	select {
	case <-done:
		t.Fatal("a held request answered before Release")
	case <-time.After(50 * time.Millisecond):
	}
	r.Release()
	if got := <-done; got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
	r.Release()
}

// The CA's files configure a mutual-TLS client the receiver accepts.
func TestCA_TransportFiles(t *testing.T) {
	ca := NewCA(t)
	files := ca.WriteTransportFiles(t, t.TempDir(), "X-Test: 1\n")
	for _, path := range []string{files.Headers, files.CA, files.Cert, files.Key} {
		if path == "" {
			t.Fatal("a transport file path is empty")
		}
	}
	files.Setenv(t)
	cfg := ca.MutualServerTLS(t)
	if cfg.ClientCAs == nil || len(cfg.Certificates) != 1 {
		t.Errorf("mutual TLS config = %+v, want one certificate and the client CA pool", cfg)
	}
}
