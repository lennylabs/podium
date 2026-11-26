package materialize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/lennylabs/podium/pkg/adapter"
)

// writeItem is one resolved output: the final bytes destined for an absolute
// path, after folding any inject/merge ops that target it.
type writeItem struct {
	resolved string
	content  []byte
	mode     os.FileMode
}

// foldOps collapses the adapter's File list into one writeItem per distinct
// destination path. OpWrite files keep their content (last write to a path
// wins). OpInject and OpMergeJSON files fold into the target's current bytes:
// the on-disk file (when present) plus any earlier ops in this batch, applied
// in input order. resolved[i] is the absolute destination for files[i]. keep
// names the artifacts whose prior entries and blocks the strip leaves in place
// (see WriteKeeping); nil keeps none.
func foldOps(files []adapter.File, resolved []string, keep func(id string) bool) ([]writeItem, error) {
	idx := map[string]int{}
	items := make([]writeItem, 0, len(files))
	for i, f := range files {
		rp := resolved[i]
		j, seen := idx[rp]
		if !seen {
			var base []byte
			// Inject/merge fold into the existing file; a plain write replaces.
			if f.Op != adapter.OpWrite {
				b, err := readMergeBase(rp)
				if err != nil {
					return nil, err
				}
				base = b
			}
			// §6.7 config-merge reconciliation: strip the prior Podium-owned
			// entries from the base before this sync's fragments fold in, so a
			// re-sync rebuilds Podium's contribution from scratch (removing
			// entries whose artifact is gone) while leaving the operator's
			// untagged entries in place. Done once per path, before the first
			// fragment, so accumulating fragments are not re-stripped.
			switch f.Op {
			case adapter.OpMergeJSON:
				base = stripPodiumOwnedBytes(base, keepUntaken(keep, base, mergeFragments(files, resolved, rp)))
			case adapter.OpInject:
				// Symmetric reconciliation for the marker-based inject files
				// (AGENTS.md / GEMINI.md / config.toml): drop every prior
				// Podium block so a removed artifact's block does not linger,
				// then the current fragments re-add only the live blocks.
				base = stripPodiumBlocks(base, commentStyleFor(f.Path), keep)
			}
			items = append(items, writeItem{resolved: rp, content: base, mode: modeOf(f)})
			j = len(items) - 1
			idx[rp] = j
		}
		out, err := applyOp(items[j].content, f)
		if err != nil {
			return nil, err
		}
		items[j].content = out
		if m := modeOf(f); m != 0o644 {
			items[j].mode = m
		}
	}
	return items, nil
}

// mergeFragments returns the config-merge fragments of files that land at the
// resolved destination rp, in input order.
func mergeFragments(files []adapter.File, resolved []string, rp string) [][]byte {
	var frags [][]byte
	for i, f := range files {
		if f.Op == adapter.OpMergeJSON && resolved[i] == rp {
			frags = append(frags, f.Content)
		}
	}
	return frags
}

// keepUntaken narrows keep, for one config-merge file whose current bytes are
// base, to the artifacts whose keyed entry no fragment in frags takes over. A
// keyed config-map entry (an mcpServers server) is keyed by the artifact's
// name rather than its ID, so another artifact's fragment can record the entry
// a kept artifact holds. Keeping that entry would deep-merge the new fragment
// into it, mixing the two artifacts' fields and leaving both IDs in the index,
// so the kept artifact's contribution to this file is stripped instead and the
// fragment replaces the entry. A nil keep stays nil.
//
// Spec: §6.6, §6.7
func keepUntaken(keep func(id string) bool, base []byte, frags [][]byte) func(id string) bool {
	if keep == nil {
		return nil
	}
	taken := EntriesTakenOver(base, frags)
	if len(taken) == 0 {
		return keep
	}
	return func(id string) bool { return keep(id) && !taken[id] }
}

// EntriesTakenOver returns the artifact IDs whose keyed config-map entry, at
// the path the index of the JSON config document doc records for it, a
// fragment in frags records for a different artifact. A document or fragment
// that carries no index contributes nothing.
//
// Spec: §6.7, §13.11.3
func EntriesTakenOver(doc []byte, frags [][]byte) map[string]bool {
	held := IndexedEntries(doc)
	taken := map[string]bool{}
	for _, frag := range frags {
		for owner, loc := range IndexedEntries(frag) {
			for id, path := range held {
				if id != owner && slices.Equal(path, loc) {
					taken[id] = true
				}
			}
		}
	}
	return taken
}

