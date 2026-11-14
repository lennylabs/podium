package audit

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/internal/testharness/auditharness"
)

// repeatReader is a Rand that yields the same byte forever. 0xff drives
// every backoff draw to its cap, and 0x80 to about half of it.
type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// received is one request a receiver recorded. lines holds the records the
// body carries; export holds the decoded request of an OTLP destination.
type received struct {
	path   string
	header http.Header
	lines  []jsonEvent
	export *collogspb.ExportLogsServiceRequest
}

// receiver is an HTTP or OTLP destination's test receiver. It records each request
// before respond answers it; respond gets the 1-based request number, and
// a nil respond answers 200.
type receiver struct {
	srv     *httptest.Server
	respond func(n int, w http.ResponseWriter, r *http.Request)

	mu       sync.Mutex
	reqs     []received
	inflight int
	maxIn    int
}

func newReceiver(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *receiver {
	t.Helper()
	rcv := &receiver{respond: respond}
	rcv.srv = httptest.NewServer(http.HandlerFunc(rcv.serve))
	t.Cleanup(rcv.srv.Close)
	return rcv
}

// newTLSReceiver starts a receiver that serves cfg, and that silences the
// handshake errors the refused-certificate cases provoke.
func newTLSReceiver(t *testing.T, cfg *tls.Config, respond func(n int, w http.ResponseWriter, r *http.Request)) *receiver {
	t.Helper()
	rcv := &receiver{respond: respond}
	rcv.srv = httptest.NewUnstartedServer(http.HandlerFunc(rcv.serve))
	rcv.srv.TLS = cfg
	rcv.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	rcv.srv.StartTLS()
	t.Cleanup(rcv.srv.Close)
	return rcv
}

func (rcv *receiver) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	got := received{path: r.URL.Path, header: r.Header.Clone()}
	var err error
	if r.Header.Get("Content-Type") == otlpContentType {
		got.export, got.lines, err = decodeExport(body)
	} else {
		got.lines, err = decodeNDJSON(body)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusTeapot)
		return
	}
	rcv.mu.Lock()
	rcv.reqs = append(rcv.reqs, got)
	n := len(rcv.reqs)
	rcv.inflight++
	rcv.maxIn = max(rcv.maxIn, rcv.inflight)
	rcv.mu.Unlock()
	defer func() {
		rcv.mu.Lock()
		rcv.inflight--
		rcv.mu.Unlock()
	}()
	if rcv.respond != nil {
		rcv.respond(n, w, r)
	}
}

// decodeNDJSON parses an HTTP destination's body, one record per line.
func decodeNDJSON(body []byte) ([]jsonEvent, error) {
	var lines []jsonEvent
	for _, line := range splitLines(body) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			return nil, err
		}
		lines = append(lines, je)
	}
	return lines, nil
}

// decodeExport parses an OTLP destination's body and the record each log
// record's body carries.
func decodeExport(body []byte) (*collogspb.ExportLogsServiceRequest, []jsonEvent, error) {
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, nil, err
	}
	var lines []jsonEvent
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				var je jsonEvent
				if err := json.Unmarshal([]byte(lr.GetBody().GetStringValue()), &je); err != nil {
					return nil, nil, err
				}
				lines = append(lines, je)
			}
		}
	}
	return &req, lines, nil
}

func (rcv *receiver) requests() []received {
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	return append([]received(nil), rcv.reqs...)
}

func (rcv *receiver) count() int { return len(rcv.requests()) }

// wait waits until the receiver has recorded n requests.
func (rcv *receiver) wait(t *testing.T, n int) {
	t.Helper()
	waitFor(t, func() bool { return rcv.count() >= n })
}

// hashes returns the distinct hashes of every recorded request, in arrival
// order. The receiver keeps one copy per hash, as §8.1 asks of a receiver.
func (rcv *receiver) hashes() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rcv.requests() {
		for _, l := range r.lines {
			if !seen[l.Hash] {
				seen[l.Hash] = true
				out = append(out, l.Hash)
			}
		}
	}
	return out
}

// sleepGate records each backoff sleep. With a non-nil release, a sleep
// waits for a value on release or for its context to end; with a nil
// release, it returns at once.
type sleepGate struct {
	release chan struct{}
	entered chan time.Duration

	mu     sync.Mutex
	delays []time.Duration
}

func stubSleepGate(t *testing.T, gated bool) *sleepGate {
	t.Helper()
	g := &sleepGate{entered: make(chan time.Duration, 1024)}
	if gated {
		g.release = make(chan struct{})
	}
	stubSleep(t, func(ctx context.Context, d time.Duration) error {
		g.mu.Lock()
		g.delays = append(g.delays, d)
		g.mu.Unlock()
		select {
		case g.entered <- d:
		default:
		}
		if g.release == nil {
			return nil
		}
		select {
		case <-g.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return g
}

func (g *sleepGate) recorded() []time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]time.Duration(nil), g.delays...)
}

