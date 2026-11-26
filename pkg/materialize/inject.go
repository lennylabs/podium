package materialize

import "strings"

// commentStyle wraps a marker comment for a given file format.
type commentStyle struct{ open, close string }

func commentStyleFor(path string) commentStyle {
	if strings.HasSuffix(path, ".toml") {
		return commentStyle{open: "# ", close: ""}
	}
	// Markdown (AGENTS.md, GEMINI.md, CLAUDE.md) and other text files.
	return commentStyle{open: "<!-- ", close: " -->"}
}

// podiumBlock is one Podium-managed block in a text inject file: a begin
// marker line, the block's lines, and the end marker line naming the same key.
type podiumBlock struct {
	// key is the artifact ID both markers name.
	key string
	// begin is the offset of the begin marker's line.
	begin int
	// markerEnd is the offset just past the end marker's text, before the
	// line's terminator.
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
// with a newline, and the removal could not restore both.
//
// Spec: §6.7
func injectBlock(base []byte, key string, block []byte, cs commentStyle) []byte {
	begin := cs.open + "podium:begin:" + key + cs.close
	end := cs.open + "podium:end:" + key + cs.close
	body := begin + "\n" + strings.TrimRight(string(block), "\n") + "\n" + end
	s := string(base)
	for from := 0; ; {
		blk, ok := nextPodiumBlock(s, from, cs)
		if !ok {
			break
		}
		if blk.key == key {
			return []byte(s[:blk.begin] + body + s[blk.markerEnd:])
		}
		from = blk.end
	}
	var b strings.Builder
	b.WriteString(s)
	if len(s) > 0 {
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
// keep strips every block. What counts as a block is nextPodiumBlock's rule,
// and removalSpan decides which line breaks around a block go with it.
//
// Spec: §6.7
func stripPodiumBlocks(base []byte, cs commentStyle, keep func(id string) bool) []byte {
	s := string(base)
	for from := 0; ; {
		blk, ok := nextPodiumBlock(s, from, cs)
		if !ok {
			return []byte(s)
		}
		if keep != nil && keep(blk.key) {
			from = blk.end
			continue
		}
		start, end := removalSpan(s, blk, cs)
		s = s[:start] + s[end:]
		from = start
	}
}

// removalSpan returns the region a removal of blk takes from s: the block's
// lines with the end marker's line terminator, plus at most one line break
// injectBlock inserted next to the block. The line break before the begin
// marker goes with the block when it forms a blank separator line, or when
// nothing follows the block, where it is the terminator injectBlock added to
// an operator line that had none. Otherwise it ends the operator's line above
// the block and stays, so the operator's lines above and below the block are
// not joined; when the next Podium block follows directly, that block's
// separator goes instead, so the next block takes the removed one's place as
// injectBlock would have written it. A line break is a line feed, or a
// carriage return and line feed when the begin marker's line ends that way,
// which is how a checkout that converts line endings leaves Podium's lines.
//
// Spec: §6.7
func removalSpan(s string, blk podiumBlock, cs commentStyle) (start, end int) {
	eol := lineEnding(s[blk.begin:])
	brk := ""
	if strings.HasSuffix(s[:blk.begin], eol) {
		brk = eol
	} else if blk.begin > 0 {
		// A begin marker starts a line, so a line feed precedes it.
		brk = "\n"
	}
	above := blk.begin - len(brk)
	blankSeparator := brk != "" && (above == 0 || s[above-1] == '\n')
	if blankSeparator || (brk != "" && blk.end == len(s)) {
		return above, blk.end
	}
	if strings.HasPrefix(s[blk.end:], eol) {
		next, ok := nextPodiumBlock(s, blk.end+len(eol), cs)
		if ok && next.begin == blk.end+len(eol) {
			return blk.begin, next.begin
		}
	}
	return blk.begin, blk.end
}

// nextPodiumBlock returns the first Podium block that begins at or after
// from, which is the start of a line. A marker counts only when it is a whole
// line: its comment text, then a line terminator or the end of the file, so
// operator prose that mentions a marker is not one and the TOML marker for a
// key is not read as the marker for a longer key that starts with it. A begin
// marker pairs with the first later end marker that names the same key; one
// that reaches another begin marker for its key, or the end of the file,
// first is unterminated and is left in place, because the operator's content
// would otherwise be taken up to an unrelated marker.
func nextPodiumBlock(s string, from int, cs commentStyle) (podiumBlock, bool) {
	for off := from; off < len(s); {
		line, next := lineAt(s, off)
		if key, ok := markerKey(line, "begin", cs); ok {
			if blk, ok := matchEnd(s, off, next, key, cs); ok {
				return blk, true
			}
		}
		off = next
	}
	return podiumBlock{}, false
}

// matchEnd finds the end marker line for the block keyed by key whose begin
// marker line starts at begin and whose content starts at from. ok is false
// when another begin marker for key, or the end of the file, comes first.
func matchEnd(s string, begin, from int, key string, cs commentStyle) (podiumBlock, bool) {
	for off := from; off < len(s); {
		line, next := lineAt(s, off)
		if k, ok := markerKey(line, "begin", cs); ok && k == key {
			return podiumBlock{}, false
		}
		if k, ok := markerKey(line, "end", cs); ok && k == key {
			text := strings.TrimSuffix(line, "\r")
			return podiumBlock{key: key, begin: begin, markerEnd: off + len(text), end: next}, true
		}
		off = next
	}
	return podiumBlock{}, false
}

// markerKey reports whether line, without its line feed, is a begin or end
// marker (kind) in the comment style cs, and returns the key it names. A
// carriage return ending the line is the line's terminator and is not part of
// the key.
func markerKey(line, kind string, cs commentStyle) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), cs.open+"podium:"+kind+":")
	if !ok {
		return "", false
	}
	key, ok := strings.CutSuffix(rest, cs.close)
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
