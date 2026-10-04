package serverboot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// ctxKey tags the context newReAnchor receives, so a test can tell the
// caller's context from a context the hook built itself.
type ctxKey struct{}

// recordingSigner is a sign.Provider that records the context value Sign
// observed and fails with err when err is set.
type recordingSigner struct {
	seen any
	err  error
}

func (s *recordingSigner) ID() string { return "recording" }

func (s *recordingSigner) Sign(ctx context.Context, _ string) (string, error) {
	s.seen = ctx.Value(ctxKey{})
	if s.err != nil {
		return "", s.err
	}
	return "envelope", nil
}

func (s *recordingSigner) Verify(context.Context, string, string) error { return nil }

func seededFileSink(t *testing.T) *audit.FileSink {
	t.Helper()
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := sink.Append(context.Background(), audit.Event{
		Type: audit.EventArtifactLoaded, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return sink
}

// Spec: §8.6 — the re-anchor hook signs the new chain head with the caller's
// context, so a request-driven re-anchor carries the request's deadline and
// cancellation.
func TestNewReAnchor_UsesCallerContext(t *testing.T) {
	sink := seededFileSink(t)
	signer := &recordingSigner{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "request")
	newReAnchor(sink, signer)(ctx)
	if signer.seen != "request" {
		t.Errorf("Sign observed context value %v, want the caller's context", signer.seen)
	}
	events := readAuditLines(t, sink.Path())
	if last := events[len(events)-1]; last.Type != string(audit.EventAuditAnchored) {
		t.Errorf("last event = %s, want audit.anchored", last.Type)
	}
}

// Spec: §8.6 — a failed re-anchor logs and leaves the chain for the next
// scheduler tick; only the periodic scheduler records audit.anchor_failed.
func TestNewReAnchor_FailureLogs(t *testing.T) {
	sink := seededFileSink(t)
	logs := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logs)
	defer log.SetOutput(prev)
	newReAnchor(sink, &recordingSigner{err: errors.New("signer down")})(context.Background())
	if !strings.Contains(logs.String(), "audit re-anchor after chain rewrite failed: ") {
		t.Errorf("logs do not report the failed re-anchor:\n%s", logs)
	}
	for _, ev := range readAuditLines(t, sink.Path()) {
		if ev.Type == string(audit.EventAuditAnchorFailed) {
			t.Errorf("re-anchor recorded audit.anchor_failed")
		}
	}
}

// auditLine is the subset of a §8.1 audit JSON line the erase tests read.
type auditLine struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Tenant string `json:"tenant"`
	Hash   string `json:"hash"`
}

func readAuditLines(t *testing.T, path string) []auditLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []auditLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev auditLine
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("decode audit line: %v", err)
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan audit log: %v", err)
	}
	return out
}

// postErase drives POST /v1/admin/erase against the booted registry and
// returns the status code.
func postErase(t *testing.T, addr string) int {
	t.Helper()
	body := strings.NewReader(`{"user_id":"carol@acme.com","salt":"s"}`)
	resp, err := http.Post("http://"+addr+"/v1/admin/erase", "application/json", body)
	if err != nil {
		t.Fatalf("POST erase: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// Spec: §8.5, §8.6, §6.3.1 — the booted registry serves the erase through the
// tenant router, and an erasure that rewrites the file-backed chain is
// followed at once by an audit.anchored record over the new head, which is
// the user.erased record the rewrite appended.
func TestRun_EraseReAnchorsTheRewrittenChain(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "3600")
	t.Setenv("PODIUM_AUDIT_SIGNING_KEY_PATH", filepath.Join(f.home, "audit-anchor.key"))
	status := 0
	if _, err := f.bootWith(t, func() { status = postErase(t, f.addr) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("erase status = %d, want 200", status)
	}
	events := readAuditLines(t, f.auditPath)
	erased := -1
	for i, ev := range events {
		if ev.Type == string(audit.EventUserErased) {
			erased = i
		}
	}
	if erased < 0 {
		t.Fatalf("no user.erased record in the audit log")
	}
	head := events[erased].Hash
	for _, ev := range events[erased+1:] {
		if ev.Type == string(audit.EventAuditAnchored) && ev.Target == head {
			return
		}
	}
	t.Errorf("no audit.anchored record over the user.erased head %s follows the erasure", head)
}

// Spec: §8.6 — with anchoring disabled no re-anchor hook is wired, so the
// erasure appends no audit.anchored record.
func TestRun_EraseWithoutAnchoringRecordsNoAnchor(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "0")
	status := 0
	if _, err := f.bootWith(t, func() { status = postErase(t, f.addr) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("erase status = %d, want 200", status)
	}
	for _, ev := range readAuditLines(t, f.auditPath) {
		if ev.Type == string(audit.EventAuditAnchored) {
			t.Errorf("audit.anchored recorded with anchoring disabled")
		}
	}
}
