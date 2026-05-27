// `podium artifact new` scaffolds a new artifact in a declared layer.
// The command is interactive by default and accepts non-interactive
// flags (`--yes`) for use in CI and scripts.
//
// Layer discovery reads `<root>/.podium/layers.yaml` when that file is
// present. When the file is absent, the command lists top-level
// directories under `<root>` as candidate layers.
//
// The command embeds five starting-body templates: skill, workflow,
// persona, policy, and conversation. Each template prints frontmatter
// plus a body matching the relevant authoring pattern.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// layerEntry mirrors the schema of .podium/layers.yaml.
type layerEntry struct {
	Name           string   `yaml:"name"`
	Description    string   `yaml:"description"`
	Owners         []string `yaml:"owners,omitempty"`
	Sensitivity    string   `yaml:"sensitivity,omitempty"`
	ReviewRequired *bool    `yaml:"review_required,omitempty"`
	Examples       []string `yaml:"examples,omitempty"`
	WhenToUse      string   `yaml:"when_to_use,omitempty"`
}

type layersFile struct {
	Layers []layerEntry `yaml:"layers"`
}

// validTemplates lists the embedded template names. Keep in sync with
// the switch in renderSkillBody().
var validTemplates = []string{"skill", "workflow", "persona", "policy", "conversation"}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func artifactNew(args []string) int {
	return artifactNewWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

// artifactNewWithIO is the testable core. Production callers pass
// os.Stdin/Stdout/Stderr; tests substitute buffers.
func artifactNewWithIO(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("artifact new", flag.ContinueOnError)
	setUsage(fs, "Scaffold a new artifact in a declared layer.")
	root := fs.String("root", ".", "registry root (directory containing layer subdirectories)")
	layer := fs.String("layer", "", "destination layer name")
	name := fs.String("name", "", "artifact name (kebab-case)")
	description := fs.String("description", "", "one-sentence description")
	tags := fs.String("tags", "", "comma-separated tags")
	template := fs.String("template", "", "template ("+strings.Join(validTemplates, "|")+")")
	force := fs.Bool("force", false, "overwrite an existing artifact directory")
	nonInteractive := fs.Bool("yes", false, "fail instead of prompting when a required flag is missing")
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	available, source, err := loadAvailableLayers(*root)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	if len(available) == 0 {
		fmt.Fprintf(errOut, "error: no layers found in %s\n", *root)
		fmt.Fprintf(errOut, "       create %s or any %s/<layer>/ directory first\n",
			filepath.Join(*root, ".podium", "layers.yaml"), *root)
		return 1
	}

	reader := bufio.NewReader(in)

	if *name == "" {
		if *nonInteractive {
			fmt.Fprintln(errOut, "error: --name required when --yes is set")
			return 2
		}
		*name = promptString(reader, out, "Skill name (kebab-case)", "")
	}
	if !skillNameRe.MatchString(*name) {
		fmt.Fprintf(errOut, "error: name %q must be kebab-case [a-z0-9-]+\n", *name)
		return 2
	}

	if *description == "" {
		if *nonInteractive {
			fmt.Fprintln(errOut, "error: --description required when --yes is set")
			return 2
		}
		*description = promptString(reader, out, "Description (1 sentence)", "")
	}
	if *description == "" {
		fmt.Fprintln(errOut, "error: description required")
		return 2
	}

	if *tags == "" {
		if !*nonInteractive {
			*tags = promptString(reader, out, "Tags (comma-separated)", "skill")
		} else {
			*tags = "skill"
		}
	}

	if *layer == "" {
		if *nonInteractive {
			fmt.Fprintln(errOut, "error: --layer required when --yes is set")
			return 2
		}
		*layer = promptLayer(reader, out, available, source)
	}
	if !layerExists(available, *layer) {
		fmt.Fprintf(errOut, "error: layer %q not in catalog (source: %s)\n", *layer, source)
		fmt.Fprintf(errOut, "       available: %s\n", joinLayerNames(available))
		return 2
	}

	if *template == "" {
		if *nonInteractive {
			fmt.Fprintln(errOut, "error: --template required when --yes is set; one of: "+strings.Join(validTemplates, ", "))
			return 2
		}
		*template = promptTemplate(reader, out)
	}
	if !contains(validTemplates, *template) {
		fmt.Fprintf(errOut, "error: template %q not one of: %s\n", *template, strings.Join(validTemplates, ", "))
		return 2
	}

	dir := filepath.Join(*root, *layer, *name)
	if _, statErr := os.Stat(dir); statErr == nil && !*force {
		fmt.Fprintf(errOut, "error: %s already exists; pass --force to overwrite\n", dir)
		return 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}

	artifact := renderArtifactMD(*description, parseTags(*tags))
	skill := renderSkillBody(*template, *name, *description)

	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(artifact), 0o644); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}

	rel, _ := filepath.Rel(*root, dir)
	fmt.Fprintf(out, "\nCreated %s/\n", rel)
	fmt.Fprintf(out, "  ARTIFACT.md\n")
	fmt.Fprintf(out, "  SKILL.md  (template: %s)\n", *template)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  1. Edit %s/SKILL.md\n", rel)
	fmt.Fprintf(out, "  2. Run: podium lint --registry %s\n", *root)
	fmt.Fprintln(out, "  3. Restart your podium-server or rely on layer watch.")

	if requiresReview(available, *layer) {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Note: layer %q is review_required. Get owner approval before merging.\n", *layer)
	}

	return 0
}

