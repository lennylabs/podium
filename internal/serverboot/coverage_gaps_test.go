package serverboot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// scrapeMetrics renders the /metrics body once so a test can assert which
// counter a record call incremented.
func scrapeMetrics(t *testing.T, m *metrics.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	return string(body)
}

// Spec: §13.8 — countIngest records one ingest outcome. A nil error increments
// podium_ingest_success_total and a non-nil error increments
// podium_ingest_failure_total.
func TestCountIngest_RecordsSuccessAndFailure(t *testing.T) {
	t.Parallel()
	m := metrics.New()

	countIngest(m, nil)
	countIngest(m, errors.New("boom"))
	countIngest(m, nil)

	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `podium_ingest_success_total 2`) {
		t.Errorf("success counter not 2 after two nil-error calls\n%s", body)
	}
	if !strings.Contains(body, `podium_ingest_failure_total 1`) {
		t.Errorf("failure counter not 1 after one error call\n%s", body)
	}
}

// Spec: §13.8 — a nil metric registry (PODIUM_METRICS=false) makes countIngest a
// no-op rather than panicking, so ingest still runs with metrics disabled.
func TestCountIngest_NilRegistryIsNoOp(t *testing.T) {
	t.Parallel()
	countIngest(nil, nil)
	countIngest(nil, errors.New("boom"))
}

// Spec: §4.6 — sourceProviderFor maps the built-in source types to their
// providers and reports an unknown type as an invalid config so the runner can
// surface registry.invalid_argument.
func TestSourceProviderFor_KnownAndUnknown(t *testing.T) {
	t.Parallel()

	git, err := sourceProviderFor("git")
	if err != nil {
		t.Fatalf("git: unexpected error %v", err)
	}
	if _, ok := git.(source.Git); !ok {
		t.Errorf("git provider type = %T, want source.Git", git)
	}

	local, err := sourceProviderFor("local")
	if err != nil {
		t.Fatalf("local: unexpected error %v", err)
	}
	if _, ok := local.(source.Local); !ok {
		t.Errorf("local provider type = %T, want source.Local", local)
	}

	prov, err := sourceProviderFor("svn")
	if prov != nil {
		t.Errorf("unknown source provider = %T, want nil", prov)
	}
	if !errors.Is(err, source.ErrInvalidConfig) {
		t.Errorf("unknown source error = %v, want ErrInvalidConfig", err)
	}
}

// Spec: §4.7.2 — applyBreakGlass leaves the freeze windows untouched when there
// are no windows to bypass or no grant was supplied, so a manual reingest with
// no override cannot accidentally mark a window as bypassed.
func TestApplyBreakGlass_NoWindowsOrNoGrant(t *testing.T) {
	t.Parallel()

	if got := applyBreakGlass(nil, &server.BreakGlass{Justification: "x"}, "alice@acme.com"); got != nil {
		t.Errorf("no windows: got %v, want nil (unchanged)", got)
	}

	windows := []ingest.FreezeWindow{{Name: "weekend", Blocks: []string{"ingest"}}}
	got := applyBreakGlass(windows, nil, "alice@acme.com")
	if len(got) != 1 || got[0].BreakGlass {
		t.Errorf("nil grant: window should be returned unchanged, got %+v", got)
	}
}

// Spec: §4.7.2 — when a grant is supplied, applyBreakGlass attaches the
// justification, the approvers, and a fresh grant timestamp to a copy of each
// window. The triggering caller is prepended as one approver so a single
// supplied approver still yields the two distinct approvers the dual-signoff
// rule requires, and the original windows are not mutated in place.
func TestApplyBreakGlass_AttachesGrantAndPrependsCaller(t *testing.T) {
	t.Parallel()

	windows := []ingest.FreezeWindow{
		{Name: "weekend", Blocks: []string{"ingest"}},
		{Name: "release", Blocks: []string{"ingest"}},
	}
	bg := &server.BreakGlass{Justification: "hotfix CVE-2026-1", Approvers: []string{"bob@acme.com"}}
	before := time.Now().UTC()

	got := applyBreakGlass(windows, bg, "alice@acme.com")

	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2", len(got))
	}
	for _, w := range got {
		if !w.BreakGlass {
			t.Errorf("window %q: BreakGlass not set", w.Name)
		}
		if w.Justification != "hotfix CVE-2026-1" {
			t.Errorf("window %q: justification = %q", w.Name, w.Justification)
		}
		if len(w.Approvers) != 2 || w.Approvers[0] != "alice@acme.com" || w.Approvers[1] != "bob@acme.com" {
			t.Errorf("window %q: approvers = %v, want [caller, supplied]", w.Name, w.Approvers)
		}
		if w.GrantedAt.Before(before) {
			t.Errorf("window %q: GrantedAt %v predates the call", w.Name, w.GrantedAt)
		}
		// The attached grant must satisfy the §4.7.2 rule (two distinct
		// approvers, non-empty justification, within the auto-expiry window).
		if err := w.ValidateBreakGlass(time.Now().UTC()); err != nil {
			t.Errorf("window %q: ValidateBreakGlass = %v, want nil", w.Name, err)
		}
	}

	// The source slice must not be mutated in place.
	if windows[0].BreakGlass || windows[0].Justification != "" || windows[0].Approvers != nil {
		t.Errorf("source window mutated: %+v", windows[0])
	}
}

