// `podium artifact scaffold` writes a new artifact directory at the
// given path. Filesystem-only; the command does not talk to the
// registry. The author picks the destination path (the last component
// becomes the artifact name; preceding components form the §4.2
// domain hierarchy) and the artifact type (per spec §4.3); the
// scaffolder produces lint-clean starting files with the universal
// frontmatter populated.
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
	"strings"
)

// firstClassTypes mirrors spec §4.3's enum. Extension types are
// permitted but produce a warning since the scaffolder cannot know
// the extension's type-specific frontmatter shape.
var firstClassTypes = []string{
	"skill", "agent", "context", "command", "rule", "hook", "mcp-server",
}

// nameRe enforces the §4.2 canonical-ID syntax: lowercase
// alphanumeric plus hyphens, no leading hyphen, no underscores.
// Tighter rules for skills (no trailing or consecutive hyphens, ≤64
// chars per the agentskills.io spec) are enforced separately.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// validRuleModes mirrors pkg/manifest.RuleMode values per §4.3.
var validRuleModes = []string{"always", "glob", "auto", "explicit"}

type scaffoldOpts struct {
	// Positional and identifying.
	path string // destination directory; last component is the artifact name
	typ  string

	// Universal frontmatter.
	description string
	tags        string
	sensitivity string
	license     string
	whenToUse   string
	version     string
	extends     string

	// Type-specific.
	inputSchema  string
	outputSchema string
	delegatesTo  string
	ruleMode     string
	ruleGlobs    string
	ruleDesc     string
	hookEvent    string
	hookAction   string
	serverID     string

	// Behavior.
	force          bool
	nonInteractive bool
}

func artifactScaffold(args []string) int {
	return artifactScaffoldWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

// artifactScaffoldWithIO is the testable core. Production callers
// pass os.Stdin/Stdout/Stderr; tests substitute buffers.
func artifactScaffoldWithIO(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("artifact scaffold", flag.ContinueOnError)
	setUsage(fs, "Scaffold a new artifact directory at <path>.")
	opts := &scaffoldOpts{}
	fs.StringVar(&opts.typ, "type", "", "artifact type ("+strings.Join(firstClassTypes, "|")+"); required")
	fs.StringVar(&opts.description, "description", "", "one-line description (required)")
	fs.StringVar(&opts.tags, "tags", "", "comma-separated tags")
	fs.StringVar(&opts.sensitivity, "sensitivity", "low", "sensitivity (low|medium|high)")
	fs.StringVar(&opts.license, "license", "", "SPDX license identifier")
	fs.StringVar(&opts.whenToUse, "when-to-use", "", "comma-separated when_to_use entries")
	fs.StringVar(&opts.version, "version", "0.1.0", "starting semver")
	fs.StringVar(&opts.extends, "extends", "", "parent artifact id to extend")
	// Type-specific.
	fs.StringVar(&opts.inputSchema, "input-schema", "", "type=agent: relative path to input JSON schema")
	fs.StringVar(&opts.outputSchema, "output-schema", "", "type=agent: relative path to output JSON schema")
	fs.StringVar(&opts.delegatesTo, "delegates-to", "", "type=agent: comma-separated delegation targets")
	fs.StringVar(&opts.ruleMode, "rule-mode", "always", "type=rule: one of "+strings.Join(validRuleModes, "|"))
	fs.StringVar(&opts.ruleGlobs, "rule-globs", "", "type=rule: required when --rule-mode=glob")
	fs.StringVar(&opts.ruleDesc, "rule-description", "", "type=rule: required when --rule-mode=auto")
	fs.StringVar(&opts.hookEvent, "hook-event", "", "type=hook: canonical event name (required)")
	fs.StringVar(&opts.hookAction, "hook-action", "", "type=hook: shell snippet body")
	fs.StringVar(&opts.serverID, "server-identifier", "", "type=mcp-server: canonical server id (required)")
	// Behavior.
	fs.BoolVar(&opts.force, "force", false, "overwrite an existing directory")
	fs.BoolVar(&opts.nonInteractive, "yes", false, "fail instead of prompting when a required value is missing")
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(errOut, "error: missing positional <path>")
		fmt.Fprintln(errOut, "usage: podium artifact scaffold --type <type> [flags] <path>")
		return 2
	}
	opts.path = fs.Arg(0)

	reader := bufio.NewReader(in)

	if opts.typ == "" {
		if opts.nonInteractive {
			fmt.Fprintln(errOut, "error: --type required when --yes is set")
			return 2
		}
		opts.typ = promptChoice(reader, out, "Type", firstClassTypes, "")
	}
	if err := validateType(opts.typ, errOut); err != nil {
		return 2
	}

	name := filepath.Base(opts.path)
	if err := validateName(name, opts.typ); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 2
	}

	if opts.description == "" {
		if opts.nonInteractive {
			fmt.Fprintln(errOut, "error: --description required when --yes is set")
			return 2
		}
		opts.description = strings.TrimSpace(promptString(reader, out, "Description (1 sentence)", ""))
	}
	if opts.description == "" {
		fmt.Fprintln(errOut, "error: description is required")
		return 2
	}

	if err := collectTypeSpecific(opts, reader, out, errOut); err != nil {
		return 2
	}

	if err := validateSensitivity(opts.sensitivity); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 2
	}

	if _, statErr := os.Stat(opts.path); statErr == nil && !opts.force {
		fmt.Fprintf(errOut, "error: %s already exists; pass --force to overwrite\n", opts.path)
		return 1
	}
	if err := os.MkdirAll(opts.path, 0o755); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}

	files, err := renderAndWrite(opts.path, name, opts)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "\nScaffolded %s at %s/\n", opts.typ, opts.path)
	for _, f := range files {
		fmt.Fprintf(out, "  %s\n", f)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  1. Edit %s/\n", opts.path)
	fmt.Fprintf(out, "  2. Validate: podium lint --registry %s\n", opts.path)
	return 0
}

