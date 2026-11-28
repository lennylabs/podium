package serverboot

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// The §13.12 author attestation settings. They are environment only;
// sigstore.instances is the one config-file key.
const (
	attestationRootFileEnv   = "PODIUM_ATTESTATION_TRUSTED_ROOT_FILE"
	sigstoreRootRefreshEnv   = "PODIUM_SIGSTORE_ROOT_REFRESH"
	sigstoreRootTimeoutEnv   = "PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT"
	sigstoreRootMaxAgeEnv    = "PODIUM_SIGSTORE_ROOT_MAX_AGE"
	signedTimeAllowanceEnv   = "PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE"
	sigstoreInstancesKey     = "sigstore.instances"
	publicGoodRootDirSegment = "sigstore"
)

// instanceName is the §13.12 instance name rule.
var instanceName = regexp.MustCompile(`\A[a-z0-9][a-z0-9._-]{0,63}\z`)

// yamlSigstore mirrors the §13.12 `sigstore:` block of registry.yaml.
type yamlSigstore struct {
	Instances []yamlSigstoreInstance `yaml:"instances,omitempty"`
}

// yamlSigstoreInstance is one sigstore.instances entry. The booleans are
// pointers so an omitted value is told apart from false: an
// operator-defined entry must state transparency_log.
type yamlSigstoreInstance struct {
	Name                    string `yaml:"name"`
	TrustedRoot             string `yaml:"trusted_root"`
	TransparencyLog         *bool  `yaml:"transparency_log,omitempty"`
	CertificateTransparency *bool  `yaml:"certificate_transparency,omitempty"`
}

// durationSetting is one resolved §13.12 duration: the value, the value as
// set, and where it came from, for `podium config show`.
type durationSetting struct {
	value  time.Duration
	raw    string
	source string
	err    error
}

// display is the value `podium config show` prints, with an invalid value
// shown as set and marked.
func (d durationSetting) display() string {
	if d.err != nil {
		return d.raw + " (invalid)"
	}
	return d.value.String()
}

// fileInstance is an instance whose trusted root is a file: a
// sigstore.instances entry, or public-good under
// PODIUM_ATTESTATION_TRUSTED_ROOT_FILE.
type fileInstance struct {
	name       string
	file       string
	tlog, ct   bool
	root       *sign.AttestationRoot
	readAt     time.Time
	fromConfig bool
}

// attestationConfig is the resolved §4.7.11 instance configuration. The
// root files are read when the configuration loads, so a start refusal and
// `podium config show --server` both see an unreadable file.
type attestationConfig struct {
	refresh, timeout, maxAge durationSetting
	// allowance is PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE, the bound of a
	// key-signed layer subject's signed time past the registry's clock.
	allowance durationSetting
	// rootFile is PODIUM_ATTESTATION_TRUSTED_ROOT_FILE as set, trimmed.
	rootFile string
	// files holds the file-backed instances in configuration order, with
	// public-good first when rootFile is set.
	files []fileInstance
	// errs holds every refusal, in the order of the settings.
	errs []error
	// limits are PODIUM_ATTESTATION_MAX_BYTES and
	// PODIUM_ATTESTATION_MAX_BUNDLES, which bound the attestation directory
	// a cycle reads.
	limits attest.Limits

	// instances is filled by loadAttestationRoots: every configured
	// instance by name, public-good included.
	instances map[string]sign.AttestationInstance
	// publicGood is the refreshed public-good source, nil when rootFile is
	// set.
	publicGood *sign.TUFAttestationRoot
}

// resolveAttestation resolves the §13.12 settings from getenv, the
// sigstore block, and configDir, the directory a relative trusted_root
// path resolves against. now is the read time of each root file.
//
// Spec: §4.7.11, §13.12
func resolveAttestation(getenv func(string) string, y yamlSigstore, configDir string, now time.Time) attestationConfig {
	a := attestationConfig{
		refresh:   resolveDuration(getenv(sigstoreRootRefreshEnv), sigstoreRootRefreshEnv, sign.DefaultRootRefresh, false),
		timeout:   resolveDuration(getenv(sigstoreRootTimeoutEnv), sigstoreRootTimeoutEnv, sign.DefaultRootRefreshTimeout, false),
		maxAge:    resolveDuration(getenv(sigstoreRootMaxAgeEnv), sigstoreRootMaxAgeEnv, sign.DefaultRootMaxAge, true),
		allowance: resolveSignedTimeAllowance(getenv(signedTimeAllowanceEnv)),
		rootFile:  strings.TrimSpace(getenv(attestationRootFileEnv)),
	}
	for _, d := range []durationSetting{a.refresh, a.timeout, a.maxAge, a.allowance} {
		if d.err != nil {
			a.errs = append(a.errs, d.err)
		}
	}
	if err := a.refreshFloor(); err != nil {
		a.errs = append(a.errs, err)
	}
	limits, err := attest.ReadLimits(getenv)
	if err != nil {
		a.errs = append(a.errs, fmt.Errorf("config.invalid: %w", err))
		limits = attest.Limits{MaxBytes: attest.DefaultMaxBundleBytes, MaxBundles: attest.DefaultMaxBundles}
	}
	a.limits = limits
	if a.rootFile != "" {
		a.addPublicGoodFile(now)
	}
	seen := map[string]bool{}
	for i, entry := range y.Instances {
		fi, err := resolveInstance(i, entry, configDir, seen)
		if err != nil {
			a.errs = append(a.errs, err)
			continue
		}
		if err := fi.load(now); err != nil {
			a.errs = append(a.errs, err)
			continue
		}
		if err := fi.checkLogs(); err != nil {
			a.errs = append(a.errs, err)
			continue
		}
		a.files = append(a.files, fi)
	}
	return a
}

