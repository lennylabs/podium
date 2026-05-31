package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// configFileScope labels which of the three §7.5.2 config files a value or
// profile came from. The order is the precedence order, low to high.
type configFileScope int

const (
	scopeUserGlobal configFileScope = iota
	scopeProjectShared
	scopeProjectLocal
)

// String renders the scope for the §7.5.2 collision warning.
func (s configFileScope) String() string {
	switch s {
	case scopeUserGlobal:
		return "user-global (~/.podium/sync.yaml)"
	case scopeProjectShared:
		return "project-shared (.podium/sync.yaml)"
	case scopeProjectLocal:
		return "project-local (.podium/sync.local.yaml)"
	default:
		return "unknown"
	}
}

// MergedConfig is the result of merging the §7.5.2 file scopes (user-global,
// project-shared, project-local) by per-key precedence. Defaults and Targets
// take the highest-precedence non-empty value; Profiles are an additive union
// with whole-profile overwrite on a name collision.
type MergedConfig struct {
	Defaults Defaults
	Profiles map[string]Profile
	Targets  []TargetEntry
	// Collisions maps a profile name defined in more than one scope to the
	// scopes that defined it, ordered low to high precedence. §7.5.2 warns
	// when such a profile is invoked.
	Collisions map[string][]configFileScope
}