// resolveMergeTarget returns the path a config-merge or inject into full
// writes. full is returned as it is when it is absent or is not a symbolic
// link. A link at full is followed when it resolves to a path inside realDest
// that passes CheckRecordedPath: workspaces link GEMINI.md or CLAUDE.md to
// AGENTS.md, and the merge then reads and rewrites the file the link names
// and leaves the link in place. A link that names an absent file is handled by
// resolveDanglingLink. A link that resolves outside the destination, into
// .git, or into the sync state under .podium/ is refused with
// ErrUnconfinedPath: a repository checkout can commit such a link
// (§7.8), and following it would write outside the checkout or fold an
// outside file's content into the rendered file the publish step commits.
//
// Spec: §6.6, §6.7, §7.8
func resolveMergeTarget(realDest, full, rel string) (string, error) {
	info, err := os.Lstat(full)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		// Absent, or not a link: readMergeBase reads or refuses it.
		return full, nil
	}
	r, err := resolveWithin(realDest, full, rel)
	if errors.Is(err, fs.ErrNotExist) {
		r, err = resolveDanglingLink(realDest, full, rel)
	}
	if err != nil {
		return "", err
	}
	if err := CheckRecordedPath(r); err != nil {
		return "", fmt.Errorf("%w: %q resolves to %q: %v", ErrUnconfinedPath, rel, r, err)
	}
	return filepath.Join(realDest, filepath.FromSlash(r)), nil
}

// resolveDanglingLink resolves a link at full that names an absent file. A
// workspace can commit GEMINI.md linked to AGENTS.md before any run has
// created AGENTS.md, so the link is followed when the directory of the file
// it names exists inside realDest; the caller then checks the resolved path
// against CheckRecordedPath, and the writer's rename creates the file there
// and leaves the link in place. The rename never follows a link at its
// destination, so a link planted at that name after this check is replaced
// rather than written through. A link whose named directory is absent or
// resolves outside realDest, and a chain whose next link is itself dangling,
// are refused with ErrUnconfinedPath.
//
// Spec: §6.6
func resolveDanglingLink(realDest, full, rel string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", fmt.Errorf("%w: %q is a symbolic link that names no file %s", ErrUnconfinedPath, rel, why)
	}
	dest, err := os.Readlink(full)
	if err != nil {
		return refuse("that can be read")
	}
	if !filepath.IsAbs(dest) {
		// The kernel resolves a relative link against the directory the link
		// sits in, which a directory link on the way can move away from the
		// lexical parent of full.
		linkDir, err := filepath.EvalSymlinks(filepath.Dir(full))
		if err != nil {
			return refuse("in an existing directory")
		}
		dest = filepath.Join(linkDir, dest)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return refuse("in an existing directory")
	}
	if !withinRoot(realDest, parent) {
		return refuse("inside the destination")
	}
	target := filepath.Join(parent, filepath.Base(dest))
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		// The name exists, so the link names another link or the target
		// changed under the resolution; a chain is not followed further.
		return refuse("that it can follow")
	}
	r, err := filepath.Rel(realDest, target)
	if err != nil {
		// Unreachable: withinRoot placed parent under realDest.
		return refuse("inside the destination")
	}
	return filepath.ToSlash(r), nil
}

// readMergeBase returns the existing bytes at rp that an inject or merge folds
// into, or nil when nothing is there. The caller has confirmed that rp's
// directories resolve inside the destination and has resolved a link at rp
// itself through resolveMergeTarget, so a link or any other non-regular file
// still at rp is refused rather than followed.
//
// Spec: §6.7, §7.8
func readMergeBase(rp string) ([]byte, error) {
	info, err := os.Lstat(rp)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// The caller's containment check already resolved rp's directory, so
		// an Lstat error other than absence needs a concurrent change to it.
		return nil, fmt.Errorf("materialize: inspect %q for merge: %w", rp, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is a symbolic link or not a regular file; refusing to merge into it", ErrUnconfinedPath, rp)
	}
	b, err := os.ReadFile(rp)
	if err != nil {
		return nil, fmt.Errorf("materialize: read %q for merge: %w", rp, err)
	}
	return b, nil
}

func modeOf(f adapter.File) os.FileMode {
	if f.Mode == 0 {
		return 0o644
	}
	return os.FileMode(f.Mode)
}