// next waits for the next sleep to start.
func (g *sleepGate) next(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-g.entered:
		return d
	case <-time.After(5 * time.Second):
		t.Fatalf("no backoff sleep started within 5s")
		return 0
	}
}

// totals sums the observer's AddQueued deltas per kind and Dropped counts
// per "kind,reason".
func totals(obs *fakeObserver) (queued, dropped map[string]int) {
	queued, dropped = map[string]int{}, map[string]int{}
	for _, c := range obs.snapshot() {
		args := strings.Split(strings.TrimSuffix(c[strings.Index(c, "(")+1:], ")"), ",")
		n, _ := strconv.Atoi(args[len(args)-1])
		if strings.HasPrefix(c, "AddQueued") {
			queued[args[0]] += n
			continue
		}
		dropped[args[0]+","+args[1]] += n
	}
	return queued, dropped
}

// openStream opens value and closes the stream when the test ends.
func openStream(t *testing.T, value string, opts OpenOptions) *Stream {
	t.Helper()
	s, err := Open(context.Background(), value, opts)
	if err != nil {
		t.Fatalf("Open(%s): %v", value, err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func appendTarget(t *testing.T, s *Stream, target string) {
	t.Helper()
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded, Caller: "alice", Target: target}); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// closeWithin closes s on a context that ends after d. A forwarder reports
// the records its flush leaves undelivered only through the drop log, so
// Close returns nil even when the deadline cut the flush short.
func closeWithin(t *testing.T, s *Stream, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close = %v, want nil: the drop is reported only through the drop log", err)
	}
}

// Batches are cut at 500 records and at 1 MiB, and a partial batch is sent
// on the tick. Each body is NDJSON, one chained record per line.
//
// Spec: §8.3
func TestHTTPDest_PostsNDJSONBatches(t *testing.T) {
	fire := stubTicker(t)
	rcv := newReceiver(t, nil)
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{})

	appendN(t, s, 500)
	rcv.wait(t, 1)
	appendN(t, s, 3)
	fire()
	rcv.wait(t, 2)
	big := strings.Repeat("x", 400_000)
	for i := 0; i < 3; i++ {
		appendTarget(t, s, big)
	}
	rcv.wait(t, 3)
	fire()
	rcv.wait(t, 4)

	reqs := rcv.requests()
	sizes := []int{500, 3, 2, 1}
	prev := ""
	for i, r := range reqs {
		if len(r.lines) != sizes[i] {
			t.Errorf("request %d carried %d records, want %d", i+1, len(r.lines), sizes[i])
		}
		if ct := r.header.Get("Content-Type"); ct != "application/x-ndjson" {
			t.Errorf("request %d Content-Type = %q", i+1, ct)
		}
		if r.path != "/p" {
			t.Errorf("request %d path = %q", i+1, r.path)
		}
		for _, l := range r.lines {
			if l.Hash == "" || l.SchemaVersion != SchemaVersion || l.PrevHash != prev {
				t.Fatalf("request %d line breaks the chain: hash %q prev %q (want %q) schema %d", i+1, l.Hash, l.PrevHash, prev, l.SchemaVersion)
			}
			prev = l.Hash
		}
	}
}

// retrySleeps opens a stream to a receiver that answers the first request
// with refuse and later ones with 200, appends one record, fires the tick,
// and returns the backoff sleeps taken before the delivery.
func retrySleeps(t *testing.T, opts OpenOptions, refuse func(w http.ResponseWriter)) []time.Duration {
	t.Helper()
	fire := stubTicker(t)
	gate := stubSleepGate(t, false)
	rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			refuse(w)
		}
	})
	s := openStream(t, rcv.srv.URL+"/p", opts)
	appendN(t, s, 1)
	fire()
	rcv.wait(t, 2)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return gate.recorded()
}

