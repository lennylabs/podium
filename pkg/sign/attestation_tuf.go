package sign

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/lennylabs/podium/internal/clock"
)

// The built-in Sigstore instances of §4.7.11.
const (
	// PublicGoodInstance is the Sigstore public-good instance, the instance
	// of a signer that names none. Its root is refreshed through TUF.
	PublicGoodInstance = "public-good"
	// GitHubInstance is GitHub's instance for private repositories. It has
	// no transparency log and no certificate transparency logs.
	GitHubInstance = "github"
)

// instanceNameRule is the §13.12 instance name rule.
var instanceNameRule = regexp.MustCompile(`\A[a-z0-9][a-z0-9._-]{0,63}\z`)

// ValidInstanceName reports whether name meets the §13.12 instance name
// rule: 1 to 64 characters from a-z, 0-9, '.', '_', and '-', beginning
// with a letter or a digit. The registry's sigstore.instances and a
// consumer policy file apply the one rule.
//
// Spec: §13.12, §4.7.11
func ValidInstanceName(name string) bool {
	return instanceNameRule.MatchString(name)
}

// The §13.12 defaults of the public-good root refresh.
const (
	DefaultRootRefresh        = 24 * time.Hour
	DefaultRootRefreshTimeout = 30 * time.Second
	DefaultRootMaxAge         = 90 * 24 * time.Hour
)

// MinRootRefresh is the shortest refresh interval §13.12 admits. The
// interval must also be at least the refresh deadline.
const MinRootRefresh = time.Minute

// firstRetryDelay is the delay before the first retry of a failed refresh.
// It doubles after each further failure, up to the refresh interval.
const firstRetryDelay = time.Minute

// The file names of the public-good root directory. trustedRootTarget is
// the TUF target the Sigstore repository publishes; rootCopyFile and
// rootTimeFile hold the last verified copy and the time of its refresh,
// apart from the TUF cache, because the TUF timestamp role expires within
// days and a copy kept only as a TUF target would not load once it had.
const (
	trustedRootTarget = "trusted_root.json"
	rootCopyFile      = "trusted_root.json"
	rootTimeFile      = "refreshed_at"
)

// ErrAttestationRootStale signals a public-good root past its maximum age.
// It is no refusal of a bundle: the verifier holds no root it may use.
//
// Spec: §4.7.11.
var ErrAttestationRootStale = errors.New("attestation trusted root is past its maximum age")

// RootOrigin names where a trusted root in use came from.
type RootOrigin string

// The origins of a trusted root.
const (
	RootFromFile  RootOrigin = "file"
	RootRefreshed RootOrigin = "refreshed"
	RootFromDisk  RootOrigin = "disk copy"
	RootEmbedded  RootOrigin = "embedded"
)

// AttestationRootStatus describes the trusted root an instance uses: what a
// start reports and what the root record of re-evaluation reads.
//
// Spec: §4.7.11.
type AttestationRootStatus struct {
	Origin RootOrigin
	// Digest is the root's digest (AttestationRoot.Digest).
	Digest string
	// Time is the root time: the last successful refresh of a refreshed
	// copy, the build date of the embedded copy, or the time a file was
	// read. The zero time means unknown, which is the embedded copy of a
	// binary built from source.
	Time time.Time
	// SHA1TimestampAuthorities lists the authorities whose chain holds a
	// SHA-1 signature.
	SHA1TimestampAuthorities []string
}

// StatusOf reports the status of a root read from a file at readAt.
func StatusOf(r *AttestationRoot, readAt time.Time) AttestationRootStatus {
	return AttestationRootStatus{
		Origin: RootFromFile, Digest: r.Digest(), Time: readAt,
		SHA1TimestampAuthorities: r.SHA1TimestampAuthorities(),
	}
}