// loadAvailableLayers returns the layer catalog and a human-readable
// source string. Preference order:
//
//  1. <root>/.podium/layers.yaml (the committed taxonomy file).
//  2. Fallback: top-level directories under <root>.
func loadAvailableLayers(root string) ([]layerEntry, string, error) {
	yamlPath := filepath.Join(root, ".podium", "layers.yaml")
	if data, err := os.ReadFile(yamlPath); err == nil {
		var lf layersFile
		if err := yaml.Unmarshal(data, &lf); err != nil {
			return nil, yamlPath, fmt.Errorf("parse %s: %w", yamlPath, err)
		}
		return lf.Layers, yamlPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, yamlPath, fmt.Errorf("read %s: %w", yamlPath, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, root, err
	}
	var out []layerEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, layerEntry{
			Name:        e.Name(),
			Description: "(no description; declare in " + filepath.Join(".podium", "layers.yaml") + ")",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, "directory listing (" + root + ")", nil
}

func layerExists(catalog []layerEntry, name string) bool {
	for _, l := range catalog {
		if l.Name == name {
			return true
		}
	}
	return false
}

func requiresReview(catalog []layerEntry, name string) bool {
	for _, l := range catalog {
		if l.Name == name && l.ReviewRequired != nil {
			return *l.ReviewRequired
		}
	}
	return false
}

func joinLayerNames(catalog []layerEntry) string {
	names := make([]string, 0, len(catalog))
	for _, l := range catalog {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

func promptString(r *bufio.Reader, w io.Writer, prompt, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(w, "%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptLayer(r *bufio.Reader, w io.Writer, catalog []layerEntry, source string) string {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Pick a layer (source: %s):\n", source)
	for i, l := range catalog {
		marker := ""
		if l.ReviewRequired != nil && *l.ReviewRequired {
			marker = "  [review required]"
		}
		fmt.Fprintf(w, "  %d) %-14s  %s%s\n", i+1, l.Name, l.Description, marker)
	}
	for {
		fmt.Fprintf(w, "Pick (1-%d): ", len(catalog))
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(catalog) {
			return catalog[idx-1].Name
		}
		for _, l := range catalog {
			if l.Name == line {
				return l.Name
			}
		}
		fmt.Fprintln(w, "  invalid; pick a number or type the layer name")
	}
}

func promptTemplate(r *bufio.Reader, w io.Writer) string {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pick a template:")
	descs := map[string]string{
		"skill":        "plain markdown body with frontmatter",
		"workflow":     "multi-step workflow over MCP tools",
		"persona":      "cross-cutting tone or format; composes with others",
		"policy":       "guardrail or redaction rules",
		"conversation": "pure conversation; no tool calls",
	}
	for i, t := range validTemplates {
		fmt.Fprintf(w, "  %d) %-14s  %s\n", i+1, t, descs[t])
	}
	for {
		fmt.Fprintf(w, "Pick (1-%d): ", len(validTemplates))
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(validTemplates) {
			return validTemplates[idx-1]
		}
		for _, t := range validTemplates {
			if t == line {
				return t
			}
		}
		fmt.Fprintln(w, "  invalid; pick a number or type the template name")
	}
}

func parseTags(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func renderArtifactMD(description string, tags []string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("type: skill\n")
	sb.WriteString("version: 0.1.0\n")
	fmt.Fprintf(&sb, "description: %s\n", description)
	fmt.Fprintf(&sb, "tags: [%s]\n", strings.Join(tags, ", "))
	sb.WriteString("sensitivity: low\n")
	sb.WriteString("license: MIT\n")
	sb.WriteString("---\n")
	sb.WriteString("<!-- skill body lives in SKILL.md -->\n")
	return sb.String()
}

func renderSkillBody(template, name, description string) string {
	header := fmt.Sprintf("---\nname: %s\ndescription: %s\nlicense: MIT\n---\n\n# %s\n\n", name, description, name)
	switch template {
	case "skill":
		return header + tmplSkill
	case "workflow":
		return header + tmplWorkflow
	case "persona":
		return header + tmplPersona
	case "policy":
		return header + tmplPolicy
	case "conversation":
		return header + tmplConversation
	}
	return header
}

// Embedded template bodies. Kept inline so the build does not require
// //go:embed glue for five small strings.

const tmplSkill = `When the user asks for X, do Y.

## Behavior

Describe what the agent should do here, step by step.

## Output format

` + "```" + `
<exact format to render>
` + "```" + `

## Do not

- Bullets of things the agent must avoid.
`

const tmplWorkflow = `When the user asks for <intent>, build the response end to end. Do
not ask follow-up questions unless explicitly instructed.

## Workflow

1. Call ` + "`<mcp_tool_name>`" + ` with arguments:
   ` + "```" + `
   {"key": "value"}
   ` + "```" + `
   This returns <what>.

2. Call ` + "`<another_tool>`" + ` if needed.

3. Combine the results.

## Output format

` + "```" + `
**Header**
- bullet
- bullet
` + "```" + `

Keep the response under <N> lines.
`

const tmplPersona = `When this skill is active (loaded alongside other skills), override
the default response style:

- <rule 1>
- <rule 2>
- <rule 3>

## Examples

Bad: "<example of what to avoid>"

Good: "<example of the desired style>"

## Compose

This skill composes with others. Follow the domain skill's tool
workflow and render its output per these rules.
`

const tmplPolicy = `When active, apply the following rules before responding:

| Pattern | Action |
|---|---|
| <pattern 1> | <redact, warn, or refuse> |
| <pattern 2> | <action> |

## Behavior

1. Call any tools you would normally call.
2. Before responding, scan the tool result for the patterns above.
3. Apply the configured action (redact, refuse, or flag).
4. If you took an action, note it in one line:
   _(N item(s) <action> by this policy)_

## Do not

- Refuse outright when a redacted answer is possible.
- Apply the policy to values it does not target.

## Compose

Cross-cutting. Load alongside any skill the user matched.
`

const tmplConversation = `When the user matches this skill, do not call any tools. This skill
is pure conversation.

## Steps

1. Acknowledge what the user said in one sentence.

2. Ask up to three clarifying questions picked from this list
   (do not ask all):
   - <question 1>
   - <question 2>
   - <question 3>

   Stop and wait for answers.

3. Draft the output in the user's voice and chosen format.

4. Offer to refine: "Want me to adjust X, or are you ready to <next step>?"

## Do not

- Call any MCP tools.
- Invent details the user did not provide.
`

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
