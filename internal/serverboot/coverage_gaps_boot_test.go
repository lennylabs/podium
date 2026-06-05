package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// selfEmbedVector is a vector.Provider that also satisfies vector.TextVectorizer
// with server-side embedding active, so drainRow takes the PutText branch and no
// embedding.Provider is required. It records the (id@version, text) it received.
type selfEmbedVector struct {
	puttext map[string]string
	putErr  error
}

func newSelfEmbedVector() *selfEmbedVector { return &selfEmbedVector{puttext: map[string]string{}} }

func (selfEmbedVector) ID() string       { return "self-embed-memory" }
func (selfEmbedVector) Dimensions() int  { return 0 }
func (selfEmbedVector) Close() error     { return nil }
func (selfEmbedVector) SelfEmbeds() bool { return true }
func (selfEmbedVector) Delete(context.Context, string, string, string) error {
	return nil
}
func (selfEmbedVector) Put(context.Context, string, string, string, []float32) error {
	return nil
}
func (selfEmbedVector) Query(context.Context, string, []float32, int) ([]vector.Match, error) {
	return nil, nil
}
func (selfEmbedVector) QueryText(context.Context, string, string, int) ([]vector.Match, error) {
	return nil, nil
}
func (v *selfEmbedVector) PutText(_ context.Context, tenantID, artifactID, version, text string) error {
	if v.putErr != nil {
		return v.putErr
	}
	v.puttext[artifactID+"@"+version] = text
	return nil
}

// Spec: §4.7.2 — startVectorOutboxWorker launches the drain goroutine and runs an
// immediate pass before the first tick. With a queued row and a configured
// backend, that pass writes the vector and empties the outbox.
func TestStartVectorOutboxWorker_DrainsQueuedRow(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Now().UTC()
	enqueue(t, st, "a/x", "1.0.0", now)

	cfg := &Config{vectorOutboxInterval: 1, vectorOutboxBatch: 50}
	startVectorOutboxWorker(cfg, st, vector.NewMemory(8), fakeEmbedder{dim: 8}, metrics.New(), nil, "default")

	// The immediate runOnce pass drains the row; poll the depth until it reaches
	// zero rather than sleeping a fixed wall-clock interval.
	if !waitForDepth(t, st, 0, time.Second) {
		depth, _, _ := st.VectorOutboxStats(ctx)
		t.Fatalf("outbox depth = %d after worker start, want 0 (immediate pass should drain)", depth)
	}
}

// Spec: §4.7.2 — a store that does not implement VectorOutbox (no transactional
// outbox) disables the worker; the call returns without spawning a goroutine.
func TestStartVectorOutboxWorker_NonOutboxStoreNoop(t *testing.T) {
	// A bare Store that does not satisfy VectorOutbox.
	startVectorOutboxWorker(&Config{}, nonOutboxStore{}, vector.NewMemory(8), fakeEmbedder{dim: 8}, nil, nil, "default")
	// No panic and no goroutine is the assertion; the type assertion guard returns
	// before any worker construction.
}

// Spec: §4.7.2 — with no vector backend opened the worker is disabled so pending
// embeddings stay queued; the call returns without draining.
func TestStartVectorOutboxWorker_NilBackendNoop(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Now().UTC()
	enqueue(t, st, "a/x", "1.0.0", now)

	startVectorOutboxWorker(&Config{vectorOutboxInterval: 1}, st, nil, fakeEmbedder{dim: 8}, nil, nil, "default")

	// The row must remain queued because no worker ran.
	if depth, _, _ := st.VectorOutboxStats(ctx); depth != 1 {
		t.Fatalf("outbox depth = %d after nil-backend start, want 1 (worker disabled)", depth)
	}
}

