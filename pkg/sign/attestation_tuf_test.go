package sign_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/internal/testharness/tufharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// pgRootJSON is a harness root that passes the public-good rule: it lists
// a transparency log and a certificate transparency log.
func pgRootJSON(h *sh.Harness) []byte {
	return h.TrustedRootJSON(sh.WithCTLogKey())
}

// logLines collects the lines a source logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// tufFixture is a TUF repository serving a harness root, the harness that
// signs bundles under that root, and an embedded root from another
// harness, so a test can tell which root verifies a bundle.
type tufFixture struct {
	served, embedded *sh.Harness
	repo             *tufharness.Repo
	clk              *clock.Frozen
	logs             *logLines
	dir              string
}

func newTUFFixture(t *testing.T) *tufFixture {
	t.Helper()
	f := &tufFixture{served: sh.New(t), embedded: sh.New(t), logs: &logLines{}, dir: filepath.Join(t.TempDir(), "public-good")}
	f.repo = tufharness.New(t, pgRootJSON(f.served))
	f.clk = clock.NewFrozen(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	return f
}

// config is a source configuration over the fixture with the given
// embedded build date.
func (f *tufFixture) config(embeddedAt time.Time) sign.TUFRootConfig {
	return sign.TUFRootConfig{
		Dir: f.dir, MirrorURL: f.repo.URL(), TUFRoot: f.repo.RootJSON(),
		Embedded: pgRootJSON(f.embedded), EmbeddedTime: embeddedAt,
		Refresh: 24 * time.Hour, Timeout: 5 * time.Second, MaxAge: sign.DefaultRootMaxAge,
		Clock: f.clk, Logf: f.logs.logf,
	}
}

func (f *tufFixture) source(t *testing.T, cfg sign.TUFRootConfig) *sign.TUFAttestationRoot {
	t.Helper()
	src, err := sign.NewTUFAttestationRoot(cfg)
	if err != nil {
		t.Fatalf("NewTUFAttestationRoot: %v", err)
	}
	return src
}

// verifiesUnder reports whether a bundle of h verifies under src.
func verifiesUnder(t *testing.T, src sign.AttestationRootSource, h *sh.Harness) error {
	t.Helper()
	inst := sign.AttestationInstance{Name: sign.PublicGoodInstance, Root: src, TransparencyLog: true, CertificateTransparency: true}
	_, err := sign.VerifyAttestationBundle(context.Background(), pgBundle(t, h), inst)
	return err
}

// compact returns raw as one line of JSON.
func compact(t *testing.T, raw []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return b.Bytes()
}

func digestOf(t *testing.T, raw []byte) string {
	t.Helper()
	r, err := sign.ParseAttestationRoot(raw)
	if err != nil {
		t.Fatalf("ParseAttestationRoot: %v", err)
	}
	return r.Digest()
}

// Spec: §4.7.11 — with no copy on disk the source serves the embedded root
// and makes no request; a refresh through TUF replaces it with the served
// target, writes the verified copy and its time to disk, and a later
// process starts from that copy with the time of the refresh.
func TestTUFRoot_RefreshReplacesEmbeddedAndPersists(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	embeddedAt := f.clk.Now().Add(-48 * time.Hour)
	src := f.source(t, f.config(embeddedAt))
	if st := src.Status(); st.Origin != sign.RootEmbedded || !st.Time.Equal(embeddedAt) {
		t.Fatalf("status before refresh = %+v, want the embedded copy at its build date", st)
	}
	if f.repo.Requests() != 0 {
		t.Fatalf("construction sent %d requests, want none", f.repo.Requests())
	}
	if err := verifiesUnder(t, src, f.embedded); err != nil {
		t.Fatalf("bundle under the embedded root: %v", err)
	}
	if err := verifiesUnder(t, src, f.served); err == nil {
		t.Fatal("a bundle of the served root verified before any refresh")
	}

	f.clk.Advance(time.Hour)
	refreshedAt := f.clk.Now()
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	st := src.Status()
	if st.Origin != sign.RootRefreshed || !st.Time.Equal(refreshedAt) || st.Digest != digestOf(t, pgRootJSON(f.served)) {
		t.Fatalf("status after refresh = %+v, want the served root at %v", st, refreshedAt)
	}
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("bundle under the refreshed root: %v", err)
	}
	copyRaw, err := os.ReadFile(filepath.Join(f.dir, "trusted_root.json"))
	if err != nil || digestOf(t, copyRaw) != st.Digest {
		t.Fatalf("copy on disk: %v, want the refreshed root", err)
	}

	f.repo.SetMode(tufharness.Fail)
	f.clk.Advance(time.Hour)
	again := f.source(t, f.config(embeddedAt))
	st2 := again.Status()
	if st2.Origin != sign.RootFromDisk || !st2.Time.Equal(refreshedAt) || st2.Digest != st.Digest {
		t.Fatalf("restarted status = %+v, want the disk copy refreshed at %v", st2, refreshedAt)
	}
	if err := verifiesUnder(t, again, f.served); err != nil {
		t.Fatalf("bundle under the disk copy: %v", err)
	}
}

