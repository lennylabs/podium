package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
)

// fakeObserver records every Observer call.
type fakeObserver struct {
	mu    sync.Mutex
	calls []string
}

func (o *fakeObserver) AddQueued(kind string, delta int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, fmt.Sprintf("AddQueued(%s,%d)", kind, delta))
}

func (o *fakeObserver) Dropped(kind, reason string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, fmt.Sprintf("Dropped(%s,%s,%d)", kind, reason, n))
}

func (o *fakeObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.calls...)
}

// fakeQueue is a recording queueDest that stands in for a network
// destination. Each push reports one queued record through the stream, as a
// forwarder does. A non-nil hold blocks push until it is closed, and a
// non-nil closeBlock makes close wait for ctx.
type fakeQueue struct {
	s    *Stream
	kind destKind
	hold chan struct{}

	mu       sync.Mutex
	records  []record
	startCtx context.Context
	closes   int
	closed   chan struct{}
	blocking bool
}

func newFakeQueue(s *Stream, kind destKind) *fakeQueue {
	q := &fakeQueue{s: s, kind: kind, closed: make(chan struct{})}
	s.queues = append(s.queues, q)
	return q
}

func (q *fakeQueue) push(r record) {
	if q.hold != nil {
		<-q.hold
	}
	q.mu.Lock()
	q.records = append(q.records, r)
	q.mu.Unlock()
	q.s.queued(q.kind, 1)
}

func (q *fakeQueue) start(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.startCtx = ctx
}

func (q *fakeQueue) close(ctx context.Context) error {
	q.mu.Lock()
	q.closes++
	blocking := q.blocking
	q.mu.Unlock()
	if blocking {
		<-ctx.Done()
		return ctx.Err()
	}
	close(q.closed)
	return nil
}

func (q *fakeQueue) got() []record {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]record(nil), q.records...)
}

// failWriter fails every Write with err.
type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

// lockedBuffer is a bytes.Buffer safe for concurrent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog redirects the standard logger for the test. Callers do not
// call t.Parallel.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf
}

// fileRecords decodes every line of the file at path.
func fileRecords(t *testing.T, path string) []jsonEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []jsonEvent
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, je)
	}
	return out
}

func openFileStream(t *testing.T, path string, opts OpenOptions) *Stream {
	t.Helper()
	s, err := Open(context.Background(), path, opts)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

func appendN(t *testing.T, s *Stream, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded, Caller: "alice", Target: fmt.Sprint(i)}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
}

// Every record the stream chains carries schema_version 1, which survives
// the JSON round trip and enters the hash, so editing it breaks the chain.
//
// Spec: §8.1, §8.6
func TestFileSink_SchemaVersionRoundTrips(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	appendN(t, s, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v, want 1 in %s", raw["schema_version"], data)
	}
	if got := eventFromJSON(fileRecords(t, path)[0]).SchemaVersion; got != 1 {
		t.Fatalf("eventFromJSON SchemaVersion = %d, want 1", got)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	edited := bytes.Replace(data, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(context.Background()); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("Verify after editing schema_version = %v, want ErrChainBroken", err)
	}
}

// A stream opened over an existing file continues the chain from the file's
// last record.
//
// Spec: §8.3, §8.6
func TestStream_RecoversHeadFromFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	first := openFileStream(t, path, OpenOptions{})
	appendN(t, first, 2)
	last := fileRecords(t, path)[1].Hash

	second := openFileStream(t, path, OpenOptions{})
	appendN(t, second, 1)
	recs := fileRecords(t, path)
	if recs[2].PrevHash != last {
		t.Fatalf("prev_hash = %q, want the file's last hash %q", recs[2].PrevHash, last)
	}
}

// Without a file destination the stream holds the head itself and starts
// it empty.
//
// Spec: §8.3, §8.6
func TestStream_NoFileStartsEmptyHead(t *testing.T) {
	t.Parallel()
	s := newStream(OpenOptions{}.withDefaults())
	q := newFakeQueue(s, kindHTTP)
	appendN(t, s, 3)
	recs := q.got()
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	if recs[0].event.PrevHash != "" {
		t.Fatalf("first prev_hash = %q, want empty", recs[0].event.PrevHash)
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].event.PrevHash != recs[i-1].event.Hash {
			t.Fatalf("record %d does not link to record %d", i, i-1)
		}
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Fatalf("Verify without a file = %v, want nil", err)
	}
	if s.File() != nil {
		t.Fatalf("File() = non-nil for a stream with no file destination")
	}
}