// Spec: §4.7.2 — a non-positive configured interval falls back to the 5s default
// so the ticker is always valid. The immediate pass still drains the queued row.
func TestStartVectorOutboxWorker_NonPositiveIntervalDefaults(t *testing.T) {
	st := store.NewMemory()
	now := time.Now().UTC()
	enqueue(t, st, "a/x", "1.0.0", now)

	// interval 0 exercises the `interval <= 0` fallback to 5s.
	startVectorOutboxWorker(&Config{vectorOutboxInterval: 0, vectorOutboxBatch: 50}, st, vector.NewMemory(8), fakeEmbedder{dim: 8}, nil, nil, "default")

	if !waitForDepth(t, st, 0, time.Second) {
		depth, _, _ := st.VectorOutboxStats(context.Background())
		t.Fatalf("outbox depth = %d, want 0 after the immediate pass", depth)
	}
}

// Spec: §4.7.2 — drainRow on a self-embedding backend sends the raw text through
// PutText with no embedding.Provider configured.
func TestDrainRow_SelfEmbeddingBackendUsesPutText(t *testing.T) {
	ctx := context.Background()
	vec := newSelfEmbedVector()
	w := &vectorDrainWorker{outbox: store.NewMemory(), vec: vec, embedder: nil, interval: time.Second}

	p := store.VectorPending{TenantID: "default", ArtifactID: "a/x", Version: "1.0.0", Text: "compose me"}
	if err := w.drainRow(ctx, p); err != nil {
		t.Fatalf("drainRow self-embed = %v, want nil", err)
	}
	if got := vec.puttext["a/x@1.0.0"]; got != "compose me" {
		t.Errorf("PutText recorded text = %q, want %q", got, "compose me")
	}
}

// Spec: §4.7.2 — drainRow against an external (non-self-embedding) backend with no
// embedder configured returns an error rather than writing a degenerate vector.
func TestDrainRow_ExternalBackendWithoutEmbedderErrors(t *testing.T) {
	ctx := context.Background()
	// vector.Memory is not a self-embedding TextVectorizer, and embedder is nil.
	w := &vectorDrainWorker{outbox: store.NewMemory(), vec: vector.NewMemory(8), embedder: nil, interval: time.Second}

	err := w.drainRow(ctx, store.VectorPending{TenantID: "default", ArtifactID: "a/x", Version: "1.0.0", Text: "t"})
	if err == nil {
		t.Fatal("drainRow with no embedder and a non-self-embedding backend = nil, want an error")
	}
	if got := err.Error(); got != "no embedder configured for external vector backend" {
		t.Errorf("error = %q, want the no-embedder message", got)
	}
}

// Spec: §4.7.2 — drainRow surfaces a self-embedding backend's PutText failure so
// runOnce retries the row with backoff.
func TestDrainRow_SelfEmbeddingPutTextError(t *testing.T) {
	ctx := context.Background()
	vec := newSelfEmbedVector()
	vec.putErr = errors.New("backend down")
	w := &vectorDrainWorker{outbox: store.NewMemory(), vec: vec, embedder: nil, interval: time.Second}

	if err := w.drainRow(ctx, store.VectorPending{ArtifactID: "a/x", Version: "1.0.0", Text: "t"}); err == nil {
		t.Fatal("drainRow = nil, want the PutText error propagated")
	}
}

// brokenEmbedder returns a vector count that disagrees with the requested text
// count, exercising the drainRow length-mismatch guard.
type brokenEmbedder struct{ dim int }

func (brokenEmbedder) ID() string        { return "broken" }
func (brokenEmbedder) Model() string     { return "broken" }
func (e brokenEmbedder) Dimensions() int { return e.dim }
func (brokenEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	// Return two vectors for the single text drainRow embeds.
	return [][]float32{{1}, {2}}, nil
}