// applyOp folds one File into the running content for its destination.
func applyOp(base []byte, f adapter.File) ([]byte, error) {
	switch f.Op {
	case adapter.OpWrite:
		return f.Content, nil
	case adapter.OpInject:
		if f.Key == "" {
			return nil, fmt.Errorf("materialize: OpInject on %q requires a Key", f.Path)
		}
		return injectBlock(base, f.Key, f.Content, commentStyleFor(f.Path)), nil
	case adapter.OpMergeJSON:
		return mergeJSON(base, f.Content)
	default:
		return nil, fmt.Errorf("materialize: unknown op %d on %q", f.Op, f.Path)
	}
}

// StripPodiumOwnedBytes is the exported form of the §6.7 config-merge JSON
// reconciliation: it removes every Podium-owned entry from a JSON config
// document, leaving the operator's entries intact. The sync layer calls it to
// clean an orphaned config file whose last contributing artifact is gone. An
// entry owned by an artifact keep reports stays; a nil keep strips them all.
func StripPodiumOwnedBytes(base []byte, keep func(id string) bool) []byte {
	return stripPodiumOwnedBytes(base, keep)
}

// mergeJSON deep-merges the fragment object into the existing JSON document,
// preserving keys the operator set that the fragment does not touch. The
// fragment's leaf values win on conflict. Output is indented with a trailing
// newline so a re-merge is stable.
func mergeJSON(base, fragment []byte) ([]byte, error) {
	dst := map[string]any{}
	if len(bytes.TrimSpace(base)) > 0 {
		if err := json.Unmarshal(base, &dst); err != nil {
			return nil, fmt.Errorf("materialize: existing JSON config is invalid: %w", err)
		}
	}
	src := map[string]any{}
	if err := json.Unmarshal(fragment, &src); err != nil {
		return nil, fmt.Errorf("materialize: JSON merge fragment is invalid: %w", err)
	}
	deepMerge(dst, src)
	out, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// deepMerge recursively merges src into dst. Nested objects are merged
// recursively; arrays at the same key are concatenated (dst first, then src),
// so multiple Podium config-merge fragments accumulate their entries instead of
// the last one replacing the rest (for example two hooks that map to the same
// native event both land in that event's array). Scalars are replaced by the
// src value. Reconciliation relies on the base having been stripped of prior
// Podium-owned entries (see stripPodiumOwnedBytes), so concatenation does not
// duplicate across re-syncs.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		if sa, ok := sv.([]any); ok {
			if da, ok := dst[k].([]any); ok {
				dst[k] = append(da, sa...)
				continue
			}
		}
		dst[k] = sv
	}
}

// stripPodiumOwnedBytes removes every Podium-owned entry (an object carrying
// the adapter.PodiumOwnedKey tag) from a JSON config document, returning the
// document with the operator's untagged entries intact. It is the read half of
// the §6.7 config-merge reconciliation: the prior sync's Podium entries are
// removed before the current sync's fragments are merged. A document that is
// empty or not valid JSON is returned unchanged (the merge step then reports
// the parse error). An entry owned by an artifact keep reports stays in place,
// with its index entry; a nil keep strips every Podium-owned entry.
func stripPodiumOwnedBytes(base []byte, keep func(id string) bool) []byte {
	if len(bytes.TrimSpace(base)) == 0 {
		return base
	}
	var v any
	if err := json.Unmarshal(base, &v); err != nil {
		return base
	}
	// First remove the keyed-map entries named by the top-level ownership index
	// (mcpServers/mcp servers), then the index. Then the recursive walk removes
	// in-entry-tagged entries (hook arrays, marketplace plugins, and legacy
	// in-entry mcp tags written before the index existed).
	if root, ok := v.(map[string]any); ok {
		stripIndexedEntries(root, keep)
	}
	stripped := stripPodiumOwned(v, keep)
	out, err := json.MarshalIndent(stripped, "", "  ")
	if err != nil {
		return base
	}
	return append(out, '\n')
}