// Spec: §4.7.11 — a refresh that fails leaves the root in use and the copy
// on disk unchanged: a repository that answers 503, and one whose
// timestamp has expired, which the TUF client refuses.
func TestTUFRoot_FailedRefreshKeepsLastVerifiedCopy(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	src := f.source(t, f.config(time.Time{}))
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := src.Status()

	f.repo.SetMode(tufharness.Fail)
	if err := src.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh against a failing repository succeeded")
	}
	f.repo.SetMode(tufharness.Serve)
	other := sh.New(t)
	f.repo.PublishExpired(pgRootJSON(other))
	if err := src.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh with an expired timestamp succeeded")
	}
	if got := src.Status(); !reflect.DeepEqual(got, want) {
		t.Fatalf("status after failed refreshes = %+v, want %+v", got, want)
	}
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("bundle under the kept root: %v", err)
	}
	copyRaw, _ := os.ReadFile(filepath.Join(f.dir, "trusted_root.json"))
	if digestOf(t, copyRaw) != want.Digest {
		t.Fatal("a failed refresh replaced the copy on disk")
	}
}

// Spec: §4.7.11 — a refreshed root that fails a load rule counts as a
// failed refresh: a root with no certificate transparency log, a root
// with a CA-first timestamp authority entry, and a document with two roots.
func TestTUFRoot_RefreshedRootMustPassLoadRules(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	src := f.source(t, f.config(time.Time{}))
	want := src.Status()
	noCT := f.served.TrustedRootJSON()
	twoRoots := append(append(compact(t, pgRootJSON(f.served)), '\n'), compact(t, pgRootJSON(f.served))...)
	for name, raw := range map[string][]byte{
		"no certificate transparency log": noCT,
		"CA-first authority":              f.served.TrustedRootJSON(sh.WithCTLogKey(), sh.WithCAFirstTSA()),
		"two roots":                       twoRoots,
	} {
		f.repo.Publish(raw)
		err := src.Refresh(context.Background())
		if !errors.Is(err, sign.ErrAttestationRootInvalid) {
			t.Errorf("%s: Refresh = %v, want ErrAttestationRootInvalid", name, err)
		}
		if got := src.Status(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: status = %+v, want it unchanged", name, got)
		}
	}
}

// Spec: §4.7.11 — a refresh runs under PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT,
// which covers every request: against a repository that never answers it
// ends at the deadline, verification keeps the disk copy, and the next
// attempt follows the retry delay.
func TestTUFRoot_RefreshDeadline(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	cfg := f.config(time.Time{})
	if err := f.source(t, cfg).Refresh(context.Background()); err != nil {
		t.Fatalf("seed the disk copy: %v", err)
	}
	f.repo.SetMode(tufharness.Hang)
	cfg.Timeout = 300 * time.Millisecond
	src := f.source(t, cfg)
	if src.Status().Origin != sign.RootFromDisk {
		t.Fatalf("origin = %s, want the disk copy", src.Status().Origin)
	}
	start := time.Now()
	err := src.Refresh(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Refresh = %v, want the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Refresh returned after %v, want shortly after the 300ms deadline", elapsed)
	}
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("bundle under the disk copy after the deadline: %v", err)
	}

	sched := newSchedule(t, &cfg, f, 0.5)
	sched.want(0)
	sched.want(time.Minute) // jitter 0.5 → factor 1.0
	sched.stop()
	if !f.logs.contains("context deadline exceeded") {
		t.Errorf("logs = %q, want the failed refresh with its deadline", f.logs.lines)
	}
}