// A redirect, a network error, and every status outside 2xx, 400, 413, and
// 422 are retried after a delay in [0, cap]. Retry-After lengthens the
// delay up to 60 s and never shortens it.
//
// Spec: §8.3
func TestHTTPDest_RetryClasses(t *testing.T) {
	half := OpenOptions{Rand: repeatReader(0x80)}
	target := newReceiver(t, nil)
	for _, code := range []int{302, 401, 403, 404, 408, 415, 429, 500, 503} {
		sleeps := retrySleeps(t, half, func(w http.ResponseWriter) {
			w.Header().Set("Location", target.srv.URL+"/elsewhere")
			w.WriteHeader(code)
		})
		if len(sleeps) != 1 || sleeps[0] <= 0 || sleeps[0] > time.Second {
			t.Errorf("status %d: sleeps %v, want one in (0, 1s]", code, sleeps)
		}
	}
	if n := target.count(); n != 0 {
		t.Errorf("the redirect target received %d request(s)", n)
	}

	frozen := clock.NewFrozen(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	opts := OpenOptions{Rand: repeatReader(0x80), Clock: frozen}
	retryAfter := func(v string) time.Duration {
		sleeps := retrySleeps(t, opts, func(w http.ResponseWriter) {
			if v != "" {
				w.Header().Set("Retry-After", v)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		if len(sleeps) != 1 {
			t.Fatalf("Retry-After %q: sleeps %v, want one", v, sleeps)
		}
		return sleeps[0]
	}
	control := retryAfter("")
	date := func(d time.Duration) string { return frozen.Now().Add(d).Format(http.TimeFormat) }
	cases := map[string]time.Duration{
		"5":                    5 * time.Second,
		"600":                  time.Minute,
		date(10 * time.Second): 10 * time.Second,
		date(10 * time.Minute): time.Minute,
		"0":                    control,
		date(-time.Minute):     control,
	}
	for v, want := range cases {
		if got := retryAfter(v); got != want {
			t.Errorf("Retry-After %q slept %v, want %v", v, got, want)
		}
	}
	if control <= 0 {
		t.Errorf("control draw = %v, want > 0", control)
	}
}

// The status alone decides an HTTP destination's outcome, so a 2xx whose
// body is longer than the read bound delivers the batch in one request.
//
// Spec: §8.3
func TestHTTPDest_OversizedSuccessBodyDelivers(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2*maxResponseBody))
	})
	obs := &fakeObserver{}
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	appendN(t, s, 2)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	queued, dropped := totals(obs)
	if queued["http"] != 0 || len(dropped) != 0 || rcv.count() != 1 {
		t.Fatalf("queued %v dropped %v after %d requests, want none after 1", queued, dropped, rcv.count())
	}
}

// A network error is retried with a backoff, and the record is counted as
// shutdown when the flush deadline passes.
//
// Spec: §8.3
func TestHTTPDest_NetworkErrorRetries(t *testing.T) {
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	obs := &fakeObserver{}
	s := openStream(t, "http://"+addr+"/p", OpenOptions{Observer: obs})
	appendN(t, s, 1)
	fire()
	if d := gate.next(t); d < 0 || d > time.Second {
		t.Fatalf("first backoff %v outside [0, 1s]", d)
	}
	closeWithin(t, s, 100*time.Millisecond)
	queued, dropped := totals(obs)
	if queued["http"] != 0 || dropped["http,shutdown"] != 1 {
		t.Fatalf("queued %v dropped %v, want 0 and one shutdown", queued, dropped)
	}
}

// The backoff cap starts at 1 s, doubles to 60 s whether or not the
// response carries Retry-After, and returns to 1 s after a delivery.
//
// Spec: §8.3
func TestHTTPDest_BackoffCapGrowsAndResets(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for _, withRetryAfter := range []bool{false, true} {
		fire := stubTicker(t)
		gate := stubSleepGate(t, false)
		rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
			if n <= len(want) || n == len(want)+2 {
				if withRetryAfter && n%2 == 0 {
					w.Header().Set("Retry-After", "1")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Rand: repeatReader(0xff)})
		appendN(t, s, 1)
		fire()
		rcv.wait(t, len(want)+1)
		appendN(t, s, 1)
		fire()
		rcv.wait(t, len(want)+3)
		got := gate.recorded()
		if len(got) != len(want)+1 {
			t.Fatalf("Retry-After %v: sleeps %v", withRetryAfter, got)
		}
		for i, d := range append(want, 1) {
			ceiling := d * time.Second
			if got[i] > ceiling || got[i] < ceiling-time.Millisecond {
				t.Errorf("Retry-After %v: sleep %d = %v, want within 1ms below %v", withRetryAfter, i+1, got[i], ceiling)
			}
		}
	}
}

