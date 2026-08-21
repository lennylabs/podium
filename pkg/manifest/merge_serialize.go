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
// id is the artifact's own canonical ID, which the requester supplied to read
// it. chain holds the artifact's ancestry, parent first and the leaf last.
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets with a
// non-empty value keeps the child's value. And it holds the assembled block to
// §4.6's hidden-parent guarantee, returning ErrUnhidableParent when a restored
// key resolves to a chain parent's reference under its own name or anywhere
// inside its value, when the assembled block still resolves an extends value,
// or when any block involved cannot be read back at all.
//
// Every key §4.6's omitted-field rule makes inheritable reaches the served
// block or the read fails. A key is never silently dropped, because a consumer
// cannot tell an inherited-as-nothing key from one the chain never set.
//
// The disclosure test is a property of the served block and does not depend on
// which member of the chain authored the value. It runs over the restored keys
// rather than over the declared fields, which the typed serialization owns and
// which carry §4.6's merge semantics the restore step cannot reproduce.
//
// It fires on a value that resolves to a parent's reference rather than on one
// that merely mentions the ID, which is what keeps a single authored sentence
// from taking the whole registry down: pkg/sync aborts on the first record a
// resolver refuses, so a test that fired on prose would let one parent's
// sentence stop every artifact from materializing.
//
// The typed serialization stays authoritative for every declared key, because
// only it carries the §4.6 merge semantics: a union for a list field and the
// most-restrictive value for sensitivity are not what a last-writer-wins
// overlay over authored text would produce.
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

	restored, err := undeclaredKeysOf(chain)
	if err != nil {
		return nil, err
	}
	for _, key := range restored {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key.name},
			uncommented(key.value))
	}

	// Spec: §4.6 hidden parents. The parent's existence and ID are not
	// surfaced to the requester, so a restored key that resolves to a chain
	// parent's reference fails the read at whatever nesting depth it sits and
	// whichever member of the chain authored it. The body that follows the
	// block is the leaf's own prose and is out of the block this section
	// constrains.
	if parentsOf(a, id, chain).namesRestored(restored) {
		return nil, ErrUnhidableParent
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

// uncommented clears the YAML comments on n and everything below it, and
// returns n.
//
// Spec: §4.6 hidden parents. A restored node comes straight out of the decoder
// and carries the comments its author wrote, which yaml.Marshal re-emits, so a
// parent's comment would otherwise carry text the requester cannot see into the
// served block under a key whose value the disclosure test cleared. The typed
// serialization already drops the comments on every declared key, so clearing
// them here is also what keeps the two halves of the block consistent.
//
// n is a node the decoder produced, and a decoded node holds no nil child, so
// the walk needs no nil guard.
func uncommented(n *yaml.Node) *yaml.Node {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		uncommented(c)
	}
	return n
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
// A chain member that stored no frontmatter at all holds no key to inherit and
// contributes none. A member that stored a block which does not read back as a
// mapping fails the read instead, because the keys that block holds can be
// neither checked against §4.6's hidden-parent rule nor inherited, and serving
// the merge without them would drop keys §4.6 makes inheritable.
func undeclaredKeysOf(chain []MergedBlock) ([]namedNode, error) {
	seen := map[string]int{}
	out := []namedNode{}
	for _, block := range chain {
		if len(block.Frontmatter) == 0 {
			continue
		}
		m, _, err := mappingOf(block.Frontmatter)
		if err != nil {
			return nil, err
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
	return out, nil
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

// parentIDs holds the canonical ID of every extends reference in a chain, which
// is the set §4.6 hides from a requester who cannot see the layer contributing
// the parent. Each ID records whether it is the requester's own artifact ID,
// which a §4.6 same-ID overlay's reference carries.
type parentIDs map[string]bool

// parentsOf collects the chain's parent IDs, including the reference the merged
// artifact itself carries. That reference is the one a leaf resolving its
// extends through a merge key never spells out at the top level. id is the
// artifact's own canonical ID.
func parentsOf(a *Artifact, id string, chain []MergedBlock) parentIDs {
	own := canonicalID(id)
	p := parentIDs{}
	for _, block := range chain {
		p.add(block.Extends, own)
	}
	p.add(a.Extends, own)
	return p
}

// add records a reference under its canonical ID, so a value naming the parent
// under a pin the extends entry does not carry still matches.
func (p parentIDs) add(ref, own string) {
	if id := canonicalID(ref); id != "" {
		p[id] = id == own
	}
}

// discloses reports whether serving s would surface a parent §4.6 hides.
//
// Spec: §4.6 hidden parents. The guarantee is about the parent's existence and
// ID reaching the requester, and the value that hands over both is one that
// resolves to the parent's reference: the ID itself, with or without a version
// pin, and with or without the leading slashes a path spelling of it carries.
// A value that merely contains the ID, such as a path below the parent
// (shared/parent/CHARTER.md) or a sentence quoting it, is not a reference and
// stays inheritable, which is what keeps §4.6's omitted-field rule working. The
// registry has no way to tell an author's sentence from a disclosure, and
// pkg/sync aborts on the first record a resolver refuses, so a test over prose
// buys nothing and costs every artifact in the registry its materialization.
//
// A same-ID overlay is the exception. §4.6's collision rule lets the
// higher-precedence artifact extend its own canonical ID, and that ID is the one
// the requester supplied to read the artifact, so spelling it surfaces nothing.
// A pin on it is a different matter: a version the requester was not served
// tells them a second row for the ID exists, which is the parent's existence,
// so a pinned reference to it still fails the read.
func (p parentIDs) discloses(s string) bool {
	ref, _, pinned := strings.Cut(strings.TrimSpace(s), "@")
	own, named := p[strings.Trim(ref, "/")]
	if !named {
		return false
	}
	return !own || pinned
}

// namesRestored reports whether any restored key discloses a chain parent,
// under its own name or anywhere inside its value. Spec: §4.6 hidden parents.
// The guarantee is a property of the served block, so which member of the chain
// authored the value does not enter into it.
func (p parentIDs) namesRestored(keys []namedNode) bool {
	for _, key := range keys {
		if p.discloses(key.name) || p.namesAny(key.value) {
			return true
		}
	}
	return false
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