// schedule drives Run through an injected After that reports each delay.
type schedule struct {
	t      *testing.T
	delays chan time.Duration
	cancel context.CancelFunc
	done   chan struct{}
}

// newSchedule starts Run over cfg with an After that blocks until the
// test reads the delay, and a fixed jitter value.
func newSchedule(t *testing.T, cfg *sign.TUFRootConfig, f *tufFixture, jitter float64) *schedule {
	t.Helper()
	s := &schedule{t: t, delays: make(chan time.Duration), done: make(chan struct{})}
	cfg.Jitter = func() float64 { return jitter }
	cfg.After = func(d time.Duration) <-chan time.Time {
		s.delays <- d
		c := make(chan time.Time, 1)
		c <- time.Time{}
		return c
	}
	src := f.source(t, *cfg)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		defer close(s.done)
		src.Run(ctx)
	}()
	t.Cleanup(s.stop)
	return s
}

// want reads the next delay Run asks for.
func (s *schedule) want(d time.Duration) {
	s.t.Helper()
	select {
	case got := <-s.delays:
		if got != d {
			s.t.Fatalf("delay = %v, want %v", got, d)
		}
	case <-time.After(30 * time.Second):
		s.t.Fatalf("Run asked for no delay, want %v", d)
	}
}

// stop cancels Run and waits for it to return, reading any delay it asks
// for meanwhile.
func (s *schedule) stop() {
	s.cancel()
	for {
		select {
		case <-s.done:
			return
		case <-s.delays:
		}
	}
}

// Spec: §4.7.11 — Run refreshes at once, then after a failure waits 1
// minute, doubling after each further failure up to the refresh interval,
// each delay multiplied by a random factor between 0.5 and 1.5; a success
// returns the schedule to the interval, and the next failure starts again
// at 1 minute.
func TestTUFRoot_RetrySchedule(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	cfg := f.config(time.Time{})
	cfg.Refresh = 10 * time.Minute
	f.repo.SetMode(tufharness.Fail)
	s := newSchedule(t, &cfg, f, 0.5)
	s.want(0)
	for _, d := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute} {
		s.want(d)
	}
	f.repo.SetMode(tufharness.Serve)
	s.want(10 * time.Minute) // the refresh before this read failed; the next succeeds
	s.want(10 * time.Minute) // after the success: the interval, with no factor
	f.repo.SetMode(tufharness.Fail)
	s.want(time.Minute)
	s.stop()
	if !f.logs.contains("refreshed the public-good trusted root") {
		t.Errorf("logs = %q, want the successful refresh", f.logs.lines)
	}
}

// Spec: §4.7.11 — each retry delay is multiplied by a factor between 0.5
// and 1.5 drawn from the jitter source.
func TestTUFRoot_RetryDelayJitter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		jitter float64
		want   time.Duration
	}{{0, 30 * time.Second}, {0.999999, 90 * time.Second}} {
		f := newTUFFixture(t)
		f.repo.SetMode(tufharness.Fail)
		cfg := f.config(time.Time{})
		s := newSchedule(t, &cfg, f, tc.jitter)
		s.want(0)
		s.wantNear(tc.want)
		s.stop()
	}
}

// wantNear reads the next delay and checks it within a millisecond.
func (s *schedule) wantNear(d time.Duration) {
	s.t.Helper()
	got := <-s.delays
	if diff := got - d; diff < -time.Millisecond || diff > time.Millisecond {
		s.t.Fatalf("delay = %v, want %v", got, d)
	}
}

// Spec: §4.7.11 — verification on public-good is refused once the root in
// use is older than PODIUM_SIGSTORE_ROOT_MAX_AGE, counted from the last
// successful refresh; the refusal is ErrAttestationRootStale and no
// refusal of the bundle; a refresh makes the root usable again.
func TestTUFRoot_MaxAgeOfRefreshedCopy(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	cfg := f.config(time.Time{})
	cfg.MaxAge = 72 * time.Hour
	src := f.source(t, cfg)
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	f.clk.Advance(72 * time.Hour)
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("at exactly the maximum age: %v", err)
	}
	f.clk.Advance(time.Second)
	err := verifiesUnder(t, src, f.served)
	if !errors.Is(err, sign.ErrAttestationRootStale) || errors.Is(err, sign.ErrAttestationRefused) {
		t.Fatalf("past the maximum age: %v, want ErrAttestationRootStale and no bundle refusal", err)
	}
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := verifiesUnder(t, src, f.served); err != nil {
		t.Fatalf("after a new refresh: %v", err)
	}
}