// 400, 413, and 422 split the batch until the one refused record is
// dropped as rejected, and its siblings are delivered. A record over 1 MiB
// is counted as rejected at push and never sent.
//
// Spec: §8.3
func TestHTTPDest_SplitsAndDropsRefusedRecord(t *testing.T) {
	t.Parallel()
	for _, code := range []int{400, 413, 422} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Parallel()
			var rcv *receiver
			rcv = newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
				if poisoned(rcv.requests()[n-1]) {
					w.WriteHeader(code)
				}
			})
			obs := &fakeObserver{}
			s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
			for _, target := range []string{"0", "1", "poison", "3", "4"} {
				appendTarget(t, s, target)
			}
			appendTarget(t, s, strings.Repeat("x", maxRecordBytes))
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}
			queued, dropped := totals(obs)
			if queued["http"] != 0 || dropped["http,rejected"] != 2 || len(dropped) != 1 {
				t.Fatalf("queued %v dropped %v, want 0 and two rejected", queued, dropped)
			}
			if adds := strings.Count(strings.Join(obs.snapshot(), " "), "AddQueued(http,1)"); adds != 5 {
				t.Errorf("AddQueued(http,1) reported %d times, want 5", adds)
			}
			delivered := map[string]bool{}
			for _, r := range rcv.requests() {
				for _, l := range r.lines {
					if len(l.Target) > maxRecordBytes/2 {
						t.Fatalf("the oversized record was sent")
					}
					if !poisoned(r) {
						delivered[l.Target] = true
					}
				}
			}
			if len(delivered) != 4 || delivered["poison"] {
				t.Fatalf("delivered %v, want the four siblings", delivered)
			}
		})
	}
}

// poisoned reports whether a request carries the record targeted
// "poison", which the split test's receiver refuses.
func poisoned(r received) bool {
	for _, l := range r.lines {
		if l.Target == "poison" {
			return true
		}
	}
	return false
}

// A record that does not fit the queue bound is dropped as queue_full even
// on an empty queue, and it still reaches the file destination.
//
// Spec: §8.3, §13.12
func TestHTTPDest_RecordLargerThanBoundDropsOnEmptyQueue(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, nil)
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openStream(t, path+","+rcv.srv.URL+"/p", OpenOptions{Observer: obs, QueueMaxBytes: 10})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := obs.snapshot(); len(got) != 1 || got[0] != "Dropped(http,queue_full,1)" {
		t.Fatalf("observer calls = %v, want one queue_full drop", got)
	}
	if n := len(fileRecords(t, path)); n != 1 {
		t.Fatalf("file holds %d records, want 1", n)
	}
	if rcv.count() != 0 {
		t.Fatalf("the receiver got a request")
	}
}

// While a blocked receiver holds the queue, a record that does not fit the
// bound is chained, written to the file, and dropped as queue_full for the
// HTTP destination. The next delivered record's prev_hash names the dropped
// record, which the receiver never got, so the receiver's §8.1 gap check
// sees the loss.
//
// Spec: §8.1, §8.3
func TestHTTPDest_QueueFullDropsAfterChaining(t *testing.T) {
	fire := stubTicker(t)
	unblock := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	rcv := newReceiver(t, func(n int, _ http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			<-unblock
		}
	})
	t.Cleanup(release)
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openStream(t, path+","+rcv.srv.URL+"/p", OpenOptions{Observer: obs, QueueMaxBytes: 3000})

	appendTarget(t, s, strings.Repeat("a", 1000))
	fire()
	rcv.wait(t, 1)
	appendTarget(t, s, strings.Repeat("b", 2000))
	appendTarget(t, s, "c")
	if _, dropped := totals(obs); dropped["http,queue_full"] != 1 || len(dropped) != 1 {
		t.Fatalf("dropped %v, want one queue_full", dropped)
	}
	release()
	fire()
	rcv.wait(t, 2)

	file := fileRecords(t, path)
	if len(file) != 3 {
		t.Fatalf("file holds %d records, want 3", len(file))
	}
	dropped := file[1].Hash
	got := rcv.requests()[1].lines
	if len(got) != 1 || got[0].Hash != file[2].Hash {
		t.Fatalf("the second request carried %d records, want the third record", len(got))
	}
	if got[0].PrevHash != dropped {
		t.Fatalf("delivered prev_hash = %q, want the dropped record's hash %q", got[0].PrevHash, dropped)
	}
	for _, h := range rcv.hashes() {
		if h == dropped {
			t.Fatalf("the receiver holds the dropped record")
		}
	}
}

