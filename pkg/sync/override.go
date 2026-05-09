package sync

import (
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/podium/internal/clock"
)

// Errors related to override / save-as / profile edit.
var (
	// ErrProfileNotFound signals that the named profile is missing
	// from sync.yaml.
	ErrProfileNotFound = errors.New("override: profile not found")
	// ErrInvalidArtifactID signals an empty or malformed --add /
	// --remove argument.
	ErrInvalidArtifactID = errors.New("override: invalid artifact id")
)

// OverrideOptions captures the §7.5.5 invocation parameters.
type OverrideOptions struct {
	Target string
	// Add IDs to materialize on top of the resolved profile.
	Add []string
	// Remove IDs to drop from the resolved profile.
	Remove []string
	// Reset clears every toggle, equivalent to a manual sync.
	Reset bool
	// DryRun resolves the new state and reports without writing.
	DryRun bool
	// Clock provides timestamps; defaults to clock.Real.
	Clock clock.Clock
}

// OverrideResult is what Override returns: the new lock state and
// whether anything actually changed.
type OverrideResult struct {
	Lock    *LockFile
	Changed bool
}

// Override applies the §7.5.5 toggle semantics to the lock file at
// opts.Target/.podium/sync.lock. Toggles persist across watcher
// events and survive until the next manual `podium sync` (which
// clears them).
func Override(opts OverrideOptions) (*OverrideResult, error) {
	if opts.Target == "" {
		return nil, ErrNoTarget
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	for _, id := range append(opts.Add, opts.Remove...) {
		if id == "" {
			return nil, fmt.Errorf("%w: empty id", ErrInvalidArtifactID)
		}
	}
	lock, err := ReadLock(opts.Target)
	if err != nil {
		return nil, err
	}
	if lock == nil {
		lock = &LockFile{Version: 1, Target: opts.Target}
	}

	now := opts.Clock.Now().UTC()
	changed := false

	if opts.Reset {
		if len(lock.Toggles.Add) > 0 || len(lock.Toggles.Remove) > 0 {
			changed = true
		}
		lock.Toggles = LockToggles{}
	}

	for _, id := range opts.Add {
		if !alreadyAdded(lock.Toggles.Add, id) {
			lock.Toggles.Add = append(lock.Toggles.Add, LockToggle{
				ID:      id,
				AddedAt: now,
			})
			changed = true
		}
		// An add invalidates any prior remove for the same ID.
		lock.Toggles.Remove = removeToggleByID(lock.Toggles.Remove, id)
	}
	for _, id := range opts.Remove {
		if !alreadyAdded(lock.Toggles.Remove, id) {
			lock.Toggles.Remove = append(lock.Toggles.Remove, LockToggle{
				ID:        id,
				RemovedAt: now,
			})
			changed = true
		}
		lock.Toggles.Add = removeToggleByID(lock.Toggles.Add, id)
	}

	if !opts.DryRun && changed {
		if err := WriteLock(opts.Target, lock); err != nil {
			return nil, err
		}
	}
	return &OverrideResult{Lock: lock, Changed: changed}, nil
}

// alreadyAdded reports whether id appears in the toggle list.
func alreadyAdded(toggles []LockToggle, id string) bool {
	for _, t := range toggles {
		if t.ID == id {
			return true
		}
	}
	return false
}

// removeToggleByID returns toggles with any matching id removed.
func removeToggleByID(toggles []LockToggle, id string) []LockToggle {
	out := toggles[:0]
	for _, t := range toggles {
		if t.ID != id {
			out = append(out, t)
		}
	}
	return out
}

// SaveAsOptions captures the §7.5.6 invocation parameters.
type SaveAsOptions struct {
	Target  string
	Profile string
	Update  bool
	DryRun  bool
}

// SaveAsResult is what SaveAs returns.
type SaveAsResult struct {
	Profile Profile
	Wrote   bool
}

// SaveAs renders the current lock-file state (scope + toggles) as a
// profile in sync.yaml per §7.5.6. The mapping:
//
//	scope.include    → profile.include (verbatim)
//	scope.exclude    → profile.exclude (verbatim)
//	toggles.add      → profile.include (one entry per id)
//	toggles.remove   → profile.exclude (one entry per id)
//	scope.type       → profile.type (verbatim)
//
// On success the lock file's toggles are cleared (the toggles are now
// part of the profile's scope).
func SaveAs(opts SaveAsOptions) (*SaveAsResult, error) {
	if opts.Target == "" {
		return nil, ErrNoTarget
	}
	if opts.Profile == "" {
		return nil, fmt.Errorf("save-as: profile name required")
	}
	lock, err := ReadLock(opts.Target)
	if err != nil {
		return nil, err
	}
	if lock == nil {
		lock = &LockFile{Version: 1, Target: opts.Target}
	}
	cfg, err := EnsureConfig(opts.Target)
	if err != nil {
		return nil, err
	}
	if _, exists := cfg.Profiles[opts.Profile]; exists && !opts.Update {
		return nil, fmt.Errorf("save-as: profile %q already exists; pass --update to overwrite", opts.Profile)
	}

	prof := Profile{
		Include: append([]string(nil), lock.Scope.Include...),
		Exclude: append([]string(nil), lock.Scope.Exclude...),
		Type:    append([]string(nil), lock.Scope.Type...),
	}
	for _, t := range lock.Toggles.Add {
		prof.Include = append(prof.Include, t.ID)
	}
	for _, t := range lock.Toggles.Remove {
		prof.Exclude = append(prof.Exclude, t.ID)
	}
	res := &SaveAsResult{Profile: prof}
	if opts.DryRun {
		return res, nil
	}
	cfg.Profiles[opts.Profile] = prof
	if err := WriteConfig(opts.Target, cfg); err != nil {
		return nil, err
	}
	// Clear toggles in the lock file.
	lock.Toggles = LockToggles{}
	if err := WriteLock(opts.Target, lock); err != nil {
		return nil, err
	}
	res.Wrote = true
	return res, nil
}

// ProfileEditOptions captures the §7.5.7 invocation parameters.
type ProfileEditOptions struct {
	Target        string
	Profile       string
	AddInclude    []string
	RemoveInclude []string
	AddExclude    []string
	RemoveExclude []string
	DryRun        bool
}

// ProfileEditResult reports the resulting profile.
type ProfileEditResult struct {
	Profile Profile
	Wrote   bool
}

// ProfileEdit modifies an entry in sync.yaml's `profiles:` block per
// §7.5.7. The target directory and lock file are untouched; a
// subsequent `podium sync` picks up the change.
func ProfileEdit(opts ProfileEditOptions) (*ProfileEditResult, error) {
	if opts.Target == "" {
		return nil, ErrNoTarget
	}
	if opts.Profile == "" {
		return nil, fmt.Errorf("profile edit: profile name required")
	}
	cfg, err := EnsureConfig(opts.Target)
	if err != nil {
		return nil, err
	}
	prof, ok := cfg.Profiles[opts.Profile]
	if !ok {
		// §7.5.7: "If .podium/sync.yaml doesn't exist, podium profile
		// edit <name> creates it with the named profile and an empty
		// defaults: block."
		prof = Profile{}
	}
	for _, p := range opts.AddInclude {
		if !containsStr(prof.Include, p) {
			prof.Include = append(prof.Include, p)
		}
	}
	prof.Include = removeStrings(prof.Include, opts.RemoveInclude)
	for _, p := range opts.AddExclude {
		if !containsStr(prof.Exclude, p) {
			prof.Exclude = append(prof.Exclude, p)
		}
	}
	prof.Exclude = removeStrings(prof.Exclude, opts.RemoveExclude)

	res := &ProfileEditResult{Profile: prof}
	if opts.DryRun {
		return res, nil
	}
	cfg.Profiles[opts.Profile] = prof
	if err := WriteConfig(opts.Target, cfg); err != nil {
		return nil, err
	}
	res.Wrote = true
	return res, nil
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func removeStrings(haystack, drop []string) []string {
	if len(drop) == 0 {
		return haystack
	}
	dropSet := map[string]bool{}
	for _, d := range drop {
		dropSet[d] = true
	}
	out := haystack[:0]
	for _, s := range haystack {
		if !dropSet[s] {
			out = append(out, s)
		}
	}
	return out
}

// Compile-time assertion that time.Time is in the unused import path
// only when the build references it; the package keeps the symbol
// reachable for clock-aware tests.
var _ = time.Time{}