// Spec: §4.7.11 — the embedded copy's age counts from the build date of a
// release binary; a source build records none, so its embedded copy is
// older than every limit until a refresh succeeds, and a limit of 0
// removes the check.
func TestTUFRoot_MaxAgeOfEmbeddedCopy(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	f.repo.SetMode(tufharness.Fail)

	released := f.config(f.clk.Now().Add(-89 * 24 * time.Hour))
	if err := verifiesUnder(t, f.source(t, released), f.embedded); err != nil {
		t.Fatalf("release build at 89 days: %v", err)
	}
	old := f.config(f.clk.Now().Add(-91 * 24 * time.Hour))
	if err := verifiesUnder(t, f.source(t, old), f.embedded); !errors.Is(err, sign.ErrAttestationRootStale) {
		t.Fatalf("release build at 91 days: %v, want stale", err)
	}
	source := f.source(t, f.config(time.Time{}))
	err := verifiesUnder(t, source, f.embedded)
	if !errors.Is(err, sign.ErrAttestationRootStale) || !strings.Contains(err.Error(), "unknown age") {
		t.Fatalf("source build: %v, want stale with an unknown age", err)
	}
	noLimit := f.config(time.Time{})
	noLimit.MaxAge = 0
	if err := verifiesUnder(t, f.source(t, noLimit), f.embedded); err != nil {
		t.Fatalf("source build with no limit: %v", err)
	}
	f.repo.SetMode(tufharness.Serve)
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := verifiesUnder(t, source, f.served); err != nil {
		t.Fatalf("source build after a refresh: %v", err)
	}
}

// Spec: §4.7.11 — a failed refresh logs a warning once the root in use is
// older than half the maximum age, and none while it is younger.
func TestTUFRoot_HalfAgeWarning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		age  time.Duration
		warn bool
	}{{44 * 24 * time.Hour, false}, {46 * 24 * time.Hour, true}} {
		f := newTUFFixture(t)
		f.repo.SetMode(tufharness.Fail)
		cfg := f.config(f.clk.Now().Add(-tc.age))
		s := newSchedule(t, &cfg, f, 0.5)
		s.want(0)
		s.want(time.Minute)
		s.stop()
		if got := f.logs.contains("past half of PODIUM_SIGSTORE_ROOT_MAX_AGE"); got != tc.warn {
			t.Errorf("age %v: warning logged = %v, want %v (logs %q)", tc.age, got, tc.warn, f.logs.lines)
		}
		if !f.logs.contains("next attempt in") {
			t.Errorf("age %v: the failed refresh was not logged", tc.age)
		}
	}
}

// Spec: §4.7.11 — a copy on disk that does not load, or whose refresh time
// is missing, is ignored and logged, and the embedded copy serves.
func TestTUFRoot_IgnoresBadDiskCopy(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]func(dir string, root []byte){
		"unparseable root": func(dir string, _ []byte) {
			_ = os.WriteFile(filepath.Join(dir, "trusted_root.json"), []byte("{"), 0o600)
			_ = os.WriteFile(filepath.Join(dir, "refreshed_at"), []byte("2026-11-27T00:00:00Z\n"), 0o600)
		},
		"no refresh time": func(dir string, root []byte) {
			_ = os.WriteFile(filepath.Join(dir, "trusted_root.json"), root, 0o600)
		},
		"bad refresh time": func(dir string, root []byte) {
			_ = os.WriteFile(filepath.Join(dir, "trusted_root.json"), root, 0o600)
			_ = os.WriteFile(filepath.Join(dir, "refreshed_at"), []byte("yesterday"), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newTUFFixture(t)
			if err := os.MkdirAll(f.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			write(f.dir, pgRootJSON(f.served))
			src := f.source(t, f.config(time.Time{}))
			if st := src.Status(); st.Origin != sign.RootEmbedded {
				t.Fatalf("origin = %s, want embedded", st.Origin)
			}
			if !f.logs.contains("ignoring the public-good root copy") {
				t.Errorf("logs = %q, want the ignored copy", f.logs.lines)
			}
		})
	}
}