// Spec: §4.7.2 — drainRow rejects an embedder that returns the wrong number of
// vectors for the single composed text rather than writing a mismatched vector.
func TestDrainRow_EmbedVectorCountMismatchErrors(t *testing.T) {
	ctx := context.Background()
	w := &vectorDrainWorker{outbox: store.NewMemory(), vec: vector.NewMemory(8), embedder: brokenEmbedder{dim: 1}, interval: time.Second}

	err := w.drainRow(ctx, store.VectorPending{ArtifactID: "a/x", Version: "1.0.0", Text: "t"})
	if err == nil {
		t.Fatal("drainRow with a 2-vector embedder result = nil, want a count-mismatch error")
	}
}

// Spec: §4.7.2 — backoff is the poll interval doubled per prior attempt, capped at
// one hour. Zero attempts yields the bare interval; growth doubles; the cap holds
// once the doubled value would exceed an hour.
func TestBackoff_GrowthAndCap(t *testing.T) {
	w := &vectorDrainWorker{interval: time.Minute}

	if got := w.backoff(0); got != time.Minute {
		t.Errorf("backoff(0) = %s, want the bare interval %s", got, time.Minute)
	}
	if got := w.backoff(1); got != 2*time.Minute {
		t.Errorf("backoff(1) = %s, want %s", got, 2*time.Minute)
	}
	if got := w.backoff(3); got != 8*time.Minute {
		t.Errorf("backoff(3) = %s, want %s", got, 8*time.Minute)
	}
	// 2^6 minutes = 64m exceeds one hour, so the cap holds.
	if got := w.backoff(6); got != time.Hour {
		t.Errorf("backoff(6) = %s, want the one-hour cap", got)
	}
	// A very large attempt count stays capped at one hour.
	if got := w.backoff(100); got != time.Hour {
		t.Errorf("backoff(100) = %s, want the one-hour cap", got)
	}
}

// Spec: §4.7.2 — when the interval already meets or exceeds the cap, backoff
// returns the cap for any attempt count, including zero attempts.
func TestBackoff_IntervalAtOrAboveCap(t *testing.T) {
	// An interval larger than the cap: the loop guard `d < time.Hour` never runs
	// and the trailing clamp returns the cap.
	w := &vectorDrainWorker{interval: 2 * time.Hour}
	if got := w.backoff(0); got != time.Hour {
		t.Errorf("backoff(0) with a >1h interval = %s, want the one-hour cap", got)
	}
	if got := w.backoff(5); got != time.Hour {
		t.Errorf("backoff(5) with a >1h interval = %s, want the one-hour cap", got)
	}
}

// Spec: §13.10 — registryYAMLExists reports whether ~/.podium/registry.yaml is
// present. It is true once the file exists under HOME and false otherwise.
func TestRegistryYAMLExists_TrueWhenPresentFalseWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if registryYAMLExists() {
		t.Fatal("registryYAMLExists = true with no ~/.podium/registry.yaml")
	}

	dir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte("registry:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !registryYAMLExists() {
		t.Error("registryYAMLExists = false after writing ~/.podium/registry.yaml")
	}
}

// Spec: §13.10 — hasExplicitServerConfig is false when the operator set no
// config file, no PODIUM_STANDALONE, no ~/.podium/registry.yaml, and none of the
// recognized PODIUM_* server settings.
func TestHasExplicitServerConfig_AllAbsentIsFalse(t *testing.T) {
	clearServerConfigEnv(t)
	t.Setenv("HOME", t.TempDir()) // no registry.yaml under a fresh HOME

	if hasExplicitServerConfig() {
		t.Error("hasExplicitServerConfig = true with no explicit configuration set")
	}
}

// Spec: §13.10 — an explicit --config (PODIUM_CONFIG_FILE) marks the configuration
// explicit so the zero-flag auto-bootstrap stays disengaged.
func TestHasExplicitServerConfig_ConfigFileIsExplicit(t *testing.T) {
	clearServerConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_CONFIG_FILE", "/tmp/custom.yaml")

	if !hasExplicitServerConfig() {
		t.Error("hasExplicitServerConfig = false with PODIUM_CONFIG_FILE set")
	}
}

