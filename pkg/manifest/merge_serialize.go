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
// returning ErrUnhidableParent when the block cannot be rewritten into one
// that resolves no extends value.
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

	for _, key := range undeclaredKeysOf(authored) {
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

// namedNode pairs a restored key with the node holding its value.
type namedNode struct {
	name  string
	value *yaml.Node
}

// undeclaredKeysOf collects the frontmatter keys Artifact does not declare
// from the chain's authored blocks, parent first, so a later block's value for
// the same key wins. A block that does not decode contributes nothing, which
// leaves the typed serialization as the whole answer for that record.
func undeclaredKeysOf(authored [][]byte) []namedNode {
	seen := map[string]int{}
	out := []namedNode{}
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
			if declaredKeys[name] || name == "extends" {
				continue
			}
			if at, ok := seen[name]; ok {
				out[at].value = m.Content[i+1]
				continue
			}
			seen[name] = len(out)
			out = append(out, namedNode{name: name, value: m.Content[i+1]})
		}
	}
	return out
}

// hidesParent reports whether a frontmatter header reads back as a mapping
// that resolves no extends value. Spec: §4.6 hidden parents.
//
// Deleting the top-level entry is not sufficient on its own. A merge key can
// carry the extends the parser acted on, and deleting an anchored extends
// value strands the aliases into it, which makes the block undecodable rather
// than parent-free. Decoding into a map both resolves merge keys and rejects
// an unresolvable alias.
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
// It shares hidesParent with SerializeMerged so the load path and the search
// path apply one definition of what hiding the parent means. Spec: §4.6.
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
	if !hidesParent(out) {
		return ""
	}
	return "---\n" + strings.TrimRight(string(out), "\n") + "\n---\n"
}