// CheckPublicGoodRoot refuses a public-good root that lists no
// transparency log or no certificate transparency log. The instance
// requires both, so such a root would accept no bundle, and a start or a
// refresh reports it rather than run on it.
//
// Spec: §4.7.11.
func CheckPublicGoodRoot(r *AttestationRoot) error {
	tlog, ctlog := r.ListsLogs()
	switch {
	case !tlog:
		return fmt.Errorf("%w: the public-good root lists no transparency log", ErrAttestationRootInvalid)
	case !ctlog:
		return fmt.Errorf("%w: the public-good root lists no certificate transparency log", ErrAttestationRootInvalid)
	}
	return nil
}

// TUFRootConfig configures the refreshed public-good root. Dir, Refresh,
// and Timeout are required; every other member has a production default.
type TUFRootConfig struct {
	// Dir holds the TUF metadata and the last verified copy. A directory
	// that cannot be created or written keeps the state in memory.
	Dir string
	// ReadOnly reads the TUF metadata and the copy in Dir and never writes
	// Dir: each refresh keeps its result in memory. A consumer sets it for
	// a refresh that a repository source configures, which starts from the
	// user's copy and must not replace it.
	ReadOnly bool
	// Memory keeps the TUF state and the copy in memory for the life of the
	// process, seeded from the embedded roots, and reads and writes no
	// directory; Dir is then empty. A consumer that has no usable home sets
	// it, so the TUF client never falls back to the default cache path of
	// the library.
	Memory bool
	// MirrorURL is the TUF repository (default tuf.DefaultMirror).
	MirrorURL string
	// TUFRoot is the TUF root.json the client starts from (default the
	// one sigstore-go embeds).
	TUFRoot []byte
	// Embedded is the trusted root used when no verified copy is on disk
	// (default EmbeddedPublicGoodRoot).
	Embedded []byte
	// EmbeddedTime is the build date of the embedded copy. The zero time
	// means unknown, and an unknown age exceeds every maximum age.
	EmbeddedTime time.Time
	// Refresh is the interval between refreshes.
	Refresh time.Duration
	// Timeout is the deadline of one refresh with all of its requests.
	Timeout time.Duration
	// MaxAge is the maximum age of the root in use; 0 removes the limit.
	MaxAge time.Duration
	// Clock supplies the refresh time and the age (default clock.Real).
	Clock clock.Clock
	// HTTPClient sends the TUF requests (default a client with no timeout
	// of its own and no kept connections: the refresh deadline bounds every
	// request).
	HTTPClient *http.Client
	// Jitter returns a value in [0, 1); a retry delay is multiplied by 0.5
	// plus that value (default math/rand/v2).
	Jitter func() float64
	// After waits for a delay (default time.After). Tests replace it to
	// observe the schedule without waiting.
	After func(time.Duration) <-chan time.Time
	// Logf receives each log line (default log.Printf).
	Logf func(format string, args ...any)
}

// TUFAttestationRoot is the public-good root source of §4.7.11. It serves
// the root in use to the verifier and refreshes it through TUF from Run,
// so AttestationRoot makes no network call.
type TUFAttestationRoot struct {
	cfg TUFRootConfig
	// dirOnce creates Dir at the first refresh, so a source that never
	// refreshes writes nothing. persist is false when Dir cannot be
	// written; the TUF state and the copy then stay in memory for the life
	// of the process.
	dirOnce sync.Once
	persist bool

	// mu guards root, at, and origin, which a refresh replaces together
	// and a verification reads together: the root and the time its age
	// counts from must come from one refresh.
	mu     sync.Mutex
	root   *AttestationRoot
	at     time.Time
	origin RootOrigin
}