// validateType checks the type against the first-class enum and
// warns (but does not reject) extension types. An empty type is an
// error so a missed interactive prompt (closed stdin under --yes,
// or a typo) fails fast instead of writing a malformed manifest.
func validateType(t string, errOut io.Writer) error {
	if t == "" {
		fmt.Fprintln(errOut, "error: --type is required")
		return errors.New("missing type")
	}
	for _, k := range firstClassTypes {
		if t == k {
			return nil
		}
	}
	// Extension types are allowed by spec §4.3 (`<extension type>`).
	// Warn so the author knows the scaffolder cannot supply the
	// extension's type-specific fields.
	fmt.Fprintf(errOut, "warning: %q is not a first-class type; scaffolding generic ARTIFACT.md\n", t)
	return nil
}

// validateName enforces the §4.2 canonical-ID syntax for any
// artifact, plus the tighter agentskills.io constraints for skills
// (≤64 chars, no consecutive or trailing hyphens).
func validateName(name, typ string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name %q must match [a-z0-9][a-z0-9-]* (kebab-case)", name)
	}
	if typ == "skill" {
		if len(name) > 64 {
			return fmt.Errorf("skill name %q exceeds 64 characters (agentskills.io constraint)", name)
		}
		if strings.HasSuffix(name, "-") {
			return fmt.Errorf("skill name %q cannot end with a hyphen", name)
		}
		if strings.Contains(name, "--") {
			return fmt.Errorf("skill name %q cannot contain consecutive hyphens", name)
		}
	}
	return nil
}

func validateSensitivity(s string) error {
	switch s {
	case "low", "medium", "high":
		return nil
	}
	return fmt.Errorf("sensitivity %q must be one of low|medium|high", s)
}

