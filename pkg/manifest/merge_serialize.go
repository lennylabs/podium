package manifest

import (
	"bytes"
	"errors"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnhidableParent reports a merged frontmatter block that cannot be
// rewritten into one that names no parent. Callers fail the read rather than
// serving the block, because the alternative to failing closed is surfacing
// the hidden parent's ID (§4.6 hidden parents).
var ErrUnhidableParent = errors.New("manifest: merged frontmatter still names the extends parent")

// declaredKeys is the set of frontmatter keys Artifact declares, derived once
// from its yaml tags. A key outside it belongs to an extension type registered
// through the §9 TypeProvider SPI, which §4.6's omitted-field rule makes
// inheritable, and which marshalling the closed struct would silently drop.
var declaredKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(Artifact{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			keys[name] = true
		}
	}
	return keys
}()

// SerializeMerged renders the merged artifact for an extends child and returns
// the frontmatter block its consumers are served. It is the one implementation
// both extends resolvers call, so the server mode and the filesystem-registry
// mode serve identical bytes for the same artifact (§11, §2.2).
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets keeps the
// child's value. And it applies the §4.6 hidden-parent strip to the result,
// returning ErrUnhidableParent when the block resolves an extends value, when
// it carries a chain parent's ID under any other key, or when it cannot be
// read back at all.
//
// The typed serialization stays authoritative for every declared key, because
// only it carries the §4.6 merge semantics: a union for a list field and the
// most-restrictive value for sensitivity are not what a last-writer-wins
// overlay over authored text would produce.
func SerializeMerged(a *Artifact, authored ...[]byte) ([]byte, error) {
	stripped := *a
	stripped.Extends = ""
	typed, err := SerializeArtifact(&stripped)
	// Not reachable: Artifact holds no channel, function, or cyclic field, so
	// marshalling it cannot fail. The arm is kept so a later field that can
	// fail surfaces as an error rather than as a truncated block.
	if err != nil {
		return nil, err
	}

	// The next three failures are all failures to read back what the line
	// above just wrote, so none of them is reachable from a valid Artifact.
	// They are kept because the alternative to failing closed is serving a
	// block whose contents were never verified.
	header, body, err := SplitFrontmatter(typed)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(header, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, ErrUnhidableParent
	}
	root := doc.Content[0]

	restored, refs := undeclaredKeysOf(authored)
	for _, key := range restored {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key.name},
			key.value)
	}
	// The merged artifact carries the leaf's reference, which a chain whose
	// leaf resolved it through a merge key never spells out at the top level.
	addParentRef(refs, a.Extends)

	out, err := yaml.Marshal(root)
	// Not reachable: every node in root came out of the decoder, and an
	// unresolved alias among the restored keys marshals back to its alias
	// form rather than failing here. It is caught by the hidesParent check
	// below, which cannot read the block back.
	if err != nil {
		return nil, err
	}
	// Spec: §4.6 hidden parents. The restored keys are authored text, so one
	// of them can carry the parent's ID under a name of its own, or an alias
	// that resolves to it. Re-read the assembled block rather than trusting
	// that deleting the extends entry was enough.
	if !hidesParent(out, refs) {
		return nil, ErrUnhidableParent
	}

	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(out)
	b.WriteString("---\n")
	if len(body) > 0 {
		b.WriteString("\n")
		b.Write(body)
	}
	return b.Bytes(), nil
}

// namedNode pairs a restored key with the node holding its value.
type namedNode struct {
	name  string
	value *yaml.Node
}

// undeclaredKeysOf collects the frontmatter keys Artifact does not declare
// from the chain's authored blocks, parent first, so a later block's value for
// the same key wins. It also returns the extends references those blocks name,
// which is what the assembled block is checked against under §4.6.
//
// The three skip arms below are not reachable behind either extends resolver.
// Both feed blocks a parser has already accepted: the server mode passes the
// stored frontmatter ingest parsed, and the filesystem mode passes the bytes
// the walk parsed. They are kept so a caller that passes an unparsed block
// contributes no keys rather than panicking on a nil node.
func undeclaredKeysOf(authored [][]byte) ([]namedNode, map[string]bool) {
	seen := map[string]int{}
	out := []namedNode{}
	refs := map[string]bool{}
	for _, raw := range authored {
		header, _, err := SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(header, &doc); err != nil {
			continue
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		m := doc.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			name := m.Content[i].Value
			if name == "extends" {
				// Resolve before recording: a chain member above the leaf can
				// carry its reference through an alias, whose node Value is the
				// anchor name rather than the parent's ID. Recording the anchor
				// name would leave the anchor-defining sibling key free to
				// restore that parent's ID into the served block.
				addParentRef(refs, resolveAliases(m.Content[i+1], map[*yaml.Node]bool{}).Value)
				continue
			}
			if declaredKeys[name] {
				continue
			}
			// Resolve the value's aliases against the authored block, which
			// is the only document that defines its anchors. The typed
			// serialization re-emits a declared key without the anchor it
			// carried, so a restored alias into one would otherwise strand
			// and cost the child a load it gets today.
			value := resolveAliases(m.Content[i+1], map[*yaml.Node]bool{})
			if at, ok := seen[name]; ok {
				out[at].value = value
				continue
			}
			seen[name] = len(out)
			out = append(out, namedNode{name: name, value: value})
		}
	}
	return out, refs
}

