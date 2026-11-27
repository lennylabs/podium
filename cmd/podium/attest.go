package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// attestCmd writes the §7.3.5 subjects of one layer for a signing tool.
//
//	podium attest [<dir>] [--root <subpath>] [--commit <rev>] [--out <dir>]
//	              [--max-subjects <n>] [--format checksums|statement]
func attestCmd(args []string) int {
	return runAttest(context.Background(), args, os.Stdout, os.Stderr)
}

// attestOptions are the parsed flags and operand of `podium attest`.
type attestOptions struct {
	dir         string
	root        string
	commit      string
	out         string
	maxSubjects int
	output      attest.Output
	// layerName is the layer subject name built from root.
	layerName string
}

// runAttest is attestCmd with its streams and context injected. It exits 2 on
// a usage error, 1 on a refusal or a failed write, and 0 once it wrote the
// files. Every refusal comes before the first file is removed or written, so
// a refused layer leaves --out as it was.
//
// Spec: §7.3.5
func runAttest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, code := parseAttestArgs(args, stderr)
	if code >= 0 {
		return code
	}
	records, files, err := readAttestLayer(ctx, opts, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	rendered, err := renderAttestFiles(ctx, opts, records, files, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	if err := writeAttestFiles(opts, rendered, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	return 0
}

// parseAttestArgs parses args. It returns code -1 with the options when the
// command proceeds, and the exit code otherwise, having written the reason.
func parseAttestArgs(args []string, stderr io.Writer) (attestOptions, int) {
	flags := flag.NewFlagSet("attest", flag.ContinueOnError)
	setUsage(flags, "Write the subjects an author attestation of a layer signs.")
	flags.SetOutput(stderr)
	root := flags.String("root", "", "the layer's registered root below <dir>")
	commit := flags.String("commit", "", "read the layer from this commit's tree, as registry ingest reads a git layer")
	out := flags.String("out", ".", "directory the subject files are written to")
	maxSubjects := flags.Int("max-subjects", attest.DefaultMaxSubjects, "most subjects in one file, the layer subject included")
	format := flags.String("format", string(attest.FormatChecksums), "checksums|statement")
	predicateType := flags.String("predicate-type", "", "predicateType of the statement format (default "+attest.DefaultPredicateType+")")
	ops, err := parseOperands(flags, args)
	if err != nil {
		return attestOptions{}, parseExit(err)
	}
	opts := attestOptions{dir: ".", commit: *commit, out: *out, maxSubjects: *maxSubjects}
	if len(ops) > 1 {
		_, _ = fmt.Fprintf(stderr, "error: podium attest takes at most one <dir>, got %d\n", len(ops))
		return opts, 2
	}
	if len(ops) == 1 {
		opts.dir = ops[0]
	}
	if opts.output.Format, err = attest.ParseFormat(*format); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: --format: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	if opts.maxSubjects < 2 {
		_, _ = fmt.Fprintf(stderr, "error: --max-subjects %d: %v\n", opts.maxSubjects, attest.ErrMaxSubjects)
		return opts, 2
	}
	if opts.root, err = attest.CheckLayerRoot(*root); err == nil {
		opts.layerName, err = attest.LayerSubjectName(opts.root)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: --root: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	if code := parsePredicateType(&opts, *predicateType, stderr); code >= 0 {
		return opts, code
	}
	return opts, -1
}

// parsePredicateType sets the predicate type of the statement format. It
// returns -1 when the value is usable, and 2, having written the reason, for a
// value that is not an absolute URI or one given with the checksums format,
// whose signing tool chooses the type.
//
// Spec: §7.3.5
func parsePredicateType(opts *attestOptions, value string, stderr io.Writer) int {
	if value == "" {
		return -1
	}
	if opts.output.Format != attest.FormatStatement {
		_, _ = fmt.Fprintln(stderr, "error: --predicate-type applies to --format statement; the tool that signs a checksums file chooses the predicate type")
		return 2
	}
	if err := attest.CheckPredicateType(value); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: --predicate-type: %s\n", oneLine(err.Error()))
		return 2
	}
	opts.output.PredicateType = value
	return -1
}

// readAttestLayer walks the layer with the walk the registry applies to its
// source: the ingest walk of a git snapshot over the commit's tree with
// --commit, and the filesystem walk of a local layer over the directory
// without it. It returns every package discovery finds and the layer's tree.
//
// Spec: §4.7.6, §7.3.5
func readAttestLayer(ctx context.Context, opts attestOptions, stderr io.Writer) ([]filesystem.ArtifactRecord, fs.FS, error) {
	if opts.commit != "" {
		tree, err := source.OpenCommitTree(ctx, opts.dir, opts.commit, opts.root)
		if err != nil {
			return nil, nil, err
		}
		records, err := ingest.LayerRecords(tree.Files)
		if err != nil {
			return nil, nil, fmt.Errorf("walk commit %s: %w", tree.Commit, err)
		}
		return records, tree.Files, nil
	}
	layerPath, err := filepath.Abs(filepath.Join(opts.dir, filepath.FromSlash(opts.root)))
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(layerPath)
	if err != nil {
		return nil, nil, fmt.Errorf("layer root: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("layer root %s is not a directory", layerPath)
	}
	// Spec: §4.7.6 — a checkout can hold other bytes and paths than its
	// commit, and the registry digests a git layer from the commit.
	if source.IsGitWorkTree(layerPath) {
		_, _ = fmt.Fprintf(stderr, "warning: %s lies in a Git working tree; the subjects digest the files as checked out, and registry ingest of a git layer digests the commit's blobs, which line-ending and LFS filters, symbolic links, and case-insensitive paths can make differ; run with --commit HEAD to read the commit\n", oneLine(layerPath))
	}
	return filesystem.LayerRecords(filesystem.Layer{ID: filepath.Base(layerPath), Path: layerPath})
}

// renderAttestFiles writes the portable-name warnings, checks that every
// package has a subject name, and renders the subject files in memory. It
// returns an error, and renders nothing, for a package with no subject name,
// so no file of the run is written for a layer that cannot be attested. The
// warnings come first, so a run prints them whether or not it then refuses.
//
// Spec: §4.2, §4.7.6, §7.3.5
func renderAttestFiles(ctx context.Context, opts attestOptions, records []filesystem.ArtifactRecord, files fs.FS, stderr io.Writer) ([][]byte, error) {
	for _, d := range (&lint.Linter{Rules: []lint.Rule{lint.NonPortableNameRule()}}).Lint(ctx, nil, records) {
		_, _ = fmt.Fprintf(stderr, "warning: %s: %s (%s)\n", oneLine(d.ArtifactID), oneLine(d.Message), d.Code)
	}
	for i := range records {
		if _, err := records[i].SubjectName(); err != nil {
			// The ID is the package's path relative to the layer root, and %q
			// writes every character the subject name rule excludes as an
			// escape.
			return nil, fmt.Errorf("the package at %q has no subject name, so the layer cannot be attested: %w", records[i].ID, err)
		}
	}
	content, err := filesystem.LayerDigestInput(records, files)
	if err != nil {
		return nil, err
	}
	layer := attest.Subject{Name: opts.layerName, Digest: attest.LayerDigest(content)}
	groups, err := attest.SubjectFiles(layer, content.Subjects, opts.maxSubjects)
	if err != nil {
		return nil, err
	}
	rendered := make([][]byte, 0, len(groups))
	for _, g := range groups {
		var b bytes.Buffer
		if err := opts.output.Write(&b, g); err != nil {
			return nil, err
		}
		rendered = append(rendered, b.Bytes())
	}
	return rendered, nil
}

// writeAttestFiles creates --out, removes the subject files an earlier run
// left there for the same layer subject, writes each rendered file under the
// lowest file number no remaining entry takes, and prints each path it wrote.
//
// Spec: §7.3.5
func writeAttestFiles(opts attestOptions, rendered [][]byte, stdout io.Writer) error {
	if err := os.MkdirAll(opts.out, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", opts.out, err)
	}
	taken, err := removeEarlierAttestFiles(opts.out, opts.layerName)
	if err != nil {
		return err
	}
	n := 0
	for _, b := range rendered {
		name := ""
		for name == "" || taken[name] {
			n++
			name = opts.output.Format.FileName(n)
		}
		p := filepath.Join(opts.out, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		_, _ = fmt.Fprintln(stdout, p)
	}
	return nil
}

// removeEarlierAttestFiles removes each subject file in out whose first
// subject is layerName, which an earlier run for the same layer wrote, and
// returns the names of the subject-file entries it leaves. A file for another
// layer subject, such as the files of another layer root that shares out,
// stays, and so does an entry it cannot read or attribute, because removing it
// could drop another run's subjects.
//
// Spec: §7.3.5
func removeEarlierAttestFiles(out, layerName string) (map[string]bool, error) {
	entries, err := os.ReadDir(out)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", out, err)
	}
	taken := map[string]bool{}
	for _, e := range entries {
		f, ok := attest.OutputFileFormat(e.Name())
		if !ok {
			continue
		}
		p := filepath.Join(out, e.Name())
		b, readErr := os.ReadFile(p)
		if name, ok := attest.FirstSubjectName(f, b); readErr != nil || !ok || name != layerName {
			taken[e.Name()] = true
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove an earlier subject file: %w", err)
		}
	}
	return taken, nil
}

// oneLine writes each control character and line or paragraph separator of s
// as a \u escape, so a message that quotes a path from the tree stays on one
// line of the output.
func oneLine(s string) string {
	if !strings.ContainsFunc(s, breaksLine) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if breaksLine(r) {
			_, _ = fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// breaksLine reports whether r is a control character or a Unicode line or
// paragraph separator.
func breaksLine(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}
