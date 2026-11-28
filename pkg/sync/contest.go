package sync

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/materialize"
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

// entrySep separates the file path from the entry name in an entry claim.
// No recorded path holds a control character, so the claim of an entry
// never equals the claim of a file.
const entrySep = "\x00"

// outputClaims returns every claim an output makes: the paths it writes
// whole, and the named entries it adds to shared configuration files.
func outputClaims(files []adapter.File) []string {
	return append(standalonePaths(files), namedEntries(files)...)
}

// namedEntries returns the named entries an output adds to shared files, as
// path, entrySep, and entry name: each key of a keyed JSON map that a merge
// fragment records in its x-podium index, such as mcpServers.<name>, and
// each TOML table that an inject block of a .toml file opens, such as
// [mcp_servers.<name>]. The name of such an entry comes from the artifact's
// name field rather than its ID, so two IDs can add one entry. An array of
// tables ([[hooks.X]]) appends and an entry keyed by the artifact ID is the
// artifact's own, so neither is claimed.
//
// Spec: §7.5 (Output paths of distinct IDs)
func namedEntries(files []adapter.File) []string {
	var out []string
	for _, f := range files {
		switch {
		case f.Op == adapter.OpMergeJSON:
			for _, loc := range materialize.IndexedEntries(f.Content) {
				out = append(out, f.Path+entrySep+strings.Join(loc, "."))
			}
		case f.Op == adapter.OpInject && strings.HasSuffix(f.Path, ".toml"):
			for _, line := range strings.Split(string(f.Content), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]") {
					out = append(out, f.Path+entrySep+strings.TrimSpace(line[1:len(line)-1]))
				}
			}
		}
	}
	return out
}

// conflictFailure is the materialize.output_conflict refusal of id, which
// names the path, the entry for a named entry, and the other ID. Both IDs
// passed the well-formed rule, and the path and the entry are reported
// through attest.ReportString.
func conflictFailure(id string, c outputContest) *attest.Failure {
	if path, entry, ok := strings.Cut(c.path, entrySep); ok {
		return attest.NewFailure(attest.CodeOutputConflict, attest.ConstraintEntry,
			fmt.Sprintf("%s and %s both add the entry %s to %s", id, c.other, attest.ReportString(entry), attest.ReportString(path)))
	}
	return attest.NewFailure(attest.CodeOutputConflict, attest.ConstraintFile,
		fmt.Sprintf("%s and %s both write %s", id, c.other, attest.ReportString(c.path)))
}
