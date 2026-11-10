package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// importCmd normalizes a directory of skills into a Podium filesystem layer
// of plain Agent Skills directories. Each immediate subdirectory of --source
// is one skill whose directory name is the artifact ID. The output for a
// skill is its SKILL.md, with metadata.version set to --version, plus every
// other file copied verbatim as a bundled resource. The command never writes
// ARTIFACT.md, because §4.3.4 makes a skill's ARTIFACT.md an optional overlay
// the author writes by hand, and it never modifies the source.
//
// Usage:
//
//	podium import --source <dir> --target <dir> [--version 1.0.0] [--dry-run]
//
// Every refusal happens while planning, before any write and also under
// --dry-run, so a refused import leaves the target untouched.
//
// Spec: §4.3.4
func importCmd(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	setUsage(fs, "Normalize a directory of skills into a layer of plain skill directories.")
	source := fs.String("source", "", "directory of skill subdirectories (required)")
	target := fs.String("target", "", "destination layer directory (required)")
	version := fs.String("version", "1.0.0", "major.minor.patch version written to each SKILL.md metadata.version")
	dryRun := fs.Bool("dry-run", false, "report the plan; write nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *source == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "error: --source and --target are required")
		return 2
	}
	// ValidateVersion accepts major.minor.patch only, so it also refuses the
	// registry-reserved 0.0.0-auto.<N> form an author cannot write (§4.7.6).
	if err := manifest.ValidateVersion(*version); err != nil {
		fmt.Fprintf(os.Stderr, "error: --version: %v (want major.minor.patch)\n", err)
		return 2
	}
	plan, err := buildImportPlan(*source, *version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(plan) == 0 {
		fmt.Fprintln(os.Stderr, "error: source directory contained no skills")
		return 1
	}
	if *dryRun {
		fmt.Printf("would write %d artifact(s) to %s:\n", len(plan), *target)
		for _, p := range plan {
			fmt.Printf("  %s/\n", p.id)
		}
		return 0
	}
	if err := os.MkdirAll(*target, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, p := range plan {
		if err := writeImportedSkill(*target, p); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", p.id, err)
			return 1
		}
		fmt.Printf("wrote %s/\n", filepath.Join(*target, p.id))
	}
	return 0
}

// importedSkill is one planned output directory: the normalized SKILL.md
// bytes and the bundled resources keyed by slash-separated relative path.
type importedSkill struct {
	id        string
	skill     []byte
	resources map[string][]byte
}

// buildImportPlan plans one skill per immediate subdirectory of source. A
// subdirectory with no root-level .md file yields no body and is skipped, so
// a skill nested deeper is not imported and a Git clone's .git directory is
// ignored. Every refusal names the directory or file it concerns.
func buildImportPlan(source, version string) ([]importedSkill, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	out := []importedSkill{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(source, e.Name())
		bodyName, body, resources, err := collectSkillFiles(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		if body == nil {
			continue
		}
		skill, err := setSkillMetadataVersion(body, version)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, bodyName), err)
		}
		out = append(out, importedSkill{id: e.Name(), skill: skill, resources: resources})
	}
	return out, nil
}

// collectSkillFiles reads one source skill directory. The body is the
// root-level SKILL.md, or, when there is none, the single root-level .md
// file. Every other file, other .md files and a SKILL.md in a subdirectory
// included, is a bundled resource copied verbatim, so a nested SKILL.md stays
// a resource of this skill. It returns a nil body when the root holds no .md
// file.
//
// Spec: §4.3.4, §4.4
func collectSkillFiles(dir string) (bodyName string, body []byte, resources map[string][]byte, err error) {
	bodyName, err = chooseSkillBody(dir)
	if err != nil || bodyName == "" {
		return "", nil, nil, err
	}
	resources = map[string][]byte{}
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == bodyName {
			body = data
			return nil
		}
		resources[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return "", nil, nil, err
	}
	return bodyName, body, resources, nil
}