// Spec: §4.7.2 — an empty caller (anonymous standalone) is not prepended as an
// approver, so the grant carries only the explicitly supplied approvers.
func TestApplyBreakGlass_EmptyCallerNotPrepended(t *testing.T) {
	t.Parallel()

	windows := []ingest.FreezeWindow{{Name: "weekend", Blocks: []string{"ingest"}}}
	bg := &server.BreakGlass{Justification: "j", Approvers: []string{"bob@acme.com", "carol@acme.com"}}

	got := applyBreakGlass(windows, bg, "")
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if len(got[0].Approvers) != 2 || got[0].Approvers[0] != "bob@acme.com" {
		t.Errorf("approvers = %v, want the supplied pair without a prepended caller", got[0].Approvers)
	}
}

// Spec: §8.1 — reingestCaller returns the empty string when the context carries
// no per-request audit metadata (an anonymous standalone caller), so the emitted
// events name no operator rather than panicking.
func TestReingestCaller_NoMetadataIsEmpty(t *testing.T) {
	t.Parallel()
	if got := reingestCaller(context.Background()); got != "" {
		t.Errorf("reingestCaller(no meta) = %q, want empty", got)
	}
}

// Spec: §8.1 / §8.3 — ingestAuditEmitter adapts the sink to the ingest emitter
// closure. The emitted event carries the supplied caller and lands in the audit
// log with its type, target, and context fields.
func TestIngestAuditEmitter_EmitsEventToSink(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	emit := ingestAuditEmitter(context.Background(), sink, audit.NewPIIScrubber(), "alice@acme.com")
	emit(string(audit.EventLayerIngested), "acme-base", map[string]string{"artifacts": "3"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		`"type":"layer.ingested"`,
		`"target":"acme-base"`,
		`"artifacts":"3"`,
		`alice@acme.com`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted event missing %q\nlog:\n%s", want, got)
		}
	}
}

// Spec: §8.2 — a nil scrubber disables query-text scrubbing, so the emitter
// still records the event verbatim rather than dropping it.
func TestIngestAuditEmitter_NilScrubberStillEmits(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	emit := ingestAuditEmitter(context.Background(), sink, nil, "")
	emit(string(audit.EventArtifactPublished), "acme/style", nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(data), `"type":"artifact.published"`) {
		t.Errorf("nil scrubber dropped the event:\n%s", data)
	}
}

// Spec: §13.10 / §13.2.2 — buildReingestRunner past the audit-volume gate
// resolves the layer's source provider and surfaces an unknown source type as
// the invalid-config error. The two pre-existing tests cover the spent-budget
// gate and the disabled-budget path; this asserts the source-resolution failure
// is the invalid-config error and is recorded as an ingest failure.
func TestBuildReingestRunner_UnknownSourceTypeIsInvalidConfig(t *testing.T) {
	t.Parallel()
	mreg := metrics.New()
	meter := server.NewAuditVolumeMeter(0) // disabled budget: pass the gate

	runner := buildReingestRunner(
		store.NewMemory(), nil, &Config{}, nil, nil, nil, nil, mreg, meter,
		"default", false, collocatedVectorIngest{},
	)
	_, err := runner(context.Background(), store.LayerConfig{SourceType: "svn"}, nil)
	if !errors.Is(err, source.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if body := scrapeMetrics(t, mreg); !strings.Contains(body, `podium_ingest_failure_total 1`) {
		t.Errorf("source-resolution failure not counted as an ingest failure\n%s", body)
	}
}

// Spec: §4.7.8 — buildReingestRunner counts the spent-budget rejection as an
// ingest failure so the §13.8 failure counter reflects the refused write.
func TestBuildReingestRunner_BudgetRejectionCounted(t *testing.T) {
	t.Parallel()
	mreg := metrics.New()
	meter := server.NewAuditVolumeMeter(1)
	meter.Record("default") // spend the single-event budget

	runner := buildReingestRunner(
		store.NewMemory(), nil, &Config{}, nil, nil, nil, nil, mreg, meter,
		"default", false, collocatedVectorIngest{},
	)
	if _, err := runner(context.Background(), store.LayerConfig{SourceType: "git"}, nil); !errors.Is(err, ingest.ErrAuditVolumeExceeded) {
		t.Fatalf("err = %v, want ErrAuditVolumeExceeded", err)
	}
	if body := scrapeMetrics(t, mreg); !strings.Contains(body, `podium_ingest_failure_total 1`) {
		t.Errorf("budget rejection not counted as an ingest failure\n%s", body)
	}
}

// Spec: §7.3.2 — parseDurationList parses a comma-separated Go-duration list and
// drops entries that do not parse or are negative. An empty or fully invalid
// input returns nil so the caller keeps its default backoff schedule.
func TestParseDurationList(t *testing.T) {
	t.Parallel()

	if got := parseDurationList(""); got != nil {
		t.Errorf("empty input = %v, want nil", got)
	}
	if got := parseDurationList("  ,  ,"); got != nil {
		t.Errorf("whitespace-only input = %v, want nil", got)
	}
	if got := parseDurationList("nonsense,-5s,xyz"); got != nil {
		t.Errorf("fully invalid input = %v, want nil", got)
	}

	got := parseDurationList(" 1s , bad , 5s , -2s , 30s ")
	want := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %v, want %v (invalid and negative entries dropped)", i, got[i], want[i])
		}
	}
}

