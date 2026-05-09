package materialize

import "github.com/lennylabs/podium/pkg/adapter"

// HookFunc is the per-file transform pkg/hook implementations
// satisfy. Materialize accepts a slice of these so the §6.6 step 4
// hook chain runs after the adapter and before the atomic write.
type HookFunc func(file adapter.File) (out adapter.File, drop bool, err error)

// Materialize runs the §6.6 pipeline against an adapter's output:
// hook chain → atomic write. Each hook in order receives the
// previous hook's output; hooks may rewrite, drop, or error. If a
// hook returns an error, no files are written.
func Materialize(destination string, files []adapter.File, hooks []HookFunc) error {
	current := files
	for _, h := range hooks {
		next := make([]adapter.File, 0, len(current))
		for _, f := range current {
			out, drop, err := h(f)
			if err != nil {
				return err
			}
			if drop {
				continue
			}
			next = append(next, out)
		}
		current = next
	}
	return Write(destination, current)
}
