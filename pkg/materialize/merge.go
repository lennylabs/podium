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

// deepMerge recursively merges src into dst. Nested objects are merged; every
// other value (scalars, arrays) is replaced by the src value.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}