// chooseSkillBody returns the name of the root-level file that becomes the
// skill's SKILL.md, or "" when the root holds no .md file. It refuses a root
// ARTIFACT.md, because import never writes one and copying it would carry an
// overlay into the output that the operator did not write for this layer. It
// refuses several root .md files with no SKILL.md, because no rule picks one
// of them as the body.
func chooseSkillBody(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var mds []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		mds = append(mds, e.Name())
	}
	switch {
	case slices.Contains(mds, "ARTIFACT.md"):
		return "", errors.New("holds an ARTIFACT.md; import writes plain skill directories and never copies an ARTIFACT.md")
	case slices.Contains(mds, "SKILL.md"):
		return "SKILL.md", nil
	case len(mds) > 1:
		return "", fmt.Errorf("holds several .md files (%s) and no SKILL.md; rename the skill's file to SKILL.md", strings.Join(mds, ", "))
	case len(mds) == 1:
		return mds[0], nil
	}
	return "", nil
}

// setSkillMetadataVersion returns src with metadata.version set to version.
// A src whose metadata.version already equals version is returned unchanged,
// byte for byte. Otherwise only the frontmatter header is re-marshaled, and
// every byte from the closing delimiter on is appended unchanged, so the body
// keeps its comments and blank lines. It refuses a src with no frontmatter
// block, a metadata that is not a mapping, a metadata.version that is not a
// string, and a metadata.version that differs from version.
//
// Spec: §4.3.4, §4.7.6
func setSkillMetadataVersion(src []byte, version string) ([]byte, error) {
	header, body, err := manifest.SplitFrontmatter(src)
	if err != nil {
		return nil, fmt.Errorf("has no frontmatter block (%w)", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(header, &doc); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	root, err := headerMapping(&doc)
	if err != nil {
		return nil, err
	}
	meta, err := metadataMapping(root)
	if err != nil {
		return nil, err
	}
	current, found, err := mappingString(meta, "version")
	if err != nil {
		return nil, err
	}
	if found {
		if current != version {
			return nil, fmt.Errorf("metadata.version %q differs from --version %q", current, version)
		}
		return src, nil
	}
	meta.Content = append(meta.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: version, Style: yaml.DoubleQuotedStyle},
	)
	encoded, err := encodeHeader(&doc)
	if err != nil {
		return nil, err
	}
	out := append([]byte("---\n"), encoded...)
	out = append(out, "---"...)
	return append(out, src[closingDelimiterEnd(src, body):]...), nil
}

// headerMapping returns the top-level mapping of a decoded frontmatter
// header. An empty or comment-only header gains an empty mapping, so a skill
// whose frontmatter declares nothing still receives metadata.version.
func headerMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter is not a mapping")
	}
	return root, nil
}

// metadataMapping returns the metadata mapping of root, appending an empty one
// when the key is absent.
func metadataMapping(root *yaml.Node) (*yaml.Node, error) {
	if v := mappingValue(root, "metadata"); v != nil {
		if v.Kind != yaml.MappingNode {
			return nil, errors.New("metadata is not a mapping")
		}
		return v, nil
	}
	meta := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "metadata"},
		meta,
	)
	return meta, nil
}

// mappingString returns the string value of key in m and whether the key is
// present. A present value that is not a string scalar, such as the integer
// in `version: 1`, is an error.
func mappingString(m *yaml.Node, key string) (string, bool, error) {
	v := mappingValue(m, key)
	if v == nil {
		return "", false, nil
	}
	if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
		return "", true, fmt.Errorf("metadata.%s is not a string", key)
	}
	return v.Value, true, nil
}

// mappingValue returns the value node of key in mapping m, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// encodeHeader marshals a frontmatter document with the two-space indent that
// `podium artifact scaffold` writes. Encode and Close fail only on a node
// yaml.v3 cannot represent, which a node it decoded never is, so no test
// reaches the error branches.
func encodeHeader(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	return buf.Bytes(), nil
}

// closingDelimiterEnd returns the offset just past the closing "---" of src.
// body is the suffix of src that manifest.SplitFrontmatter returned, which
// drops the CR and LF bytes after the delimiter, so stepping back over those
// bytes from the start of body lands on the delimiter's end. Reusing the
// parser's split keeps import and the registry agreed on where the header
// ends.
func closingDelimiterEnd(src, body []byte) int {
	end := len(src) - len(body)
	for end > 0 && (src[end-1] == '\n' || src[end-1] == '\r') {
		end--
	}
	return end
}

// writeImportedSkill writes one planned skill directory under target.
func writeImportedSkill(target string, p importedSkill) error {
	dest := filepath.Join(target, p.id)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), p.skill, 0o644); err != nil {
		return err
	}
	for rel, body := range p.resources {
		full := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
