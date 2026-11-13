package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
)

// lineWriter records each Write as one line without its newline.
type lineWriter struct {
	mu    sync.Mutex
	lines [][]byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, bytes.TrimSuffix(append([]byte(nil), p...), []byte("\n")))
	return len(p), nil
}

func (w *lineWriter) got() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.lines...)
}

// rawReceiver is an HTTP destination's receiver that keeps each NDJSON line
// as the bytes it arrived as.
type rawReceiver struct {
	srv *httptest.Server

	mu    sync.Mutex
	lines [][]byte
}

func newRawReceiver(t *testing.T) *rawReceiver {
	t.Helper()
	rcv := &rawReceiver{}
	rcv.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rcv.mu.Lock()
		defer rcv.mu.Unlock()
		for _, line := range splitLines(body) {
			if len(line) > 0 {
				rcv.lines = append(rcv.lines, line)
			}
		}
	}))
	t.Cleanup(rcv.srv.Close)
	return rcv
}

func (rcv *rawReceiver) got() [][]byte {
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	return append([][]byte(nil), rcv.lines...)
}

// lastFileLine returns the file's last non-empty line.
func lastFileLine(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := splitLines(bytes.TrimRight(data, "\n"))
	return lines[len(lines)-1]
}

// appendRewriteFixture appends an artifacts.searched record whose query
// text is 8 days older than now, which the §8.4 query-text window redacts,
// and a record whose caller is alice, which EraseUser("alice") redacts.
func appendRewriteFixture(t *testing.T, s *Stream, now time.Time) {
	t.Helper()
	events := []Event{
		{Type: EventArtifactsSearched, Timestamp: now.Add(-8 * 24 * time.Hour), Caller: "bob", Context: map[string]string{"query": "apollo"}},
		{Type: EventArtifactLoaded, Timestamp: now.Add(-time.Hour), Caller: "alice", Target: "skill/x"},
	}
	for _, e := range events {
		if err := s.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

// A pass that only redacts query text still rewrites the chain, so it
// closes with an audit.retention_enforced event that names the superseded
// head, and that event reaches the stream's other destinations.
//
// Spec: §8.6
func TestEnforce_RedactionOnlyPassClosesWithMarker(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	q := newFakeQueue(s, kindHTTP)
	now := time.Now().UTC()
	if err := s.Append(context.Background(), Event{
		Type: EventArtifactsSearched, Timestamp: now.Add(-8 * 24 * time.Hour), Caller: "bob",
		Context: map[string]string{"query": "apollo"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	head := s.File().lastHash

	res, err := Enforce(context.Background(), s, now, nil, DefaultQueryRetention())
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if res != (EnforceResult{QueriesRedacted: 1}) {
		t.Fatalf("result = %+v, want QueriesRedacted 1 only", res)
	}
	recs := fileRecords(t, path)
	marker := recs[len(recs)-1]
	if marker.Type != string(EventRetentionEnforced) {
		t.Fatalf("last record type = %q, want %q", marker.Type, EventRetentionEnforced)
	}
	want := map[string]string{"dropped": "0", "queries_redacted": "1", "superseded_head": head}
	for k, v := range want {
		if marker.Context[k] != v {
			t.Errorf("marker context %s = %q, want %q", k, marker.Context[k], v)
		}
	}
	got := q.got()
	if len(got) != 2 || got[1].event.Type != EventRetentionEnforced {
		t.Fatalf("recording destination got %d records, want the event and its marker", len(got))
	}
	if got[1].event.Hash != marker.Hash {
		t.Errorf("forwarded marker hash = %s, want the file's %s", got[1].event.Hash, marker.Hash)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// A pass that changes nothing leaves the file, its inode, and its head
// alone. The fixture holds two chains, each starting from an empty
// prev_hash, so a rewrite that re-chained the unchanged file would join
// them and change the second record's hash.
//
// Spec: §8.4, §8.6
func TestStream_NoChangePassDoesNotRewrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	other := filepath.Join(dir, "other.log")
	for _, p := range []string{path, other} {
		s, err := OpenFile(p, OpenOptions{})
		if err != nil {
			t.Fatalf("OpenFile: %v", err)
		}
		appendN(t, s, 1)
	}
	line, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	out := &lineWriter{}
	obs := &fakeObserver{}
	s := openFileStream(t, path+",stdout", OpenOptions{Stdout: out, Observer: obs})
	res, err := Enforce(context.Background(), s, time.Now().UTC(),
		[]Policy{{Type: EventArtifactLoaded, MaxAge: 365 * 24 * time.Hour}}, nil)
	if err != nil || res != (EnforceResult{}) {
		t.Fatalf("Enforce = %+v, %v; want EnforceResult{}, nil", res, err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("file bytes changed:\nbefore %s\nafter  %s", before, after)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(infoBefore, infoAfter) {
		t.Error("file was replaced by another inode")
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf("modification time changed: %v -> %v", infoBefore.ModTime(), infoAfter.ModTime())
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file present: %v", err)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if got := out.got(); len(got) != 0 {
		t.Errorf("stdout received %d writes, want none", len(got))
	}
	if calls := obs.snapshot(); len(calls) != 0 {
		t.Errorf("observer calls = %v, want none", calls)
	}
}

// A stdout write that fails on a closing event is counted with write_error
// and not returned, because the file already holds the rewrite.
//
// Spec: §8.3, §8.6
func TestStream_RewriteClosingEventStdoutFailureIsCounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	captureLog(t)
	obs := &fakeObserver{}
	s := openFileStream(t, path+",stdout", OpenOptions{Stdout: failWriter{err: syscall.EPIPE}, Observer: obs})
	now := time.Now().UTC()
	appendRewriteFixture(t, s, now)

	res, err := Enforce(context.Background(), s, now, nil, DefaultQueryRetention())
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if res.QueriesRedacted != 1 {
		t.Errorf("QueriesRedacted = %d, want 1", res.QueriesRedacted)
	}
	if _, err := EraseUser(context.Background(), s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true}); err != nil {
		t.Fatalf("EraseUser: %v", err)
	}

	recs := fileRecords(t, path)
	if n := len(recs); n != 4 || recs[2].Type != string(EventRetentionEnforced) || recs[3].Type != string(EventUserErased) {
		t.Fatalf("file records = %d, want the fixture closed by retention_enforced and then user.erased", n)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
	want := []string{
		"Dropped(stdout,write_error,1)", "Dropped(stdout,write_error,1)",
		"Dropped(stdout,write_error,1)", "Dropped(stdout,write_error,1)",
	}
	if got := obs.snapshot(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("observer calls = %v, want %v", got, want)
	}
}

// Each closing event reaches stdout and the HTTP receiver as the record
// rewriteWithChain chained it, byte-identical to the file's last line right
// after its pass, and the next appended record links to it.
//
// Spec: §8.3, §8.6
func TestStream_RewriteForwardsFinalClosingRecord(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	rcv := newRawReceiver(t)
	out := &lineWriter{}
	s, err := Open(context.Background(), path+",stdout,"+rcv.srv.URL, OpenOptions{Stdout: out})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now().UTC()
	appendRewriteFixture(t, s, now)

	if _, err := Enforce(context.Background(), s, now, nil, DefaultQueryRetention()); err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	retained := lastFileLine(t, path)
	if _, err := EraseUser(context.Background(), s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true}); err != nil {
		t.Fatalf("EraseUser: %v", err)
	}
	erased := lastFileLine(t, path)
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for name, lines := range map[string][][]byte{"stdout": out.got(), "receiver": rcv.got()} {
		if len(lines) != 5 {
			t.Fatalf("%s got %d lines, want 5", name, len(lines))
		}
		if !bytes.Equal(lines[2], retained) {
			t.Errorf("%s retention_enforced line = %s, want the file's %s", name, lines[2], retained)
		}
		if !bytes.Equal(lines[3], erased) {
			t.Errorf("%s user.erased line = %s, want the file's %s", name, lines[3], erased)
		}
	}
	var closing, next jsonEvent
	if err := json.Unmarshal(erased, &closing); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lastFileLine(t, path), &next); err != nil {
		t.Fatal(err)
	}
	if closing.Hash == "" || next.PrevHash != closing.Hash {
		t.Errorf("next record prev_hash = %q, want the forwarded user.erased hash %q", next.PrevHash, closing.Hash)
	}
}

// Rewrites, anchors, and appends interleave and the chain still verifies.
// The test must pass under go test -race, which is how it checks that the
// stream and file locks cover every access to the head.
//
// Spec: §8.6
func TestStream_RewriteKeepsChainValid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	ctx := context.Background()
	now := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				e := Event{Type: EventArtifactLoaded, Caller: "alice", Timestamp: now.Add(-48 * time.Hour)}
				if err := s.Append(ctx, e); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if _, err := Enforce(ctx, s, now, []Policy{{Type: EventArtifactLoaded, MaxAge: 24 * time.Hour}}, nil); err != nil {
				errs <- err
			}
			if _, err := EraseUser(ctx, s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true}); err != nil {
				errs <- err
			}
			if _, err := Anchor(ctx, s, sign.Noop{}); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation: %v", err)
	}
	if err := s.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// blockingSigner blocks Sign until release is closed, after reporting that
// it was called.
type blockingSigner struct {
	called  chan struct{}
	release chan struct{}
}

func (b blockingSigner) ID() string { return "blocking" }
func (b blockingSigner) Sign(context.Context, string) (string, error) {
	close(b.called)
	<-b.release
	return "{}", nil
}
func (b blockingSigner) Verify(context.Context, string, string) error { return nil }

// Anchor signs with no stream or file lock held, so an Append proceeds
// while a slow signer runs.
//
// Spec: §8.6
func TestAnchor_DoesNotHoldStreamLockWhileSigning(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	appendN(t, s, 1)
	signer := blockingSigner{called: make(chan struct{}), release: make(chan struct{})}
	anchored := make(chan error, 1)
	go func() {
		_, err := Anchor(context.Background(), s, signer)
		anchored <- err
	}()
	<-signer.called

	appended := make(chan error, 1)
	go func() { appended <- s.Append(context.Background(), Event{Type: EventArtifactLoaded, Caller: "bob"}) }()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(signer.release)
		t.Fatal("Append blocked while the signer ran")
	}
	close(signer.release)
	if err := <-anchored; err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// A rewrite that fails leaves the file's chain and head in place, hands no
// closing event to any destination, and counts nothing, because the event
// describes a rewrite that did not happen.
//
// Spec: §8.6
func TestStream_FailedRewriteForwardsNoClosingEvent(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	obs := &fakeObserver{}
	s := openFileStream(t, path, OpenOptions{Observer: obs})
	q := newFakeQueue(s, kindHTTP)
	appendN(t, s, 1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	head := s.File().lastHash
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := EraseUser(context.Background(), s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true}); err == nil {
		t.Fatal("EraseUser returned nil with an unwritable directory")
	}
	for _, r := range q.got() {
		if r.event.Type == EventUserErased {
			t.Error("recording destination received user.erased from a failed rewrite")
		}
	}
	for _, call := range obs.snapshot() {
		if strings.HasPrefix(call, "Dropped(") {
			t.Errorf("observer recorded %s for a failed rewrite", call)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("file changed after a failed rewrite")
	}
	appendN(t, s, 1)
	recs := fileRecords(t, path)
	if recs[len(recs)-1].PrevHash != head {
		t.Errorf("next record prev_hash = %q, want the pre-rewrite head %q", recs[len(recs)-1].PrevHash, head)
	}
}

// A rewrite of a stream with no file destination is refused, because
// retention and erasure act on the file only.
//
// Spec: §8.4, §8.5
func TestStream_RewriteWithoutFileIsRefused(t *testing.T) {
	t.Parallel()
	s := newStream(OpenOptions{}.withDefaults())
	if _, err := Enforce(context.Background(), s, time.Now(), nil, nil); !errors.Is(err, errNoFileDestination) {
		t.Errorf("Enforce err = %v, want errNoFileDestination", err)
	}
	if _, err := Anchor(context.Background(), s, sign.Noop{}); !errors.Is(err, errNoFileDestination) {
		t.Errorf("Anchor err = %v, want errNoFileDestination", err)
	}
}

// A pass whose fn fails, or that reports a change with no closing event,
// leaves the file untouched and forwards nothing, because every rewrite
// closes with an event.
//
// Spec: §8.6
func TestStream_RewriteRefusalLeavesFileUntouched(t *testing.T) {
	t.Parallel()
	fnErr := errors.New("pass failed")
	for name, tc := range map[string]struct {
		fn   rewriteFunc
		want error
	}{
		"fn error": {
			fn:   func([]Event) ([]Event, *Event, bool, error) { return nil, nil, false, fnErr },
			want: fnErr,
		},
		"no closing event": {
			fn:   func(events []Event) ([]Event, *Event, bool, error) { return events, nil, true, nil },
			want: errNoClosingEvent,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "audit.log")
			s := openFileStream(t, path, OpenOptions{})
			q := newFakeQueue(s, kindHTTP)
			appendN(t, s, 1)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.rewrite(context.Background(), tc.fn); !errors.Is(err, tc.want) {
				t.Fatalf("rewrite err = %v, want %v", err, tc.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Error("file changed after a refused rewrite")
			}
			if n := len(q.got()); n != 1 {
				t.Errorf("destination got %d records, want only the appended one", n)
			}
		})
	}
}

// A sealed stream refuses a rewrite with ErrSealed and leaves the file
// untouched.
//
// Spec: §8.3, §8.6
func TestStream_SealedRewriteIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{})
	appendN(t, s, 1)
	s.Seal()
	if _, err := EraseUser(context.Background(), s, "alice", "salt", "carol", nil, EraseScope{Unlabeled: true}); !errors.Is(err, ErrSealed) {
		t.Errorf("EraseUser err = %v, want ErrSealed", err)
	}
	if n := len(fileRecords(t, path)); n != 1 {
		t.Errorf("file holds %d records, want 1", n)
	}
}

// Stream.Append normalizes caller.client before it chains, so every
// destination receives the sanitized fields and a hash computed over them.
//
// Spec: §8.1, §8.6
func TestStream_NormalizesClientBeforeHash(t *testing.T) {
	t.Parallel()
	s := newStream(OpenOptions{}.withDefaults())
	q := newFakeQueue(s, kindHTTP)
	ts := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	longName := strings.Repeat("n", 200)
	raw := Event{Type: EventArtifactLoaded, Caller: "alice", Target: "c", Timestamp: ts,
		CallerClient: &CallerClient{Kind: "MCP", Host: "evil\x00\x1bhost", Name: longName}}
	if err := s.Append(context.Background(), raw); err != nil {
		t.Fatalf("Append: %v", err)
	}
	sanitized := raw
	sanitized.SchemaVersion = SchemaVersion
	sanitized.CallerClient = &CallerClient{Kind: "other", Host: "evil__host", Name: longName[:64]}
	want := chainEvent(sanitized, "")

	got := q.got()
	if len(got) != 1 {
		t.Fatalf("destination got %d records, want 1", len(got))
	}
	var je jsonEvent
	if err := json.Unmarshal(got[0].line, &je); err != nil {
		t.Fatal(err)
	}
	if je.Hash != want.Hash {
		t.Errorf("hash = %s, want %s over the sanitized client", je.Hash, want.Hash)
	}
	c := je.Caller.Client
	if c == nil || c.Kind != "other" || c.Host != "evil__host" || c.Name != longName[:64] {
		t.Errorf("client = %+v, want kind other, host evil__host, and a 64-byte name", c)
	}
}