// Concurrent appends reach the file and another destination in one order.
// It must pass under go test -race ./pkg/audit/.
//
// Spec: §8.3, §8.6
func TestStream_ConcurrentAppendKeepsOneOrder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	q := newFakeQueue(s, kindHTTP)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded, Caller: fmt.Sprintf("w%d", w)}); err != nil {
					t.Errorf("Append: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	recs, file := q.got(), fileRecords(t, path)
	if len(recs) != 200 || len(file) != 200 {
		t.Fatalf("got %d forwarded and %d file records, want 200 each", len(recs), len(file))
	}
	for i, r := range recs {
		if r.event.Hash != file[i].Hash {
			t.Fatalf("record %d: forwarded order differs from file order", i)
		}
		if i > 0 && r.event.PrevHash != recs[i-1].event.Hash {
			t.Fatalf("record %d does not link to the previous record", i)
		}
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// A failed file write is returned, counted once for the file destination,
// and handed to no other destination, and the head does not advance.
//
// Spec: §8.3, §8.6
func TestStream_FileWriteErrorIsCountedAndReturned(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openFileStream(t, path, OpenOptions{Observer: obs})
	q := newFakeQueue(s, kindHTTP)
	appendN(t, s, 1)
	written := fileRecords(t, path)[0].Hash

	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); err == nil {
		t.Fatalf("Append to an unwritable file returned nil")
	}
	want := []string{"AddQueued(http,1)", "Dropped(file,write_error,1)"}
	if got := obs.snapshot(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("observer calls = %v, want %v", got, want)
	}
	if n := len(q.got()); n != 1 {
		t.Fatalf("other destination received %d records, want 1", n)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	appendN(t, s, 1)
	recs := fileRecords(t, path)
	if len(recs) != 2 || recs[1].PrevHash != written {
		t.Fatalf("after recovery, prev_hash = %q, want %q", recs[1].PrevHash, written)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// AsSink keeps a nil stream a nil interface, and the lifecycle methods are
// safe on a nil stream.
//
// Spec: §8.3, §9.1
func TestAsSink_NilStreamIsNilInterface(t *testing.T) {
	t.Parallel()
	var nilStream *Stream
	if AsSink(nilStream) != nil {
		t.Fatalf("AsSink(nil) is a non-nil interface")
	}
	s := openFileStream(t, filepath.Join(t.TempDir(), "a.log"), OpenOptions{})
	if AsSink(s) == nil {
		t.Fatalf("AsSink of an open stream is nil")
	}
	if nilStream.File() != nil {
		t.Fatalf("nil File() is non-nil")
	}
	if err := nilStream.Close(context.Background()); err != nil {
		t.Fatalf("nil Close = %v", err)
	}
	nilStream.Shutdown()
	nilStream.Seal()
}

// A failed stdout write is counted with write_error and never returned.
//
// Spec: §8.3, §8.6
func TestStream_StdoutFailureIsNeverReturned(t *testing.T) {
	t.Parallel()
	epipe := failWriter{err: syscall.EPIPE}
	t.Run("file and stdout", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "audit.log")
		obs := &fakeObserver{}
		s := openFileStream(t, path, OpenOptions{Observer: obs})
		s.stdout = epipe
		appendN(t, s, 1)
		if n := len(fileRecords(t, path)); n != 1 {
			t.Fatalf("file holds %d records, want 1", n)
		}
		if got := obs.snapshot(); len(got) != 1 || got[0] != "Dropped(stdout,write_error,1)" {
			t.Fatalf("observer calls = %v", got)
		}
	})
	t.Run("stdout alone", func(t *testing.T) {
		t.Parallel()
		obs := &fakeObserver{}
		s := newStream(OpenOptions{Observer: obs}.withDefaults())
		s.stdout = epipe
		appendN(t, s, 1)
		if got := obs.snapshot(); len(got) != 1 || got[0] != "Dropped(stdout,write_error,1)" {
			t.Fatalf("observer calls = %v", got)
		}
	})
}

// blockingWriter blocks each Write until release is closed and records the
// bytes.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	buf     lockedBuffer
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.buf.Write(p)
}

// Every network queue receives the record before the stdout write, whatever
// order the list names them in. It must pass under go test -race
// ./pkg/audit/.
//
// Spec: §8.3
func TestStream_QueuesNetworkCopiesBeforeStdoutWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openFileStream(t, path, OpenOptions{Observer: obs})
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	s.stdout = w
	newFakeQueue(s, kindHTTP)
	done := make(chan error, 1)
	go func() { done <- s.Append(context.Background(), Event{Type: EventArtifactLoaded}) }()
	<-w.entered
	if got := obs.snapshot(); len(got) != 1 || got[0] != "AddQueued(http,1)" {
		t.Fatalf("while stdout blocks, observer calls = %v, want AddQueued(http,1)", got)
	}
	if n := len(fileRecords(t, path)); n != 1 {
		t.Fatalf("while stdout blocks, file holds %d records, want 1", n)
	}
	close(w.release)
	if err := <-done; err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	line := strings.TrimSuffix(w.buf.String(), "\n")
	data, _ := os.ReadFile(path)
	if line != strings.TrimSuffix(string(data), "\n") {
		t.Fatalf("stdout line %q differs from the file line %q", line, data)
	}
}

// Every drop reaches the observer, and the log carries at most one line per
// destination and reason per minute, naming a URL destination without its
// query.
//
// Spec: §8.3
func TestStream_DropFunnelCountsAndLogs(t *testing.T) {
	skipIfRoot(t)
	logs := captureLog(t)
	frozen := clock.NewFrozen(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{Clock: frozen})
	u, err := url.Parse("http://collector.example/x?tok=s3cr3tvalue")
	if err != nil {
		t.Fatal(err)
	}
	name := redactedURL(u)

	s.dropped(kindHTTP, name, ReasonQueueFull, 1)
	s.dropped(kindHTTP, name, ReasonQueueFull, 1)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); err == nil {
		t.Fatalf("Append to an unwritable file returned nil")
	}
	frozen.Advance(time.Minute)
	s.dropped(kindHTTP, name, ReasonQueueFull, 1)
	s.dropped(kindHTTP, name, ReasonQueueFull, 0)

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d log lines, want 3:\n%s", len(lines), logs.String())
	}
	for _, l := range []string{lines[0], lines[2]} {
		if !strings.Contains(l, "http://collector.example/x") || !strings.Contains(l, ReasonQueueFull) {
			t.Errorf("line %q does not name the destination and reason", l)
		}
	}
	if !strings.Contains(lines[2], "dropped 2 ") {
		t.Errorf("line %q does not carry the suppressed drop", lines[2])
	}
	if !strings.Contains(lines[1], path) || !strings.Contains(lines[1], ReasonWriteError) {
		t.Errorf("line %q does not name the file and write_error", lines[1])
	}
	if strings.Contains(logs.String(), "tok") || strings.Contains(logs.String(), "s3cr3tvalue") {
		t.Errorf("log carries the query: %s", logs.String())
	}

	// Bursts whose suppressed drops are followed by no further drop for
	// their key still reach the log at Shutdown, one line per key in key
	// order, and a key with nothing pending writes no extra line.
	s.dropped(kindHTTP, name, ReasonRejected, 1)
	s.dropped(kindHTTP, name, ReasonRejected, 4)
	s.dropped(kindHTTP, name, ReasonRejected, 5)
	s.dropped(kindStdout, "stdout", ReasonWriteError, 1)
	s.dropped(kindStdout, "stdout", ReasonWriteError, 1)
	s.dropped(kindHTTP, name, ReasonShutdown, 2)
	s.dropped(kindHTTP, name, ReasonShutdown, 3)
	before := len(strings.Split(strings.TrimSpace(logs.String()), "\n"))
	if before != 6 {
		t.Fatalf("got %d log lines before Shutdown, want 6:\n%s", before, logs.String())
	}
	s.Shutdown()
	lines = strings.Split(strings.TrimSpace(logs.String()), "\n")
	wantTail := []struct {
		count, dest, reason string
	}{
		{"dropped 9 ", name, ReasonRejected},
		{"dropped 3 ", name, ReasonShutdown},
		{"dropped 1 ", "destination stdout", ReasonWriteError},
	}
	if len(lines) != before+len(wantTail) {
		t.Fatalf("got %d log lines after Shutdown, want %d:\n%s", len(lines), before+len(wantTail), logs.String())
	}
	for i, w := range wantTail {
		l := lines[before+i]
		if !strings.Contains(l, w.count) || !strings.Contains(l, w.dest) || !strings.HasSuffix(l, w.reason) {
			t.Errorf("Shutdown line %d = %q, want %q for %s: %s", i, l, w.count, w.dest, w.reason)
		}
	}
	s.drops.flush()
	if got := len(strings.Split(strings.TrimSpace(logs.String()), "\n")); got != len(lines) {
		t.Errorf("a second flush wrote a line; log:\n%s", logs.String())
	}

	obs := &fakeObserver{}
	counted := newStream(OpenOptions{Observer: obs, Clock: frozen}.withDefaults())
	counted.dropped(kindHTTP, name, ReasonQueueFull, 1)
	counted.dropped(kindHTTP, name, ReasonQueueFull, 1)
	counted.queued(kindHTTP, 0)
	want := []string{"Dropped(http,queue_full,1)", "Dropped(http,queue_full,1)"}
	if got := obs.snapshot(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("observer calls = %v, want %v", got, want)
	}
}