// A batch held across a backoff counts toward the bound, a tick during the
// backoff sends nothing, and close counts the held batch as shutdown when
// its context ends. The shutdown drop reaches the log once, through the
// drop log, and Shutdown writes no second line for it.
//
// Spec: §8.3, §13.12
func TestHTTPDest_HeldBatchCountsTowardBound(t *testing.T) {
	logs := captureLog(t)
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	flushCtx, cancelFlush := context.WithCancel(context.Background())
	defer cancelFlush()
	rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 2 {
			cancelFlush()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	obs := &fakeObserver{}
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs, QueueMaxBytes: 3000})
	target := strings.Repeat("x", 1000)
	appendTarget(t, s, target)
	appendTarget(t, s, target)
	fire()
	gate.next(t)
	appendTarget(t, s, target)
	fire()
	fire()
	if n := rcv.count(); n != 1 {
		t.Fatalf("receiver recorded %d requests during the backoff, want 1", n)
	}
	queued, dropped := totals(obs)
	if queued["http"] != 2 || dropped["http,queue_full"] != 1 {
		t.Fatalf("queued %v dropped %v, want 2 queued and one queue_full", queued, dropped)
	}
	if err := s.Close(flushCtx); err != nil {
		t.Fatalf("Close = %v, want nil: the drop is reported only through the drop log", err)
	}
	queued, dropped = totals(obs)
	if queued["http"] != 0 || dropped["http,shutdown"] != 2 {
		t.Fatalf("after Close queued %v dropped %v, want 0 and two shutdown", queued, dropped)
	}
	s.Shutdown()
	out := logs.String()
	if n := strings.Count(out, "audit: dropped"); n != 2 {
		t.Fatalf("log holds %d drop lines, want one queue_full and one shutdown: %q", n, out)
	}
	if n := strings.Count(out, "record(s) for destination "+rcv.srv.URL+"/p: shutdown"); n != 1 {
		t.Fatalf("log holds %d shutdown drop lines, want 1: %q", n, out)
	}
	if strings.Contains(out, "shutdown flush") {
		t.Fatalf("Shutdown logged the drop a second time: %q", out)
	}
}

// Close during a backoff stops the sender and sends the held batch ahead of
// the later records, in chain order, one request at a time, and counts
// nothing when every record is delivered.
//
// Spec: §8.3
func TestHTTPDest_CloseDuringBackoffFlushesHeldBatch(t *testing.T) {
	logs := captureLog(t)
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	obs := &fakeObserver{}
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	appendN(t, s, 3)
	fire()
	gate.next(t)
	appendN(t, s, 2)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 2 || len(reqs[1].lines) != 5 {
		t.Fatalf("requests = %d, want the refused batch and one flush of 5", len(reqs))
	}
	for i := 1; i < 5; i++ {
		if reqs[1].lines[i].PrevHash != reqs[1].lines[i-1].Hash {
			t.Fatalf("flush line %d is out of chain order", i)
		}
	}
	for i, l := range reqs[0].lines {
		if l.Hash != reqs[1].lines[i].Hash {
			t.Fatalf("the held batch was not resent first")
		}
	}
	if rcv.maxIn != 1 {
		t.Errorf("%d requests were in flight at once", rcv.maxIn)
	}
	if _, dropped := totals(obs); len(dropped) != 0 {
		t.Errorf("a delivered flush counted drops: %v", dropped)
	}
	if strings.Contains(logs.String(), "dropped") {
		t.Errorf("a delivered flush wrote a drop line: %s", logs.String())
	}
}

// Close sends every queued record and reports nothing when the flush
// delivers them all. When its context ends part-way through the flush, the
// batches already delivered stay delivered and the rest are counted as
// shutdown in one drop line. Either way the queue-depth deltas sum to 0.
//
// Spec: §8.3
func TestHTTPDest_CloseFlushesThenCountsShutdown(t *testing.T) {
	t.Run("delivers everything", func(t *testing.T) {
		logs := captureLog(t)
		stubTicker(t)
		rcv := newReceiver(t, nil)
		obs := &fakeObserver{}
		s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
		appendN(t, s, 3)
		if err := s.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if n := len(rcv.hashes()); n != 3 {
			t.Fatalf("receiver holds %d records, want 3", n)
		}
		queued, _ := totals(obs)
		if queued["http"] != 0 {
			t.Fatalf("AddQueued deltas sum to %d, want 0", queued["http"])
		}
		if calls := strings.Join(obs.snapshot(), " "); strings.Contains(calls, "Dropped") {
			t.Fatalf("a full drain reported a drop: %s", calls)
		}
		if strings.Contains(logs.String(), "audit: dropped") {
			t.Fatalf("a full drain wrote a drop line: %s", logs.String())
		}
	})

	t.Run("deadline ends the flush", func(t *testing.T) {
		logs := captureLog(t)
		fire := stubTicker(t)
		gate := stubSleepGate(t, true)
		flushCtx, cancelFlush := context.WithCancel(context.Background())
		defer cancelFlush()
		// The first request holds the sender in a backoff, so the whole
		// queue is left for Close: the flush delivers one full batch, and
		// its second request ends the flush's context.
		rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
			switch n {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
			case 3:
				cancelFlush()
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		obs := &fakeObserver{}
		s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
		appendN(t, s, 1)
		fire()
		gate.next(t)
		appendN(t, s, maxBatchRecords+100)
		if err := s.Close(flushCtx); err != nil {
			t.Fatalf("Close = %v, want nil: the drop is reported only through the drop log", err)
		}
		reqs := rcv.requests()
		if len(reqs) != 3 || len(reqs[1].lines) != maxBatchRecords {
			t.Fatalf("requests = %d, want the refused one, a delivered full batch, and the refused remainder", len(reqs))
		}
		queued, dropped := totals(obs)
		if queued["http"] != 0 || dropped["http,shutdown"] != 101 || len(dropped) != 1 {
			t.Fatalf("queued %v dropped %v, want 0 and 101 shutdown", queued, dropped)
		}
		if n := strings.Count(logs.String(), "audit: dropped 101 record(s) for destination "+rcv.srv.URL+"/p: shutdown"); n != 1 {
			t.Fatalf("log holds %d shutdown drop lines for 101 records, want 1: %s", n, logs.String())
		}
	})
}

// Against a receiver that refuses throughout, Close during a backoff
// flushes once, waits out the retry until its context is cancelled, and
// then counts the held batch and the later records as shutdown.
//
// Spec: §8.3
func TestHTTPDest_CloseDuringBackoffCountsHeldBatch(t *testing.T) {
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	rcv := newReceiver(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	obs := &fakeObserver{}
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	appendN(t, s, 3)
	fire()
	gate.next(t)
	appendN(t, s, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx) }()
	gate.next(t)
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v before its context was cancelled", err)
	default:
	}
	if n := rcv.count(); n != 2 {
		t.Fatalf("receiver recorded %d requests, want the refused batch and the refused flush", n)
	}
	cancel()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v, want nil: the drop is reported only through the drop log", err)
	}
	if got := rcv.requests()[1].lines; len(got) != 5 {
		t.Fatalf("the flush sent %d records, want the held three and the later two", len(got))
	}
	queued, dropped := totals(obs)
	if queued["http"] != 0 || dropped["http,shutdown"] != 5 || len(dropped) != 1 {
		t.Fatalf("queued %v dropped %v, want 0 and five shutdown", queued, dropped)
	}
}