// resolveDuration parses one duration setting. An unset or blank value
// takes def. allowZero admits 0, which PODIUM_SIGSTORE_ROOT_MAX_AGE reads
// as no limit.
func resolveDuration(raw, name string, def time.Duration, allowZero bool) durationSetting {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return durationSetting{value: def, source: "default"}
	}
	d, err := time.ParseDuration(raw)
	switch {
	case err == nil && (d > 0 || allowZero && d == 0):
		return durationSetting{value: d, raw: raw, source: name}
	case allowZero:
		return durationSetting{value: def, raw: raw, source: name,
			err: fmt.Errorf("config.invalid: %s must be a duration of zero or more, such as 2160h, got %q", name, raw)}
	default:
		return durationSetting{value: def, raw: raw, source: name,
			err: fmt.Errorf("config.invalid: %s must be a duration above zero, such as 24h, got %q", name, raw)}
	}
}

// The §13.12 range and default of PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE.
const (
	defaultSignedTimeAllowance = 5 * time.Minute
	maxSignedTimeAllowance     = 24 * time.Hour
)

// resolveSignedTimeAllowance parses PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE:
// a duration from zero to 24 hours, and the 5-minute default when unset or
// blank. Another value is a config.invalid refusal of the start.
//
// Spec: §13.12, §4.7.11 (Layer step)
func resolveSignedTimeAllowance(raw string) durationSetting {
	d := resolveDuration(raw, signedTimeAllowanceEnv, defaultSignedTimeAllowance, true)
	if d.err != nil || d.value > maxSignedTimeAllowance {
		d.value = defaultSignedTimeAllowance
		d.err = fmt.Errorf("config.invalid: %s must be a duration from 0s to %s, got %q", signedTimeAllowanceEnv, maxSignedTimeAllowance, d.raw)
	}
	return d
}

// refreshFloor refuses a refresh interval below sign.MinRootRefresh or
// below the refresh deadline: an interval shorter than its own deadline
// would overlap refreshes, and one of a few milliseconds sends a request
// burst to the Sigstore TUF repository.
//
// Spec: §13.12
func (a *attestationConfig) refreshFloor() error {
	if a.refresh.err != nil || a.timeout.err != nil {
		return nil
	}
	if a.refresh.value >= sign.MinRootRefresh && a.refresh.value >= a.timeout.value {
		return nil
	}
	a.refresh.err = fmt.Errorf("config.invalid: %s must be at least %s and at least %s (%s), got %s",
		sigstoreRootRefreshEnv, sign.MinRootRefresh, sigstoreRootTimeoutEnv, a.timeout.value, a.refresh.value)
	if a.refresh.raw == "" {
		a.refresh.raw = a.refresh.value.String()
	}
	return a.refresh.err
}

// addPublicGoodFile loads the static public-good root, which must also list
// the logs the instance requires.
func (a *attestationConfig) addPublicGoodFile(now time.Time) {
	fi := fileInstance{name: sign.PublicGoodInstance, file: a.rootFile, tlog: true, ct: true}
	err := fi.load(now)
	if err == nil {
		if cerr := sign.CheckPublicGoodRoot(fi.root); cerr != nil {
			err = fmt.Errorf("config.invalid: %s %s: %w", attestationRootFileEnv, a.rootFile, cerr)
		}
	}
	if err != nil {
		a.errs = append(a.errs, err)
		return
	}
	a.files = append([]fileInstance{fi}, a.files...)
}

