package materialize

import (
	"math/rand"
	"strings"
	"testing"
)

// propertyPieces are the fragments the property test builds operator files
// from: line endings of every kind, comment openers and closers, a code fence,
// and inline mentions of Podium's markers.
var propertyPieces = []string{
	"a", "b", "line", " ", "\n", "\n", "\r\n", "\r", "\t", "x = 1", "#",
	"<!--", "-->", "```\n", "podium", "\n\n",
	"see <!-- podium:begin:team/a --> here", "x = 1 # podium:end:team/a",
}

// propertyBodies are the artifact bodies the property test injects.
var propertyBodies = []string{"", "Rule.", "Rule.\n", "Rule.\n\n", "x\r\ny", "\n", "multi\nline\n"}

// propertyOperatorFile returns a random operator file. ok is false when one of
// its lines is itself a Podium marker, which the operator does not write.
func propertyOperatorFile(r *rand.Rand, cs commentStyle) (string, bool) {
	var b strings.Builder
	for n := r.Intn(12); n > 0; n-- {
		b.WriteString(propertyPieces[r.Intn(len(propertyPieces))])
	}
	s := b.String()
	for off := 0; off < len(s); {
		line, next := lineAt(s, off)
		if _, ok := markerKey(line, "begin", cs); ok {
			return "", false
		}
		if _, ok := markerKey(line, "end", cs); ok {
			return "", false
		}
		off = next
	}
	return s, true
}

// legacyInjectBlock is injectBlock as releases before this change appended a
// block: two line feeds after a file without a final newline, one after a file
// with one.
func legacyInjectBlock(base []byte, key string, block []byte, cs commentStyle) []byte {
	s := string(base)
	body := cs.open + "podium:begin:" + key + cs.close + "\n" + strings.TrimRight(string(block), "\n") + "\n" + cs.open + "podium:end:" + key + cs.close
	if len(s) > 0 {
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += "\n"
	}
	return []byte(s + body + "\n")
}

// injectEach injects bodies[i] under keys[i] into base in order.
func injectEach(base string, keys, bodies []string, cs commentStyle, inject func([]byte, string, []byte, commentStyle) []byte) string {
	out := []byte(base)
	for i, k := range keys {
		out = inject(out, k, []byte(bodies[i]), cs)
	}
	return string(out)
}

// keepSet returns a keep predicate that reports exactly ids.
func keepSet(ids ...string) func(string) bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return func(id string) bool { return m[id] }
}

// toCRLF converts every bare line feed to a carriage return and line feed, as
// a checkout that converts line endings does.
func toCRLF(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// injectProperties checks the add-and-remove properties for one operator
// file u, one set of keys and bodies, and the subset of keys kept.
func injectProperties(t *testing.T, cs commentStyle, u string, keys, bodies []string, mask int) {
	t.Helper()
	full := injectEach(u, keys, bodies, cs, injectBlock)
	// Removing every block restores the operator's file.
	if got := string(stripPodiumBlocks([]byte(full), cs, nil)); got != u {
		t.Fatalf("round trip: u=%q full=%q got=%q", u, full, got)
	}
	// A resync rebuilds the same bytes.
	if again := injectEach(string(stripPodiumBlocks([]byte(full), cs, nil)), keys, bodies, cs, injectBlock); again != full {
		t.Fatalf("resync: u=%q full=%q again=%q", u, full, again)
	}
	// Removing some blocks leaves what injecting only the rest writes.
	var kept, keptBodies []string
	for i, k := range keys {
		if mask&(1<<i) != 0 {
			kept, keptBodies = append(kept, k), append(keptBodies, bodies[i])
		}
	}
	want := injectEach(u, kept, keptBodies, cs, injectBlock)
	if got := string(stripPodiumBlocks([]byte(full), cs, keepSet(kept...))); got != want {
		t.Fatalf("partial: u=%q kept=%v got=%q want=%q", u, kept, got, want)
	}
	// A checkout that converts the whole file to CRLF round-trips too. A
	// carriage return ending the operator's file merges with the line feed
	// Podium appends after it, so such a file has no CRLF form to compare.
	if !strings.HasSuffix(u, "\r") {
		if got := string(stripPodiumBlocks([]byte(toCRLF(full)), cs, nil)); got != toCRLF(u) {
			t.Fatalf("crlf round trip: u=%q got=%q", toCRLF(u), got)
		}
		if got := string(stripPodiumBlocks([]byte(toCRLF(full)), cs, keepSet(kept...))); got != toCRLF(want) {
			t.Fatalf("crlf partial: u=%q kept=%v got=%q want=%q", toCRLF(u), kept, got, toCRLF(want))
		}
	}
	// A file an earlier release wrote loses its blocks and keeps the
	// operator's bytes, plus a final newline when the file had none, and a
	// resync is stable after the first one.
	legacy := injectEach(u, keys, bodies, cs, legacyInjectBlock)
	lw := u
	if u != "" && !strings.HasSuffix(u, "\n") {
		lw = u + "\n"
	}
	if got := string(stripPodiumBlocks([]byte(legacy), cs, nil)); got != lw {
		t.Fatalf("legacy strip: u=%q got=%q want=%q", u, got, lw)
	}
	s1 := injectEach(string(stripPodiumBlocks([]byte(legacy), cs, nil)), keys, bodies, cs, injectBlock)
	if s2 := injectEach(string(stripPodiumBlocks([]byte(s1), cs, nil)), keys, bodies, cs, injectBlock); s1 != s2 {
		t.Fatalf("legacy resync: u=%q s1=%q s2=%q", u, s1, s2)
	}
}

// Spec: §6.7, §13.11.3 — over random operator files in both marker styles,
// adding blocks and removing them restores the operator's bytes, removing
// some leaves what adding only the rest writes, a resync is stable, a CRLF
// checkout round-trips, and operator text an edit adds below the blocks
// survives their removal.
func TestInjectStrip_Properties(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewSource(1))
	allKeys := []string{"team/a", "team/b", "team/c"}
	for iter := 0; iter < 50000; iter++ {
		cs := commentStyleFor([]string{"GEMINI.md", ".codex/config.toml"}[r.Intn(2)])
		u, ok := propertyOperatorFile(r, cs)
		if !ok {
			continue
		}
		keys := allKeys[:1+r.Intn(len(allKeys))]
		bodies := make([]string, len(keys))
		for i := range bodies {
			bodies[i] = propertyBodies[r.Intn(len(propertyBodies))]
		}
		injectProperties(t, cs, u, keys, bodies, r.Intn(1<<len(keys)))
		tail, ok := propertyOperatorFile(r, cs)
		if !ok {
			continue
		}
		// The line feed that ended the operator's last line stays when text
		// follows the blocks, so the lines above and below are not joined.
		want := u + tail
		if u != "" && !strings.HasSuffix(u, "\n") && tail != "" {
			want = u + "\n" + tail
		}
		full := injectEach(u, keys, bodies, cs, injectBlock)
		if got := string(stripPodiumBlocks([]byte(full+tail), cs, nil)); got != want {
			t.Fatalf("operator text below the blocks: u=%q tail=%q got=%q want=%q", u, tail, got, want)
		}
	}
}
