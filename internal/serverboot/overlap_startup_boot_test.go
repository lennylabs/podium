package serverboot

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/vector"
)

// The providers the startup-pass boot tests register in embedding.Default and
// vector.Default. Those registries are process-global, so the ids are unique
// to these tests and registered once.
const (
	overlapBootEmbedderID = "overlap-startup-test"
	gatedVectorID         = "overlap-gated-test"
)

var (
	registerOverlapBootProviders sync.Once
	// queryGate holds the channel the gated backend's neighbour queries wait
	// on, and putGate the channel its vector writes wait on; a test stores a
	// fresh one before it boots.
	queryGate atomic.Pointer[chan struct{}]
	putGate   atomic.Pointer[chan struct{}]
)

// gatedVector is a managed vector backend over the memory backend whose
// neighbour queries wait until the current queryGate closes, so a test can
// hold every overlap computation in flight, and whose writes wait until the
// current putGate closes, so a test can keep the outbox rows pending.
type gatedVector struct{ *vector.Memory }

func (g gatedVector) ID() string { return gatedVectorID }

// waitGate blocks until the channel in gate closes or ctx ends; an empty
// gate does not block.
func waitGate(ctx context.Context, gate *atomic.Pointer[chan struct{}]) error {
	ch := gate.Load()
	if ch == nil {
		return nil
	}
	select {
	case <-*ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g gatedVector) Put(ctx context.Context, tenant, id, version string, vec []float32) error {
	if err := waitGate(ctx, &putGate); err != nil {
		return err
	}
	return g.Memory.Put(ctx, tenant, id, version, vec)
}

func (g gatedVector) Query(ctx context.Context, tenant string, vec []float32, k int) ([]vector.Match, error) {
	if err := waitGate(ctx, &queryGate); err != nil {
		return nil, err
	}
	return g.Memory.Query(ctx, tenant, vec, k)
}

func (g gatedVector) QueryModel(ctx context.Context, tenant string, vec []float32, k int, model string) ([]vector.Match, error) {
	if err := waitGate(ctx, &queryGate); err != nil {
		return nil, err
	}
	return g.Memory.QueryModel(ctx, tenant, vec, k, model)
}

// registerOverlapProviders registers the word embedder and the gated vector
// backend once per process.
func registerOverlapProviders(t *testing.T) {
	t.Helper()
	registerOverlapBootProviders.Do(func() {
		if err := embedding.Default.Register(overlapBootEmbedderID, func(map[string]string) (embedding.Provider, error) {
			return wordEmbedder{}, nil
		}); err != nil {
			t.Fatalf("register embedder: %v", err)
		}
		if err := vector.Default.Register(gatedVectorID, func(_ map[string]string, dim int) (vector.Provider, error) {
			return gatedVector{vector.NewMemory(dim)}, nil
		}); err != nil {
			t.Fatalf("register vector backend: %v", err)
		}
	})
}

// overlapBootFixture boots with two bootstrap layers holding fin/invoices
// and fin/ledger (team-a) and fin/payables (team-b), the word embedder, the
// given vector backend, and the semantic overlap check on with the given
// sweep interval.
func overlapBootFixture(t *testing.T, backend, interval string) *bootFixture {
	t.Helper()
	registerOverlapProviders(t)
	f := newBootFixture(t)
	layers := filepath.Join(f.home, "layers")
	body := func(desc string) string {
		return "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody\n"
	}
	testharness.WriteTree(t, layers,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team-a/fin/invoices/ARTIFACT.md", Content: body("reconciles invoices")},
		testharness.WriteTreeOption{Path: "team-a/fin/ledger/ARTIFACT.md", Content: body("closes the ledger")},
		testharness.WriteTreeOption{Path: "team-b/fin/payables/ARTIFACT.md", Content: body("reconciles payables")},
	)
	t.Setenv("PODIUM_LAYER_PATH", layers)
	t.Setenv("PODIUM_VECTOR_BACKEND", backend)
	t.Setenv("PODIUM_EMBEDDING_PROVIDER", overlapBootEmbedderID)
	t.Setenv("PODIUM_LINT_SEMANTIC_OVERLAP", "on")
	t.Setenv("PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL", interval)
	t.Setenv("PODIUM_VECTOR_OUTBOX_INTERVAL_SECONDS", "1")
	t.Setenv("PODIUM_INGEST_OFFLINE", "true")
	return f
}

// waitComplete polls GET /v1/overlap until computed equals total, and returns
// the last state and semantic diagnostics it read.
func waitComplete(t *testing.T, addr string) (overlapState, []string) {
	t.Helper()
	var state overlapState
	var semantic []string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state, semantic = getOverlap(t, addr)
		if state.Computed != nil && state.Total != nil && *state.Computed == *state.Total {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return state, semantic
}

// readyzOK reports whether GET /readyz answers 200.
func readyzOK(addr string) bool {
	resp, err := http.Get("http://" + addr + "/readyz")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// wordEmbedder embeds a text as the vector of the first table word it
// contains, so a description fixes an artifact's position.
type wordEmbedder struct{}

func (wordEmbedder) ID() string      { return overlapBootEmbedderID }
func (wordEmbedder) Model() string   { return "word-table" }
func (wordEmbedder) Dimensions() int { return 4 }
func (wordEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	table := []struct {
		word string
		vec  []float32
	}{
		{"invoices", []float32{1, 0, 0, 0}},
		{"payables", []float32{0.99, 0.12, 0, 0}},
		{"ledger", []float32{0, 1, 0, 0}},
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{0, 0, 0, 1}
		for _, e := range table {
			if strings.Contains(text, e.word) {
				out[i] = e.vec
				break
			}
		}
	}
	return out, nil
}

// overlapState is the semantic_overlap member of GET /v1/overlap.
type overlapState struct {
	Computed *int `json:"computed"`
	Total    *int `json:"total"`
}

// getOverlap reads GET /v1/overlap from the booted registry.
func getOverlap(t *testing.T, addr string) (overlapState, []string) {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/v1/overlap")
	if err != nil {
		t.Fatalf("GET /v1/overlap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		SemanticOverlap overlapState `json:"semantic_overlap"`
		Diagnostics     []struct {
			ArtifactID string `json:"artifact_id"`
			Code       string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /v1/overlap: %v", err)
	}
	var semantic []string
	for _, d := range body.Diagnostics {
		if d.Code == "lint.semantic_overlap" {
			semantic = append(semantic, d.ArtifactID)
		}
	}
	return body.SemanticOverlap, semantic
}

// Spec: §7.3.1, §13.12 — the ingest a registry runs at startup computes no
// overlap. Once every bootstrap layer is ingested, one startup pass computes
// all of their artifacts in the background, with the periodic sweep off and
// with it on: the pass logs that it computed every artifact, which it would
// not if the startup ingest had computed them, and a registry with two
// bootstrap layers runs it once. /readyz answers, and the report reaches
// computed == total and names the paraphrase pair.
func TestRun_StartupPassComputesBootstrapArtifacts(t *testing.T) {
	for _, interval := range []string{"0", "1h"} {
		t.Run("interval="+interval, func(t *testing.T) {
			f := overlapBootFixture(t, "memory", interval)
			var state overlapState
			var semantic []string
			logs, err := f.bootWith(t, func() {
				if !readyzOK(f.addr) {
					t.Error("/readyz did not answer 200")
				}
				state, semantic = waitComplete(t, f.addr)
			})
			if err != nil {
				t.Fatalf("run: %v\n%s", err, logs)
			}
			if state.Computed == nil || state.Total == nil || *state.Total != 3 || *state.Computed != 3 {
				t.Fatalf("computed/total = %v/%v, want 3/3\n%s", state.Computed, state.Total, logs)
			}
			if strings.Join(semantic, ",") != "fin/invoices,fin/payables" {
				t.Errorf("semantic diagnostics name %v, want fin/invoices and fin/payables", semantic)
			}
			passLine := regexp.MustCompile(`lint\.semantic_overlap: startup pass of tenant \S+ computed 3, failed 0`)
			if n := len(passLine.FindAllString(logs, -1)); n != 1 || strings.Count(logs, "startup pass of tenant") != 1 {
				t.Errorf("the log carries %d startup-pass lines computing every artifact, want 1\n%s", n, logs)
			}
			if strings.Contains(logs, "lint.semantic_overlap: sweep of tenant") {
				t.Errorf("a periodic pass ran during the boot\n%s", logs)
			}
		})
	}
}

// Spec: §7.3.1, §13.9 — readiness never waits on a semantic overlap
// computation: with every computation held in flight at its neighbour query,
// the startup pass's and the outbox write's included, /readyz answers 200 and
// the report says nothing is computed yet. Once the computations are
// released, the report reaches computed == total.
func TestRun_ReadinessDoesNotWaitOnTheStartupPass(t *testing.T) {
	gate := make(chan struct{})
	queryGate.Store(&gate)
	t.Cleanup(func() { queryGate.Store(nil) })
	f := overlapBootFixture(t, gatedVectorID, "0")
	var held, done overlapState
	logs, err := f.bootWith(t, func() {
		if !readyzOK(f.addr) {
			t.Error("/readyz did not answer 200 while the startup pass was held")
		}
		held, _ = getOverlap(t, f.addr)
		close(gate)
		done, _ = waitComplete(t, f.addr)
	})
	if err != nil {
		t.Fatalf("run: %v\n%s", err, logs)
	}
	if held.Computed == nil || *held.Computed != 0 || held.Total == nil || *held.Total != 3 {
		t.Errorf("while held: computed/total = %v/%v, want 0/3", held.Computed, held.Total)
	}
	if done.Computed == nil || done.Total == nil || *done.Computed != 3 || *done.Total != 3 {
		t.Errorf("after release: computed/total = %v/%v, want 3/3\n%s", done.Computed, done.Total, logs)
	}
	if !strings.Contains(logs, "startup pass running") {
		t.Errorf("the startup pass did not start\n%s", logs)
	}
}

// Spec: §7.3.1 — on a fresh managed deployment the startup pass skips every
// artifact whose vector the outbox has not written, counting it as neither
// computed nor failed, so it logs no stored-vector failure. With the outbox
// writes held, the startup pass logs the three artifacts as skipped. Once the
// writes are released, the outbox write computes them with the vectors it
// wrote, the report reaches computed == total, and the log carries no
// stored-vector read failure and no failed computation.
func TestRun_ManagedBootSkipsUnwrittenVectors(t *testing.T) {
	gate := make(chan struct{})
	putGate.Store(&gate)
	t.Cleanup(func() { putGate.Store(nil) })
	f := overlapBootFixture(t, gatedVectorID, "0")
	var state overlapState
	var semantic []string
	logs, err := f.bootWith(t, func() {
		buf, ok := log.Writer().(*syncBuffer)
		if !ok {
			t.Fatal("the boot did not capture the standard logger")
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && !strings.Contains(buf.String(), "startup pass of tenant") {
			time.Sleep(20 * time.Millisecond)
		}
		close(gate)
		state, semantic = waitComplete(t, f.addr)
	})
	if err != nil {
		t.Fatalf("run: %v\n%s", err, logs)
	}
	skipped := regexp.MustCompile(`lint\.semantic_overlap: startup pass of tenant \S+ computed 0, failed 0, skipped 3 whose vectors the outbox has not written`)
	if !skipped.MatchString(logs) {
		t.Errorf("the startup pass did not log the three unwritten artifacts as skipped\n%s", logs)
	}
	if strings.Contains(logs, "stored-vector read") || regexp.MustCompile(`failed [1-9]`).MatchString(logs) {
		t.Errorf("the boot logged a stored-vector read failure or a failed computation\n%s", logs)
	}
	if state.Computed == nil || state.Total == nil || *state.Computed != 3 || *state.Total != 3 {
		t.Fatalf("computed/total = %v/%v, want 3/3\n%s", state.Computed, state.Total, logs)
	}
	if strings.Join(semantic, ",") != "fin/invoices,fin/payables" {
		t.Errorf("semantic diagnostics name %v, want fin/invoices and fin/payables", semantic)
	}
}
