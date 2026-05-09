// Package sync orchestrates filesystem-source materialization (spec §7.5,
// §13.11): open the filesystem registry, walk every visible artifact in
// the caller's effective view, run the configured HarnessAdapter, and
// write atomically through pkg/materialize.
package sync

import (
	"errors"
	"fmt"
	"sort"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Errors returned by Run. Tests assert against them via errors.Is.
var (
	// ErrNoTarget signals that Options.Target was empty.
	ErrNoTarget = errors.New("sync: target directory not specified")
)

// Options are the inputs to Run. RegistryPath is the filesystem-source
// registry path (per §13.11). Target is the destination directory where
// adapter output lands. AdapterID selects the HarnessAdapter from the
// registry; the default is "none" (canonical layout pass-through).
type Options struct {
	RegistryPath    string
	Target          string
	AdapterID       string
	AdapterRegistry *adapter.Registry
	DryRun          bool
}

// Result describes what a Run actually did. Used by callers (CLI, tests)
// for reporting.
type Result struct {
	Adapter   string
	Target    string
	Artifacts []ArtifactResult
}

// ArtifactResult is one artifact's contribution to the materialized output.
type ArtifactResult struct {
	ID    string
	Layer string
	Files []string
}

// Run executes one filesystem-source sync. The function does not consult
// any HTTP service; it reads the registry, applies layer composition with
// CollisionPolicyHighestWins (per §4.6), and writes the adapter output to
// Target.
//
// When Options.DryRun is true, Run resolves the artifact set, returns the
// Result, and writes nothing.
func Run(opts Options) (*Result, error) {
	if opts.Target == "" && !opts.DryRun {
		return nil, ErrNoTarget
	}
	if opts.AdapterID == "" {
		opts.AdapterID = "none"
	}
	if opts.AdapterRegistry == nil {
		opts.AdapterRegistry = adapter.DefaultRegistry()
	}
	a, err := opts.AdapterRegistry.Get(opts.AdapterID)
	if err != nil {
		return nil, err
	}

	reg, err := filesystem.Open(opts.RegistryPath)
	if err != nil {
		return nil, err
	}
	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		return nil, err
	}

	res := &Result{Adapter: a.ID(), Target: opts.Target}

	allFiles := []adapter.File{}
	for _, rec := range records {
		out, err := a.Adapt(adapter.Source{
			ArtifactID:    rec.ID,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		})
		if err != nil {
			return nil, fmt.Errorf("adapter %q failed for %s: %w", a.ID(), rec.ID, err)
		}
		paths := make([]string, len(out))
		for i, f := range out {
			paths[i] = f.Path
		}
		sort.Strings(paths)
		res.Artifacts = append(res.Artifacts, ArtifactResult{
			ID:    rec.ID,
			Layer: rec.Layer.ID,
			Files: paths,
		})
		allFiles = append(allFiles, out...)
	}

	if opts.DryRun {
		return res, nil
	}
	if err := materialize.Write(opts.Target, allFiles); err != nil {
		return nil, err
	}
	return res, nil
}
