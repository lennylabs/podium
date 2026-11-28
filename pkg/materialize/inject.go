package materialize

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
)

// commentStyle wraps a marker comment for a given file format.
type commentStyle struct{ open, close string }

func commentStyleFor(path string) commentStyle {
	if strings.HasSuffix(path, ".toml") {
		return commentStyle{open: "# ", close: ""}
	}
	// Markdown (AGENTS.md, GEMINI.md, CLAUDE.md) and other text files.
	return commentStyle{open: "<!-- ", close: " -->"}
}

// utf8BOM is the byte order mark an editor can write at the start of a text
// file. It belongs to the operator's file, so a marker on the first line is
// read after it and a removal leaves it in place.
const utf8BOM = "\xef\xbb\xbf"

// podiumBlock is one Podium-managed block in a text inject file: a begin
// marker line, the block's lines, and the end marker line naming the same key.
type podiumBlock struct {
	// key is the artifact ID both markers name.
	key string
	// begin is the offset of the begin marker's line, after a byte order
	// mark that starts the file.
	begin int
	// markerEnd is the offset just past the end marker line's text, before
	// the line's terminator.
	markerEnd int
	// end is the offset just past the end marker line's terminator, or the
	// length of the file when that line has none.
	end int
}

// injectBlock replaces the Podium-managed block keyed by key with block,
// wrapped in begin/end markers. When no such block exists it is appended.
// Re-applying the same block is idempotent.
//
// An appended block adds exactly one line feed before the begin marker when
// the file is not empty, and one after the end marker. After a file that ends
// with a newline the line feed is a blank separator line; after one that does
// not, it terminates the operator's last line. stripPodiumBlocks removes the
// same line feeds, so removing the block restores the operator's bytes, final
// newline or its absence included. Adding a second line feed when the file
// ends without one would make that file indistinguishable from one that ends
// with a newline, and the removal could not restore both. A file that holds
// only a byte order mark counts as empty: stripping the block from a file
// Podium created and an editor gave a byte order mark leaves the mark alone,
// and a separator after it would change the file on an unchanged sync.
//
// Spec: §6.7
func injectBlock(base []byte, key string, block []byte, cs commentStyle) []byte {
	begin := cs.open + markerName("begin", key) + cs.close
	end := cs.open + markerName("end", key) + cs.close
	body := begin + "\n" + strings.TrimRight(string(block), "\n") + "\n" + end
	s := string(base)
	for _, blk := range podiumBlocks(s, cs) {
		if blk.key == key {
			return []byte(s[:blk.begin] + body + s[blk.markerEnd:])
		}
	}
	var b strings.Builder
	b.WriteString(s)
	if len(s) > 0 && s != utf8BOM {
		b.WriteByte('\n')
	}
	b.WriteString(body)
	b.WriteByte('\n')
	return []byte(b.String())
}

// stripPodiumBlocks removes every Podium-managed inject block from a text
// file, leaving the operator's surrounding content intact. It is the inject
// counterpart to stripPodiumOwnedBytes: the prior sync's blocks are removed
// before the current fragments re-add the live ones, so a removed artifact's
// block does not survive. A block whose key keep reports stays in place; a nil
// keep strips every block. What counts as a block is podiumBlocks's rule, and
// removalSpan decides which line breaks around a block go with it. The blocks
// are removed in file order, each against the text the earlier removals left.
//
// Spec: §6.7
func stripPodiumBlocks(base []byte, cs commentStyle, keep func(id string) bool) []byte {
	s := string(base)
	blocks := podiumBlocks(s, cs)
	out := make([]byte, 0, len(s))
	pos := 0
	for i, blk := range blocks {
		if keep != nil && keep(blk.key) {
			continue
		}
		out = append(out, s[pos:blk.begin]...)
		nextBegin := -1
		if i+1 < len(blocks) {
			nextBegin = blocks[i+1].begin
		}
		drop, end := removalSpan(out, s, blk, nextBegin)
		out = out[:len(out)-drop]
		pos = end
	}
	return append(out, s[pos:]...)
}

// removalSpan decides what a removal of blk takes beyond the block's lines
// and the end marker's line terminator. before is the text that precedes blk
// once the earlier removals are applied, and nextBegin is the offset of the
// next block in s, or -1. drop is the number of trailing bytes of before the
// removal takes, and end is the offset in s where the kept text resumes.
//
// At most one line break injectBlock inserted goes with the block. The line
// break before the begin marker goes when it forms a blank separator line, or
// when nothing follows the block, where it is the terminator injectBlock added
// to an operator line that had none. Otherwise it ends the operator's line
// above the block and stays, so the operator's lines above and below the
// block are not joined; when the next Podium block follows directly, that
// block's separator goes instead, so the next block takes the removed one's
// place as injectBlock would have written it. A line break is a line feed, or
// a carriage return and line feed when the begin marker's line ends that way,
// which is how a checkout that converts line endings leaves Podium's lines.
//
// Spec: §6.7
func removalSpan(before []byte, s string, blk podiumBlock, nextBegin int) (drop, end int) {
	eol := lineEnding(s[blk.begin:])
	brk := 0
	if bytes.HasSuffix(before, []byte(eol)) {
		brk = len(eol)
	} else if bytes.HasSuffix(before, []byte("\n")) {
		brk = 1
	}
	above := len(before) - brk
	blankSeparator := brk > 0 && (above == 0 || before[above-1] == '\n')
	if blankSeparator || (brk > 0 && blk.end == len(s)) {
		return brk, blk.end
	}
	if nextBegin == blk.end+len(eol) && strings.HasPrefix(s[blk.end:], eol) {
		return 0, nextBegin
	}
	return 0, blk.end
}

