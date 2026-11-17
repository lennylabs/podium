package adapter

import (
	"bytes"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// This file holds the §6.7 user-only skill controls. A skill whose resolved
// §4.3.4 invocation is user is never written where the harness would load it
// by itself: a harness with a documented user-only control gets that control
// (a SKILL.md frontmatter key, or Codex's agents/openai.yaml policy), and a
// harness without one gets its native command file instead. skillMarkdown is
// the one writer of a materialized SKILL.md, so every adapter and emitter that
// writes one applies the compatibility derivation and the control the same
// way.

// userOnlyControl names the harness control a skill writer adds to a
// user-invoked skill.
type userOnlyControl int

const (
	// controlNone adds no control: the harness has none, or it reaches the
	// skill through a command-file fallback instead.
	controlNone userOnlyControl = iota
	// controlFrontmatter writes the top-level SKILL.md key
	// `disable-model-invocation: true` (claude-code, cursor, and pi).
	controlFrontmatter
	// controlOpenAIYAML writes agents/openai.yaml beside SKILL.md with
	// policy.allow_implicit_invocation set to false (codex).
	controlOpenAIYAML
)

// userOnlyKey is the SKILL.md frontmatter key controlFrontmatter writes.
const userOnlyKey = "disable-model-invocation"

// openAIYAMLPath is the skill-relative path of Codex's per-skill policy file.
const openAIYAMLPath = "agents/openai.yaml"

// implicitInvocationKey is the policy key the Codex control sets to false.
const implicitInvocationKey = "allow_implicit_invocation"

// minimalOpenAIYAML is the policy file written when the skill bundles no
// agents/openai.yaml, or bundles one the adapter cannot merge into.
const minimalOpenAIYAML = "policy:\n  " + implicitInvocationKey + ": false\n"

// userInvoked reports whether src is a skill whose resolved invocation is
// user. manifest.ResolveArtifact resolves the skill from both manifests, so a
// metadata-only value counts, and falls back to ARTIFACT.md alone when
// SKILL.md does not resolve, the same resolution admission and the §6.9 guard
// sites use, so the adapter, UsedCapabilities, and the guard read one value.
// A failure to resolve either file yields the any default.
//
// Spec: §4.3.4
func userInvoked(src Source) bool {
	a, err := manifest.ResolveArtifact(src.ArtifactBytes, src.SkillBytes)
	return err == nil && a.UserInvoked()
}

// skillMarkdown renders the SKILL.md a skill writer materializes: the
// authored bytes with compatibility derived per §4.3.4, then, for
// controlFrontmatter on a user-invoked skill, the user-only control. The
// control lands as the first frontmatter line, ahead of a derived
// compatibility line. An authored control that already holds the YAML
// boolean true is kept byte for byte, and one holding any other value is
// replaced. A skill whose invocation is any keeps an authored control
// unchanged.
//
// Spec: §4.3.4, §6.7, §7.8
func skillMarkdown(src Source, ctl userOnlyControl) []byte {
	b := deriveSkillCompatibility(src.SkillBytes, src.ArtifactBytes)
	if ctl != controlFrontmatter || !userInvoked(src) {
		return b
	}
	return withUserOnlyFrontmatter(b)
}

// withUserOnlyFrontmatter makes `disable-model-invocation: true` the single
// top-level occurrence of the key in b's frontmatter. A frontmatter that does
// not decode to a mapping takes the injected line anyway, because the skill
// is user-invoked and a lenient harness reader still sees the control.
func withUserOnlyFrontmatter(b []byte) []byte {
	fm, _, err := manifest.SplitFrontmatter(b)
	if err != nil {
		return b
	}
	var doc yaml.Node
	if yaml.Unmarshal(fm, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return injectFrontmatterLine(b, userOnlyKey, "true")
	}
	root := doc.Content[0]
	var keys []int
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == userOnlyKey {
			keys = append(keys, i)
		}
	}
	switch {
	case len(keys) == 0:
		return injectFrontmatterLine(b, userOnlyKey, "true")
	case len(keys) == 1 && isYAMLTrue(root.Content[keys[0]+1]):
		return b
	case blockKeyLines(root, keys):
		return replaceUserOnlyLines(b, fm, root, keys)
	}
	return reencodeUserOnly(b, &doc, keys)
}

// isYAMLTrue reports whether v is the YAML boolean true (after alias
// resolution). A quoted "true" is a string and does not count.
func isYAMLTrue(v *yaml.Node) bool {
	if v.Kind == yaml.AliasNode && v.Alias != nil {
		v = v.Alias
	}
	return v.Kind == yaml.ScalarNode && v.ShortTag() == "!!bool" && strings.EqualFold(v.Value, "true")
}

// blockKeyLines reports whether every key at the given root.Content indices
// starts its own column-one line in a block mapping, which is the layout the
// line replacement can edit without touching another key.
func blockKeyLines(root *yaml.Node, keys []int) bool {
	if root.Style&yaml.FlowStyle != 0 {
		return false
	}
	for _, i := range keys {
		if root.Content[i].Column != 1 {
			return false
		}
		if i+2 < len(root.Content) && root.Content[i+2].Line <= root.Content[i].Line {
			return false
		}
	}
	return true
}