// Spec: §13.12 — a directory that cannot be written keeps the TUF state
// and the copy in memory: the refresh still succeeds and writes nothing.
func TestTUFRoot_UnwritableDirectoryStaysInMemory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop this user from writing")
	}
	f := newTUFFixture(t)
	if err := os.MkdirAll(f.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dir, 0o700) })
	src := f.source(t, f.config(time.Time{}))
	if !f.logs.contains("stay in memory") {
		t.Fatalf("logs = %q, want the in-memory notice", f.logs.lines)
	}
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if src.Status().Origin != sign.RootRefreshed {
		t.Fatalf("origin = %s, want refreshed", src.Status().Origin)
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 0 {
		t.Fatalf("the unwritable directory holds %d entries, want none", len(entries))
	}
}

// Spec: §4.7.11 — the source refuses a configuration it cannot run: no
// directory, an interval or a deadline that is not above zero, a negative
// maximum age, and an embedded root that does not pass the public-good rule.
func TestTUFRoot_ConfigurationErrors(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	for name, edit := range map[string]func(*sign.TUFRootConfig){
		"no directory":        func(c *sign.TUFRootConfig) { c.Dir = "" },
		"zero interval":       func(c *sign.TUFRootConfig) { c.Refresh = 0 },
		"zero deadline":       func(c *sign.TUFRootConfig) { c.Timeout = 0 },
		"negative max age":    func(c *sign.TUFRootConfig) { c.MaxAge = -time.Second },
		"embedded with no CT": func(c *sign.TUFRootConfig) { c.Embedded = f.embedded.TrustedRootJSON() },
	} {
		cfg := f.config(time.Time{})
		edit(&cfg)
		if _, err := sign.NewTUFAttestationRoot(cfg); err == nil {
			t.Errorf("%s: NewTUFAttestationRoot succeeded", name)
		}
	}
}

// Spec: §4.7.11 — FetchPublicGoodRoot returns the verified target and
// refuses one that fails the public-good rule.
func TestFetchPublicGoodRoot(t *testing.T) {
	t.Parallel()
	f := newTUFFixture(t)
	raw, err := sign.FetchPublicGoodRoot(context.Background(), f.repo.URL(), f.repo.RootJSON(), nil)
	if err != nil || digestOf(t, raw) != digestOf(t, pgRootJSON(f.served)) {
		t.Fatalf("FetchPublicGoodRoot = %v, want the served root", err)
	}
	f.repo.Publish(f.served.TrustedRootJSON())
	if _, err := sign.FetchPublicGoodRoot(context.Background(), f.repo.URL(), f.repo.RootJSON(), nil); !errors.Is(err, sign.ErrAttestationRootInvalid) {
		t.Fatalf("FetchPublicGoodRoot of a root with no CT log = %v, want ErrAttestationRootInvalid", err)
	}
	f.repo.SetMode(tufharness.Fail)
	if _, err := sign.FetchPublicGoodRoot(context.Background(), f.repo.URL(), f.repo.RootJSON(), nil); err == nil {
		t.Fatal("FetchPublicGoodRoot against a failing repository succeeded")
	}
}

// Spec: §4.7.11 — the embedded public-good root loads under the load rules
// and lists the transparency logs and certificate transparency logs the
// instance requires.
func TestEmbeddedPublicGoodRoot(t *testing.T) {
	t.Parallel()
	r, err := sign.ParseAttestationRoot(sign.EmbeddedPublicGoodRoot())
	if err != nil {
		t.Fatalf("ParseAttestationRoot: %v", err)
	}
	if err := sign.CheckPublicGoodRoot(r); err != nil {
		t.Fatalf("CheckPublicGoodRoot: %v", err)
	}
	if tlog, ctlog := r.ListsLogs(); !tlog || !ctlog {
		t.Fatalf("ListsLogs = %v, %v, want both", tlog, ctlog)
	}
}
