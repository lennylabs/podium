package sync

import (
	"fmt"
	"maps"
	"slices"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
)

// outputContest is one ID's contest of an output path: the path it writes
// and another ID that writes the same path.
type outputContest struct {
	path  string
	other string
}

// contestedOutputs compares the standalone output paths of distinct IDs.
// claims maps an ID to the paths its output writes whole; a path that a §6.7
// config merge or inject shares between artifacts by design is left out by
// the caller. Two IDs contest a path when both write it, compared byte for
// byte and again with the ASCII letters A to Z folded to lower case, because
// a file system that ignores letter case writes both to one file. The result
// maps each contesting ID to its first contest in sorted path order, so the
// report reads the same on every run.
//
// Spec: §7.5 (Output paths of distinct IDs), §7.8 (Author attestation)
func contestedOutputs(claims map[string][]string) map[string]outputContest {
	byKey := map[string]map[string]string{}
	for _, id := range slices.Sorted(maps.Keys(claims)) {
		for _, p := range claims[id] {
			for _, k := range []string{"exact:" + p, "folded:" + asciiLower(p)} {
				if byKey[k] == nil {
					byKey[k] = map[string]string{}
				}
				if _, ok := byKey[k][id]; !ok {
					byKey[k][id] = p
				}
			}
		}
	}
	out := map[string]outputContest{}
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		ids := slices.Sorted(maps.Keys(byKey[k]))
		if len(ids) < 2 {
			continue
		}
		for i, id := range ids {
			if _, ok := out[id]; ok {
				continue
			}
			other := ids[(i+1)%len(ids)]
			out[id] = outputContest{path: byKey[k][id], other: other}
		}
	}
	return out
}

// asciiLower folds the ASCII letters A to Z of s to lower case and leaves
// every other byte as it is.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// standalonePaths returns the paths of files that an output writes whole.
func standalonePaths(files []adapter.File) []string {
	var out []string
	for _, f := range files {
		if mergeKind(f.Op) == "" {
			out = append(out, f.Path)
		}
	}
	return out
}

// conflictFailure is the materialize.output_conflict refusal of id, which
// names the path and the other ID. Both IDs passed the well-formed rule, and
// the path is reported through attest.ReportString.
func conflictFailure(id string, c outputContest) *attest.Failure {
	return attest.NewFailure(attest.CodeOutputConflict, attest.ConstraintFile,
		fmt.Sprintf("%s and %s both write %s", id, c.other, attest.ReportString(c.path)))
}
