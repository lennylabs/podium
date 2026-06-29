package sync

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"gopkg.in/yaml.v3"
)

// This file holds the reusable marketplace-output component types a
// `kind: marketplace` target carries (§7.5.2, §7.8): the git remote, the plugin
// filters, the workflow command lists, the per-command duration, the resolved
// output payload, and the config validation that rejects a non-publish harness,
// a malformed plugin glob, or a malformed workflow command. They live beside the
// ScopeFilter, lock, and reconciliation machinery a marketplace render reuses,
// so plugin selection and command validation apply the same glob and harness
// rules as a workspace sync (§7.5.1).

// ErrConfigInvalid signals a marketplace target that fails validation: a harness
// set naming a non-publish-target harness, a malformed plugin glob, or a
// malformed workflow command. It maps to config.invalid in §6.10. Callers assert
// against it via errors.Is.
var ErrConfigInvalid = errors.New("config.invalid")

// GitRemote is the `git:` block of a marketplace target: the remote URL the
// workflow clones and pushes, and the branch it writes (§7.8).
type GitRemote struct {
	Remote string `yaml:"remote,omitempty"`
	Branch string `yaml:"branch,omitempty"`
}

// PluginFilter is one entry under `plugins:`: a named bundle of selected
// artifacts defined by a §7.5.1 scope filter (include, exclude, type). The
// publishing pipeline assigns each selected artifact to its plugin by evaluating
// the filters in declaration order (§7.8). Description is the optional
// human-readable plugin description the marketplace emitter carries into the
// per-plugin manifest (§6.7 "Plugin descriptor"); it is empty when the operator
// omits it.
type PluginFilter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Include     []string `yaml:"include,omitempty"`
	Exclude     []string `yaml:"exclude,omitempty"`
	Type        []string `yaml:"type,omitempty"`
}

// ScopeFilter returns the §7.5.1 selection this plugin filter expresses, reusing
// the scope-filter machinery so plugin selection and sync selection apply
// identical glob semantics (§7.8).
func (p PluginFilter) ScopeFilter() ScopeFilter {
	return ScopeFilter{Include: p.Include, Exclude: p.Exclude, Types: p.Type}
}

// Workflow groups the `prepare` and `publish` command lists `podium sync` runs
// around the render phase of a target (§7.5.2, §7.8). prepare places a checkout
// of the destination repository at the working directory, and publish takes the
// rendered tree to the remote. PrepareOnError and PublishOnError are optional
// per-phase cleanup lists: a failure in the prepare phase runs PrepareOnError,
// and a failure in the publish phase runs PublishOnError, before the failure
// propagates. The cleanup is scoped to the phase that failed, so a prepare
// failure does not run cleanup authored for a publish-phase checkout.
type Workflow struct {
	Prepare        []Command `yaml:"prepare,omitempty"`
	Publish        []Command `yaml:"publish,omitempty"`
	PrepareOnError []Command `yaml:"prepare_on_error,omitempty"`
	PublishOnError []Command `yaml:"publish_on_error,omitempty"`
}

// IsZero reports whether the workflow declares no commands.
func (w Workflow) IsZero() bool {
	return len(w.Prepare) == 0 && len(w.Publish) == 0 &&
		len(w.PrepareOnError) == 0 && len(w.PublishOnError) == 0
}

// commands returns every command the workflow declares across its phases and
// per-phase cleanup lists, so config validation (§7.5.2 --check) can check each
// for well-formedness before any side effect.
func (w Workflow) commands() []Command {
	all := make([]Command, 0, len(w.Prepare)+len(w.Publish)+len(w.PrepareOnError)+len(w.PublishOnError))
	all = append(all, w.Prepare...)
	all = append(all, w.Publish...)
	all = append(all, w.PrepareOnError...)
	all = append(all, w.PublishOnError...)
	return all
}