// Every request takes its deadline from requestContext with 10 s. A request
// that times out while the sender runs is retried with the same batch, and
// Close interrupts an in-flight request, resends the batch, and returns
// once its own context ends.
//
// Spec: §8.3
func TestHTTPDest_HungReceiverIsBoundedByCloseDeadline(t *testing.T) {
	hung := func(t *testing.T) *receiver {
		stop := make(chan struct{})
		rcv := newReceiver(t, func(_ int, _ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-stop:
			}
		})
		t.Cleanup(func() { close(stop) })
		return rcv
	}
	var mu sync.Mutex
	var deadlines []time.Duration
	record := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		deadlines = append(deadlines, d)
	}

	t.Run("timeout", func(t *testing.T) {
		fire := stubTicker(t)
		gate := stubSleepGate(t, true)
		stubRequestContext(t, func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
			record(d)
			return context.WithTimeout(parent, 20*time.Millisecond)
		})
		rcv := hung(t)
		obs := &fakeObserver{}
		s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
		appendN(t, s, 1)
		fire()
		gate.next(t)
		gate.release <- struct{}{}
		rcv.wait(t, 2)
		reqs := rcv.requests()
		if reqs[0].lines[0].Hash != reqs[1].lines[0].Hash {
			t.Fatalf("the retry carried a different batch")
		}
		closeWithin(t, s, 100*time.Millisecond)
		if _, dropped := totals(obs); dropped["http,shutdown"] != 1 {
			t.Fatalf("dropped %v, want one shutdown", dropped)
		}
	})

	t.Run("close", func(t *testing.T) {
		fire := stubTicker(t)
		stubSleepGate(t, true)
		stubRequestContext(t, func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
			record(d)
			return context.WithTimeout(parent, d)
		})
		rcv := hung(t)
		obs := &fakeObserver{}
		s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
		appendN(t, s, 1)
		fire()
		rcv.wait(t, 1)
		appendN(t, s, 2)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closed := make(chan error, 1)
		go func() { closed <- s.Close(ctx) }()
		rcv.wait(t, 2)
		cancel()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatalf("Close = %v, want nil: the drop is reported only through the drop log", err)
			}
		case <-time.After(time.Second):
			t.Fatalf("Close did not return within 1s of its cancellation")
		}
		queued, dropped := totals(obs)
		if queued["http"] != 0 || dropped["http,shutdown"] != 3 {
			t.Fatalf("queued %v dropped %v, want 0 and three shutdown", queued, dropped)
		}
		if got := rcv.requests()[1].lines; len(got) != 3 {
			t.Fatalf("the flush sent %d records, want the held one and the later two", len(got))
		}
	})

	mu.Lock()
	defer mu.Unlock()
	for _, d := range deadlines {
		if d != 10*time.Second {
			t.Fatalf("a request took deadline %v, want 10s", d)
		}
	}
}