// replaceUserOnlyLines rewrites the lines each occurrence of the key spans:
// the first occurrence becomes `disable-model-invocation: true` and later
// occurrences are removed. An occurrence spans from its key line to the line
// before the next top-level key, less trailing blank and comment lines, so
// every other frontmatter line stays byte-identical. Frontmatter line n is
// line n of b, because the opening delimiter is line 0.
func replaceUserOnlyLines(b, fm []byte, root *yaml.Node, keys []int) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	closing := bytes.Count(fm, []byte("\n")) + 2 // index of the closing "---"
	for k := len(keys) - 1; k >= 0; k-- {
		i := keys[k]
		start := root.Content[i].Line
		end := closing
		if i+2 < len(root.Content) {
			end = root.Content[i+2].Line
		}
		for end-1 > start && isBlankOrComment(lines[end-1]) {
			end--
		}
		var repl []string
		if k == 0 {
			eol := "\n"
			if strings.HasSuffix(lines[start], "\r\n") {
				eol = "\r\n"
			}
			repl = []string{userOnlyKey + ": true" + eol}
		}
		lines = append(lines[:start], append(repl, lines[end:]...)...)
	}
	return []byte(strings.Join(lines, ""))
}

// isBlankOrComment reports whether line is empty or a column-one comment.
func isBlankOrComment(line string) bool {
	t := strings.TrimRight(line, "\r\n")
	return strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#")
}

// reencodeUserOnly handles a frontmatter whose keys share a line, such as a
// flow mapping, which the line replacement cannot edit. It sets the first
// occurrence to the boolean true, drops later ones, and re-encodes the
// frontmatter. The re-encoding normalizes that frontmatter's layout.
func reencodeUserOnly(b []byte, doc *yaml.Node, keys []int) []byte {
	root := doc.Content[0]
	for k := len(keys) - 1; k > 0; k-- {
		i := keys[k]
		root.Content = append(root.Content[:i], root.Content[i+2:]...)
	}
	root.Content[keys[0]+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	out, err := yaml.Marshal(doc)
	if err != nil {
		// Unreachable for a node tree yaml.v3 just decoded; kept so a future
		// encoder failure leaves the authored bytes rather than an empty file.
		return b
	}
	_, body, _ := manifest.SplitFrontmatter(b)
	return append([]byte("---\n"+string(out)+"---\n\n"), body...)
}

// openAIYAMLFile returns the Codex agents/openai.yaml for a user-invoked
// skill materialized at dir. It never fails: an authored file that strictly
// decodes to a non-empty mapping keeps its other keys and gains
// policy.allow_implicit_invocation: false, and every other authored input,
// or none, yields the minimal policy file. An error here would fail the whole
// sync or publish run over one bundled file ingest never checks.
//
// Spec: §6.7
func openAIYAMLFile(dir string, src Source) File {
	return File{Path: path.Join(dir, openAIYAMLPath), Content: openAIYAMLPolicy(src.Resources[openAIYAMLPath])}
}

// openAIYAMLPolicy merges the user-only policy into an authored
// agents/openai.yaml. yaml.v3 decodes only the first document of a stream,
// resolves aliases and merge keys, and rejects a repeated mapping key when it
// decodes into a map, which is the strict decode §6.7 names. Comments and key
// order are not preserved.
func openAIYAMLPolicy(authored []byte) []byte {
	var doc map[string]any
	if yaml.Unmarshal(authored, &doc) != nil || len(doc) == 0 {
		return []byte(minimalOpenAIYAML)
	}
	doc["policy"] = withImplicitInvocationOff(doc["policy"])
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if enc.Encode(doc) != nil || enc.Close() != nil {
		// Unreachable for values yaml.v3 just decoded; the minimal file keeps
		// the never-fails contract if the encoder ever refuses one.
		return []byte(minimalOpenAIYAML)
	}
	return buf.Bytes()
}

// withImplicitInvocationOff returns a copy of policy with
// allow_implicit_invocation set to false. The copy matters because an
// aliased policy shares its map with the anchored node, which keeps its
// authored value. A policy that is not a mapping becomes a mapping holding
// only that key.
func withImplicitInvocationOff(policy any) any {
	switch m := policy.(type) {
	case map[string]any:
		out := make(map[string]any, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		out[implicitInvocationKey] = false
		return out
	case map[any]any:
		out := make(map[any]any, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		out[implicitInvocationKey] = false
		return out
	}
	return map[string]any{implicitInvocationKey: false}
}

// withoutResource returns res without the entry at rel, leaving res itself
// unmodified because the Source map belongs to the caller.
func withoutResource(res map[string][]byte, rel string) map[string][]byte {
	if _, ok := res[rel]; !ok {
		return res
	}
	out := make(map[string][]byte, len(res)-1)
	for k, v := range res {
		if k != rel {
			out[k] = v
		}
	}
	return out
}

// geminiSkillCommandTOML renders a user-invoked skill as a Gemini CLI TOML
// command: the SKILL.md description and the SKILL.md body. A skill's
// ARTIFACT.md holds neither. A SKILL.md that does not parse is written whole
// as the prompt, so the command still carries the author's text.
//
// Spec: §6.7, §7.8
func geminiSkillCommandTOML(src Source) []byte {
	s, err := manifest.ParseSkill(src.SkillBytes)
	if err != nil {
		return geminiCommandTOML("", string(src.SkillBytes))
	}
	return geminiCommandTOML(s.Description, s.Body)
}