// Command is one step of a workflow phase (§7.8). It is an argv list under
// `run:` executed directly without a shell, or a string under `sh:` executed
// through `sh -c`. The per-command flags control failure handling:
// SkipIfNoChanges skips the command when the render produced no diff,
// ContinueOnError lets the pipeline proceed past a non-zero exit, and Timeout
// bounds the command's wall-clock duration.
type Command struct {
	Run             []string `yaml:"run,omitempty"`
	Sh              string   `yaml:"sh,omitempty"`
	SkipIfNoChanges bool     `yaml:"skip_if_no_changes,omitempty"`
	ContinueOnError bool     `yaml:"continue_on_error,omitempty"`
	Timeout         Duration `yaml:"timeout,omitempty"`
}

// validate rejects a command that declares neither run: nor sh:, or both, so a
// malformed step surfaces at config validation rather than running an empty
// command.
func (c Command) validate() error {
	switch {
	case len(c.Run) == 0 && c.Sh == "":
		return fmt.Errorf("%w: command declares neither run: nor sh:", ErrConfigInvalid)
	case len(c.Run) > 0 && c.Sh != "":
		return fmt.Errorf("%w: command declares both run: and sh:", ErrConfigInvalid)
	}
	return nil
}

// display returns a short human label for a command, used in error messages and
// skip logs. It does not substitute variables, so the label names the command as
// the operator wrote it.
func (c Command) display() string {
	if c.Sh != "" {
		return "sh -c " + strconv.Quote(c.Sh)
	}
	return strings.Join(c.Run, " ")
}

// Duration is a time.Duration that unmarshals from a Go duration string such as
// "30s" or "5m". A bare YAML integer is rejected so a unit is always explicit,
// because an ambiguous "timeout: 30" reads as nanoseconds under the default yaml
// decoding and surprises an operator who meant seconds.
type Duration time.Duration

// Duration returns the timeout as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a duration string into d.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("timeout: expected a duration string such as \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("timeout: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// ResolvedOutput is one marketplace target with its defaults applied: the
// registry, the publishing identity, and the effective workflow. The git
// destination, harness set, plugins, and commit message come from the target
// entry.
type ResolvedOutput struct {
	ID            string
	Registry      string
	Identity      string
	Git           GitRemote
	Harnesses     []string
	CommitMessage string
	Plugins       []PluginFilter
	Workflow      Workflow
}

// validateOutput rejects a marketplace target whose harness set names a
// non-publish-target harness (opencode, none, or an unknown id), a target with a
// malformed plugin glob, and a target with a malformed workflow command. All map
// to config.invalid (§6.10). The harness check reuses the §7.8 publish-target
// selector (adapter.EmitterForHarness), the glob check reuses the §7.5.1 sync
// glob validator (ValidateGlob), and the command check reuses the per-step
// validation (Command.validate), so config validation and the render path agree
// on which harnesses publish, which globs are well-formed, and which commands are
// well-formed. Validating the workflow commands here makes --check fail-closed: a
// command that declares neither run: nor sh:, or both, is rejected before the
// prepare clone or the render runs (§7.5.2 "--check validates the config only").
func validateOutput(out ResolvedOutput) error {
	for _, h := range out.Harnesses {
		if _, err := adapter.EmitterForHarness(h); err != nil {
			return fmt.Errorf("%w: marketplace %q harness %q is not a publish target (opencode and none have no git-repo distribution): %w",
				ErrConfigInvalid, out.ID, h, err)
		}
	}
	for _, p := range out.Plugins {
		for _, g := range append(append([]string(nil), p.Include...), p.Exclude...) {
			if err := ValidateGlob(g); err != nil {
				return fmt.Errorf("%w: marketplace %q plugin %q has a malformed glob %q: %w",
					ErrConfigInvalid, out.ID, p.Name, g, err)
			}
		}
	}
	for _, c := range out.Workflow.commands() {
		if err := c.validate(); err != nil {
			return fmt.Errorf("%w: marketplace %q command %q: %w",
				ErrConfigInvalid, out.ID, c.display(), err)
		}
	}
	return nil
}