// stripIndexedEntries removes every config-map entry named by the top-level
// adapter.PodiumIndexKey object, then removes the index itself. Each index value
// is a path ([container, key]) into the document; the entry it points at is the
// Podium-owned one to drop before the current sync's fragments merge in. A
// missing or malformed index, or a path to an entry that no longer exists, is a
// no-op so the strip is safe on a hand-edited file. The index is keyed by
// artifact ID, so an entry keep reports stays together with its index entry,
// and the index is removed only when no kept entry remains in it.
func stripIndexedEntries(root map[string]any, keep func(id string) bool) {
	idx, ok := root[adapter.PodiumIndexKey].(map[string]any)
	if !ok {
		return
	}
	for id, loc := range idx {
		if keep != nil && keep(id) {
			continue
		}
		delete(idx, id)
		path, ok := loc.([]any)
		if !ok || len(path) == 0 {
			continue
		}
		deleteAtPath(root, path)
	}
	if len(idx) == 0 {
		delete(root, adapter.PodiumIndexKey)
	}
}

// IndexedEntries returns the keyed config-map entries that a JSON config
// document or a config-merge fragment records in its top-level
// adapter.PodiumIndexKey object, as artifact ID -> entry path ([container,
// key]). Two artifacts can write one entry, because the key is the artifact's
// name rather than its ID, so a caller compares these paths to find an entry
// another artifact takes over. A document that is not a JSON object, a missing
// index, and an index value that is not a list of strings contribute nothing.
//
// Spec: §6.7, §13.11.3
func IndexedEntries(doc []byte) map[string][]string {
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil
	}
	idx, ok := root[adapter.PodiumIndexKey].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(idx))
	for id, loc := range idx {
		raw, ok := loc.([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		path := make([]string, 0, len(raw))
		for _, el := range raw {
			s, ok := el.(string)
			if !ok {
				path = nil
				break
			}
			path = append(path, s)
		}
		if path != nil {
			out[id] = path
		}
	}
	return out
}

// deleteAtPath walks the string path from root and deletes the leaf key from its
// parent object. A non-string path element, or a parent that is not an object,
// stops the walk without deleting.
func deleteAtPath(root map[string]any, path []any) {
	cur := root
	for i := 0; i < len(path)-1; i++ {
		k, ok := path[i].(string)
		if !ok {
			return
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	if leaf, ok := path[len(path)-1].(string); ok {
		delete(cur, leaf)
	}
}

// stripPodiumOwned walks a decoded JSON value and removes any object that
// carries the Podium ownership tag: object members whose value is a tagged
// object are deleted, and array elements that are tagged objects are dropped.
// When removing the tagged elements empties an array (a hook event whose only
// handler was Podium's), the now-empty array is dropped from its parent map so
// a changed hook_event does not leave a stale empty native-event key behind
// (for example .gemini/settings.json {"hooks": {"BeforeTool": []}} after the
// event moved to AfterTool). An array the operator left empty carried no Podium
// element, so it is preserved. An object whose tag names an artifact keep
// reports is left in place.
func stripPodiumOwned(v any, keep func(id string) bool) any {
	out, _ := stripPodiumOwnedTracked(v, keep)
	return out
}

// stripPodiumOwnedTracked is stripPodiumOwned with an extra return reporting
// whether v is an array that this call emptied by removing a Podium-tagged
// element. The map case uses that signal to delete a key whose array value was
// emptied by stripping while keeping a key whose array the operator already left
// empty.
func stripPodiumOwnedTracked(v any, keep func(id string) bool) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if owner, ok := podiumOwner(val); ok {
				if keep == nil || !keep(owner) {
					delete(t, k)
				}
				continue
			}
			nv, emptied := stripPodiumOwnedTracked(val, keep)
			if emptied {
				delete(t, k)
				continue
			}
			t[k] = nv
		}
		return t, false
	case []any:
		out := make([]any, 0, len(t))
		removed := false
		for _, el := range t {
			if owner, ok := podiumOwner(el); ok {
				if keep != nil && keep(owner) {
					out = append(out, el)
				} else {
					removed = true
				}
				continue
			}
			nv, _ := stripPodiumOwnedTracked(el, keep)
			out = append(out, nv)
		}
		return out, removed && len(out) == 0
	default:
		return v, false
	}
}

// podiumOwner reports whether v is a JSON object carrying the
// adapter.PodiumOwnedKey tag, and returns the tag's value, which is the owning
// artifact ID (or a marketplace plugin name). A tag whose value is not a string
// still marks the object as Podium-owned and names no owner.
func podiumOwner(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	tag, has := m[adapter.PodiumOwnedKey]
	if !has {
		return "", false
	}
	owner, _ := tag.(string)
	return owner, true
}
