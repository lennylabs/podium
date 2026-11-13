package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/sign"
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
		s.stdout = &stdoutDest{w: epipe}
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
		s.stdout = &stdoutDest{w: epipe}
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
	s.stdout = &stdoutDest{w: w}
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

	// A drop suppressed within the interval stays held back at Shutdown,
	// so the exit writes no second line for a key inside its minute.
	s.dropped(kindHTTP, name, ReasonQueueFull, 1)
	before := logs.String()
	s.Shutdown()
	if after := logs.String(); after != before {
		t.Errorf("Shutdown wrote a drop line inside the interval:\n%s", strings.TrimPrefix(after, before))
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

// A part of a line that a failed write stored stays in the file and joins
// the next record's line. Verify reports the joined line, and Enforce,
// EraseUser, and Anchor each refuse a file they cannot parse rather than
// rewrite or anchor over the loss. With the part removed the chain
// verifies, because the failed write never advanced the head.
//
// Spec: §8.3, §8.6
func TestStream_StoredPartOfFailedWriteIsReportedByVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	now := time.Now().UTC()
	appendRewriteFixture(t, s, now)
	at := len(mustRead(t, path))

	// Another handle writes the part, standing in for the bytes a failed
	// write stored before it returned.
	part := []byte(`{"type":"artifact.loaded","timest`)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(part); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	appendN(t, s, 1)
	stored := mustRead(t, path)
	joined := append(append([]byte(nil), part...), '{')
	if !bytes.HasPrefix(stored[at:], joined) {
		t.Fatalf("the part did not join the next record's line:\n%s", stored)
	}

	if err := s.Verify(ctx); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("Verify = %v, want ErrChainBroken", err)
	}
	refusals := map[string]func() error{
		"Enforce": func() error {
			_, err := Enforce(ctx, s, now, nil, DefaultQueryRetention())
			return err
		},
		"EraseUser": func() error {
			_, err := EraseUser(ctx, s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true})
			return err
		},
		"Anchor": func() error {
			_, err := Anchor(ctx, s, sign.Noop{})
			return err
		},
	}
	for name, run := range refusals {
		if err := run(); err == nil {
			t.Errorf("%s over the joined line returned nil", name)
		}
		if got := mustRead(t, path); !bytes.Equal(got, stored) {
			t.Errorf("%s changed the file", name)
		}
	}

	repaired := append(append([]byte(nil), stored[:at]...), stored[at+len(part):]...)
	if err := os.WriteFile(path, repaired, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx); err != nil {
		t.Fatalf("Verify with the part removed = %v, want nil", err)
	}
}

// Stream B keeps its head across a rewrite that stream A runs on the same
// file, so B's next record links to a hash the rewrite replaced, and Verify
// reports it. No coordination exists between writers of one file, and this
// pins that the outcome is reported rather than silent.
//
// Spec: §8.3, §8.6
func TestStream_AppendAfterForeignRewriteIsReported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.log")
	now := time.Now().UTC()
	a := openFileStream(t, path, OpenOptions{})
	if err := a.Append(ctx, Event{
		Type: EventArtifactsSearched, Timestamp: now.Add(-8 * 24 * time.Hour), Caller: "bob",
		Context: map[string]string{"query": "apollo"},
	}); err != nil {
		t.Fatalf("A Append: %v", err)
	}
	b := openFileStream(t, path, OpenOptions{})
	appendN(t, b, 1)
	first := fileRecords(t, path)[1].Hash

	res, err := Enforce(ctx, a, now, nil, DefaultQueryRetention())
	if err != nil || res.QueriesRedacted != 1 {
		t.Fatalf("A Enforce = %+v, %v; want one query redacted", res, err)
	}
	appendN(t, b, 1)

	recs := fileRecords(t, path)
	if got := recs[len(recs)-1].PrevHash; got != first {
		t.Fatalf("B's next prev_hash = %q, want the pre-rewrite hash %q of B's first record", got, first)
	}
	for i, r := range recs {
		if r.Hash == first {
			t.Fatalf("line %d still holds the pre-rewrite hash", i)
		}
	}
	for name, s := range map[string]*Stream{"A": a, "B": b} {
		if err := s.Verify(ctx); !errors.Is(err, ErrChainBroken) {
			t.Errorf("%s Verify = %v, want ErrChainBroken", name, err)
		}
	}
}

// lossCase is one drop source of TestStream_EveryChainedRecordIsDeliveredOrCounted.
type lossCase struct {
	name string
	// kind and reason name the one destination and drop reason the case
	// may count.
	kind, reason string
	// refuse returns the status the receiver answers a request with, or 0
	// to accept it.
	refuse     func(r received) int
	failStdout bool
	queueMax   int64
	// drive appends the case's events, closes s, and returns the number of
	// events it appended.
	drive func(t *testing.T, s *Stream, path string) int
}