// resolveInstance applies the §13.12 entry rules. seen collects the names
// of the earlier entries.
func resolveInstance(i int, e yamlSigstoreInstance, configDir string, seen map[string]bool) (fileInstance, error) {
	where := fmt.Sprintf("%s[%d]", sigstoreInstancesKey, i)
	name := strings.TrimSpace(e.Name)
	switch {
	case name == "":
		return fileInstance{}, fmt.Errorf("config.invalid: %s has no name", where)
	case name == sign.PublicGoodInstance:
		return fileInstance{}, fmt.Errorf("config.invalid: %s: the public-good instance is built in; set %s to give it a trusted root file", where, attestationRootFileEnv)
	case !instanceName.MatchString(name):
		return fileInstance{}, fmt.Errorf("config.invalid: %s: instance name %q must be 1 to 64 characters from a-z, 0-9, '.', '_', and '-', beginning with a letter or a digit", where, name)
	case seen[name]:
		return fileInstance{}, fmt.Errorf("config.invalid: %s: instance %q is defined twice", where, name)
	}
	seen[name] = true
	fi := fileInstance{name: name, file: strings.TrimSpace(e.TrustedRoot), fromConfig: true}
	if fi.file == "" {
		return fi, fmt.Errorf("config.invalid: %s: instance %q has no trusted_root", where, name)
	}
	if !filepath.IsAbs(fi.file) && configDir != "" {
		fi.file = filepath.Join(configDir, fi.file)
	}
	if name == sign.GitHubInstance {
		// Spec: §4.7.11 — GitHub's instance has no transparency log and no
		// certificate transparency logs.
		if isSet(e.TransparencyLog) || isSet(e.CertificateTransparency) {
			return fi, fmt.Errorf("config.invalid: %s: the github instance has no transparency log and no certificate transparency logs; remove transparency_log and certificate_transparency or set them to false", where)
		}
		return fi, nil
	}
	if e.TransparencyLog == nil {
		return fi, fmt.Errorf("config.invalid: %s: instance %q must state transparency_log", where, name)
	}
	fi.tlog = *e.TransparencyLog
	fi.ct = isSet(e.CertificateTransparency)
	return fi, nil
}

// isSet reports whether b holds true.
func isSet(b *bool) bool { return b != nil && *b }

// load reads and parses the instance's root file under the §4.7.11 load
// rules, recording now as the root time.
func (fi *fileInstance) load(now time.Time) error {
	r, err := sign.LoadAttestationRootFile(fi.file)
	if err != nil {
		setting := sigstoreInstancesKey
		if !fi.fromConfig {
			setting = attestationRootFileEnv
		}
		return fmt.Errorf("config.invalid: Sigstore instance %q (%s): %w", fi.name, setting, err)
	}
	fi.root, fi.readAt = r, now
	return nil
}

// checkLogs refuses an instance that requires a log of a kind its root does
// not list: every bundle of it would be refused, so the start reports the
// configuration instead.
//
// Spec: §13.12
func (fi *fileInstance) checkLogs() error {
	tlog, ctlog := fi.root.ListsLogs()
	switch {
	case fi.tlog && !tlog:
		return fmt.Errorf("config.invalid: Sigstore instance %q (%s): transparency_log is true and the trusted root %s lists no transparency log", fi.name, sigstoreInstancesKey, fi.file)
	case fi.ct && !ctlog:
		return fmt.Errorf("config.invalid: Sigstore instance %q (%s): certificate_transparency is true and the trusted root %s lists no certificate transparency log", fi.name, sigstoreInstancesKey, fi.file)
	}
	return nil
}

// err is the first refusal, which validate returns.
func (a attestationConfig) err() error {
	if len(a.errs) == 0 {
		return nil
	}
	return a.errs[0]
}

// publicGoodRootDir is where the registry keeps the TUF metadata and the
// last verified copy of the public-good root: beside the SQLite store
// file, whatever the store type (§13.12).
func publicGoodRootDir(sqlitePath string) string {
	return filepath.Join(filepath.Dir(sqlitePath), publicGoodRootDirSegment, sign.PublicGoodInstance)
}

// loadAttestationRoots builds every configured instance with no network
// call and no write: the public-good instance serves the copy on disk or
// the embedded copy until a refresh that StartAttestationRefresh starts
// completes. It logs the root of
// each instance and the SHA-1 timestamp authorities of each root.
//
// Spec: §4.7.11
func (c *Config) loadAttestationRoots() error {
	a := &c.attestation
	a.instances = map[string]sign.AttestationInstance{}
	if a.rootFile == "" {
		embeddedAt, _ := buildinfo.ReleaseTime()
		src, err := sign.NewTUFAttestationRoot(sign.TUFRootConfig{
			Dir:          publicGoodRootDir(c.sqlitePath),
			MirrorURL:    c.sigstoreMirror,
			TUFRoot:      c.sigstoreTUFRoot,
			EmbeddedTime: embeddedAt,
			Refresh:      a.refresh.value,
			Timeout:      a.timeout.value,
			MaxAge:       a.maxAge.value,
		})
		// Only an embedded root that fails the load rules errs here, as
		// validate has already checked the durations; TestEmbeddedPublicGoodRoot
		// keeps the embedded root valid.
		if err != nil {
			return err
		}
		a.publicGood = src
		a.instances[sign.PublicGoodInstance] = sign.AttestationInstance{
			Name: sign.PublicGoodInstance, Root: src, TransparencyLog: true, CertificateTransparency: true,
		}
		logRootStatus(sign.PublicGoodInstance, "TUF refresh", src.Status())
	}
	for _, fi := range a.files {
		a.instances[fi.name] = sign.AttestationInstance{
			Name: fi.name, Root: sign.FileAttestationRoot{Root: fi.root, ReadAt: fi.readAt},
			TransparencyLog: fi.tlog, CertificateTransparency: fi.ct,
		}
		logRootStatus(fi.name, fi.file, sign.StatusOf(fi.root, fi.readAt))
	}
	return nil
}