// collectTypeSpecific prompts for or rejects type-specific required
// fields once the type is known.
func collectTypeSpecific(o *scaffoldOpts, r *bufio.Reader, w, errOut io.Writer) error {
	switch o.typ {
	case "rule":
		if !contains(validRuleModes, o.ruleMode) {
			fmt.Fprintf(errOut, "error: --rule-mode %q must be one of %s\n", o.ruleMode, strings.Join(validRuleModes, ", "))
			return errors.New("invalid rule-mode")
		}
		if o.ruleMode == "glob" && o.ruleGlobs == "" {
			if o.nonInteractive {
				fmt.Fprintln(errOut, "error: --rule-globs required when --rule-mode=glob")
				return errors.New("missing rule-globs")
			}
			o.ruleGlobs = promptString(r, w, "Rule globs (e.g. src/**/*.ts)", "")
			if o.ruleGlobs == "" {
				fmt.Fprintln(errOut, "error: rule-globs is required")
				return errors.New("missing rule-globs")
			}
		}
		if o.ruleMode == "auto" && o.ruleDesc == "" {
			if o.nonInteractive {
				fmt.Fprintln(errOut, "error: --rule-description required when --rule-mode=auto")
				return errors.New("missing rule-description")
			}
			o.ruleDesc = promptString(r, w, "Rule description (when to apply)", "")
			if o.ruleDesc == "" {
				fmt.Fprintln(errOut, "error: rule-description is required")
				return errors.New("missing rule-description")
			}
		}
	case "hook":
		if o.hookEvent == "" {
			if o.nonInteractive {
				fmt.Fprintln(errOut, "error: --hook-event required for type=hook")
				return errors.New("missing hook-event")
			}
			o.hookEvent = promptString(r, w, "Hook event (e.g. pre_tool_use)", "")
			if o.hookEvent == "" {
				fmt.Fprintln(errOut, "error: hook-event is required")
				return errors.New("missing hook-event")
			}
		}
	case "mcp-server":
		if o.serverID == "" {
			if o.nonInteractive {
				fmt.Fprintln(errOut, "error: --server-identifier required for type=mcp-server")
				return errors.New("missing server-identifier")
			}
			o.serverID = promptString(r, w, "Server identifier (e.g. npx:@org/my-mcp)", "")
			if o.serverID == "" {
				fmt.Fprintln(errOut, "error: server-identifier is required")
				return errors.New("missing server-identifier")
			}
		}
	}
	return nil
}

// renderAndWrite writes ARTIFACT.md for every type and additionally
// SKILL.md for type=skill. Returns the relative basenames written.
func renderAndWrite(dir, name string, o *scaffoldOpts) ([]string, error) {
	written := []string{}
	if o.typ == "skill" {
		artifact := renderSkillArtifactMD(o)
		skill := renderSkillMD(name, o)
		if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(artifact), 0o644); err != nil {
			return nil, err
		}
		written = append(written, "ARTIFACT.md")
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
			return nil, err
		}
		written = append(written, "SKILL.md")
		return written, nil
	}
	body := renderArtifactMD(name, o)
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		return nil, err
	}
	written = append(written, "ARTIFACT.md")
	return written, nil
}

// renderArtifactMD assembles a non-skill ARTIFACT.md. Universal
// fields plus the type-specific block, then a placeholder body.
func renderArtifactMD(name string, o *scaffoldOpts) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "type: %s\n", o.typ)
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "version: %s\n", o.version)
	fmt.Fprintf(&sb, "description: %s\n", o.description)
	writeWhenToUse(&sb, o.whenToUse)
	writeTags(&sb, o.tags)
	fmt.Fprintf(&sb, "sensitivity: %s\n", o.sensitivity)
	if o.license != "" {
		fmt.Fprintf(&sb, "license: %s\n", o.license)
	}
	if o.extends != "" {
		fmt.Fprintf(&sb, "extends: %s\n", o.extends)
	}
	writeTypeSpecific(&sb, o)
	sb.WriteString("---\n\n")
	sb.WriteString(placeholderBody(o.typ))
	return sb.String()
}

// renderSkillArtifactMD assembles a skill's ARTIFACT.md. Per §4.3.4
// `name`, `description`, and `license` live in SKILL.md; ARTIFACT.md
// carries the Podium-specific frontmatter and an empty body marker.
func renderSkillArtifactMD(o *scaffoldOpts) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("type: skill\n")
	fmt.Fprintf(&sb, "version: %s\n", o.version)
	writeWhenToUse(&sb, o.whenToUse)
	writeTags(&sb, o.tags)
	fmt.Fprintf(&sb, "sensitivity: %s\n", o.sensitivity)
	if o.extends != "" {
		fmt.Fprintf(&sb, "extends: %s\n", o.extends)
	}
	sb.WriteString("---\n\n")
	sb.WriteString("<!-- Skill body lives in SKILL.md. -->\n")
	return sb.String()
}

// renderSkillMD writes SKILL.md per the agentskills.io spec.
func renderSkillMD(name string, o *scaffoldOpts) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", o.description)
	if o.license != "" {
		fmt.Fprintf(&sb, "license: %s\n", o.license)
	}
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", name)
	sb.WriteString("When the user asks for X, do Y.\n\n")
	sb.WriteString("## Behavior\n\nDescribe what the agent should do here, step by step.\n\n")
	sb.WriteString("## Output format\n\n```\n<exact format to render>\n```\n")
	return sb.String()
}