// The failure log names the destination without its query, once at the
// first failure, at most once a minute while failing, and once at each
// transition between failing and delivering.
//
// Spec: §8.3
func TestHTTPDest_FailureLogCadence(t *testing.T) {
	logs := captureLog(t)
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	frozen := clock.NewFrozen(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n <= 3 || n == 5 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	s := openStream(t, rcv.srv.URL+"/p?tok=s3cr3tvalue", OpenOptions{Clock: frozen})
	lines := func() []string {
		out := strings.TrimSpace(logs.String())
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
	appendN(t, s, 1)
	fire()
	gate.next(t)
	gate.release <- struct{}{}
	gate.next(t)
	if n := len(lines()); n != 1 {
		t.Fatalf("after two refusals in one minute: %d lines, want 1", n)
	}
	frozen.Advance(time.Minute)
	gate.release <- struct{}{}
	gate.next(t)
	if n := len(lines()); n != 2 {
		t.Fatalf("after a refusal a minute later: %d lines, want 2", n)
	}
	gate.release <- struct{}{}
	waitFor(t, func() bool { return len(lines()) == 3 })
	appendN(t, s, 1)
	fire()
	gate.next(t)
	got := lines()
	if len(got) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(got), logs.String())
	}
	name := rcv.srv.URL + "/p"
	for i, want := range []string{"status 503", "status 503", "recovered", "status 503"} {
		if !strings.Contains(got[i], name) || !strings.Contains(got[i], want) {
			t.Errorf("line %d %q does not name %s and %q", i+1, got[i], name, want)
		}
	}
	if strings.Contains(logs.String(), "tok") || strings.Contains(logs.String(), "s3cr3tvalue") {
		t.Errorf("the log carries the query: %s", logs.String())
	}
}

// The failure line and the shutdown drop line name the destination by its
// scheme, host, and path. The query, which can carry a credential, appears
// in neither. The request context the hook returns has already expired, so
// the attempt fails before any dial and the test needs no resolvable host.
//
// Spec: §8.3
func TestHTTPDest_FailureLogNamesRedactedEndpoint(t *testing.T) {
	logs := captureLog(t)
	fire := stubTicker(t)
	gate := stubSleepGate(t, true)
	stubRequestContext(t, func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
		return context.WithDeadline(parent, time.Time{})
	})
	s := openStream(t, "http://h/p?tok=secret", OpenOptions{})
	appendN(t, s, 1)
	fire()
	gate.next(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "audit: delivery to http://h/p failed") {
		t.Fatalf("the failure line does not name http://h/p: %q", out)
	}
	if !strings.Contains(out, "for destination http://h/p: shutdown") {
		t.Fatalf("the drop line does not name http://h/p: %q", out)
	}
	if strings.Contains(out, "tok") || strings.Contains(out, "secret") {
		t.Fatalf("the log carries the query: %q", out)
	}
}

// The headers file's headers are sent, a receiver that requires a client
// certificate accepts the configured one and refuses a stream without it,
// and a receiver certificate the client does not trust is never sent a
// request, whether or not the CA file is set.
//
// Spec: §8.3, §13.12
func TestHTTPDest_HeadersAndTLS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca := auditharness.NewCA(t)
	certPEM, keyPEM := ca.Issue(t)
	caFile := auditharness.WriteFile(t, dir, "ca.pem", ca.PEM)
	certFile := auditharness.WriteFile(t, dir, "client.crt", certPEM)
	keyFile := auditharness.WriteFile(t, dir, "client.key", keyPEM)
	headersFile := auditharness.WriteFile(t, dir, "headers", []byte("X-Audit-Token: zq7k\n"))

	mtls := ca.ServerTLS(t)
	mtls.ClientAuth = tls.RequireAndVerifyClientCert
	mtls.ClientCAs = ca.Pool()
	rcv := newTLSReceiver(t, mtls, nil)

	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{CAFile: caFile, CertFile: certFile, KeyFile: keyFile, HeadersFile: headersFile})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close with the client certificate: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 || reqs[0].header.Get("X-Audit-Token") != "zq7k" {
		t.Fatalf("receiver requests = %+v, want one carrying the header", reqs)
	}

	refused := func(name, url string, opts OpenOptions, rcv *receiver) {
		t.Run(name, func(t *testing.T) {
			obs := &fakeObserver{}
			opts.Observer = obs
			before := rcv.count()
			s := openStream(t, url, opts)
			appendN(t, s, 1)
			closeWithin(t, s, 300*time.Millisecond)
			if _, dropped := totals(obs); dropped["http,shutdown"] != 1 {
				t.Fatalf("dropped %v, want one shutdown", dropped)
			}
			if rcv.count() != before {
				t.Fatalf("the handler received a request")
			}
		})
	}
	refused("no client certificate", rcv.srv.URL+"/p", OpenOptions{CAFile: caFile}, rcv)
	other := newTLSReceiver(t, auditharness.NewCA(t).ServerTLS(t), nil)
	refused("untrusted CA with CAFile set", other.srv.URL+"/p", OpenOptions{CAFile: caFile}, other)
	// A nil configuration serves httptest's own certificate, which no
	// system trust store holds.
	sysRcv := newTLSReceiver(t, nil, nil)
	refused("untrusted certificate with CAFile unset", sysRcv.srv.URL+"/p", OpenOptions{}, sysRcv)
}

