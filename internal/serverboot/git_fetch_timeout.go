package serverboot

import (
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// gitFetchTimeoutKey is the registry.yaml key of PODIUM_GIT_FETCH_TIMEOUT.
const gitFetchTimeoutKey = "git_fetch_timeout"

// gitFetchConfig is the resolved §13.12 PODIUM_GIT_FETCH_TIMEOUT setting: the
// deadline of one Git snapshot, where it came from, and the refusal validate
// returns when the value is not a duration above zero.
type gitFetchConfig struct {
	timeout time.Duration
	// source is the environment variable, "registry.yaml", or "default",
	// for `podium config show`.
	source string
	err    error
}

// resolveGitFetchTimeout resolves PODIUM_GIT_FETCH_TIMEOUT from the
// environment value, then the registry.yaml git_fetch_timeout value, then the
// default. A value that is set and is not a Go duration above zero is carried
// as a config.invalid error that names the setting it came from, so the start
// is refused rather than run with no deadline or an instant one.
//
// Spec: §7.3.1, §13.12
func resolveGitFetchTimeout(envValue, fileValue string) gitFetchConfig {
	if raw := strings.TrimSpace(envValue); raw != "" {
		return parseGitFetchTimeout(raw, source.FetchTimeoutSetting, source.FetchTimeoutSetting)
	}
	if raw := strings.TrimSpace(fileValue); raw != "" {
		return parseGitFetchTimeout(raw, "registry.yaml "+gitFetchTimeoutKey, "registry.yaml")
	}
	return gitFetchConfig{timeout: source.DefaultFetchTimeout, source: "default"}
}

// parseGitFetchTimeout parses one set value. name is what the refusal names
// and from is what `podium config show` reports as the source.
func parseGitFetchTimeout(raw, name, from string) gitFetchConfig {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return gitFetchConfig{
			timeout: source.DefaultFetchTimeout,
			source:  from,
			err:     fmt.Errorf("config.invalid: %s must be a duration above zero, such as 10m, got %q", name, raw),
		}
	}
	return gitFetchConfig{timeout: d, source: from}
}
