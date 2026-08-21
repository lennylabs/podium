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
// chain holds the artifact's ancestry, parent first and the leaf last.
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets with a
// non-empty value keeps the child's value. And it holds the restored keys to
// §4.6's hidden-parent guarantee, leaving out a key whose name or value names a
// chain parent and returning ErrUnhidableParent when the assembled block still
// resolves an extends value or cannot be read back at all.
//
// A restored key that names a parent is left out rather than costing the child
// its whole read. The abort is reserved for a block the rewrite cannot produce,
// which is the input class an extends reference carried by a YAML merge key or
// an anchored mapping falls into. A declared key reaches the block through the
// typed serialization, on the same terms as before this helper existed and as
// the search descriptor serves it, so the §4.4 replaced_by pointer of a child
// deprecated in favour of the artifact it extends still loads.
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

	// Not reachable: reading back the block the line above just wrote cannot
	// fail for a valid Artifact. The arm is kept because the alternative to
	// failing closed is serving a block whose contents were never verified.
	root, body, err := mappingOf(typed)
	if err != nil {
		return nil, err
	}

	// Spec: §4.6 hidden parents. The parent's existence and ID are not
	// surfaced to the requester, so a restored key is tested under its own name
	// and at every nesting depth of its value, whichever chain member authored
	// it.
	parents := parentsOf(a, chain)
	for _, key := range undeclaredKeysOf(chain) {
		if parents.discloses(key.name) || parents.namesAny(key.value) {
			continue
		}
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
	// Spec: §4.6 hidden parents. A restored key can carry the extends the
	// parser acted on, through a merge key or an anchored mapping, so re-read
	// the assembled block rather than trusting that deleting the extends entry
	// was enough.
	if !hidesParent(out) {
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

// mappingOf splits a frontmatter block and returns its header as the mapping
// node the header decodes to, together with the body that followed it. A block
// that does not split, does not decode, or decodes to anything other than one
// mapping returns ErrUnhidableParent: it is a block whose contents cannot be
// checked against §4.6, and every caller here fails closed on it.
func mappingOf(src []byte) (*yaml.Node, []byte, error) {
	header, body, err := SplitFrontmatter(src)
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(header, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, ErrUnhidableParent
	}
	return doc.Content[0], body, nil
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
// A block that does not read back as a mapping contributes no keys. Neither
// extends resolver reaches that arm, because both feed blocks a parser has
// already accepted, and a caller that passes an unparsed block gets the typed
// serialization rather than a panic on a nil node.
func undeclaredKeysOf(chain []MergedBlock) []namedNode {
	seen := map[string]int{}
	out := []namedNode{}
	for _, block := range chain {
		m, _, err := mappingOf(block.Frontmatter)
		if err != nil {
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			name := m.Content[i].Value
			if declaredKeys[name] {
				continue
			}
			value := m.Content[i+1]
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

// canonicalID reduces an artifact reference to its canonical ID by dropping the
// version pin. §4.6's guarantee covers the parent's ID, so the pin an extends
// entry happens to carry does not decide what the disclosure test looks for.
func canonicalID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// parentIDs is the canonical ID of every extends reference in a chain, which is
// the set §4.6 hides from a requester who cannot see the layer contributing the
// parent.
type parentIDs map[string]bool

// parentsOf collects the chain's parent IDs, including the reference the merged
// artifact itself carries. That reference is the one a leaf resolving its
// extends through a merge key never spells out at the top level.
func parentsOf(a *Artifact, chain []MergedBlock) parentIDs {
	p := parentIDs{}
	for _, block := range chain {
		p.add(block.Extends)
	}
	p.add(a.Extends)
	return p
}

// add records a reference under its canonical ID, so a value naming the parent
// under a pin the extends entry does not carry still matches.
func (p parentIDs) add(ref string) {
	if id := canonicalID(ref); id != "" {
		p[id] = true
	}
}

// discloses reports whether serving s would surface a parent §4.6 hides.
//
// Spec: §4.6 hidden parents. The guarantee is about the parent's ID reaching
// the requester, and a path below the parent or prose quoting the ID hands over
// that ID together with the evidence that the artifact exists. The test
// therefore fires wherever the ID appears at an identifier boundary, which
// leaves a different artifact whose ID starts or ends with the parent's, such as
// shared/parent-legacy or team/shared/parent, inheritable under §4.6's
// omitted-field rule.
func (p parentIDs) discloses(s string) bool {
	for id := range p {
		if mentions(s, id) {
			return true
		}
	}
	return false
}

// mentions reports whether s names id at an identifier boundary. The occurrence
// starts the string or follows a character that neither continues an identifier
// nor extends a path leftward, and it ends the string or is followed by a
// character that does not continue an identifier, so a version pin, a path
// separator, and surrounding prose all count as boundaries.
//
// id is never empty, because add records a reference only under a non-empty
// canonical ID.
func mentions(s, id string) bool {
	for from := 0; from+len(id) <= len(s); {
		at := strings.Index(s[from:], id)
		if at < 0 {
			return false
		}
		start := from + at
		if boundedLeft(s, start) && boundedRight(s, start+len(id)) {
			return true
		}
		from = start + 1
	}
	return false
}

// boundedLeft reports whether the occurrence starting at i begins an
// identifier. A preceding slash extends the path leftward, which makes the
// occurrence the tail of a longer ID rather than the parent's.
func boundedLeft(s string, i int) bool {
	if i == 0 {
		return true
	}
	return !identifierByte(s[i-1]) && s[i-1] != '/'
}

// boundedRight reports whether the occurrence ending at i ends an identifier.
func boundedRight(s string, i int) bool {
	return i == len(s) || !identifierByte(s[i])
}

// identifierByte reports whether c continues an artifact ID segment. A slash is
// deliberately absent: shared/parent/CHARTER.md names shared/parent.
func identifierByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '_'
	}
}

// namesAny reports whether any scalar reachable from n discloses a parent. It
// walks the whole subtree, so an ID nested in a list or a mapping, or used as a
// key inside a restored value, is found on the same terms as a top-level
// scalar. It does not follow an alias, whose target is either a node this walk
// reaches anyway or one the assembled block cannot resolve, which the read-back
// below catches.
func (p parentIDs) namesAny(n *yaml.Node) bool {
	// Not reachable: the caller passes a node the decoder produced, and a
	// decoded node holds no nil child. The guard is kept because the
	// alternative to returning here is a panic on the §4.6 path.
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode && p.discloses(n.Value) {
		return true
	}
	for _, c := range n.Content {
		if p.namesAny(c) {
			return true
		}
	}
	return false
}

// hidesParent reports whether a frontmatter header reads back as a mapping
// that resolves no extends value. Spec: §4.6 hidden parents.
//
// Deleting the top-level entry is not sufficient on its own. A merge key can
// carry the extends the parser acted on. Decoding into a map resolves merge
// keys and aliases, and rejects a block whose anchors do not resolve, which is
// what an anchored extends value leaves behind once the entry is gone.
func hidesParent(header []byte) bool {
	var resolved map[string]any
	if err := yaml.Unmarshal(header, &resolved); err != nil {
		return false
	}
	_, named := resolved["extends"]
	return !named
}

// FrontmatterHidingParent returns src's frontmatter block with the extends
// entry removed, or the empty string when the result would still name the
// parent. It rewrites one authored block and merges nothing, which is what the
// search descriptor needs: the descriptor serves the child's own frontmatter
// and the merge has already been applied to the indexed columns.
//
// It shares hidesParent with SerializeMerged and applies no parent-ID test,
// because proposal 0009 settled the search descriptor on the node-level strip
// of the record's own block and this repair does not reopen it. Spec: §4.6.
//
// The guard is scoped to what the parser resolves rather than to the literal
// top-level key. ParseArtifact resolves YAML merge keys, so a child can carry
// an operative extends inside an anchored mapping it merges in, and deleting
// an anchored extends value leaves a dangling alias behind. Both are caught by
// re-reading the rewritten block. A value under a key the parser never
// resolves is the child's authored text and survives with every sibling key.
func FrontmatterHidingParent(src []byte) string {
	root, _, err := mappingOf(src)
	if err != nil {
		return ""
	}
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
	if !hidesParent(out) {
		return ""
	}
	return "---\n" + strings.TrimRight(string(out), "\n") + "\n---\n"
}