// Spec: §13.10 — PODIUM_STANDALONE marks the configuration explicit.
func TestHasExplicitServerConfig_StandaloneFlagIsExplicit(t *testing.T) {
	clearServerConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_STANDALONE", "1")

	if !hasExplicitServerConfig() {
		t.Error("hasExplicitServerConfig = false with PODIUM_STANDALONE=1")
	}
}

// Spec: §13.10 — a present ~/.podium/registry.yaml marks the configuration
// explicit even with no PODIUM_* env set.
func TestHasExplicitServerConfig_RegistryYAMLIsExplicit(t *testing.T) {
	clearServerConfigEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.yaml"), []byte("registry:\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !hasExplicitServerConfig() {
		t.Error("hasExplicitServerConfig = false with ~/.podium/registry.yaml present")
	}
}

// Spec: §13.10 — any one of the recognized PODIUM_* server settings marks the
// configuration explicit. This exercises the env-key loop branch.
func TestHasExplicitServerConfig_ServerEnvIsExplicit(t *testing.T) {
	clearServerConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PODIUM_SQLITE_PATH", "/tmp/podium.db")

	if !hasExplicitServerConfig() {
		t.Error("hasExplicitServerConfig = false with PODIUM_SQLITE_PATH set")
	}
}

// Spec: §13.10 — a standard (Postgres) deployment is never a zero-flag standalone
// first run; standaloneStartup returns nil and writes no standalone files.
func TestStandaloneStartup_PostgresReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearServerConfigEnv(t)

	if err := standaloneStartup(&Config{storeType: "postgres", publicURL: "http://127.0.0.1:8080"}); err != nil {
		t.Fatalf("standaloneStartup(postgres) = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); !os.IsNotExist(err) {
		t.Errorf("postgres startup wrote sync.yaml: err=%v", err)
	}
}

// Spec: §13.10 — with explicit configuration present, standaloneStartup writes any
// missing default files and returns nil without emitting the first-run notice.
func TestStandaloneStartup_ExplicitConfigWritesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearServerConfigEnv(t)
	// Mark the configuration explicit via a recognized server env so the
	// auto-bootstrap branch is bypassed and bootstrapStandaloneFiles runs.
	t.Setenv("PODIUM_BIND", "127.0.0.1:8080")

	cfg := &Config{
		bind:      "127.0.0.1:8080",
		publicURL: "http://127.0.0.1:8080",
		storeType: "sqlite",
	}
	if err := standaloneStartup(cfg); err != nil {
		t.Fatalf("standaloneStartup(explicit) = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); err != nil {
		t.Errorf("explicit-config startup did not write sync.yaml: err=%v", err)
	}
}

// Spec: §13.10 — with no explicit configuration, --strict (PODIUM_NO_AUTOSTANDALONE)
// makes a missing config a hard error rather than a cue to auto-bootstrap.
func TestStandaloneStartup_NoAutostandaloneIsHardError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearServerConfigEnv(t)
	t.Setenv("PODIUM_NO_AUTOSTANDALONE", "1")

	err := standaloneStartup(&Config{bind: "127.0.0.1:8080", storeType: "sqlite", publicURL: "http://127.0.0.1:8080"})
	if err == nil {
		t.Fatal("standaloneStartup with --strict and no config = nil, want a hard error")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("hard-error path still wrote sync.yaml: err=%v", statErr)
	}
}

