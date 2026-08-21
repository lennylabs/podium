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
// artifactID is the canonical ID of the artifact being served. A chain
// reference to that same ID is a §4.6 layer overlay rather than a hidden
// parent, so it is excluded from the disclosure test below.
//
// It does two things the typed serialization alone cannot. It restores the
// frontmatter keys Artifact does not declare, taking them from the chain's
// authored blocks in parent-first order so a key the child also sets with a
// non-empty value keeps the child's value. And it applies the §4.6
// hidden-parent strip to the result, returning ErrUnhidableParent when the
// block resolves an extends value, when a key restored from a block above the
// leaf references a chain parent, or when the block cannot be read back at all.
//
// The typed serialization stays authoritative for every declared key, because
// only it carries the §4.6 merge semantics: a union for a list field and the
// most-restrictive value for sensitivity are not what a last-writer-wins
// overlay over authored text would produce.
func SerializeMerged(a *Artifact, artifactID string, chain ...MergedBlock) ([]byte, error) {
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

	refs := chainRefs(a, artifactID, chain)
	for _, key := range undeclaredKeysOf(chain) {
		// Spec: §4.6 hidden parents. A key restored from a block above the
		// leaf is text the requester cannot otherwise read, so a value in it
		// that references a chain parent discloses the parent's ID. A key the
		// leaf itself authored discloses nothing the leaf's own record does
		// not already carry, and the search descriptor serves it, so the two
		// surfaces agree on it (§2.2).
		if key.inherited && referencesAny(key.value, refs) {
			return nil, ErrUnhidableParent
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

// namedNode pairs a restored key with the node holding its value, and records
// whether that value came from a block above the leaf.
type namedNode struct {
	name      string
	value     *yaml.Node
	inherited bool
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
	for idx, block := range chain {
		inherited := idx < len(chain)-1
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
				out[prev].inherited = inherited
				continue
			}
			seen[name] = len(out)
			out = append(out, namedNode{name: name, value: value, inherited: inherited})
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

// parentID reduces an artifact reference to its canonical ID by dropping the
// version pin. §4.6's guarantee covers the parent's ID, so the pin an extends
// entry happens to carry does not decide what the disclosure test looks for.
func parentID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// addParentRef records an extends reference under its canonical ID.
// referencesAny compares a value's canonical ID against that set, so a value
// naming the parent under a pin the extends entry does not carry still matches.
func addParentRef(refs map[string]bool, ref string) {
	if id := parentID(ref); id != "" {
		refs[id] = true
	}
}

// chainRefs returns the canonical IDs the chain's extends references name,
// less the ID of the artifact being served.
//
// Spec: §4.6 hidden parents. The guarantee is conditional on the requester
// being unable to see the layer that contributes the parent. Under §4.6 layer
// stacking a higher-precedence layer may extend the same artifact ID in a
// lower layer, and the "parent" is then the ID the requester just asked for,
// so a value naming it discloses nothing the response does not already carry.
func chainRefs(a *Artifact, artifactID string, chain []MergedBlock) map[string]bool {
	refs := map[string]bool{}
	for _, block := range chain {
		addParentRef(refs, block.Extends)
	}
	// The merged artifact carries the leaf's reference, which a chain whose
	// leaf resolved it through a merge key never spells out at the top level.
	addParentRef(refs, a.Extends)
	delete(refs, parentID(artifactID))
	return refs
}

// referencesAny reports whether any scalar within n is a reference to one of
// refs. A scalar references a parent when its canonical ID, which is the value
// with any version pin dropped, is one of the IDs the chain names. The test is
// a reference test rather than a text search: an ordinary sentence or a path
// that happens to contain the ID is the value §4.6's omitted-field rule makes
// inheritable, and refusing it would cost the child the whole load.
func referencesAny(n *yaml.Node, refs map[string]bool) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode && refs[parentID(n.Value)] {
		return true
	}
	for _, c := range n.Content {
		if referencesAny(c, refs) {
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
// because it merges nothing. That test guards the keys the merge copies out of
// chain blocks the requester cannot see, and this block holds only the child's
// own authored text, which the merged load path serves for the same artifact.
// Spec: §4.6.
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
