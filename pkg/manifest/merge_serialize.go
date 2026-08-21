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
// id is the canonical ID of the artifact being served, and chain holds its
// ancestry, parent first and the leaf last.
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets with a
// non-empty value keeps the child's value. And it holds the assembled block to
// §4.6's hidden-parent guarantee, returning ErrUnhidableParent when any key or
// value in it stands as a reference to a chain parent, when the block still
// resolves an extends value, or when any block involved cannot be read back at
// all.
//
// Every key §4.6's omitted-field rule makes inheritable reaches the served
// block or the read fails. A key is never silently dropped, because a consumer
// cannot tell an inherited-as-nothing key from one the chain never set.
//
// The typed serialization stays authoritative for the merge semantics of every
// declared key, because only it carries §4.6's merge table: the table there
// appends the parent's delegates_to and external_resources entries onto the
// child's and takes the child's replaced_by. What the table produces is then
// held to the same hidden-parent rule as everything else, so a delegates_to
// entry or an external resource path the child inherits cannot hand the
// requester an ID the merge is what brings down.
//
// The disclosure test runs once over the assembled block, so a declared field
// and a restored key are held to one rule and the origin of a value does not
// decide whether it is checked. §4.6's guarantee is a property of what the
// requester is served, and a leaf-authored literal names the hidden parent to
// that requester on the same terms as an inherited value or one an alias
// produced. That the same text also reaches the requester through
// raw_frontmatter is a separate disclosure recorded elsewhere, and it does not
// license the merged block to repeat it. The materialized bytes have no such
// second route at all: pkg/sync feeds this block into the harness adapters.
//
// The test is bounded to a value that stands as an artifact reference: a scalar
// equal to a chain parent's ID once its version pin and the whitespace and
// slashes around it are removed. A value that merely contains those characters,
// such as a path below the parent or a description quoting it, names no artifact
// the next read resolves and stays inheritable, which is what keeps the
// omitted-field rule working for ordinary extension keys.
func SerializeMerged(a *Artifact, id string, chain ...MergedBlock) ([]byte, error) {
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

	authored, err := chainMappings(chain)
	if err != nil {
		return nil, err
	}
	parents := parentsOf(a, id, chain)
	for _, key := range undeclaredKeysOf(authored) {
		value, err := resolved(key.value)
		if err != nil {
			return nil, err
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key.name}, value)
	}
	// Spec: §4.6 hidden parents. The parent's existence and ID are not
	// surfaced to the requester, so the assembled block fails the read when
	// anything in it references a chain parent, at whatever nesting depth,
	// under whichever key, and whoever authored it.
	if parents.names(root) {
		return nil, ErrUnhidableParent
	}

	out, err := yaml.Marshal(root)
	// Not reachable: every node in root came out of the decoder, and the
	// restored ones have had their aliases expanded, so nothing here is left
	// pointing outside the tree being written.
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