// lossCases lists one case per §8.3 drop source.
func lossCases() []lossCase {
	closeNow := func(t *testing.T, s *Stream) { closeWithin(t, s, 10*time.Second) }
	return []lossCase{
		{
			name: "queue_full", kind: "http", reason: ReasonQueueFull, queueMax: 1000,
			drive: func(t *testing.T, s *Stream, _ string) int {
				appendN(t, s, 10)
				closeNow(t, s)
				return 10
			},
		},
		{
			name: "rejected by the receiver", kind: "http", reason: ReasonRejected,
			refuse: func(r received) int {
				if poisoned(r) {
					return http.StatusBadRequest
				}
				return 0
			},
			drive: func(t *testing.T, s *Stream, _ string) int {
				for _, target := range []string{"0", "1", "poison", "3", "4"} {
					appendTarget(t, s, target)
				}
				closeNow(t, s)
				return 5
			},
		},
		{
			name: "rejected as larger than 1 MiB", kind: "http", reason: ReasonRejected, queueMax: maxRecordBytes / 2,
			drive: func(t *testing.T, s *Stream, _ string) int {
				appendN(t, s, 2)
				appendTarget(t, s, strings.Repeat("x", maxRecordBytes))
				appendN(t, s, 2)
				closeNow(t, s)
				return 5
			},
		},
		{
			name: "shutdown after a refused flush", kind: "http", reason: ReasonShutdown,
			refuse: func(received) int { return http.StatusServiceUnavailable },
			drive: func(t *testing.T, s *Stream, _ string) int {
				appendN(t, s, 3)
				closeWithin(t, s, 200*time.Millisecond)
				return 3
			},
		},
		{
			name: "shutdown for an append after Close", kind: "http", reason: ReasonShutdown,
			drive: func(t *testing.T, s *Stream, _ string) int {
				appendN(t, s, 2)
				closeNow(t, s)
				appendN(t, s, 3)
				return 5
			},
		},
		{
			name: "stdout write_error", kind: "stdout", reason: ReasonWriteError, failStdout: true,
			drive: func(t *testing.T, s *Stream, _ string) int {
				appendN(t, s, 3)
				closeNow(t, s)
				return 3
			},
		},
		{
			name: "file write_error", kind: "file", reason: ReasonWriteError,
			drive: func(t *testing.T, s *Stream, path string) int {
				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3; i++ {
					if err := s.Append(context.Background(), Event{Type: EventArtifactLoaded}); err == nil {
						t.Fatalf("Append %d to an unwritable file returned nil", i)
					}
				}
				closeNow(t, s)
				return 3
			},
		},
	}
}

// Every record the stream chains is delivered to each destination or
// counted as dropped for it with the case's reason, and the queue depth
// returns to 0. An event the file did not store is not chained, and it is
// counted for the file alone. It must pass under go test -race
// ./pkg/audit/.
//
// Spec: §8.3
func TestStream_EveryChainedRecordIsDeliveredOrCounted(t *testing.T) {
	captureLog(t)
	for _, tc := range lossCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kind == "file" {
				skipIfRoot(t)
			}
			stubTicker(t)
			stubSleepGate(t, true)
			var mu sync.Mutex
			accepted := map[string]bool{}
			var rcv *receiver
			rcv = newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
				req := rcv.requests()[n-1]
				if tc.refuse != nil {
					if code := tc.refuse(req); code != 0 {
						w.WriteHeader(code)
						return
					}
				}
				mu.Lock()
				defer mu.Unlock()
				for _, l := range req.lines {
					accepted[l.Hash] = true
				}
			})
			var out io.Writer = &lineWriter{}
			if tc.failStdout {
				out = failWriter{err: syscall.EPIPE}
			}
			obs := &fakeObserver{}
			path := filepath.Join(t.TempDir(), "audit.log")
			s := openStream(t, path+",stdout,"+rcv.srv.URL+"/p", OpenOptions{Stdout: out, Observer: obs, QueueMaxBytes: tc.queueMax})
			appended := tc.drive(t, s, path)

			recs := 0
			if _, err := os.Stat(path); err == nil {
				recs = len(fileRecords(t, path))
			}
			queued, dropped := totals(obs)
			byKind := map[string]int{}
			for key, n := range dropped {
				if key != tc.kind+","+tc.reason {
					t.Errorf("dropped %d with %s, want only %s,%s", n, key, tc.kind, tc.reason)
				}
				byKind[strings.SplitN(key, ",", 2)[0]] += n
			}
			if byKind[tc.kind] == 0 {
				t.Errorf("the case counted no %s drop", tc.reason)
			}
			for kind, sum := range queued {
				if sum != 0 {
					t.Errorf("AddQueued deltas of %s sum to %d, want 0", kind, sum)
				}
			}
			if appended != recs+byKind["file"] {
				t.Errorf("appended %d, want the file's %d records plus %d file drops", appended, recs, byKind["file"])
			}
			mu.Lock()
			delivered := len(accepted)
			mu.Unlock()
			if delivered+byKind["http"] != recs {
				t.Errorf("http: %d accepted plus %d dropped, want the file's %d records", delivered, byKind["http"], recs)
			}
			written := 0
			if lw, ok := out.(*lineWriter); ok {
				written = len(lw.got())
			}
			if written+byKind["stdout"] != recs {
				t.Errorf("stdout: %d written plus %d dropped, want the file's %d records", written, byKind["stdout"], recs)
			}
			if tc.kind == "file" && len(queued) != 0 {
				t.Errorf("an unchained event reached another destination: %v", obs.snapshot())
			}
		})
	}
}

// The observer sees the first Append's queue count, because Open fixes it
// before any record is chained, and the counts balance after Close.
//
// Spec: §8.3, §13.8
func TestStream_ObserverCountsFromFirstAppend(t *testing.T) {
	stubTicker(t)
	rcv := newReceiver(t, nil)
	obs := &fakeObserver{}
	s := openStream(t, rcv.srv.URL+"/p", OpenOptions{Observer: obs})
	appendN(t, s, 1)
	if got := obs.snapshot(); len(got) != 1 || got[0] != "AddQueued(http,1)" {
		t.Fatalf("observer calls after the first Append = %v, want AddQueued(http,1)", got)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	queued, dropped := totals(obs)
	if queued["http"] != 0 || len(dropped) != 0 {
		t.Fatalf("after Close queued %v dropped %v, want deltas summing to 0 and no drop", queued, dropped)
	}
	if n := len(rcv.hashes()); n != 1 {
		t.Fatalf("receiver accepted %d records, want 1", n)
	}
}