// NewTUFAttestationRoot builds the public-good source with no network
// call and no write: Dir is created by the first refresh. Of the copy on
// disk, when one loads and passes the public-good rule, and the embedded
// copy, it uses the one with the later root time, because both were
// verified through TUF. An embedded copy of unknown time counts as the
// earlier one, and the copy on disk wins a tie, so an upgrade during a TUF
// outage serves the newer root the binary carries. It fails only on a
// configuration error, which includes an embedded root that does not load.
//
// Spec: §4.7.11.
func NewTUFAttestationRoot(cfg TUFRootConfig) (*TUFAttestationRoot, error) {
	if err := cfg.fill(); err != nil {
		return nil, err
	}
	embedded, err := parsePublicGoodRoot(cfg.Embedded)
	if err != nil {
		return nil, fmt.Errorf("attestation: embedded public-good root: %w", err)
	}
	s := &TUFAttestationRoot{cfg: cfg, root: embedded, at: cfg.EmbeddedTime, origin: RootEmbedded}
	if cfg.Memory {
		return s, nil
	}
	if r, at, ok := s.loadDiskCopy(); ok && (cfg.EmbeddedTime.IsZero() || !at.Before(cfg.EmbeddedTime)) {
		s.root, s.at, s.origin = r, at, RootFromDisk
	}
	return s, nil
}

// fill checks the required members and sets the defaults.
func (c *TUFRootConfig) fill() error {
	switch {
	case c.Memory && (c.Dir != "" || c.ReadOnly):
		return errors.New("attestation: an in-memory public-good root names no directory")
	case c.Dir == "" && !c.Memory:
		return errors.New("attestation: the public-good root needs a directory")
	case c.Timeout <= 0:
		return fmt.Errorf("attestation: refresh deadline %s is not above zero", c.Timeout)
	case c.Refresh < MinRootRefresh || c.Refresh < c.Timeout:
		return fmt.Errorf("attestation: refresh interval %s is below %s or below the refresh deadline %s", c.Refresh, MinRootRefresh, c.Timeout)
	case c.MaxAge < 0:
		return fmt.Errorf("attestation: maximum age %s is below zero", c.MaxAge)
	}
	if c.MirrorURL == "" {
		c.MirrorURL = tuf.DefaultMirror
	}
	if c.TUFRoot == nil {
		c.TUFRoot = tuf.DefaultRoot()
	}
	if c.Embedded == nil {
		c.Embedded = EmbeddedPublicGoodRoot()
	}
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	if c.HTTPClient == nil {
		// One refresh a day gains nothing from a kept connection, and an
		// idle HTTP/2 connection would keep a reader goroutine alive past
		// the process context.
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableKeepAlives = true
		c.HTTPClient = &http.Client{Transport: t}
	}
	if c.Jitter == nil {
		c.Jitter = rand.Float64
	}
	if c.After == nil {
		c.After = time.After
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
	return nil
}

// parsePublicGoodRoot loads raw under the §4.7.11 load rules and the
// public-good rule.
func parsePublicGoodRoot(raw []byte) (*AttestationRoot, error) {
	r, err := ParseAttestationRoot(raw)
	if err != nil {
		return nil, err
	}
	if err := CheckPublicGoodRoot(r); err != nil {
		return nil, err
	}
	return r, nil
}

// prepareDir creates Dir and reports whether the process can write it. The
// first refresh calls it. A failure is logged and leaves the state in
// memory.
func (s *TUFAttestationRoot) prepareDir() bool {
	if s.cfg.Memory || s.cfg.ReadOnly {
		return false
	}
	err := os.MkdirAll(s.cfg.Dir, 0o700)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(s.cfg.Dir, ".probe-*"); err == nil {
			_ = f.Close()
			err = os.Remove(f.Name())
		}
	}
	if err != nil {
		s.cfg.Logf("attestation: public-good root directory %s cannot be written (%v); the TUF state and the root copy stay in memory", s.cfg.Dir, err)
		return false
	}
	return true
}

