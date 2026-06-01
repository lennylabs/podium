package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
// in input order. resolved[i] is the absolute destination for files[i].
func foldOps(files []adapter.File, resolved []string) ([]writeItem, error) {
	idx := map[string]int{}
	items := make([]writeItem, 0, len(files))
	for i, f := range files {
		rp := resolved[i]
		j, seen := idx[rp]
		if !seen {
			var base []byte
			// Inject/merge fold into the existing file; a plain write replaces.
			if f.Op != adapter.OpWrite {
				if b, err := os.ReadFile(rp); err == nil {
					base = b
				} else if !os.IsNotExist(err) {
					return nil, fmt.Errorf("materialize: read %q for merge: %w", rp, err)
				}
			}
			// §6.7 config-merge reconciliation: strip the prior Podium-owned
			// entries from the base before this sync's fragments fold in, so a
			// re-sync rebuilds Podium's contribution from scratch (removing
			// entries whose artifact is gone) while leaving the operator's
			// untagged entries in place. Done once per path, before the first
			// fragment, so accumulating fragments are not re-stripped.
			switch f.Op {
			case adapter.OpMergeJSON:
				base = stripPodiumOwnedBytes(base)
			case adapter.OpInject:
				// Symmetric reconciliation for the marker-based inject files
				// (AGENTS.md / GEMINI.md / config.toml): drop every prior
				// Podium block so a removed artifact's block does not linger,
				// then the current fragments re-add only the live blocks.
				base = stripPodiumBlocks(base, commentStyleFor(f.Path))
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

// commentStyle wraps a marker comment for a given file format.
type commentStyle struct{ open, close string }

func commentStyleFor(path string) commentStyle {
	if strings.HasSuffix(path, ".toml") {
		return commentStyle{open: "# ", close: ""}
	}
	// Markdown (AGENTS.md, GEMINI.md, CLAUDE.md) and other text files.
	return commentStyle{open: "<!-- ", close: " -->"}
}

// injectBlock replaces the Podium-managed block keyed by key with block,
// wrapped in begin/end markers. When no such block exists it is appended.
// Re-applying the same block is idempotent.
func injectBlock(base []byte, key string, block []byte, cs commentStyle) []byte {
	begin := cs.open + "podium:begin:" + key + cs.close
	end := cs.open + "podium:end:" + key + cs.close
	body := begin + "\n" + strings.TrimRight(string(block), "\n") + "\n" + end
	s := string(base)
	if bi := strings.Index(s, begin); bi >= 0 {
		if rel := strings.Index(s[bi:], end); rel >= 0 {
			ei := bi + rel + len(end)
			return []byte(s[:bi] + body + s[ei:])
		}
	}
	var b strings.Builder
	b.WriteString(s)
	if len(s) > 0 {
		if !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(body)
	b.WriteByte('\n')
	return []byte(b.String())
}

// stripPodiumBlocks removes every Podium-managed inject block (the region
// between a `podium:begin:<key>` and `podium:end:<key>` marker pair in the
// given comment style) from a text file, leaving the operator's surrounding
// content intact. It is the inject counterpart to stripPodiumOwnedBytes: the
// prior sync's blocks are removed before the current fragments re-add the live
// ones, so a removed artifact's block does not survive.
func stripPodiumBlocks(base []byte, cs commentStyle) []byte {
	s := string(base)
	beginTag := cs.open + "podium:begin:"
	endTag := cs.open + "podium:end:"
	for {
		bi := strings.Index(s, beginTag)
		if bi < 0 {
			break
		}
		ej := strings.Index(s[bi:], endTag)
		if ej < 0 {
			break
		}
		ej += bi
		var ee int
		if cs.close != "" {
			ce := strings.Index(s[ej:], cs.close)
			if ce < 0 {
				break
			}
			ee = ej + ce + len(cs.close)
		} else {
			if nl := strings.IndexByte(s[ej:], '\n'); nl < 0 {
				ee = len(s)
			} else {
				ee = ej + nl
			}
		}
		if ee < len(s) && s[ee] == '\n' {
			ee++
		}
		start := bi
		for start > 0 && s[start-1] == '\n' {
			start--
		}
		sep := ""
		if start > 0 && ee < len(s) {
			sep = "\n"
		}
		s = s[:start] + sep + s[ee:]
	}
	return []byte(s)
}

// StripPodiumOwnedBytes is the exported form of the §6.7 config-merge JSON
// reconciliation: it removes every Podium-owned entry from a JSON config
// document, leaving the operator's entries intact. The sync layer calls it to
// clean an orphaned config file whose last contributing artifact is gone.
func StripPodiumOwnedBytes(base []byte) []byte { return stripPodiumOwnedBytes(base) }

// StripPodiumBlocks is the exported form of the inject reconciliation: it
// removes every Podium-managed block from a text inject file (AGENTS.md /
// GEMINI.md / config.toml), selecting the marker style from the path.
func StripPodiumBlocks(base []byte, path string) []byte {
	return stripPodiumBlocks(base, commentStyleFor(path))
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
// the parse error).
func stripPodiumOwnedBytes(base []byte) []byte {
	if len(bytes.TrimSpace(base)) == 0 {
		return base
	}
	var v any
	if err := json.Unmarshal(base, &v); err != nil {
		return base
	}
	stripped := stripPodiumOwned(v)
	out, err := json.MarshalIndent(stripped, "", "  ")
	if err != nil {
		return base
	}
	return append(out, '\n')
}

// stripPodiumOwned walks a decoded JSON value and removes any object that
// carries the Podium ownership tag: object members whose value is a tagged
// object are deleted, and array elements that are tagged objects are dropped.
func stripPodiumOwned(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if podiumOwned(val) {
				delete(t, k)
				continue
			}
			t[k] = stripPodiumOwned(val)
		}
		return t
	case []any:
		out := make([]any, 0, len(t))
		for _, el := range t {
			if podiumOwned(el) {
				continue
			}
			out = append(out, stripPodiumOwned(el))
		}
		return out
	default:
		return v
	}
}

// podiumOwned reports whether v is a JSON object carrying the
// adapter.PodiumOwnedKey tag.
func podiumOwned(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, has := m[adapter.PodiumOwnedKey]
	return has
}
