package sync

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/adapter"
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

	// Materialization inputs (§7.5.5). When RegistryPath is set and DryRun is
	// false, Override re-materializes the target after updating the toggles:
	// --add then writes the artifact's files through the active adapter and
	// --remove deletes them, just like a full sync would. The scope and
	// profile come from the lock so the baseline matches the last sync. When
	// RegistryPath is empty, Override only records the toggles (the caller
	// materializes separately).
	RegistryPath    string
	AdapterID       string
	AdapterRegistry *adapter.Registry
	OverlayPath     string
	HTTPClient      *http.Client
}

// OverrideResult is what Override returns: the new lock state, whether
// anything actually changed, and any advisory warnings (e.g. a redundant
// --add on an already-materialized artifact, per §7.5.5).
type OverrideResult struct {
	Lock     *LockFile
	Changed  bool
	Warnings []string
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
	var warnings []string

	if opts.Reset {
		if len(lock.Toggles.Add) > 0 || len(lock.Toggles.Remove) > 0 {
			changed = true
		}
		lock.Toggles = LockToggles{}
	}

	// "Already materialized" is the set in the lock's artifacts list, minus any
	// pending removal: an ID slated for removal is not currently on disk, so
	// re-adding it is a real operation rather than a redundant no-op.
	materialized := map[string]bool{}
	for _, a := range lock.Artifacts {
		materialized[a.ID] = true
	}
	pendingRemove := map[string]bool{}
	for _, t := range lock.Toggles.Remove {
		pendingRemove[t.ID] = true
	}

	for _, id := range opts.Add {
		// spec: §7.5.5 — "running --add on something already materialized is a
		// no-op with a warning." An ID already in toggles.add, or already in the
		// resolved/materialized set (and not pending removal), is redundant: skip
		// the toggle so it does not survive into a later save-as as a stray
		// pinned include, and surface a warning.
		if alreadyAdded(lock.Toggles.Add, id) || (materialized[id] && !pendingRemove[id]) {
			warnings = append(warnings, fmt.Sprintf("%q is already materialized; --add is a no-op", id))
			continue
		}
		lock.Toggles.Add = append(lock.Toggles.Add, LockToggle{
			ID:      id,
			AddedAt: now,
		})
		changed = true
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

	// §7.5.5: --add writes the artifact's files and --remove deletes them.
	// Re-materialize the target from the lock's scope + the updated toggles
	// so the on-disk set matches. PreserveToggles keeps the toggles we just
	// wrote and rewrites the lock with the new materialized paths.
	if !opts.DryRun && opts.RegistryPath != "" {
		if _, err := Run(Options{
			RegistryPath:    opts.RegistryPath,
			Target:          opts.Target,
			AdapterID:       opts.AdapterID,
			AdapterRegistry: opts.AdapterRegistry,
			OverlayPath:     opts.OverlayPath,
			HTTPClient:      opts.HTTPClient,
			Profile:         string(lock.Profile),
			Scope: ScopeFilter{
				Include: lock.Scope.Include,
				Exclude: lock.Scope.Exclude,
				Types:   lock.Scope.Type,
			},
			PreserveToggles: true,
			// spec: §7.5.3 — override-driven lock writes stamp
			// last_synced_by: override.
			LastSyncedBy: "override",
		}); err != nil {
			return nil, fmt.Errorf("override: materialize: %w", err)
		}
		// Reflect the rewritten lock (materialized paths, last_synced_at) in
		// the returned state.
		if reread, rerr := ReadLock(opts.Target); rerr == nil && reread != nil {
			lock = reread
		}
	}
	return &OverrideResult{Lock: lock, Changed: changed, Warnings: warnings}, nil
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
	// Detect a fresh sync.yaml so the write can emit the empty defaults: block
	// §7.5.6 documents for a newly created file.
	cfg, err := ReadConfig(opts.Target)
	if err != nil {
		return nil, err
	}
	fresh := cfg == nil
	if cfg == nil {
		cfg = &SyncConfig{Profiles: map[string]Profile{}}
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
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
	if err := writeConfig(opts.Target, cfg, fresh); err != nil {
		return nil, err
	}
	// spec: §7.5.6 / §11 "Save-as test" — the toggles are now part of the
	// profile's scope, so clear them, and the saved profile becomes the target's
	// active profile (the lock's `profile:` field).
	lock.Toggles = LockToggles{}
	lock.Profile = nullProfile(opts.Profile)
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
// subsequent `podium sync` picks up the change. The edit round-trips the
// file through a yaml.Node tree so comments and formatting around the
// untouched keys survive (§7.5.7 "preserving formatting and comments").
func ProfileEdit(opts ProfileEditOptions) (*ProfileEditResult, error) {
	if opts.Target == "" {
		return nil, ErrNoTarget
	}
	if opts.Profile == "" {
		return nil, fmt.Errorf("profile edit: profile name required")
	}
	return editProfileYAML(opts)
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