// chainMappings decodes each chain member's authored block into the mapping
// node it holds, in chain order, with a nil entry for a member that stored no
// frontmatter at all. Decoding once here is what lets the restore step and the
// §4.6 disclosure test read the same nodes.
//
// A member that stored a block which does not read back as a mapping fails the
// read, because the keys that block holds can be neither checked against
// §4.6's hidden-parent rule nor inherited, and serving the merge without them
// would drop keys §4.6 makes inheritable.
func chainMappings(chain []MergedBlock) ([]*yaml.Node, error) {
	out := make([]*yaml.Node, len(chain))
	for i, block := range chain {
		if len(block.Frontmatter) == 0 {
			continue
		}
		m, _, err := mappingOf(block.Frontmatter)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

// namedNode pairs a restored key with the node holding its value.
type namedNode struct {
	name  string
	value *yaml.Node
}

// nodeBudget bounds how many nodes one restored value may expand to. Aliases
// compose, so a block of nested aliased sequences expands to a node count that
// grows as a power of the nesting depth, and the read would run for the life of
// the process instead of answering.
//
// The budget is not operator-tunable, for the same reason yaml.v3's own
// alias-expansion limit is not: it is a bound on what an author can make one
// read cost, and it sits far above any frontmatter block a person writes.
// yaml.v3 charges that limit while decoding into a typed value, which is the
// mode neither ParseArtifact nor this file uses on an undeclared key, so the
// bound has to be applied here.
const nodeBudget = 10000

// expansion carries the state of one resolved call: the nodes on the path from
// the root, and the nodes left in the budget.
type expansion struct {
	open   map[*yaml.Node]bool
	budget int
}

// resolved returns a copy of n with every alias below it replaced by the node it
// points at, and with the anchors and the authors' comments cleared.
//
// Spec: §4.6 hidden parents. The restore step drops an ancestor's node when the
// child sets the same key, so an alias into a dropped node would strand and the
// assembled block would stop reading back at all. Expanding first is what keeps
// a parent's anchor that has nothing to do with extends from costing the load.
// Clearing the comments is the same guarantee in another medium: yaml.Marshal
// re-emits an author's comment, so a parent's comment would otherwise carry text
// the requester cannot see into the served block, and the typed serialization
// drops the comments on every declared key.
func resolved(n *yaml.Node) (*yaml.Node, error) {
	e := &expansion{open: map[*yaml.Node]bool{}, budget: nodeBudget}
	return e.expand(n)
}

// expand implements resolved. open holds the nodes on the path from the root,
// so an anchor that contains itself is an error rather than an unbounded walk.
// A decoded node holds no nil child, so the walk needs no nil guard.
func (e *expansion) expand(n *yaml.Node) (*yaml.Node, error) {
	if e.open[n] {
		return nil, ErrUnhidableParent
	}
	e.budget--
	if e.budget < 0 {
		return nil, ErrUnhidableParent
	}
	e.open[n] = true
	defer delete(e.open, n)

	if n.Kind == yaml.AliasNode {
		// Not reachable: the decoder rejects an alias with no anchor to
		// resolve, so a node that reached here has its target. The arm is kept
		// because the alternative to failing closed is a nil dereference on the
		// §4.6 path.
		if n.Alias == nil {
			return nil, ErrUnhidableParent
		}
		return e.expand(n.Alias)
	}

	out := *n
	out.Anchor = ""
	out.HeadComment, out.LineComment, out.FootComment = "", "", ""
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, c := range n.Content {
		child, err := e.expand(c)
		if err != nil {
			return nil, err
		}
		out.Content[i] = child
	}
	return &out, nil
}

// undeclaredKeysOf collects the frontmatter keys Artifact does not declare
// from the chain's decoded blocks, parent first, so a later block's non-empty
// value for the same key wins. The last block is the leaf's.
//
// Spec: §4.6 omitted fields. A child that omits a key, or sets it to an empty
// scalar, inherits the parent's value, and every other field takes the child's
// value, including the extension-type fields a TypeProvider does not declare.
//
// A chain member that stored no frontmatter at all holds no key to inherit and
// contributes none.
func undeclaredKeysOf(authored []*yaml.Node) []namedNode {
	seen := map[string]int{}
	out := []namedNode{}
	for _, m := range authored {
		if m == nil {
			continue
		}
		for j := 0; j+1 < len(m.Content); j += 2 {
			name := m.Content[j].Value
			if declaredKeys[name] {
				continue
			}
			entry := namedNode{name: name, value: m.Content[j+1]}
			if prev, ok := seen[name]; ok {
				if isEmptyValue(entry.value) {
					continue
				}
				out[prev] = entry
				continue
			}
			seen[name] = len(out)
			out = append(out, entry)
		}
	}
	return out
}

// isEmptyValue reports whether n is a value §4.6 treats as an omitted field: an
// empty scalar.
//
// Spec: §4.6 omitted fields. The section names a child that "omits a
// frontmatter field, or sets an empty scalar", and gives every other field the
// child's value, naming the extension-type fields a TypeProvider does not
// declare. A zero-length sequence or mapping is a value the child declared, so
// it wins over the ancestor's and the child can clear an inherited extension
// list. MergeExtends inherits a declared collection whenever the child's is
// zero-length, but that follows from a decoded Go slice not distinguishing an
// authored `[]` from an absent key rather than from §4.6, and the restored keys
// hold yaml.Node values that carry the distinction.
//
// The null tag rather than the raw text decides an empty scalar, because
// yaml.v3 decodes `x_owner:` to an empty value while `x_owner: null` and
// `x_owner: ~` keep their spelling as the node's value. ParseArtifact reduces
// all of them to a declared field's zero value, so testing the tag is what
// keeps the restored keys agreeing with MergeExtends.
//
// n is the value node of a decoded mapping entry, which the decoder never
// leaves nil, so the test needs no nil guard. An alias node holds whatever its
// anchor does and is a value the child set, so it is not empty here.
func isEmptyValue(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Value == "" || n.Tag == "!!null")
}

// canonicalID reduces an artifact reference to its canonical ID by dropping the
// version pin. §4.6's guarantee covers the parent's ID, so the pin an extends
// entry happens to carry does not decide what the disclosure test looks for.
func canonicalID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// parentIDs is the set of canonical IDs the chain's extends references name,
// which is what §4.6 hides from a requester who cannot see the layer
// contributing the parent.
type parentIDs map[string]bool

// parentsOf collects the chain's parent IDs, including the reference the merged
// artifact itself carries. That reference is the one a leaf resolving its
// extends through a merge key never spells out at the top level.
//
// Spec: §4.6 collisions. The same-ID overlay exception gives the parent the
// canonical ID of the artifact the requester asked for, so serving that ID
// discloses neither an ID the requester lacks nor the existence of a second
// row, and it is dropped from the set rather than making an overlay that
// inherits a self-referencing key permanently unloadable.
func parentsOf(a *Artifact, id string, chain []MergedBlock) parentIDs {
	p := parentIDs{}
	for _, block := range chain {
		p.add(block.Extends)
	}
	p.add(a.Extends)
	delete(p, canonicalID(id))
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
// Spec: §4.6 hidden parents. The test is whether the value stands as an
// artifact reference to a chain parent, so s is a disclosure when it equals a
// parent's ID once its version pin and its surrounding whitespace and slashes
// are removed. A value that only contains those characters names a different
// artifact or none at all: shared/parent-legacy and docs/shared/parent.md are
// other IDs, shared/parent/CHARTER.md is a path the registry resolves nothing
// from, and a sentence quoting the ID is prose an extension key carries. All of
// them stay inheritable, which is what keeps §4.6's omitted-field rule working
// for the keys an extension type contributes.
func (p parentIDs) discloses(s string) bool {
	ref, _, _ := strings.Cut(strings.TrimSpace(s), "@")
	return p[strings.Trim(ref, "/")]
}

// names reports whether any node reachable from n discloses a parent. It walks
// the whole subtree, so an ID nested in a list or a mapping, or standing as a
// key, is found on the same terms as a top-level scalar.
//
// The caller passes a node resolved has already copied, which carries no anchor,
// no alias, and no nil child, so the walk tests the values alone.
//
// Spec: §4.6 hidden parents.
func (p parentIDs) names(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && p.discloses(n.Value) {
		return true
	}
	for _, c := range n.Content {
		if p.names(c) {
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
