package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// shellTags are fenced-block language tags whose content is shell commands.
var shellTags = map[string]bool{
	"bash":          true,
	"sh":            true,
	"shell":         true,
	"console":       true,
	"zsh":           true,
	"shell-session": true,
	"shellsession":  true,
	"sh-session":    true,
	"command":       true,
	"commandline":   true,
	"shell-script":  true,
}

// commandLineRegex matches a fenced-block line that begins with a command a
// reader would run in a shell. It tolerates a leading "$ " or "> " prompt and
// leading whitespace. The command name is anchored at the start so a path that
// merely contains "podium" (for example "~/podium-artifacts/") does not match.
var commandLineRegex = regexp.MustCompile(
	`^(?:\$ |> )?(?:sudo )?(?:podium|podium-mcp|podium-server|make|curl|go (?:build|run|install|test)|pip3?|python3? -m|npm|npx|pytest|docker|git clone|brew|cargo|kubectl|helm)(?:\s|$)`)

// ScanRunnablePages walks docsDir under root plus the repository README and
// returns the repo-relative (forward-slash) paths of every page that contains a
// runnable command example. The result is sorted for deterministic output.
func ScanRunnablePages(root, docsDir string) ([]string, error) {
	var pages []string
	seen := map[string]bool{}

	add := func(abs string) error {
		runnable, err := pageHasRunnableBlock(abs)
		if err != nil {
			return err
		}
		if runnable {
			rel := repoRel(root, abs)
			if !seen[rel] {
				seen[rel] = true
				pages = append(pages, rel)
			}
		}
		return nil
	}

	// The repository README is a documentation source even though it lives at
	// the repo root rather than under docs/.
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); err == nil {
		if err := add(readme); err != nil {
			return nil, err
		}
	}

	docsRoot := filepath.Join(root, docsDir)
	err := filepath.Walk(docsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		return add(path)
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(pages)
	return pages, nil
}

// pageHasRunnableBlock reports whether the markdown file at path contains at
// least one runnable command block under the classification rules.
func pageHasRunnableBlock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return contentHasRunnableBlock(string(data)), nil
}

// contentHasRunnableBlock implements the runnable-block classifier on raw
// markdown content. A block is runnable when its language tag is a shell tag,
// or when it carries no tag (or a "text"/"plaintext" tag) and one of its lines
// begins with a known command. Config and data blocks (yaml, json, toml,
// python, go, markdown, and any other non-shell tag) are never runnable.
func contentHasRunnableBlock(content string) bool {
	lines := strings.Split(content, "\n")
	inFence := false
	var fenceMarker string // the ``` or ~~~ run that opened the current fence
	shell := false
	plain := false
	blockHasCommand := false

	for _, raw := range lines {
		trimmed := strings.TrimLeft(raw, " \t")
		if !inFence {
			marker := fenceOpener(trimmed)
			if marker == "" {
				continue
			}
			inFence = true
			fenceMarker = marker
			lang := fenceLang(trimmed, marker)
			shell = shellTags[lang]
			plain = lang == "" || lang == "text" || lang == "plaintext" || lang == "txt"
			blockHasCommand = false
			continue
		}
		// Inside a fence: a line that is exactly the fence marker closes it.
		if isFenceCloser(trimmed, fenceMarker) {
			if shell || (plain && blockHasCommand) {
				return true
			}
			inFence = false
			continue
		}
		if plain && !blockHasCommand && commandLineRegex.MatchString(trimmed) {
			blockHasCommand = true
		}
	}
	return false
}

// fenceOpener returns the fence marker (a run of at least three backticks or
// tildes) if trimmed opens a fenced code block, or "" otherwise.
func fenceOpener(trimmed string) string {
	for _, ch := range []byte{'`', '~'} {
		n := leadingRun(trimmed, ch)
		if n >= 3 {
			return strings.Repeat(string(ch), n)
		}
	}
	return ""
}

// isFenceCloser reports whether trimmed is a closing fence for an open block
// opened with marker. A closer is a run of the same character at least as long
// as the opener, with no trailing info string.
func isFenceCloser(trimmed, marker string) bool {
	ch := marker[0]
	n := leadingRun(trimmed, ch)
	if n < len(marker) {
		return false
	}
	rest := strings.TrimSpace(trimmed[n:])
	return rest == ""
}

// fenceLang extracts the lowercased language tag that follows the opening fence
// marker. An info string like "bash title=…" yields "bash".
func fenceLang(trimmed, marker string) string {
	info := strings.TrimSpace(trimmed[len(marker):])
	if info == "" {
		return ""
	}
	fields := strings.FieldsFunc(info, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '{' || r == ','
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func leadingRun(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

// docSlugRegex captures the doc path and D-slug from an end-to-end test header
// comment of the form:
//
//	// End-to-end tests for docs/getting-started/quickstart.md (D-quickstart).
//
// The path and the trailing "(D-<slug>)" may wrap across two comment lines, so
// the scanner joins consecutive comment lines before matching.
var docSlugRegex = regexp.MustCompile(`End-to-end tests for\s+(\S+\.md)\s+\((D-[a-z0-9-]+)\)`)

// ScanDocSlugs walks the end-to-end test directory and returns a map from each
// declared D-<slug> to the repo-absolute path of the test file that declares
// it. The header path itself is informational; the value is the test file so
// check can stat it.
func ScanDocSlugs(e2eDir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(e2eDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(e2eDir, e.Name())
		slug, err := slugFromFile(path)
		if err != nil {
			return nil, err
		}
		if slug != "" {
			out[slug] = path
		}
	}
	return out, nil
}

// slugFromFile returns the D-<slug> declared in a test file's header comment,
// or "" when the file declares none. The header is the leading "//" comment
// block, which may sit before or after the `package` clause and may wrap the
// "(D-<slug>)" onto a second comment line. The scan joins the leading comment
// lines and stops at the first declaration that starts the test body.
func slugFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var joined strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "" || strings.HasPrefix(t, "package"):
			// Blank lines and the package clause may interleave with build-tag
			// and header comments; keep scanning.
			continue
		case strings.HasPrefix(t, "//"):
			joined.WriteString(strings.TrimSpace(strings.TrimPrefix(t, "//")))
			joined.WriteByte(' ')
		default:
			// First real declaration (import/var/func/...) ends the header.
			goto done
		}
	}
done:
	m := docSlugRegex.FindStringSubmatch(joined.String())
	if m == nil {
		return "", nil
	}
	return m[2], nil
}
