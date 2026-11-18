package sync

import "fmt"

// OnUnparseable selects what a filesystem-source sync does with the previous
// output of an artifact the §4.1 unparseable-manifest rule drops. The zero
// value is OnUnparseableKeep, the default.
//
// Spec: §13.11.3
type OnUnparseable string

const (
	// OnUnparseableKeep leaves the files and lock entry an earlier run wrote
	// for the refused artifact, because an unparseable manifest is usually a
	// file in the middle of an edit.
	OnUnparseableKeep OnUnparseable = "keep"
	// OnUnparseableRemove removes the refused artifact's previous output, as a
	// collision, no-inheritance, or removed-type drop does.
	OnUnparseableRemove OnUnparseable = "remove"
)

// OnUnparseableEnv is the environment variable that sets OnUnparseable below
// the --on-unparseable flag and above defaults.on_unparseable (§7.5.2).
const OnUnparseableEnv = "PODIUM_SYNC_ON_UNPARSEABLE"

// valid reports whether o is a value Run accepts. The empty value is the
// keep default.
func (o OnUnparseable) valid() bool {
	return o == "" || o == OnUnparseableKeep || o == OnUnparseableRemove
}

// check returns an error for a value Run and Override refuse. A caller that
// builds the value through ResolveOnUnparseable never hits it.
func (o OnUnparseable) check() error {
	if o.valid() {
		return nil
	}
	return fmt.Errorf("sync: on_unparseable must be keep | remove, got %q", string(o))
}

// ResolveOnUnparseable resolves the setting by the §7.5.2 precedence: the
// --on-unparseable flag value, then OnUnparseableEnv read through getenv, then
// defaults.on_unparseable in the merged sync.yaml, then keep. An empty value at
// any step counts as unset, and a nil merged config sets nothing. A value
// other than keep or remove is an error that names the source it came from;
// the CLI reports it as a usage error and exits 2.
//
// Spec: §7.5.2, §13.11.3
func ResolveOnUnparseable(flagVal string, getenv func(string) string, merged *MergedConfig) (OnUnparseable, error) {
	candidates := []struct{ value, source string }{
		{flagVal, "--on-unparseable"},
		{getenv(OnUnparseableEnv), OnUnparseableEnv},
	}
	if merged != nil {
		candidates = append(candidates, struct{ value, source string }{merged.Defaults.OnUnparseable, "defaults.on_unparseable"})
	}
	for _, c := range candidates {
		if c.value == "" {
			continue
		}
		o := OnUnparseable(c.value)
		if !o.valid() {
			return "", fmt.Errorf("%s must be keep | remove, got %q", c.source, c.value)
		}
		return o, nil
	}
	return OnUnparseableKeep, nil
}