// resolveAliases returns a copy of n with every alias replaced by the value it
// points at and every anchor dropped, so the node stands on its own in a
// document that defines neither. A cycle leaves the alias in place, and the
// assembled block then fails the decode in hidesParent, which is the
// fail-closed outcome for frontmatter that cannot be read back.
func resolveAliases(n *yaml.Node, visiting map[*yaml.Node]bool) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil || visiting[n.Alias] {
			return n
		}
		return resolveAliases(n.Alias, visiting)
	}
	cp := *n
	cp.Anchor = ""
	if len(n.Content) == 0 {
		return &cp
	}
	visiting[n] = true
	defer delete(visiting, n)
	cp.Content = make([]*yaml.Node, len(n.Content))
	for i, c := range n.Content {
		cp.Content[i] = resolveAliases(c, visiting)
	}
	return &cp
}

// parentID reduces an artifact reference to its canonical ID by dropping the
// version pin. An authored value can spell a parent's ID with any pin, so both
// sides of the §4.6 comparison are reduced this way before they are matched.
func parentID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// addParentRef records an extends reference under its canonical ID. namesAny
// reduces each candidate string the same way, so a restored key naming the
// parent under a pin the extends entry does not carry still matches.
func addParentRef(refs map[string]bool, ref string) {
	if id := parentID(ref); id != "" {
		refs[id] = true
	}
}

// hidesParent reports whether a frontmatter header reads back as a mapping
// that resolves no extends value and names none of refs. Spec: §4.6 hidden
// parents, whose guarantee covers the parent's existence and its ID, so a
// block that carries the ID under a key of its own discloses as much as one
// that keeps the extends entry.
//
// Deleting the top-level entry is not sufficient on its own. A merge key can
// carry the extends the parser acted on, and a value elsewhere in the block
// can spell the parent's ID out. Decoding into a map resolves merge keys and
// aliases, and rejects a block whose anchors do not resolve.
//
// refs holds the canonical IDs the chain names. Both callers collect it from
// the blocks they are rewriting, so the load path and the search path apply
// one definition of what naming the parent means.
func hidesParent(header []byte, refs map[string]bool) bool {
	var resolved map[string]any
	if err := yaml.Unmarshal(header, &resolved); err != nil {
		return false
	}
	if _, named := resolved["extends"]; named {
		return false
	}
	return !namesAny(resolved, refs)
}

// namesAny reports whether any string within v names one of refs, comparing
// canonical IDs so the pin a value carries does not decide the outcome.
func namesAny(v any, refs map[string]bool) bool {
	switch t := v.(type) {
	case string:
		return scalarNamesAny(t, refs)
	case []any:
		for _, e := range t {
			if namesAny(e, refs) {
				return true
			}
		}
	case map[string]any:
		for k, e := range t {
			if scalarNamesAny(k, refs) || namesAny(e, refs) {
				return true
			}
		}
	case map[any]any:
		for k, e := range t {
			if namesAny(k, refs) || namesAny(e, refs) {
				return true
			}
		}
	}
	return false
}

// scalarNamesAny reports whether s names one of refs. A restored key is copied
// out of a chain block a requester may not be able to see, so the check covers
// a value that embeds the ID as well as one that is the reference: a path under
// the parent's directory and a sentence quoting the reference both disclose the
// parent's ID, which is what §4.6 scopes its guarantee to.
func scalarNamesAny(s string, refs map[string]bool) bool {
	if refs[parentID(s)] {
		return true
	}
	for id := range refs {
		if embedsID(s, id) {
			return true
		}
	}
	return false
}

// embedsID reports whether s contains id at token boundaries, so
// "shared/parent/README.md" and "see shared/parent@1.x" match while
// "shared/parenthetical" does not.
func embedsID(s, id string) bool {
	for at := 0; ; {
		i := strings.Index(s[at:], id)
		if i < 0 {
			return false
		}
		i += at
		before := i == 0 || !isIDRune(rune(s[i-1]))
		end := i + len(id)
		after := end == len(s) || !isIDRune(rune(s[end]))
		if before && after {
			return true
		}
		at = i + 1
	}
}

// isIDRune reports whether c can continue an artifact ID's final path segment.
// A separator such as "/", "@", or a space ends the segment, so an occurrence
// followed by one is the ID rather than a longer word.
func isIDRune(c rune) bool {
	return c == '_' || c == '-' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// FrontmatterHidingParent returns src's frontmatter block with the extends
// entry removed, or the empty string when the result would still name the
// parent. It rewrites one authored block and merges nothing, which is what the
// search descriptor needs: the descriptor serves the child's own frontmatter
// and the merge has already been applied to the indexed columns.
//
// It collects the parent's ID from the entry it removes and shares hidesParent
// with SerializeMerged, so the load path and the search path apply one
// definition of what hiding the parent means: a sibling key that spells the
// parent's ID out fails the descriptor closed exactly as it fails the load.
// Spec: §4.6.
//
// The guard is scoped to what the parser resolves rather than to the literal
// top-level key. ParseArtifact resolves YAML merge keys, so a child can carry
// an operative extends inside an anchored mapping it merges in, and deleting
// an anchored extends value leaves a dangling alias behind. Both are caught by
// re-reading the rewritten block. A value under a key the parser never
// resolves is the child's authored text and survives with every sibling key.
func FrontmatterHidingParent(src []byte) string {
	fm, _, err := SplitFrontmatter(src)
	if err != nil {
		return ""
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return ""
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return ""
	}
	root := doc.Content[0]
	kept := make([]*yaml.Node, 0, len(root.Content))
	refs := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "extends" {
			addParentRef(refs, resolveAliases(root.Content[i+1], map[*yaml.Node]bool{}).Value)
			continue
		}
		kept = append(kept, root.Content[i], root.Content[i+1])
	}
	root.Content = kept
	out, err := yaml.Marshal(root)
	if err != nil {
		// Not reachable from a node the decoder just produced; kept because
		// the alternative to failing closed here is emitting the parent.
		return ""
	}
	if !hidesParent(out, refs) {
		return ""
	}
	return "---\n" + strings.TrimRight(string(out), "\n") + "\n---\n"
}