// Spec: §7.3.2 — envInt64 returns the parsed value of the env var and falls back
// to the default when the var is unset, unparseable, or negative.
func TestEnvInt64(t *testing.T) {
	const key = "PODIUM_TEST_ENVINT64"

	t.Run("unset returns default", func(t *testing.T) {
		t.Setenv(key, "")
		if got := envInt64(key, 7); got != 7 {
			t.Errorf("unset = %d, want default 7", got)
		}
	})
	t.Run("valid value parsed", func(t *testing.T) {
		t.Setenv(key, "1048576")
		if got := envInt64(key, 7); got != 1048576 {
			t.Errorf("valid = %d, want 1048576", got)
		}
	})
	t.Run("zero is honored", func(t *testing.T) {
		t.Setenv(key, "0")
		if got := envInt64(key, 7); got != 0 {
			t.Errorf("zero = %d, want 0 (not the default)", got)
		}
	})
	t.Run("unparseable returns default", func(t *testing.T) {
		t.Setenv(key, "not-a-number")
		if got := envInt64(key, 7); got != 7 {
			t.Errorf("unparseable = %d, want default 7", got)
		}
	})
	t.Run("negative returns default", func(t *testing.T) {
		t.Setenv(key, "-3")
		if got := envInt64(key, 7); got != 7 {
			t.Errorf("negative = %d, want default 7", got)
		}
	})
}

// Spec: §8.6 — loadOrGenerateAuditSigner generates a fresh keypair on the first
// call to a missing path and reloads the same key on the second call. The signer
// is a RegistryManagedKey whose KeyID is the sha256 fingerprint of the public
// key, and that fingerprint is stable across reloads so the envelope key_id
// pins to one key across restarts.
func TestLoadOrGenerateAuditSigner_StableKeyIDFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.key")

	signer1, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	signer2, err := loadOrGenerateAuditSigner(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	rk1, ok := signer1.(sign.RegistryManagedKey)
	if !ok {
		t.Fatalf("signer1 type = %T, want sign.RegistryManagedKey", signer1)
	}
	rk2, ok := signer2.(sign.RegistryManagedKey)
	if !ok {
		t.Fatalf("signer2 type = %T, want sign.RegistryManagedKey", signer2)
	}

	if rk1.KeyID == "" {
		t.Error("KeyID is empty; the envelope would carry no key fingerprint")
	}
	if rk1.KeyID != rk2.KeyID {
		t.Errorf("KeyID changed across reloads: %s vs %s", rk1.KeyID, rk2.KeyID)
	}
	if want := keyIDFor(rk1.PublicKey); rk1.KeyID != want {
		t.Errorf("KeyID = %s, want sha256 fingerprint %s of the public key", rk1.KeyID, want)
	}
}

// Spec: §8.6 — loadOrGenerateAuditSigner surfaces a read error other than
// not-exist rather than silently generating a new key. A path whose parent is a
// regular file (so the path cannot be a readable key and cannot be created)
// produces an error.
func TestLoadOrGenerateAuditSigner_UnreadablePathErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create a regular file, then treat it as a directory in the key path. The
	// open of <file>/audit.key fails with a non-not-exist error (ENOTDIR), and
	// the subsequent MkdirAll also fails, so the signer build returns an error.
	notADir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	_, err := loadOrGenerateAuditSigner(filepath.Join(notADir, "audit.key"))
	if err == nil {
		t.Fatal("expected an error when the key path is unreachable, got nil")
	}
}
