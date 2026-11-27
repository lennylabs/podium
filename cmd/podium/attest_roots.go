package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/sign"
)

// The §13.12 settings `podium attest check --requirements` reads for the
// public-good root.
const (
	attestRootFileEnv     = "PODIUM_ATTESTATION_TRUSTED_ROOT_FILE"
	attestRootTimeoutEnv  = "PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT"
	attestRootMaxAgeEnv   = "PODIUM_SIGSTORE_ROOT_MAX_AGE"
	attestRootDirSegments = ".podium/sigstore/public-good"
)

// attestTUFMirror replaces the Sigstore TUF repository in tests. It is a
// package variable rather than a setting, so no production binary reads a
// redirect from its environment.
var attestTUFMirror string

// instanceNameRule is the §13.12 instance name rule.
var instanceNameRule = regexp.MustCompile(`\A[a-z0-9][a-z0-9._-]{0,63}\z`)

// trustedRootFlags collects the repeatable --trusted-root <instance>=<file>.
type trustedRootFlags map[string]string

// String implements flag.Value.
func (f trustedRootFlags) String() string { return "" }

// Set implements flag.Value. It refuses a value that is not
// <instance>=<file>, an instance name the §13.12 rule refuses, and an
// instance named twice.
func (f trustedRootFlags) Set(v string) error {
	name, file, ok := strings.Cut(v, "=")
	switch {
	case !ok || file == "":
		return fmt.Errorf("want <instance>=<file>, got %q", v)
	case !instanceNameRule.MatchString(name):
		return fmt.Errorf("instance name %q must be 1 to 64 characters from a-z, 0-9, '.', '_', and '-', beginning with a letter or a digit", name)
	case f[name] != "":
		return fmt.Errorf("instance %q is named twice", name)
	}
	f[name] = file
	return nil
}

// rootSettings are the §13.12 public-good root settings of the check.
type rootSettings struct {
	rootFile string
	timeout  time.Duration
	maxAge   time.Duration
}

// readRootSettings reads the public-good root settings under the §13.12
// rules: a refresh deadline above zero and a maximum age of zero or more.
//
// Spec: §13.12
func readRootSettings(getenv func(string) string) (rootSettings, error) {
	s := rootSettings{rootFile: strings.TrimSpace(getenv(attestRootFileEnv)), timeout: sign.DefaultRootRefreshTimeout, maxAge: sign.DefaultRootMaxAge}
	if raw := strings.TrimSpace(getenv(attestRootTimeoutEnv)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return s, fmt.Errorf("%s must be a duration above zero, such as 30s, got %q", attestRootTimeoutEnv, raw)
		}
		s.timeout = d
	}
	if raw := strings.TrimSpace(getenv(attestRootMaxAgeEnv)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return s, fmt.Errorf("%s must be a duration of zero or more, such as 2160h, got %q", attestRootMaxAgeEnv, raw)
		}
		s.maxAge = d
	}
	return s, nil
}

// checkInstances resolves the Sigstore instances of `podium attest check`
// from --trusted-root and the public-good settings. The public-good root is
// built when a signer first names it, so a check whose signers name another
// instance makes no TUF request.
type checkInstances struct {
	ctx      context.Context
	files    trustedRootFlags
	settings rootSettings
	stderr   io.Writer
	built    map[string]sign.AttestationInstance
}

// known reports whether name has a trusted root: public-good always, and
// every instance --trusted-root names.
func (c *checkInstances) known(name string) bool {
	return name == sign.PublicGoodInstance || c.files[name] != ""
}

// instance returns the instance name, building it on first use.
//
// Spec: §4.7.11, §7.3.5
func (c *checkInstances) instance(name string) (sign.AttestationInstance, error) {
	if inst, ok := c.built[name]; ok {
		return inst, nil
	}
	inst, err := c.build(name)
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	c.built[name] = inst
	return inst, nil
}

// build loads the trusted root of one instance. A public-good root from a
// file must list both logs, as the registry requires. The github instance
// requires no log. Another instance requires each kind of log its root
// lists, because the command reads no registry.yaml that would state it.
func (c *checkInstances) build(name string) (sign.AttestationInstance, error) {
	file := c.files[name]
	if name == sign.PublicGoodInstance && file == "" {
		file = c.settings.rootFile
	}
	if name == sign.PublicGoodInstance && file == "" {
		return c.refreshedPublicGood()
	}
	r, err := sign.LoadAttestationRootFile(file)
	if err != nil {
		return sign.AttestationInstance{}, fmt.Errorf("the Sigstore instance %q: %w", name, err)
	}
	inst := sign.AttestationInstance{Name: name, Root: r}
	switch name {
	case sign.PublicGoodInstance:
		if err := sign.CheckPublicGoodRoot(r); err != nil {
			return sign.AttestationInstance{}, fmt.Errorf("the Sigstore instance %q: %s: %w", name, file, err)
		}
		inst.TransparencyLog, inst.CertificateTransparency = true, true
	case sign.GitHubInstance:
	default:
		inst.TransparencyLog, inst.CertificateTransparency = r.ListsLogs()
	}
	return inst, nil
}

// refreshedPublicGood refreshes the public-good root once through TUF into
// the user's .podium/sigstore/public-good directory, and falls back to the
// copy there or the embedded copy when the refresh fails. The maximum age
// applies when the bundle is verified.
func (c *checkInstances) refreshedPublicGood() (sign.AttestationInstance, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sign.AttestationInstance{}, fmt.Errorf("the Sigstore instance %q: the home directory: %w", sign.PublicGoodInstance, err)
	}
	embeddedAt, _ := buildinfo.ReleaseTime()
	src, err := sign.NewTUFAttestationRoot(sign.TUFRootConfig{
		Dir:          filepath.Join(home, filepath.FromSlash(attestRootDirSegments)),
		MirrorURL:    attestTUFMirror,
		EmbeddedTime: embeddedAt,
		Refresh:      max(sign.DefaultRootRefresh, c.settings.timeout),
		Timeout:      c.settings.timeout,
		MaxAge:       c.settings.maxAge,
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(c.stderr, "warning: "+format+"\n", args...)
		},
	})
	// Only an embedded root that fails the load rules errs here;
	// TestEmbeddedPublicGoodRoot keeps the embedded root valid.
	if err != nil {
		return sign.AttestationInstance{}, err
	}
	if err := src.Refresh(c.ctx); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "warning: %s; the check uses the %s public-good root\n", oneLine(err.Error()), src.Status().Origin)
	}
	return sign.AttestationInstance{Name: sign.PublicGoodInstance, Root: src, TransparencyLog: true, CertificateTransparency: true}, nil
}
