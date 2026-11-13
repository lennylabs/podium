package serverboot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/lennylabs/podium/internal/testharness/auditharness"
	"github.com/lennylabs/podium/pkg/audit"
)

// withReceiver points the fixture's PODIUM_AUDIT_LOG_PATH at its audit file
// and at a fresh receiver, with anchoring off, and returns the receiver.
func (f *bootFixture) withReceiver(t *testing.T) *auditharness.Receiver {
	t.Helper()
	rcv := auditharness.NewReceiver(t)
	t.Setenv("PODIUM_AUDIT_LOG_PATH", f.auditPath+","+rcv.URL())
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "0")
	return rcv
}

// fileLines returns the non-empty lines of the audit file at path.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// lineOfType returns the first line of lines whose record type is typ, or "".
func lineOfType(lines []string, typ string) string {
	for _, line := range lines {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == typ {
			return line
		}
	}
	return ""
}

// Spec: §8.3 — with a file and an HTTP destination listed, one load_artifact
// call leaves the same record, byte for byte, in the file and at the
// receiver: the stream chains the event once and fans the line out.
func TestAuditDestinations_FileAndHTTPCarryOneRecord(t *testing.T) {
	f := newBootFixture(t)
	rcv := f.withReceiver(t)
	status := 0
	if _, err := f.bootWith(t, func() { status, _ = f.loadStatus(t) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("load_artifact status = %d, want 200", status)
	}
	inFile := lineOfType(fileLines(t, f.auditPath), string(audit.EventArtifactLoaded))
	if inFile == "" {
		t.Fatal("the audit file holds no artifact.loaded record")
	}
	atReceiver := lineOfType(rcv.Lines(), string(audit.EventArtifactLoaded))
	if atReceiver != inFile {
		t.Errorf("receiver line differs from the file line\nreceiver: %s\nfile:     %s", atReceiver, inFile)
	}
}

// Spec: §8.5, §8.6 — the erase endpoint's user.erased reaches the receiver.
// Its superseded_head is a hash the receiver already holds, and its hash is
// the file's head after the rewrite, so a receiver links the closing event
// to the records the rewrite replaced.
func TestAuditDestinations_EraseReachesReceiver(t *testing.T) {
	f := newBootFixture(t)
	rcv := f.withReceiver(t)
	t.Setenv("PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS", "0")
	status := 0
	if _, err := f.bootWith(t, func() {
		if code, _ := f.loadStatus(t); code != http.StatusOK {
			t.Errorf("load_artifact status = %d, want 200", code)
		}
		status = postErase(t, f.addr)
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("erase status = %d, want 200", status)
	}
	received := rcv.Records(t)
	var erased *auditharness.Record
	hashes := map[string]bool{}
	for i, rec := range received {
		hashes[rec.Hash] = true
		if rec.Type == string(audit.EventUserErased) {
			erased = &received[i]
		}
	}
	if erased == nil {
		t.Fatalf("the receiver holds no user.erased record; got %d record(s)", len(received))
	}
	superseded := erased.Context["superseded_head"]
	if superseded == "" || !hashes[superseded] {
		t.Errorf("user.erased superseded_head = %q, want a hash the receiver already holds", superseded)
	}
	lines := fileLines(t, f.auditPath)
	var last struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode the file's last line: %v", err)
	}
	if erased.Hash != last.Hash {
		t.Errorf("user.erased hash = %s, want the file's last hash %s", erased.Hash, last.Hash)
	}
}

// Spec: §8.3, §8.6 — with anchoring on, the audit.anchored record the
// scheduler appends passes through the stream and reaches the receiver. The
// file is seeded with one record, because the scheduler's start-up anchor
// skips an empty chain.
func TestAuditDestinations_AnchorReachesReceiver(t *testing.T) {
	f := newBootFixture(t)
	rcv := f.withReceiver(t)
	seed, err := audit.NewFileSink(f.auditPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := seed.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "3600")
	t.Setenv("PODIUM_AUDIT_SIGNING_KEY_PATH", filepath.Join(f.home, "audit-anchor.key"))
	found := false
	if _, err := f.bootWith(t, func() {
		_, found = rcv.WaitForRecord(t, 15*time.Second, auditharness.OfType(string(audit.EventAuditAnchored)))
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !found {
		t.Errorf("the receiver holds no audit.anchored record; lines:\n%s", strings.Join(rcv.Lines(), "\n"))
	}
}

// Spec: §8.3, §13.8 — an otlp+http destination posts an
// ExportLogsServiceRequest to /v1/logs whose resource names the registry by
// service.name and carries a service.instance.id.
func TestAuditDestinations_OTLPWiring(t *testing.T) {
	f := newBootFixture(t)
	rcv := auditharness.NewReceiver(t)
	t.Setenv("PODIUM_AUDIT_LOG_PATH", "otlp+"+rcv.URL())
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "0")
	if _, err := f.bootWith(t, func() { f.loadStatus(t) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	reqs := rcv.Requests()
	if len(reqs) == 0 {
		t.Fatal("the OTLP receiver got no request")
	}
	for _, req := range reqs {
		if req.Path != "/v1/logs" {
			t.Errorf("request path = %s, want /v1/logs", req.Path)
		}
		attrs := otlpResourceAttributes(t, req.Body)
		if got := attrs["service.name"]; got != "podium-registry" {
			t.Errorf("service.name = %q, want podium-registry", got)
		}
		if attrs["service.instance.id"] == "" {
			t.Error("service.instance.id is empty")
		}
	}
}

// otlpResourceAttributes decodes an OTLP/HTTP logs export request and
// returns the string attributes of its first resource.
func otlpResourceAttributes(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode export request: %v", err)
	}
	if len(req.GetResourceLogs()) == 0 {
		t.Fatal("the export request carries no resource logs")
	}
	out := map[string]string{}
	for _, kv := range req.GetResourceLogs()[0].GetResource().GetAttributes() {
		out[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return out
}

// heldEmbedder is an Ollama /api/embeddings stub. A prompt in hold waits
// for its release channel before it answers, which holds the search that
// embeds it in flight; every other prompt answers at once.
type heldEmbedder struct {
	arrived map[string]chan struct{}
	release map[string]chan struct{}
}

func newHeldEmbedder(t *testing.T, prompts ...string) *heldEmbedder {
	t.Helper()
	h := &heldEmbedder{arrived: map[string]chan struct{}{}, release: map[string]chan struct{}{}}
	for _, p := range prompts {
		h.arrived[p] = make(chan struct{})
		h.release[p] = make(chan struct{})
	}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(func() {
		h.releaseAll()
		srv.Close()
	})
	t.Setenv("PODIUM_OLLAMA_URL", srv.URL)
	return h
}

func (h *heldEmbedder) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if arrived, ok := h.arrived[req.Prompt]; ok {
		close(arrived)
		<-h.release[req.Prompt]
	}
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.01
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"embedding": vec})
}

// releaseAll releases every prompt not yet released, so the cleanup never
// waits on a held handler.
func (h *heldEmbedder) releaseAll() {
	for _, ch := range h.release {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

// awaitArrival waits for prompt to reach the stub.
func (h *heldEmbedder) awaitArrival(t *testing.T, prompt string) {
	t.Helper()
	select {
	case <-h.arrived[prompt]:
	case <-time.After(15 * time.Second):
		t.Fatalf("the search for %q never reached the embedder", prompt)
	}
}

// awaitListenerClosed waits until addr refuses connections, which is when
// srv.Shutdown has closed the listener.
func awaitListenerClosed(addr string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// searchStatus runs one search_artifacts request for query and sends its
// status, or 0 when the request failed, on the returned channel.
func searchStatus(addr, query string) <-chan int {
	out := make(chan int, 1)
	go func() {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get("http://" + addr + "/v1/search_artifacts?query=" + url.QueryEscape(query))
		if err != nil {
			out <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		out <- resp.StatusCode
	}()
	return out
}

// searchedWithQuery matches an artifacts.searched record for query.
func searchedWithQuery(query string) func(auditharness.Record) bool {
	return func(rec auditharness.Record) bool {
		return rec.Type == "artifacts.searched" && rec.Context["query"] == query
	}
}

// Spec: §8.3 — the senders keep delivering while srv.Shutdown drains the
// requests in flight at the shutdown signal, and the stream flushes after the
// drain. Two searches are held in the embedder across the cancellation of
// run's context. A is released once the listener has closed, and its record
// reaches the receiver while B is still held, which a sender stopped by the
// serve context's cancellation would not deliver. B is released next, and its
// record is at the receiver when run returns, which a stream closed before
// or during the drain would count as shutdown instead.
func TestAuditDestinations_DrainedRequestIsDelivered(t *testing.T) {
	f := newBootFixture(t)
	rcv := f.withReceiver(t)
	const queryA, queryB = "drained-alpha", "drained-bravo"
	emb := newHeldEmbedder(t, queryA, queryB)

	var statusA, statusB <-chan int
	var aBeforeB bool
	var wg sync.WaitGroup
	logs, err := f.bootWith(t, func() {
		statusA = searchStatus(f.addr, queryA)
		statusB = searchStatus(f.addr, queryB)
		emb.awaitArrival(t, queryA)
		emb.awaitArrival(t, queryB)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer emb.releaseAll()
			if !awaitListenerClosed(f.addr, 15*time.Second) {
				t.Error("the listener did not close after the cancellation")
				return
			}
			close(emb.release[queryA])
			_, aBeforeB = rcv.WaitForRecord(t, 10*time.Second, searchedWithQuery(queryA))
		}()
	})
	wg.Wait()
	if err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if got := <-statusA; got != http.StatusOK {
		t.Errorf("search A status = %d, want 200", got)
	}
	if got := <-statusB; got != http.StatusOK {
		t.Errorf("search B status = %d, want 200", got)
	}
	if !aBeforeB {
		t.Error("A's record did not reach the receiver during the drain, before B was released")
	}
	lines := fileLines(t, f.auditPath)
	for _, q := range []string{queryA, queryB} {
		if !containsRecord(lines, searchedWithQuery(q)) {
			t.Errorf("the audit file holds no artifacts.searched record for %q", q)
		}
	}
	if _, ok := rcv.WaitForRecord(t, 0, searchedWithQuery(queryB)); !ok {
		t.Error("B's record is not at the receiver when run returns")
	}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "audit: dropped") && strings.HasSuffix(line, ": "+audit.ReasonShutdown) {
			t.Errorf("the log holds a shutdown drop line: %s", line)
		}
	}
}

// containsRecord reports whether any line decodes to a record match accepts.
func containsRecord(lines []string, match func(auditharness.Record) bool) bool {
	for _, line := range lines {
		var rec auditharness.Record
		if json.Unmarshal([]byte(line), &rec) == nil && match(rec) {
			return true
		}
	}
	return false
}

// Spec: §8.3, §8.5 — with no file destination Append never fails, so a
// receiver that answers 503 throughout does not fail the erase endpoint.
func TestAuditDestinations_EraseWithoutFileSurvivesReceiverOutage(t *testing.T) {
	f := newBootFixture(t)
	rcv := auditharness.NewReceiver(t)
	rcv.SetResponse(http.StatusServiceUnavailable, "")
	t.Setenv("PODIUM_AUDIT_LOG_PATH", rcv.URL())
	status := 0
	if _, err := f.bootWith(t, func() { status = postErase(t, f.addr) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("erase status = %d, want 200", status)
	}
}

// Spec: §8.5, §13.12 — a lone file destination that exists but cannot be
// opened for appending refuses the start with anchoring off, so no registry
// answers an erase it cannot record. The file is left byte-identical.
func TestAuditDestinations_UnappendableFileRefusesStartup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a 0o444 file for appending")
	}
	f := newBootFixture(t)
	t.Setenv("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", "0")
	seed, err := audit.NewFileSink(f.auditPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := seed.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(f.auditPath, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.auditPath, 0o600) })
	before, err := os.ReadFile(f.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := f.bootWith(t, func() {
		status := eraseUser(t, f.addr, "alice")
		after, _ := os.ReadFile(f.auditPath)
		t.Errorf("the registry served with an unappendable audit file: erase for alice answered %d, file still names alice: %v",
			status, bytes.Contains(after, []byte("alice")))
	})
	if runErr == nil || !strings.HasPrefix(runErr.Error(), "config.audit_sink_unavailable:") ||
		!strings.Contains(runErr.Error(), "PODIUM_AUDIT_LOG_PATH") || !strings.Contains(runErr.Error(), f.auditPath) {
		t.Fatalf("run = %v, want a config.audit_sink_unavailable refusal naming PODIUM_AUDIT_LOG_PATH and %s", runErr, f.auditPath)
	}
	after, err := os.ReadFile(f.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a refused start changed the audit file\nbefore: %s\nafter:  %s", before, after)
	}
}

// eraseUser posts an erase for user to the registry at addr and returns the
// status.
func eraseUser(t *testing.T, addr, user string) int {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"user_id":%q,"salt":"s"}`, user))
	resp, err := http.Post("http://"+addr+"/v1/admin/erase", "application/json", body)
	if err != nil {
		t.Errorf("POST erase: %v", err)
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// Spec: §8.3, §13.8 — with metrics off the stream's observer is a nil
// interface, so the first count after a load_artifact call does not panic,
// and the record reaches both destinations.
func TestAuditDestinations_MetricsDisabledDoesNotPanic(t *testing.T) {
	f := newBootFixture(t)
	rcv := f.withReceiver(t)
	t.Setenv("PODIUM_METRICS", "false")
	status := 0
	logs, err := f.bootWith(t, func() { status, _ = f.loadStatus(t) })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("load_artifact status = %d, want 200", status)
	}
	if lineOfType(fileLines(t, f.auditPath), string(audit.EventArtifactLoaded)) == "" {
		t.Error("the audit file holds no artifact.loaded record")
	}
	if lineOfType(rcv.Lines(), string(audit.EventArtifactLoaded)) == "" {
		t.Error("the receiver holds no artifact.loaded record")
	}
	if strings.Contains(logs, "panic") {
		t.Errorf("the log reports a panic:\n%s", logs)
	}
}

// fakeObserver records the stream's delivery counts.
type fakeObserver struct {
	mu      sync.Mutex
	queued  []string
	dropped []string
}

func (o *fakeObserver) AddQueued(kind string, delta int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queued = append(o.queued, fmt.Sprintf("%s %d", kind, delta))
}

func (o *fakeObserver) Dropped(kind, reason string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dropped = append(o.dropped, fmt.Sprintf("%s %s %d", kind, reason, n))
}

func (o *fakeObserver) snapshot() (queued, dropped []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.queued...), append([]string(nil), o.dropped...)
}

// appendAndClose appends one event to stream and closes it within budget.
func appendAndClose(t *testing.T, stream *audit.Stream, budget time.Duration) {
	t.Helper()
	if err := stream.Append(context.Background(), audit.Event{Type: audit.EventArtifactLoaded, Caller: "alice"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	_ = stream.Close(ctx)
	stream.Seal()
}

// Spec: §13.12 — openAuditStream passes the four transport variables and the
// queue bound to audit.Open. A receiver that only the CA file trusts and that
// requires the client certificate gets the record with the headers file's
// header, and gets nothing without the certificate. A 64-byte queue bound
// drops the record as queue_full, which the 64 MiB default would admit.
func TestOpenAuditStream_PassesTransportSettings(t *testing.T) {
	ca := auditharness.NewCA(t)
	rcv := auditharness.NewTLSReceiver(t, ca.MutualServerTLS(t))
	files := ca.WriteTransportFiles(t, t.TempDir(), "Authorization: Bearer zq7kx\n")
	files.Setenv(t)

	stream, err := openAuditStream(&Config{auditLogPath: rcv.URL()}, nil)
	if err != nil {
		t.Fatalf("openAuditStream: %v", err)
	}
	appendAndClose(t, stream, 10*time.Second)
	reqs := rcv.Requests()
	if len(reqs) == 0 || lineOfType(rcv.Lines(), string(audit.EventArtifactLoaded)) == "" {
		t.Fatalf("the mutual-TLS receiver holds no record; %d request(s)", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer zq7kx" {
		t.Errorf("Authorization = %q, want the headers file's value", got)
	}

	t.Run("without the client certificate", func(t *testing.T) {
		bare := auditharness.NewTLSReceiver(t, ca.MutualServerTLS(t))
		t.Setenv("PODIUM_AUDIT_TLS_CERT_FILE", "")
		t.Setenv("PODIUM_AUDIT_TLS_KEY_FILE", "")
		stream, err := openAuditStream(&Config{auditLogPath: bare.URL()}, nil)
		if err != nil {
			t.Fatalf("openAuditStream: %v", err)
		}
		appendAndClose(t, stream, 2*time.Second)
		if n := len(bare.Lines()); n != 0 {
			t.Errorf("the receiver holds %d line(s) from a client with no certificate, want 0", n)
		}
	})

	t.Run("queue bound", func(t *testing.T) {
		for _, name := range []string{"PODIUM_AUDIT_HEADERS_FILE", "PODIUM_AUDIT_TLS_CA_FILE", "PODIUM_AUDIT_TLS_CERT_FILE", "PODIUM_AUDIT_TLS_KEY_FILE"} {
			t.Setenv(name, "")
		}
		t.Setenv("PODIUM_AUDIT_QUEUE_MAX_BYTES", "64")
		plain := auditharness.NewReceiver(t)
		obs := &fakeObserver{}
		stream, err := openAuditStream(&Config{auditLogPath: plain.URL()}, obs)
		if err != nil {
			t.Fatalf("openAuditStream: %v", err)
		}
		appendAndClose(t, stream, 5*time.Second)
		queued, dropped := obs.snapshot()
		if len(queued) != 0 || len(dropped) != 1 || dropped[0] != "http queue_full 1" {
			t.Errorf("observer queued %v and dropped %v, want no queue count and one http queue_full drop", queued, dropped)
		}
		if n := len(plain.Lines()); n != 0 {
			t.Errorf("the receiver holds %d line(s), want 0", n)
		}
	})
}