// markerLine is a line of an inject file that is a Podium marker.
type markerLine struct {
	// begin is true for a begin marker and false for an end marker.
	begin bool
	// key is the artifact ID the marker names.
	key string
	// start is the offset of the line, after a byte order mark that starts
	// the file.
	start int
	// textEnd is the offset just past the line's text, before its
	// terminator.
	textEnd int
	// next is the offset of the following line.
	next int
}

// podiumBlocks returns the Podium blocks in s in file order. A marker is a
// line whose text, apart from spaces and tabs around it and its line
// terminator, is the marker comment, so operator prose that mentions a marker
// is not one and the TOML marker for a key is not read as the marker for a
// longer key that starts with it. A begin marker pairs with the first later
// end marker that names the same key. One that reaches another begin marker
// for its key, or the end of the file, first is unterminated and is left in
// place, because the operator's content would otherwise be taken up to an
// unrelated marker. Markers inside a block belong to its content.
//
// The file is read once, and each begin marker finds its end marker by a
// search in the per-key marker positions, so a file with many unterminated
// begin markers costs no more than reading it.
func podiumBlocks(s string, cs commentStyle) []podiumBlock {
	markers := scanMarkers(s, cs)
	begins, ends := map[string][]int{}, map[string][]int{}
	for i, m := range markers {
		if m.begin {
			begins[m.key] = append(begins[m.key], i)
		} else {
			ends[m.key] = append(ends[m.key], i)
		}
	}
	var blocks []podiumBlock
	from := 0
	for i, m := range markers {
		if !m.begin || m.start < from {
			continue
		}
		e := firstAfter(ends[m.key], i)
		if e < 0 {
			continue
		}
		if b := firstAfter(begins[m.key], i); b >= 0 && b < e {
			continue
		}
		end := markers[e]
		blocks = append(blocks, podiumBlock{key: m.key, begin: m.start, markerEnd: end.textEnd, end: end.next})
		from = end.next
	}
	return blocks
}

// firstAfter returns the first element of the ascending list idx that is
// greater than i, or -1 when none is.
func firstAfter(idx []int, i int) int {
	if j := sort.SearchInts(idx, i+1); j < len(idx) {
		return idx[j]
	}
	return -1
}

// scanMarkers returns every marker line of s in file order.
func scanMarkers(s string, cs commentStyle) []markerLine {
	var out []markerLine
	for off := 0; off < len(s); {
		line, next := lineAt(s, off)
		start := off
		if off == 0 && strings.HasPrefix(line, utf8BOM) {
			line, start = line[len(utf8BOM):], len(utf8BOM)
		}
		for _, begin := range []bool{true, false} {
			if key, ok := markerKey(line, begin, cs); ok {
				text := strings.TrimSuffix(line, "\r")
				out = append(out, markerLine{begin: begin, key: key, start: start, textEnd: start + len(text), next: next})
			}
		}
		off = next
	}
	return out
}

// markerName returns the marker text, without its comment delimiters, that
// names key for kind ("begin" or "end"). A key that holds a control character
// takes the quoted form podium:<kind>-q:"<escaped key>": written as it is, a
// line feed in an artifact ID would end the marker's comment line and add the
// rest of the ID to the file, such as a table to .codex/config.toml. Every
// other key is written as it is, so the markers of existing blocks still
// match.
//
// Spec: §6.7 (Encoding)
func markerName(kind, key string) string {
	if strings.ContainsFunc(key, isControl) {
		return "podium:" + kind + "-q:" + strconv.Quote(key)
	}
	return "podium:" + kind + ":" + key
}

// markerKey reports whether line, without its line feed, is a begin marker
// (begin true) or an end marker in the comment style cs, and returns the key
// it names, decoded from the quoted form markerName writes. A carriage return
// ending the line is the line's terminator, and spaces and tabs around the
// marker are indentation and trailing whitespace an editor can add; none of
// them is part of the key. A plain marker that holds a control character, as
// an earlier release wrote one, still names its key, so the quoted block
// replaces it.
func markerKey(line string, begin bool, cs commentStyle) (string, bool) {
	kind := "end"
	if begin {
		kind = "begin"
	}
	text := strings.Trim(strings.TrimSuffix(line, "\r"), " \t")
	body, ok := strings.CutSuffix(text, cs.close)
	if !ok {
		return "", false
	}
	if quoted, ok := strings.CutPrefix(body, cs.open+"podium:"+kind+"-q:"); ok {
		key, err := strconv.Unquote(quoted)
		if err != nil || key == "" {
			return "", false
		}
		return key, true
	}
	key, ok := strings.CutPrefix(body, cs.open+"podium:"+kind+":")
	if !ok || key == "" {
		return "", false
	}
	return key, true
}

// lineAt returns the line of s that starts at off, without its line feed,
// and the offset of the next line, which is the length of s for the last
// line.
func lineAt(s string, off int) (line string, next int) {
	nl := strings.IndexByte(s[off:], '\n')
	if nl < 0 {
		return s[off:], len(s)
	}
	return s[off : off+nl], off + nl + 1
}

// lineEnding returns the terminator of the first line of s: a carriage return
// and line feed when the line ends with both, and a line feed otherwise.
func lineEnding(s string) string {
	if nl := strings.IndexByte(s, '\n'); nl > 0 && s[nl-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// StripPodiumBlocks is the exported form of the inject reconciliation: it
// removes every Podium-managed block from a text inject file (AGENTS.md /
// GEMINI.md / config.toml), selecting the marker style from the path. A block
// keyed by an artifact keep reports stays; a nil keep strips them all.
func StripPodiumBlocks(base []byte, path string, keep func(id string) bool) []byte {
	return stripPodiumBlocks(base, commentStyleFor(path), keep)
}
