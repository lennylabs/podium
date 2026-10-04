package sign_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// hangMode selects where a hanging handler stops answering.
type hangMode int

const (
	// hangHeaders blocks before writing any response.
	hangHeaders hangMode = iota
	// hangBody writes a 200 status and a partial body, flushes, and blocks.
	hangBody
)

// returnBound is how long a Sign against a hanging endpoint may take. It is
// well above the 1s per-request deadline the hang cases set, so a slow
// race-instrumented run does not fail it, and well below an unbounded wait.
const returnBound = 5 * time.Second

// hangingServer serves every path with a handler that never completes. The
// handler also returns when the test ends: httptest.Server.Close waits for
// in-flight handlers, so the release channel is closed before the server
// closes. Cleanups run last-registered first.
func hangingServer(t *testing.T, mode hangMode) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == hangBody {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"signedCertificateEmbeddedSct":`))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv
}

// timedSign runs Sign and returns its error and how long it took.
func timedSign(ctx context.Context, s sign.SigstoreKeyless) (time.Duration, error) {
	start := time.Now()
	_, err := s.Sign(ctx, hashOf([]byte("body")))
	return time.Since(start), err
}

// Spec: §4.7.9, §6.2 — a request that misses its per-request deadline fails
// the signing, naming the service and the endpoint URL, and no later request
// is sent. The body-mode case pins that the deadline also bounds the body
// read.
func TestSigstoreKeyless_SignRequestDeadline(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	cases := []struct {
		name    string
		mode    hangMode
		prefix  string
		point   func(s *sign.SigstoreKeyless, url string) string
		wantReq int64 // requests the working FakeServer sees; -1 skips the check
	}{
		{"fulcio headers", hangHeaders, "fulcio:", func(s *sign.SigstoreKeyless, u string) string { s.FulcioURL = u; return u }, 0},
		{"tsa headers", hangHeaders, "tsa:", func(s *sign.SigstoreKeyless, u string) string {
			s.TSAURL = u + "/api/v1/timestamp"
			return s.TSAURL
		}, 1},
		{"rekor headers", hangHeaders, "rekor:", func(s *sign.SigstoreKeyless, u string) string { s.RekorURL = u; return u }, -1},
		{"fulcio body", hangBody, "fulcio:", func(s *sign.SigstoreKeyless, u string) string { s.FulcioURL = u; return u }, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var count atomic.Int64
			provider := signer(t, h, h.FakeServer(t, sh.WithRequestCounter(&count)))
			provider.RequestTimeout = time.Second
			hangURL := c.point(&provider, hangingServer(t, c.mode).URL)

			elapsed, err := timedSign(context.Background(), provider)
			if err == nil {
				t.Fatal("Sign succeeded against a hanging endpoint")
			}
			if elapsed > returnBound {
				t.Errorf("Sign took %s, want under %s", elapsed, returnBound)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, c.prefix) {
				t.Errorf("err = %q, want prefix %q", msg, c.prefix)
			}
			if !strings.Contains(msg, hangURL) {
				t.Errorf("err = %q, want it to name %s", msg, hangURL)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want context.DeadlineExceeded", err)
			}
			if !strings.HasSuffix(msg, "(no response within 1s)") {
				t.Errorf("err = %q, want suffix (no response within 1s)", msg)
			}
			if c.wantReq >= 0 && count.Load() != c.wantReq {
				t.Errorf("working server saw %d requests, want %d", count.Load(), c.wantReq)
			}
		})
	}
}

// delayTransport holds every request for delay before delegating it, so
// each response arrives no earlier than delay after its request starts.
type delayTransport struct {
	delay time.Duration
	next  http.RoundTripper
}

func (d delayTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case <-time.After(d.delay):
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return d.next.RoundTrip(r)
}

// Spec: §4.7.9, §6.2 — the deadline applies to each request separately: three
// requests that each take 600ms succeed under a 1s deadline although their
// total exceeds it.
func TestSigstoreKeyless_SignDeadlineIsPerRequest(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	srv := h.FakeServer(t)
	provider := signer(t, h, srv)
	provider.RequestTimeout = time.Second
	provider.Client = &http.Client{Transport: delayTransport{delay: 600 * time.Millisecond, next: srv.Client().Transport}}
	if _, err := provider.Sign(context.Background(), hashOf([]byte("body"))); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

// deadlineRecord is what recordingTransport notes about one request.
type deadlineRecord struct {
	path     string
	start    time.Time
	deadline time.Time
	ok       bool
}

// recordingTransport notes each request's start time and context deadline
// before delegating it.
type recordingTransport struct {
	next http.RoundTripper
	mu   sync.Mutex // guards seen
	seen []deadlineRecord
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := deadlineRecord{path: r.URL.Path, start: time.Now()}
	rec.deadline, rec.ok = r.Context().Deadline()
	rt.mu.Lock()
	rt.seen = append(rt.seen, rec)
	rt.mu.Unlock()
	return rt.next.RoundTrip(r)
}

func (rt *recordingTransport) records() []deadlineRecord {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]deadlineRecord(nil), rt.seen...)
}

// Spec: §4.7.9, §6.2 — a zero or negative RequestTimeout falls back to
// DefaultRequestTimeout rather than leaving a request unbounded, and a
// positive value is used as given.
func TestSigstoreKeyless_SignDefaultRequestTimeout(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	cases := []struct {
		name     string
		timeout  time.Duration
		min, max time.Duration
	}{
		{"zero", 0, sign.DefaultRequestTimeout - 5*time.Second, sign.DefaultRequestTimeout},
		{"negative", -1, sign.DefaultRequestTimeout - 5*time.Second, sign.DefaultRequestTimeout},
		{"control", time.Second, 0, time.Second},
	}
	wantPaths := []string{"/api/v2/signingCert", "/api/v1/timestamp", "/api/v2/log/entries"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := h.FakeServer(t)
			rt := &recordingTransport{next: srv.Client().Transport}
			provider := signer(t, h, srv)
			provider.RequestTimeout = c.timeout
			provider.Client = &http.Client{Transport: rt}
			if _, err := provider.Sign(context.Background(), hashOf([]byte("body"))); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			seen := rt.records()
			if len(seen) != len(wantPaths) {
				t.Fatalf("recorded %d requests, want %d", len(seen), len(wantPaths))
			}
			for i, rec := range seen {
				if rec.path != wantPaths[i] {
					t.Errorf("request %d path = %s, want %s", i, rec.path, wantPaths[i])
				}
				if !rec.ok {
					t.Errorf("request %s carried no deadline", rec.path)
					continue
				}
				if left := rec.deadline.Sub(rec.start); left <= c.min || left > c.max {
					t.Errorf("request %s deadline %s after start, want in (%s, %s]", rec.path, left, c.min, c.max)
				}
			}
		})
	}
}

// Spec: §4.7.9, §6.2 — a caller's own cancellation is returned as
// context.Canceled without the per-request deadline suffix.
func TestSigstoreKeyless_SignParentCancel(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t))
	provider.RequestTimeout = time.Minute
	provider.FulcioURL = hangingServer(t, hangHeaders).URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)

	elapsed, err := timedSign(ctx, provider)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "no response within") {
		t.Errorf("err = %q, want no per-request deadline suffix", err)
	}
	if elapsed > returnBound {
		t.Errorf("Sign took %s, want under %s", elapsed, returnBound)
	}
}

// Spec: §4.7.9, §6.2 — a caller's own deadline is returned as
// context.DeadlineExceeded without the per-request deadline suffix, because
// the suffix applies only while the parent context has no error.
func TestSigstoreKeyless_SignParentDeadline(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t))
	provider.RequestTimeout = time.Minute
	provider.FulcioURL = hangingServer(t, hangHeaders).URL
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	elapsed, err := timedSign(ctx, provider)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), "no response within") {
		t.Errorf("err = %q, want no per-request deadline suffix", err)
	}
	if elapsed > returnBound {
		t.Errorf("Sign took %s, want under %s", elapsed, returnBound)
	}
}

// Spec: §4.7.9, §6.2 — an HTTP error status fails the signing with the
// service prefix, the status, and the endpoint URL, and Fulcio's prefix
// appears once.
func TestSigstoreKeyless_SignErrorStatusNamesEndpoint(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	cases := []struct {
		service string
		opt     sh.ServerOpt
	}{
		{"fulcio", sh.WithFulcioFailure()},
		{"tsa", sh.WithTSAFailure()},
		{"rekor", sh.WithRekorFailure()},
	}
	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			t.Parallel()
			srv := h.FakeServer(t, c.opt)
			_, err := signer(t, h, srv).Sign(context.Background(), hashOf([]byte("body")))
			want := c.service + ": HTTP 503 from " + srv.URL
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
			if strings.Contains(err.Error(), "fulcio: fulcio:") {
				t.Errorf("err = %q repeats the fulcio prefix", err)
			}
		})
	}
}

// Spec: §4.7.9, §6.2 — a Fulcio response larger than the response bound is
// truncated and fails to decode, and no TSA or Rekor request follows.
func TestSigstoreKeyless_SignBoundsFulcioResponse(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	var count atomic.Int64
	provider := signer(t, h, h.FakeServer(t, sh.WithRequestCounter(&count)))
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"signedCertificateEmbeddedSct":"` + strings.Repeat("a", 5<<20) + `"}`))
	}))
	t.Cleanup(big.Close)
	provider.FulcioURL = big.URL

	_, err := provider.Sign(context.Background(), hashOf([]byte("body")))
	if err == nil || !strings.HasPrefix(err.Error(), "fulcio:") || !strings.Contains(err.Error(), "decode response from") {
		t.Fatalf("err = %v, want a fulcio: decode response from error", err)
	}
	if n := count.Load(); n != 0 {
		t.Errorf("TSA and Rekor saw %d requests, want 0", n)
	}
}