// loadDiskCopy reads the last verified copy and its refresh time. An absent
// copy is silent; a copy that does not load is logged and ignored.
func (s *TUFAttestationRoot) loadDiskCopy() (*AttestationRoot, time.Time, bool) {
	raw, err := os.ReadFile(filepath.Join(s.cfg.Dir, rootCopyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, time.Time{}, false
	}
	var r *AttestationRoot
	var at time.Time
	if err == nil {
		r, err = parsePublicGoodRoot(raw)
	}
	if err == nil {
		at, err = readRootTime(filepath.Join(s.cfg.Dir, rootTimeFile))
	}
	if err != nil {
		s.cfg.Logf("attestation: ignoring the public-good root copy in %s: %v", s.cfg.Dir, err)
		return nil, time.Time{}, false
	}
	return r, at, true
}

// readRootTime reads the RFC 3339 refresh time of the copy.
func readRootTime(path string) (time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s: %w", rootTimeFile, err)
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", rootTimeFile, err)
	}
	return t, nil
}

// AttestationRoot returns the root in use, or ErrAttestationRootStale when
// it is past the maximum age. It makes no network call.
//
// Spec: §4.7.11.
func (s *TUFAttestationRoot) AttestationRoot(context.Context) (*AttestationRoot, error) {
	return s.rootUnder(s.cfg.MaxAge, "PODIUM_SIGSTORE_ROOT_MAX_AGE")
}

// rootUnder returns the root in use under the maximum age maxAge, which
// the setting named setting gives.
func (s *TUFAttestationRoot) rootUnder(maxAge time.Duration, setting string) (*AttestationRoot, error) {
	s.mu.Lock()
	r, at := s.root, s.at
	s.mu.Unlock()
	if s.staleUnder(at, maxAge) {
		return nil, fmt.Errorf("%w: the public-good root in use is %s old, above %s (%s)",
			ErrAttestationRootStale, describeAge(s.cfg.Clock.Now(), at), setting, maxAge)
	}
	return r, nil
}

// stale reports whether a root with root time at is past the maximum age.
// An unknown root time exceeds every limit.
func (s *TUFAttestationRoot) stale(at time.Time) bool {
	return s.staleUnder(at, s.cfg.MaxAge)
}

// staleUnder reports whether a root with root time at is past maxAge; 0
// removes the limit.
func (s *TUFAttestationRoot) staleUnder(at time.Time, maxAge time.Duration) bool {
	if maxAge == 0 {
		return false
	}
	return at.IsZero() || s.cfg.Clock.Now().Sub(at) > maxAge
}

// WithMaxAge returns a view of the root in use under another maximum age.
// The view shares the refresh of s, so two policy sources that set their
// own maximum age verify against one refreshed copy, each under its own
// limit.
//
// Spec: §4.7.11 (Trusted roots of a consumer)
func (s *TUFAttestationRoot) WithMaxAge(maxAge time.Duration) AttestationRootSource {
	return maxAgeView{s: s, maxAge: maxAge}
}

// maxAgeView is the root of a TUFAttestationRoot under its own maximum age.
type maxAgeView struct {
	s      *TUFAttestationRoot
	maxAge time.Duration
}

// AttestationRoot returns the shared root, or ErrAttestationRootStale when
// it is past the view's maximum age.
func (v maxAgeView) AttestationRoot(context.Context) (*AttestationRoot, error) {
	return v.s.rootUnder(v.maxAge, "its maximum age")
}

// Status reports the shared root in use.
func (v maxAgeView) Status() AttestationRootStatus { return v.s.Status() }

// describeAge renders the age of a root for a log line or an error.
func describeAge(now, at time.Time) string {
	if at.IsZero() {
		return "of unknown age (a source build records no build date)"
	}
	return now.Sub(at).Round(time.Second).String()
}

// Status reports the root in use.
func (s *TUFAttestationRoot) Status() AttestationRootStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AttestationRootStatus{
		Origin: s.origin, Digest: s.root.Digest(), Time: s.at,
		SHA1TimestampAuthorities: s.root.SHA1TimestampAuthorities(),
	}
}