func writeWhenToUse(sb *strings.Builder, raw string) {
	entries := splitCSV(raw)
	if len(entries) == 0 {
		return
	}
	sb.WriteString("when_to_use:\n")
	for _, e := range entries {
		fmt.Fprintf(sb, "  - %q\n", e)
	}
}

func writeTags(sb *strings.Builder, raw string) {
	entries := splitCSV(raw)
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(sb, "tags: [%s]\n", strings.Join(entries, ", "))
}

// writeTypeSpecific emits type-specific frontmatter lines for the
// non-skill types. Skill artifacts use renderSkillArtifactMD and
// have no type-specific fields in ARTIFACT.md.
func writeTypeSpecific(sb *strings.Builder, o *scaffoldOpts) {
	switch o.typ {
	case "agent":
		// input/output are typed as string in pkg/manifest (the
		// spec's `{ $ref: ... }` example is illustrative; the
		// concrete schema is a string path). Emit a plain path so
		// the manifest parses cleanly.
		if o.inputSchema != "" {
			fmt.Fprintf(sb, "input: %s\n", o.inputSchema)
		}
		if o.outputSchema != "" {
			fmt.Fprintf(sb, "output: %s\n", o.outputSchema)
		}
		targets := splitCSV(o.delegatesTo)
		if len(targets) > 0 {
			sb.WriteString("delegates_to:\n")
			for _, t := range targets {
				fmt.Fprintf(sb, "  - %s\n", t)
			}
		}
	case "rule":
		fmt.Fprintf(sb, "rule_mode: %s\n", o.ruleMode)
		if o.ruleGlobs != "" {
			fmt.Fprintf(sb, "rule_globs: %q\n", o.ruleGlobs)
		}
		if o.ruleDesc != "" {
			fmt.Fprintf(sb, "rule_description: %q\n", o.ruleDesc)
		}
	case "hook":
		fmt.Fprintf(sb, "hook_event: %s\n", o.hookEvent)
		if o.hookAction != "" {
			fmt.Fprintf(sb, "hook_action: |\n  %s\n", o.hookAction)
		} else {
			sb.WriteString("hook_action: |\n  # shell snippet; receives event payload on stdin\n  echo \"hook fired\"\n")
		}
	case "mcp-server":
		fmt.Fprintf(sb, "server_identifier: %s\n", o.serverID)
	}
}

// placeholderBody returns a minimal body for non-skill types. Skill
// bodies live in SKILL.md and are produced by renderSkillMD.
func placeholderBody(typ string) string {
	switch typ {
	case "context":
		return "Reference content the agent loads on demand. Replace this paragraph with the actual reference material.\n"
	case "command":
		return "Slash-command body. Describe the command's prompt template here.\n"
	case "agent":
		return "Agent definition body. Describe the agent's role, tools, and behavior here.\n"
	case "rule":
		return "Rule body. Describe the constraint the agent should observe when this rule is loaded.\n"
	case "hook":
		return "Hook body. Document the side effect the hook_action performs.\n"
	case "mcp-server":
		return "MCP server registration body. Document install, transport, and required credentials.\n"
	}
	return "Replace this paragraph with the artifact body.\n"
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

func promptChoice(r *bufio.Reader, w io.Writer, prompt string, choices []string, def string) string {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s:\n", prompt)
	for i, c := range choices {
		fmt.Fprintf(w, "  %d) %s\n", i+1, c)
	}
	for {
		hint := ""
		if def != "" {
			hint = fmt.Sprintf(" [%s]", def)
		}
		fmt.Fprintf(w, "Pick (1-%d)%s: ", len(choices), hint)
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" && def != "" {
			return def
		}
		var idx int
		if _, sErr := fmt.Sscanf(line, "%d", &idx); sErr == nil && idx >= 1 && idx <= len(choices) {
			return choices[idx-1]
		}
		if contains(choices, line) {
			return line
		}
		// On EOF (closed stdin / non-interactive test with no
		// remaining input), exit the loop with an empty string so
		// the caller's required-field check fires instead of
		// looping forever and flooding stdout with "invalid".
		if err != nil {
			return ""
		}
		fmt.Fprintln(w, "  invalid; pick a number or type the value")
	}
}

func splitCSV(raw string) []string {
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

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