// StartAttestationRefresh starts the background refresh of the public-good
// root and returns the function that stops it: the stop cancels the
// refresh and waits for it to return, so a shutdown leaves no request or
// write to the root directory behind. Under a static root file, or before
// loadAttestationRoots, it starts nothing.
//
// §4.7.11 runs the refresh only on a registry that can verify on
// public-good: a trust default, tenant floor, or layer policy above none
// that names a public-good signer. Ingest verification decides that and
// calls this, so the registry start does not; a registry that never
// verifies on public-good makes no TUF request and creates no directory.
//
// Spec: §4.7.11
func (c *Config) StartAttestationRefresh(ctx context.Context) (stop func()) {
	src := c.attestation.publicGood
	if src == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		src.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// logRootStatus writes the §4.7.11 start report of one instance.
func logRootStatus(name, from string, st sign.AttestationRootStatus) {
	at := "unknown"
	if !st.Time.IsZero() {
		at = st.Time.UTC().Format(time.RFC3339)
	}
	log.Printf("attestation: instance %s: trusted root %s from %s (%s), root time %s", name, st.Digest, st.Origin, from, at)
	for _, uri := range st.SHA1TimestampAuthorities {
		log.Printf("warning: attestation: instance %s: the chain of timestamp authority %q holds a SHA-1 signature; TypeScript consumers refuse its timestamps", name, uri)
	}
}

// AttestationInstances returns the configured Sigstore instances by name,
// each with its trusted root source, once the registry has started them.
// Ingest verification reads them; before the start it returns nil.
//
// Spec: §4.7.11
func (c *Config) AttestationInstances() map[string]sign.AttestationInstance {
	if c.attestation.instances == nil {
		return nil
	}
	out := make(map[string]sign.AttestationInstance, len(c.attestation.instances))
	for k, v := range c.attestation.instances {
		out[k] = v
	}
	return out
}

// attestationSettings are the §13.12 rows `podium config show --server`
// prints: the configured instances, each file-backed instance, and the four
// environment settings.
func (c *Config) attestationSettings(envOrSrc func(env, src string) string) []Setting {
	a := c.attestation
	names := []string{sign.PublicGoodInstance}
	src := "default"
	var rows []Setting
	for _, fi := range a.files {
		if fi.fromConfig {
			names = append(names, fi.name)
			src = "registry.yaml"
		}
		rows = append(rows, Setting{
			Name:   sigstoreInstancesKey + "." + fi.name,
			Value:  fmt.Sprintf("trusted_root=%s transparency_log=%t certificate_transparency=%t", fi.file, fi.tlog, fi.ct),
			Source: instanceSource(fi),
		})
	}
	out := []Setting{{Name: sigstoreInstancesKey, Value: strings.Join(names, ","), Source: src}}
	out = append(out, rows...)
	return append(out,
		Setting{"attestation.trusted_root_file", a.rootFile, envOrSrc(attestationRootFileEnv, "")},
		Setting{"sigstore.root_refresh", a.refresh.display(), a.refresh.source},
		Setting{"sigstore.root_refresh_timeout", a.timeout.display(), a.timeout.source},
		Setting{"sigstore.root_max_age", a.maxAge.display(), a.maxAge.source},
		Setting{"attestation.signed_time_allowance", a.allowance.display(), a.allowance.source},
		Setting{"attestation.max_bytes", fmt.Sprint(a.limits.MaxBytes), envOrSrc(attest.MaxBytesSetting, "")},
		Setting{"attestation.max_bundles", fmt.Sprint(a.limits.MaxBundles), envOrSrc(attest.MaxBundlesSetting, "")},
	)
}

// instanceSource names where a file-backed instance came from.
func instanceSource(fi fileInstance) string {
	if fi.fromConfig {
		return "registry.yaml"
	}
	return attestationRootFileEnv
}
