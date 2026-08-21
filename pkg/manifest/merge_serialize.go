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

// MergedBlock is one member of an extends chain as SerializeMerged reads it:
// the frontmatter its author wrote, and the extends reference its parsed
// manifest declares.
//
// The reference is carried beside the block rather than re-read out of it,
// because a resolver hands back blocks it has already rewritten. The
// filesystem resolver replaces a record's ArtifactBytes with the merged block
// as soon as it processes that record, and the merged block names no parent,
// so a chain whose middle member happened to be processed first would
// contribute no reference at all. The set of IDs the served block is checked
// against would then depend on slice order, and the filesystem mode would
// serve a block the server mode refuses (§11, §2.2).
type MergedBlock struct {
	// Frontmatter is the member's authored block, with its delimiters.
	Frontmatter []byte
	// Extends is the reference the member's parsed manifest declares, empty
	// for the chain's root.
	Extends string
}

// SerializeMerged renders the merged artifact for an extends child and returns
// the frontmatter block its consumers are served. It is the one implementation
// both extends resolvers call, so the server mode and the filesystem-registry
// mode serve identical bytes for the same artifact (§11, §2.2).
//
// chain holds the artifact's ancestry parent first, the leaf last.
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets with a
// non-empty value keeps the child's value. And it applies the §4.6
// hidden-parent strip to the result, returning ErrUnhidableParent when the
// block resolves an extends value, when it names a chain parent's ID under any
// other key, or when it cannot be read back at all.
//
// The typed serialization stays authoritative for every declared key, because
// only it carries the §4.6 merge semantics: a union for a list field and the
// most-restrictive value for sensitivity are not what a last-writer-wins
// overlay over authored text would produce.
func SerializeMerged(a *Artifact, chain ...MergedBlock) ([]byte, error) {
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

	refs := map[string]bool{}
	for _, block := range chain {
		addParentRef(refs, block.Extends)
	}
	// The merged artifact carries the leaf's reference, which a chain whose
	// leaf resolved it through a merge key never spells out at the top level.
	addParentRef(refs, a.Extends)

	for _, key := range undeclaredKeysOf(chain) {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key.name},
			key.value)
	}

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
// from the chain's authored blocks, parent first, so a later block's non-empty
// value for the same key wins. chain's last member is the leaf.
//
// Spec: §4.6 omitted fields. A child that omits a key, or sets it to an empty
// scalar, inherits the parent's value, which is what MergeExtends applies to
// every declared field. The restored keys follow the same rule, so the two
// halves of a merged block agree on what an empty child value means.
//
// The three skip arms below are not reachable behind either extends resolver.
// Both feed blocks a parser has already accepted: the server mode passes the
// stored frontmatter ingest parsed, and the filesystem mode passes the bytes
// the walk parsed. They are kept so a caller that passes an unparsed block
// contributes no keys rather than panicking on a nil node.
func undeclaredKeysOf(chain []MergedBlock) []namedNode {
	seen := map[string]int{}
	out := []namedNode{}
	for _, block := range chain {
		header, _, err := SplitFrontmatter(block.Frontmatter)
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
			if declaredKeys[name] {
				continue
			}
			// Resolve the value's aliases against the authored block, which
			// is the only document that defines its anchors. The typed
			// serialization re-emits a declared key without the anchor it
			// carried, so a restored alias into one would otherwise strand
			// and cost the child a load it gets today.
			value := resolveAliases(m.Content[i+1], map[*yaml.Node]bool{})
			if prev, ok := seen[name]; ok {
				if isEmptyScalar(value) {
					continue
				}
				out[prev].value = value
				continue
			}
			seen[name] = len(out)
			out = append(out, namedNode{name: name, value: value})
		}
	}
	return out
}

// isEmptyScalar reports whether n carries no value, which covers both spellings
// of an empty frontmatter entry: `x_owner:` decodes as a null scalar and
// `x_owner: ""` as an empty string. A mapping or a list that holds no entries
// is a value the child authored and is not empty in this sense.
func isEmptyScalar(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Value == ""
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
// version pin. §4.6's guarantee covers the parent's ID, so the pin an extends
// entry happens to carry does not decide what the disclosure test looks for.
func parentID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// addParentRef records an extends reference under its canonical ID. namesAny
// looks for that ID as a token, so a value naming the parent under a pin the
// extends entry does not carry still matches.
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
// refs holds the canonical IDs the chain names, and an empty set reduces the
// check to the extends entry alone. SerializeMerged passes the chain's
// references because it copies keys out of blocks other than the served
// record's; FrontmatterHidingParent passes none because it rewrites the
// record's own block.
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

// namesAny reports whether any string within v names one of refs. It is the
// whole §4.6 disclosure test on the assembled block, and it applies to every
// key regardless of which chain member authored it, so one definition of
// naming the parent governs the served bytes.
//
// A string names a reference when it spells the canonical ID as a token, so
// the ID discloses the hidden parent whether it stands alone, carries a pin,
// or sits inside longer text. §4.6's guarantee is that no served byte names
// the parent, and prose quoting the ID discloses the parent as plainly as a
// bare reference does.
func namesAny(v any, refs map[string]bool) bool {
	switch t := v.(type) {
	case string:
		return mentionsRef(t, refs)
	case []any:
		for _, e := range t {
			if namesAny(e, refs) {
				return true
			}
		}
	case map[string]any:
		for k, e := range t {
			if mentionsRef(k, refs) || namesAny(e, refs) {
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

// mentionsRef reports whether s spells one of refs as a whole token.
func mentionsRef(s string, refs map[string]bool) bool {
	for id := range refs {
		if containsToken(s, id) {
			return true
		}
	}
	return false
}

// containsToken reports whether id occurs in s bounded on both sides by a byte
// that cannot continue an identifier. A match inside a longer identifier is not
// a mention, because "shared/parenthetical" and "shared/parent-legacy" name
// something other than "shared/parent". Everything else bounds the token,
// including the pin separator and the path separator, so both
// "see shared/parent@1.x for details" and "docs/shared/parent" name the parent.
func containsToken(s, id string) bool {
	for at := 0; at+len(id) <= len(s); {
		i := strings.Index(s[at:], id)
		if i < 0 {
			return false
		}
		i += at
		before := i == 0 || !identByte(s[i-1])
		after := i+len(id) == len(s) || !identByte(s[i+len(id)])
		if before && after {
			return true
		}
		at = i + 1
	}
	return false
}

// identByte reports whether b can continue an identifier, which is what bounds
// a token in containsToken.
func identByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.':
		return true
	}
	return false
}

// FrontmatterHidingParent returns src's frontmatter block with the extends
// entry removed, or the empty string when the result would still name the
// parent. It rewrites one authored block and merges nothing, which is what the
// search descriptor needs: the descriptor serves the child's own frontmatter
// and the merge has already been applied to the indexed columns.
//
// It shares hidesParent with SerializeMerged and passes no chain references,
// because it merges nothing. The parent-ID test guards the keys the merge
// copies out of chain blocks the requester may not be able to see, and this
// block holds only the child's own authored text. Spec: §4.6.
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
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "extends" {
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
	if !hidesParent(out, nil) {
		return ""
	}
	return "---\n" + strings.TrimRight(string(out), "\n") + "\n---\n"
}