// Refresh runs one refresh under the refresh deadline. On success the
// refreshed root replaces the root in use and, when the directory can be
// written, the copy on disk. A failure changes nothing.
//
// Spec: §4.7.11.
func (s *TUFAttestationRoot) Refresh(ctx context.Context) error {
	s.dirOnce.Do(func() { s.persist = s.prepareDir() })
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	defer s.cfg.HTTPClient.CloseIdleConnections()
	raw, err := s.fetch(ctx, s.persist)
	if err != nil && s.persist && isFileError(err) {
		// A write of the TUF cache failed, as a rename over a file that
		// another process holds open fails on Windows. The earlier files
		// stay as they are, and this refresh runs again in memory.
		s.cfg.Logf("attestation: the TUF cache in %s cannot be written (%v); refreshing in memory", s.cfg.Dir, err)
		raw, err = s.fetch(ctx, false)
	}
	if err != nil {
		return fmt.Errorf("attestation: refresh the public-good root: %w", err)
	}
	r, err := parsePublicGoodRoot(raw)
	if err != nil {
		return fmt.Errorf("attestation: refreshed public-good root: %w", err)
	}
	now := s.cfg.Clock.Now()
	if s.persist {
		if err := s.writeCopy(raw, now); err != nil {
			s.cfg.Logf("attestation: keeping the refreshed public-good root in memory: %v", err)
		}
	}
	s.mu.Lock()
	s.root, s.at, s.origin = r, now, RootRefreshed
	s.mu.Unlock()
	return nil
}

// fetch runs the TUF client over the mirror under ctx and returns the
// verified trusted_root.json target. With persist false the client writes
// nothing. An in-memory source reads no metadata either: its cache path
// names a directory that cannot exist, because the client reads the
// metadata of a relative path from the working directory, which a
// repository controls.
func (s *TUFAttestationRoot) fetch(ctx context.Context, persist bool) ([]byte, error) {
	dir := s.cfg.Dir
	if s.cfg.Memory {
		dir = filepath.Join(os.DevNull, "podium-tuf")
	}
	return fetchTUFTarget(ctx, s.cfg.MirrorURL, s.cfg.TUFRoot, dir, persist, s.cfg.HTTPClient)
}

// isFileError reports whether err comes from a file operation, which a
// write of the TUF cache returns, rather than from the network or from
// the TUF verification.
func isFileError(err error) bool {
	var pe *fs.PathError
	var le *os.LinkError
	return errors.As(err, &pe) || errors.As(err, &le)
}

// fetchTUFTarget runs the TUF client of sigstore-go over mirror and returns
// the verified trusted_root.json target. The client walks the root chain
// from tufRoot on each call, so a cached root.json that tufRoot does not
// reach is never an anchor. With persist false the client writes nothing
// and reads the metadata cached in dir, which it verifies like any other.
func fetchTUFTarget(ctx context.Context, mirror string, tufRoot []byte, dir string, persist bool, client *http.Client) ([]byte, error) {
	opts := &tuf.Options{
		Root:              tufRoot,
		RepositoryBaseURL: mirror,
		CachePath:         dir,
		DisableLocalCache: !persist,
		Fetcher:           &contextFetcher{ctx: ctx, client: client},
	}
	c, err := tuf.New(opts)
	if err != nil {
		return nil, err
	}
	return c.GetTarget(trustedRootTarget)
}

