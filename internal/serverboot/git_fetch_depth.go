package serverboot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// gitFetchDepthKey is the registry.yaml key of PODIUM_GIT_FETCH_DEPTH.
const gitFetchDepthKey = "git_fetch_depth"

// gitFetchDepthConfig is the resolved §13.12 PODIUM_GIT_FETCH_DEPTH setting:
// the starting depth of each call of the Git source, where it came from, and
// the refusal validate returns when the value is not an integer from 1 to
// source.MaxFetchDepth.
type gitFetchDepthConfig struct {
	depth int
	// source is the environment variable, "registry.yaml", or "default",
	// for `podium config show`.
	source string
	// raw is the value as set, kept so `podium config show` reports an
	// invalid value as written rather than as the default.
	raw string
	err error
}

// display is the value `podium config show` prints: the resolved depth, or
// the value as set marked invalid when validate would refuse the start with
// it.
func (g gitFetchDepthConfig) display() string {
	if g.err != nil {
		return g.raw + " (invalid)"
	}
	return strconv.Itoa(g.depth)
}

// resolveGitFetchDepth resolves PODIUM_GIT_FETCH_DEPTH from the environment
// value, then the registry.yaml git_fetch_depth value, then the default. A
// value that is set and is not an integer from 1 to source.MaxFetchDepth is
// carried as a config.invalid error that names the setting it came from. The
// setting has no value for the complete history: Git reads the next value,
// 2147483647, as that request, and only a deepening round sends it.
//
// Spec: §7.3.1 (Git fetch), §13.12
func resolveGitFetchDepth(envValue, fileValue string) gitFetchDepthConfig {
	if raw := strings.TrimSpace(envValue); raw != "" {
		return parseGitFetchDepth(raw, source.FetchDepthSetting, source.FetchDepthSetting)
	}
	if raw := strings.TrimSpace(fileValue); raw != "" {
		return parseGitFetchDepth(raw, "registry.yaml "+gitFetchDepthKey, "registry.yaml")
	}
	return gitFetchDepthConfig{depth: source.DefaultFetchDepth, source: "default"}
}

// parseGitFetchDepth parses one set value. name is what the refusal names and
// from is what `podium config show` reports as the source.
func parseGitFetchDepth(raw, name, from string) gitFetchDepthConfig {
	d, err := strconv.Atoi(raw)
	if err != nil || d < 1 || d > source.MaxFetchDepth {
		return gitFetchDepthConfig{
			depth:  source.DefaultFetchDepth,
			source: from,
			raw:    raw,
			err: fmt.Errorf("config.invalid: %s must be an integer from 1 to %d, such as %d, got %q",
				name, source.MaxFetchDepth, source.DefaultFetchDepth, raw),
		}
	}
	return gitFetchDepthConfig{depth: d, source: from, raw: raw}
}