// Spec: §13.10 — zero-flag first run: no explicit config and no --strict emits the
// first-run notice and writes the standalone default files.
func TestStandaloneStartup_ZeroFlagBootstraps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearServerConfigEnv(t)

	cfg := &Config{
		bind:      "127.0.0.1:8080",
		publicURL: "http://127.0.0.1:8080",
		storeType: "sqlite",
	}
	if err := standaloneStartup(cfg); err != nil {
		t.Fatalf("standaloneStartup(zero-flag) = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".podium", "sync.yaml")); err != nil {
		t.Errorf("zero-flag startup did not write sync.yaml: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".podium", "registry.yaml")); err != nil {
		t.Errorf("zero-flag startup did not write registry.yaml: err=%v", err)
	}
}

// Spec: §13.10 — writeFileIfAbsent writes the file when the path is absent and
// reports it wrote.
func TestWriteFileIfAbsent_WritesWhenAbsent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "new.yaml")

	wrote, err := writeFileIfAbsent(path, []byte("data\n"))
	if err != nil {
		t.Fatalf("writeFileIfAbsent = %v, want nil", err)
	}
	if !wrote {
		t.Error("writeFileIfAbsent reported false on an absent path, want true")
	}
	if got := mustReadFile(t, path); got != "data\n" {
		t.Errorf("file contents = %q, want %q", got, "data\n")
	}
}

// Spec: §13.10 — writeFileIfAbsent leaves an existing file untouched and reports it
// did not write, so an operator's hand-written config is never overwritten.
func TestWriteFileIfAbsent_SkipsWhenPresent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "existing.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := writeFileIfAbsent(path, []byte("replacement\n"))
	if err != nil {
		t.Fatalf("writeFileIfAbsent = %v, want nil", err)
	}
	if wrote {
		t.Error("writeFileIfAbsent reported true on an existing path, want false")
	}
	if got := mustReadFile(t, path); got != "original\n" {
		t.Errorf("existing file clobbered: got %q, want %q", got, "original\n")
	}
}

// Spec: §13.10 — writeFileIfAbsent surfaces a stat/write failure other than
// not-exist. A path whose parent is a regular file cannot be stat-ed as absent
// nor created, so the call returns an error.
func TestWriteFileIfAbsent_ErrorPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// <blocker>/child treats a regular file as a directory: the write fails with a
	// non-not-exist error (ENOTDIR).
	wrote, err := writeFileIfAbsent(filepath.Join(blocker, "child.yaml"), []byte("data"))
	if err == nil {
		t.Fatal("writeFileIfAbsent under a file parent = nil, want an error")
	}
	if wrote {
		t.Error("writeFileIfAbsent reported a write on the error path, want false")
	}
}

// nonOutboxStore is a store.Store that does not implement store.VectorOutbox, so
// startVectorOutboxWorker's capability probe returns before constructing a worker.
type nonOutboxStore struct{ store.Store }

// clearServerConfigEnv unsets every env var hasExplicitServerConfig and the
// standalone bootstrap consult, so a test starts from a clean zero-flag baseline
// regardless of the ambient environment.
func clearServerConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PODIUM_CONFIG_FILE", "PODIUM_STANDALONE", "PODIUM_NO_AUTOSTANDALONE",
		"PODIUM_BIND", "PODIUM_REGISTRY_STORE", "PODIUM_POSTGRES_DSN",
		"PODIUM_SQLITE_PATH", "PODIUM_OBJECT_STORE", "PODIUM_FILESYSTEM_ROOT",
		"PODIUM_S3_BUCKET", "PODIUM_S3_ENDPOINT", "PODIUM_VECTOR_BACKEND",
		"PODIUM_IDENTITY_PROVIDER", "PODIUM_PUBLIC_MODE", "PODIUM_LAYER_PATH",
	} {
		t.Setenv(k, "")
	}
}

// waitForDepth polls the outbox depth until it equals want or the deadline
// elapses. It returns true on a match. The worker drains on its own goroutine, so
// a short poll keeps the test deterministic without coupling to the tick cadence.
func waitForDepth(t *testing.T, ob store.VectorOutbox, want int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		depth, _, err := ob.VectorOutboxStats(context.Background())
		if err == nil && depth == want {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	depth, _, err := ob.VectorOutboxStats(context.Background())
	return err == nil && depth == want
}