// A blank line and a comment line in the headers file add no header.
//
// Spec: §13.12
func TestHeadersFile_ParseRules(t *testing.T) {
	t.Parallel()
	headers := auditharness.WriteFile(t, t.TempDir(), "headers", []byte("\n# comment: zq7k\n  \nX-Audit-Token: t1\r\n"))
	rcv := newReceiver(t, nil)
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{HeadersFile: headers})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 || reqs[0].header.Get("X-Audit-Token") != "t1" {
		t.Fatalf("requests = %+v, want one carrying X-Audit-Token: t1", reqs)
	}
	for name := range reqs[0].header {
		if strings.HasPrefix(name, "#") {
			t.Fatalf("a comment line became header %q", name)
		}
	}
}

// After Close, an Append still chains to the file and each network
// destination counts the record as shutdown without queueing it.
//
// Spec: §8.3
func TestStream_AppendAfterCloseCountsShutdown(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, nil)
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openStream(t, path+","+rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	appendN(t, s, 1)
	if got := obs.snapshot(); len(got) != 1 || got[0] != "Dropped(http,shutdown,1)" {
		t.Fatalf("observer calls = %v, want one shutdown drop", got)
	}
	if n := len(fileRecords(t, path)); n != 1 {
		t.Fatalf("file holds %d records, want 1", n)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Appends that race Close never panic, and every record in the file is
// delivered or counted as shutdown. It must pass under go test -race
// ./pkg/audit/.
//
// Spec: §8.3
func TestStream_AppendConcurrentWithCloseLosesNothing(t *testing.T) {
	defer goleak.VerifyNone(t)
	rcv := &receiver{}
	rcv.srv = httptest.NewServer(http.HandlerFunc(rcv.serve))
	defer rcv.srv.Close()
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s, err := Open(context.Background(), path+","+rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				errs <- s.Append(context.Background(), Event{Type: EventArtifactLoaded, Target: fmt.Sprint(g, i)})
			}
		}()
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	queued, dropped := totals(obs)
	if queued["http"] != 0 {
		t.Fatalf("AddQueued deltas sum to %d", queued["http"])
	}
	if got, want := len(rcv.hashes())+dropped["http,shutdown"], len(fileRecords(t, path)); got != want || want != 400 {
		t.Fatalf("delivered plus shutdown = %d, file holds %d, want 400", got, want)
	}
}

// retryAfter reads seconds and HTTP-dates against the injected clock, caps
// them at 60 s, and reads an absent, past, negative, or unparseable value
// as 0.
//
// Spec: §8.3
func TestForwarder_RetryAfterValues(t *testing.T) {
	t.Parallel()
	frozen := clock.NewFrozen(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	f := &forwarder{clock: frozen}
	cases := map[string]time.Duration{
		"":            0,
		"garbage":     0,
		"-3":          0,
		" 7 ":         7 * time.Second,
		"61":          time.Minute,
		"99999999999": time.Minute,
		frozen.Now().Add(30 * time.Second).Format(http.TimeFormat): 30 * time.Second,
		frozen.Now().Add(-time.Second).Format(http.TimeFormat):     0,
	}
	for v, want := range cases {
		if got := f.retryAfter(v); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", v, got, want)
		}
	}
}

// A failing Rand yields the cap, so a broken source never removes the
// backoff.
//
// Spec: §8.3
func TestForwarder_DrawWithFailingRandIsTheCap(t *testing.T) {
	t.Parallel()
	f := &forwarder{rand: errReader{}}
	if got := f.draw(4 * time.Second); got != 4*time.Second {
		t.Fatalf("draw = %v, want the cap", got)
	}
	f.rand = repeatReader(0)
	if got := f.draw(4 * time.Second); got != 0 {
		t.Fatalf("draw of zero bytes = %v, want 0", got)
	}
}

// A network error is described by its cause, without the request URL and
// its query.
//
// Spec: §8.3
func TestFailureText_OmitsTheURL(t *testing.T) {
	t.Parallel()
	err := &url.Error{Op: "Post", URL: "http://h/p?tok=s3cr3tvalue", Err: errors.New("connection refused")}
	if got := failureText(sendResult{}, err); got != "connection refused" {
		t.Fatalf("failureText = %q", got)
	}
	if got := failureText(sendResult{status: 502}, nil); got != "status 502" {
		t.Fatalf("failureText = %q", got)
	}
}