// FetchPublicGoodRoot runs one TUF refresh from tufRoot against mirror in
// a temporary cache and returns the trusted_root.json target after the
// public-good load rules accept it. tools/sigstoreroot uses it to refresh
// and check the embedded copy. A nil client is http.DefaultClient; ctx
// bounds every request.
//
// Spec: §4.7.11.
func FetchPublicGoodRoot(ctx context.Context, mirror string, tufRoot []byte, client *http.Client) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	dir, err := os.MkdirTemp("", "podium-tuf-*")
	if err != nil {
		return nil, fmt.Errorf("attestation: TUF cache: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	raw, err := fetchTUFTarget(ctx, mirror, tufRoot, dir, true, client)
	if err != nil {
		return nil, fmt.Errorf("attestation: fetch the public-good root: %w", err)
	}
	if _, err := parsePublicGoodRoot(raw); err != nil {
		return nil, fmt.Errorf("attestation: fetched public-good root: %w", err)
	}
	return raw, nil
}

// writeCopy replaces the copy and then its time, each by a rename. A crash
// between the two leaves the new root with the earlier time, which only
// overstates its age.
func (s *TUFAttestationRoot) writeCopy(raw []byte, at time.Time) error {
	if err := writeFileAtomic(s.cfg.Dir, rootCopyFile, raw); err != nil {
		return err
	}
	return writeFileAtomic(s.cfg.Dir, rootTimeFile, []byte(at.UTC().Format(time.RFC3339Nano)+"\n"))
}

// writeFileAtomic writes data to dir/name through a temporary file and a
// rename, so a reader never finds a partial file.
func writeFileAtomic(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// Run refreshes the root at once and then on the §4.7.11 schedule until
// ctx ends: after a success the Refresh interval times a factor between
// 0.9 and 1.1, so replicas started together spread their requests, and
// after a failure a delay that starts at one minute, doubles up to the
// interval, and is multiplied by a random factor between 0.5 and 1.5. The
// caller starts Run only on a registry that can verify on public-good.
//
// Spec: §4.7.11.
func (s *TUFAttestationRoot) Run(ctx context.Context) {
	delay := time.Duration(0)
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.After(delay):
		}
		err := s.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			failures = 0
			delay = time.Duration(float64(s.cfg.Refresh) * (0.9 + 0.2*s.cfg.Jitter()))
			st := s.Status()
			s.cfg.Logf("attestation: refreshed the public-good trusted root (%s); next refresh in %s", st.Digest, delay)
			continue
		}
		failures++
		delay = s.retryDelay(failures)
		s.logFailure(err, delay)
	}
}

// retryDelay is the delay after the n-th consecutive failure.
func (s *TUFAttestationRoot) retryDelay(n int) time.Duration {
	base := firstRetryDelay
	for i := 1; i < n && base < s.cfg.Refresh; i++ {
		base *= 2
	}
	base = min(base, s.cfg.Refresh)
	return time.Duration(float64(base) * (0.5 + s.cfg.Jitter()))
}

// logFailure logs a failed refresh, with the warning of §4.7.11 once the
// root in use is older than half the maximum age.
func (s *TUFAttestationRoot) logFailure(err error, next time.Duration) {
	// err carries the "attestation:" prefix of Refresh.
	s.cfg.Logf("%v; next attempt in %s", err, next.Round(time.Second))
	if s.cfg.MaxAge == 0 {
		return
	}
	s.mu.Lock()
	at := s.at
	s.mu.Unlock()
	now := s.cfg.Clock.Now()
	if at.IsZero() || now.Sub(at) > s.cfg.MaxAge/2 {
		s.cfg.Logf("warning: attestation: the public-good trusted root in use is %s, past half of PODIUM_SIGSTORE_ROOT_MAX_AGE (%s); verification on public-good is refused once it is older than the limit",
			describeAge(now, at), s.cfg.MaxAge)
	}
}

// contextFetcher is the TUF fetcher of one refresh. Every request carries
// the refresh context, so the refresh deadline bounds all of them. It
// answers as go-tuf's default fetcher does, so a 404 ends the walk of the
// root chain.
type contextFetcher struct {
	ctx    context.Context
	client *http.Client
}

// DownloadFile fetches urlPath, reading at most maxLength bytes. The
// timeout argument is deprecated in go-tuf and unused.
func (f *contextFetcher) DownloadFile(urlPath string, maxLength int64, _ time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, urlPath, nil)
	if err != nil {
		return nil, err
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &metadata.ErrDownloadHTTP{StatusCode: res.StatusCode, URL: urlPath}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxLength+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxLength {
		return nil, &metadata.ErrDownloadLengthMismatch{Msg: fmt.Sprintf("download of %s is larger than %d bytes", urlPath, maxLength)}
	}
	return data, nil
}