// Seal waits for an Append that holds the stream lock to finish its
// hand-off, and every later Append returns ErrSealed.
//
// Spec: §6.8, §8.3
func TestStream_SealWaitsForAppendInProgress(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	obs := &fakeObserver{}
	s := openFileStream(t, path, OpenOptions{Observer: obs})
	q := newFakeQueue(s, kindHTTP)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := obs.snapshot(); len(got) != 0 {
		t.Fatalf("Close on an empty queue recorded %v", got)
	}
	q.hold = make(chan struct{})
	appended := make(chan error, 1)
	go func() { appended <- s.Append(context.Background(), Event{Type: EventArtifactLoaded}) }()
	waitFor(t, func() bool { return len(fileRecords(t, path)) == 1 })

	sealed := make(chan struct{})
	go func() { s.Seal(); close(sealed) }()
	select {
	case <-sealed:
		t.Fatalf("Seal returned while an Append held the stream lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(q.hold)
	<-sealed
	if err := <-appended; err != nil {
		t.Fatalf("Append in progress = %v, want nil", err)
	}
	if got := obs.snapshot(); len(got) != 1 || got[0] != "AddQueued(http,1)" {
		t.Fatalf("observer calls = %v", got)
	}
	before, _ := os.ReadFile(path)
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); !errors.Is(err, ErrSealed) {
		t.Fatalf("Append after Seal = %v, want ErrSealed", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(obs.snapshot()) != 1 {
		t.Fatalf("a sealed Append changed the file or the observer")
	}
	s.Seal()
}

// Shutdown flushes every network destination and then seals the stream.
//
// Spec: §8.3
func TestStream_ShutdownSealsAfterFlush(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	q := newFakeQueue(s, kindHTTP)
	appendN(t, s, 2)
	s.Shutdown()
	select {
	case <-q.closed:
	default:
		t.Fatalf("Shutdown did not close the network destination")
	}
	before, _ := os.ReadFile(path)
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); !errors.Is(err, ErrSealed) {
		t.Fatalf("Append after Shutdown = %v, want ErrSealed", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("a sealed Append changed the file")
	}
}

// Shutdown logs a flush error and still seals the stream.
//
// Spec: §8.3
func TestStream_ShutdownLogsFlushError(t *testing.T) {
	logs := captureLog(t)
	s := newStream(OpenOptions{}.withDefaults())
	q := newFakeQueue(s, kindOTLP)
	q.blocking = true
	start := time.Now()
	s.Shutdown()
	if elapsed := time.Since(start); elapsed > ShutdownTimeout+2*time.Second {
		t.Fatalf("Shutdown took %v, beyond the flush budget", elapsed)
	}
	if !strings.Contains(logs.String(), "audit: shutdown flush") {
		t.Fatalf("Shutdown did not log the flush error: %q", logs.String())
	}
	if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); !errors.Is(err, ErrSealed) {
		t.Fatalf("Append after Shutdown = %v, want ErrSealed", err)
	}
}

// Close flushes the network destinations concurrently, cancels the stream's
// context, and returns the first result on every later call.
//
// Spec: §8.3
func TestStream_CloseFlushesDestinationsConcurrently(t *testing.T) {
	t.Parallel()
	s := newStream(OpenOptions{}.withDefaults())
	hung := newFakeQueue(s, kindHTTP)
	hung.blocking = true
	ok := newFakeQueue(s, kindOTLP)
	s.startQueues(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closed := make(chan error, 1)
	start := time.Now()
	go func() { closed <- s.Close(ctx) }()
	select {
	case <-ok.closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("the healthy destination waited for the hung one")
	}
	first := <-closed
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %v with a 1s deadline", elapsed)
	}
	if !errors.Is(first, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want the hung destination's deadline error", first)
	}
	if hung.startCtx.Err() == nil {
		t.Fatalf("Close did not cancel the stream's context")
	}
	if again := s.Close(context.Background()); !errors.Is(again, context.DeadlineExceeded) {
		t.Fatalf("second Close = %v, want the first result", again)
	}
	if hung.closes != 1 || ok.closes != 1 {
		t.Fatalf("a second Close flushed again: %d, %d", hung.closes, ok.closes)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