// DiscoverWorkspace walks up from start until it finds a directory containing
// a `.podium/` subdirectory, mirroring how git locates `.git` (§7.5.2). It
// returns the directory holding `.podium` and true, or ("", false) when none
// is found before the filesystem root.
func DiscoverWorkspace(start string) (string, bool) {
	dir := start
	for {
		info, err := os.Stat(filepath.Join(dir, ".podium"))
		if err == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ReadConfigFile reads a SyncConfig from an explicit path. A missing file
// returns (nil, nil) so callers can treat absent scopes as empty.
func ReadConfigFile(path string) (*SyncConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LoadMergedConfig discovers the workspace by walking up from startDir, loads
// the three §7.5.2 file scopes, and merges them by per-key precedence
// (project-local > project-shared > user-global). homeDir locates the
// user-global file (`<homeDir>/.podium/sync.yaml`); tests pass an explicit
// directory. An absent scope file contributes nothing. The returned workspace
// is "" when no `.podium/` is found; only the user-global scope then applies.
func LoadMergedConfig(startDir, homeDir string) (*MergedConfig, string, error) {
	workspace, _ := DiscoverWorkspace(startDir)

	type scoped struct {
		scope configFileScope
		cfg   *SyncConfig
	}
	var files []scoped

	if homeDir != "" {
		cfg, err := ReadConfigFile(filepath.Join(homeDir, ".podium", "sync.yaml"))
		if err != nil {
			return nil, workspace, err
		}
		files = append(files, scoped{scopeUserGlobal, cfg})
	}
	if workspace != "" {
		shared, err := ReadConfigFile(filepath.Join(workspace, ".podium", "sync.yaml"))
		if err != nil {
			return nil, workspace, err
		}
		files = append(files, scoped{scopeProjectShared, shared})
		local, err := ReadConfigFile(filepath.Join(workspace, ".podium", "sync.local.yaml"))
		if err != nil {
			return nil, workspace, err
		}
		files = append(files, scoped{scopeProjectLocal, local})
	}

	merged := &MergedConfig{
		Profiles:   map[string]Profile{},
		Collisions: map[string][]configFileScope{},
	}
	profileScopes := map[string][]configFileScope{}

	// files is ordered low to high precedence, so applying each in order lets
	// a higher-precedence non-empty value overwrite the prior one.
	for _, f := range files {
		if f.cfg == nil {
			continue
		}
		mergeDefaults(&merged.Defaults, f.cfg.Defaults)
		if len(f.cfg.Targets) > 0 {
			merged.Targets = f.cfg.Targets
		}
		for name, prof := range f.cfg.Profiles {
			merged.Profiles[name] = prof
			profileScopes[name] = append(profileScopes[name], f.scope)
		}
	}
	for name, scopes := range profileScopes {
		if len(scopes) > 1 {
			merged.Collisions[name] = scopes
		}
	}
	return merged, workspace, nil
}

// mergeDefaults overlays src onto dst, keeping a non-empty src field.
func mergeDefaults(dst *Defaults, src Defaults) {
	if src.Registry != "" {
		dst.Registry = src.Registry
	}
	if src.Harness != "" {
		dst.Harness = src.Harness
	}
	if src.Target != "" {
		dst.Target = src.Target
	}
	if src.Profile != "" {
		dst.Profile = src.Profile
	}
}

// ResolveInput carries the sync CLI flag values. An empty string or slice
// means the flag was not given, so the next precedence level applies.
type ResolveInput struct {
	Registry string
	Target   string
	Harness  string
	Profile  string
	Include  []string
	Exclude  []string
	Types    []string
}

// Resolved is the merged outcome of CLI flags, `PODIUM_*` env vars, and the
// merged config per §7.5.2.
type Resolved struct {
	Registry string
	Target   string
	Harness  string // resolved adapter id; "none" when unset everywhere
	Profile  string // active profile name; "" when none
	Scope    ScopeFilter
	// CollisionWarning is non-empty when the invoked profile is defined in
	// more than one scope (§7.5.2). The CLI prints it to stderr.
	CollisionWarning string
}

// Resolve merges CLI flags (highest precedence), `PODIUM_*` env vars, and the
// merged config (lowest) per §7.5.2. It selects the active profile (explicit
// --profile, else defaults.profile), computes the scope (CLI lists replace the
// profile's per field), and reports a profile-name collision. env is
// os.Getenv or a test stub.
//
// spec: §7.5.2 (precedence, profile merge, collision warning).
func Resolve(in ResolveInput, merged *MergedConfig, env func(string) string) (*Resolved, error) {
	if merged == nil {
		merged = &MergedConfig{Profiles: map[string]Profile{}}
	}
	if env == nil {
		env = func(string) string { return "" }
	}
	out := &Resolved{}

	name := in.Profile
	explicit := in.Profile != ""
	if name == "" {
		name = merged.Defaults.Profile
	}
	var prof Profile
	if name != "" {
		p, ok := merged.Profiles[name]
		switch {
		case ok:
			prof = p
			out.Profile = name
			if cols := merged.Collisions[name]; len(cols) > 1 {
				out.CollisionWarning = fmt.Sprintf(
					"warning: profile %q is defined in multiple scopes (%s); the highest-precedence definition wins",
					name, joinScopes(cols))
			}
		case explicit:
			// An explicit --profile that names a missing profile is an error.
			return nil, fmt.Errorf("%w: %q", ErrProfileNotFound, name)
		default:
			// A stale defaults.profile is ignored rather than fatal.
		}
	}

	out.Registry = firstNonEmpty(in.Registry, env("PODIUM_REGISTRY"), merged.Defaults.Registry)
	out.Target = firstNonEmpty(in.Target, prof.Target, merged.Defaults.Target)
	// spec: §7.5.2 — harness resolves per key by precedence: CLI flag, then
	// PODIUM_HARNESS, then the active profile's harness, then defaults.harness,
	// then the built-in "none" adapter.
	out.Harness = firstNonEmpty(in.Harness, env("PODIUM_HARNESS"), prof.Harness, merged.Defaults.Harness, "none")
	out.Scope = ScopeFilter{
		Include: pickList(in.Include, prof.Include),
		Exclude: pickList(in.Exclude, prof.Exclude),
		Types:   pickList(in.Types, prof.Type),
	}
	return out, nil
}

// Check validates a merged config per §7.5.2 `podium sync --check` and returns
// the warnings (never errors): unresolved profile references (defaults.profile
// and per-target profile names), malformed include/exclude globs, duplicate
// target ids, and profile-name collisions across scopes. The returned slice is
// sorted for deterministic output and is empty when the config is clean.
//
// spec: §7.5.2 — "validate the merged config against the schema and report
// unresolved profile references, malformed globs, target collisions, and
// profile-name collisions across scopes (warning, not error)".
func Check(merged *MergedConfig) []string {
	if merged == nil {
		return nil
	}
	var warns []string

	for name, scopes := range merged.Collisions {
		if len(scopes) > 1 {
			warns = append(warns, fmt.Sprintf(
				"profile %q is defined in multiple scopes (%s); the highest-precedence definition wins",
				name, joinScopes(scopes)))
		}
	}

	if p := merged.Defaults.Profile; p != "" {
		if _, ok := merged.Profiles[p]; !ok {
			warns = append(warns, fmt.Sprintf("defaults.profile references undefined profile %q", p))
		}
	}

	for name, prof := range merged.Profiles {
		for _, g := range append(append([]string(nil), prof.Include...), prof.Exclude...) {
			if err := validateGlob(g); err != nil {
				warns = append(warns, fmt.Sprintf("profile %q: malformed glob %q (%v)", name, g, err))
			}
		}
	}

	seen := map[string]bool{}
	for _, t := range merged.Targets {
		if t.ID != "" && seen[t.ID] {
			warns = append(warns, fmt.Sprintf("target id %q is defined more than once", t.ID))
		}
		seen[t.ID] = true
		if t.Profile != "" {
			if _, ok := merged.Profiles[t.Profile]; !ok {
				warns = append(warns, fmt.Sprintf("target %q references undefined profile %q", t.ID, t.Profile))
			}
		}
		for _, g := range append(append([]string(nil), t.Include...), t.Exclude...) {
			if err := validateGlob(g); err != nil {
				warns = append(warns, fmt.Sprintf("target %q: malformed glob %q (%v)", t.ID, g, err))
			}
		}
	}

	sort.Strings(warns)
	return warns
}

// MultiTargetPlan is one resolved entry from a §7.5.2 `targets:` list. Each
// plan runs as an independent sync with its own target, scope, and lock.
type MultiTargetPlan struct {
	ID       string
	Registry string
	Target   string
	Harness  string
	Profile  string
	Scope    ScopeFilter
}

// PlanMultiTarget resolves every entry in cfg.Targets into a runnable plan
// (§7.5.2 multi-target). Per entry the registry is the --config-shared
// registry (registryOverride or defaults.registry, resolved against
// workspace), the harness is the entry's harness then defaults.harness then
// "none", and the scope comes from the named profile (merged with any inline
// lists) or the inline lists directly. A target with no resolvable directory
// or an unresolved profile reference is an error.
//
// spec: §7.5.2 — "podium sync --config <path> iterates targets: and runs one
// sync per entry; each target writes its own lock".
func PlanMultiTarget(cfg *SyncConfig, registryOverride, workspace string) ([]MultiTargetPlan, error) {
	if cfg == nil || len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("sync --config: no targets: defined")
	}
	registry := firstNonEmpty(registryOverride, cfg.Defaults.Registry)
	if registry == "" {
		return nil, ErrNoRegistry
	}
	registry = ResolveRegistryPath(workspace, registry)

	plans := make([]MultiTargetPlan, 0, len(cfg.Targets))
	for _, entry := range cfg.Targets {
		target := firstNonEmpty(entry.Target, cfg.Defaults.Target)
		if target == "" {
			return nil, fmt.Errorf("sync --config: target %q has no target directory", entry.ID)
		}
		scope, err := targetScope(entry, cfg)
		if err != nil {
			return nil, err
		}
		plans = append(plans, MultiTargetPlan{
			ID:       entry.ID,
			Registry: registry,
			Target:   target,
			Harness:  firstNonEmpty(entry.Harness, cfg.Defaults.Harness, "none"),
			Profile:  entry.Profile,
			Scope:    scope,
		})
	}
	return plans, nil
}

// targetScope resolves one TargetEntry's scope: the named profile's lists
// (when entry.Profile is set) with any inline list overriding per field, or
// the inline lists alone.
func targetScope(entry TargetEntry, cfg *SyncConfig) (ScopeFilter, error) {
	var base Profile
	if entry.Profile != "" {
		p, ok := cfg.Profiles[entry.Profile]
		if !ok {
			return ScopeFilter{}, fmt.Errorf("%w: %q (target %q)", ErrProfileNotFound, entry.Profile, entry.ID)
		}
		base = p
	}
	return ScopeFilter{
		Include: pickList(entry.Include, base.Include),
		Exclude: pickList(entry.Exclude, base.Exclude),
		Types:   pickList(entry.Type, base.Type),
	}, nil
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// pickList returns primary when it has entries, else fallback. §7.5.2: a CLI
// (or per-target inline) list replaces the profile's list rather than
// appending.
func pickList(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

// joinScopes renders the collision scopes for the §7.5.2 warning.
func joinScopes(scopes []configFileScope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = s.String()
	}
	return strings.Join(parts, ", ")
}
