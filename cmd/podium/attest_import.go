package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// importOptions are the parsed flags and operands of `podium attest import`.
type importOptions struct {
	files   []string
	layer   attestOptions
	noPrune bool
	limits  attestLimits
	// keyFile is --public-key, the file that holds the public key of a
	// key-based signer, and key the key read from it.
	keyFile string
	key     *sign.AttestationPublicKey
}

// importedBundle is one bundle an import writes: its bytes after the
// removal of the timestamp certificates, its file name, and what it states.
type importedBundle struct {
	source  string
	name    string
	raw     []byte
	summary sign.BundleSummary
}

// runAttestImport is `podium attest import` with its streams and context
// injected. It reads and checks every input, and reads the layer, before it
// writes a file, so a refused input or layer leaves the directory as it was.
// It exits 2 on a usage error, 1 on a refusal or a failed write or removal,
// and 0 once it wrote the bundles and pruned the directory.
//
// Spec: §7.3.5
func runAttestImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, code := parseImportArgs(args, os.Getenv, stderr)
	if code >= 0 {
		return code
	}
	if err := importBundles(ctx, opts, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	return 0
}

// importBundles reads the inputs and, unless --no-prune, the layer, then
// writes the bundles and prunes the directory. The attestation directory is
// checked, and created, only after every refusal that writes nothing, and
// checked again before the pruning reads it.
//
// Spec: §4.7.11, §7.3.5
func importBundles(ctx context.Context, opts importOptions, stdout, stderr io.Writer) error {
	// The layer root is checked before the layer is read from a commit,
	// because the bundles are written below it on disk.
	if err := checkRootSegments(opts.layer.dir, opts.layer.root); err != nil {
		return err
	}
	if err := opts.readKey(); err != nil {
		return err
	}
	imported, err := readImportInputs(opts)
	if err != nil {
		return err
	}
	var cur attest.CurrentSubjects
	if !opts.noPrune {
		if cur, err = currentSubjects(ctx, opts.layer, stderr); err != nil {
			return err
		}
		warnUnmatchedImports(imported, cur, stderr)
	}
	dir, _, err := attestationDir(opts.layer.dir, opts.layer.root, true)
	if err != nil {
		return err
	}
	if err := writeImported(dir, imported, stdout); err != nil {
		return err
	}
	if !opts.noPrune {
		if _, _, err := attestationDir(opts.layer.dir, opts.layer.root, false); err != nil {
			return err
		}
		if err := pruneBundleDir(dir, imported, cur, opts, stdout, stderr); err != nil {
			return err
		}
	}
	warnBundleCount(dir, opts.limits, stderr)
	return nil
}

// warnUnmatchedImports warns for each imported bundle none of whose subjects
// matches the layer, which the import writes and a later import prunes.
//
// Spec: §7.3.5
func warnUnmatchedImports(imported []importedBundle, cur attest.CurrentSubjects, stderr io.Writer) {
	for _, b := range imported {
		if len(cur.Matching(b.summary.Statement)) == 0 {
			_, _ = fmt.Fprintf(stderr, "warning: the bundle %s from %s lists no subject of the layer\n", b.name, oneLine(b.source))
		}
	}
}

// parseImportArgs parses args. It returns code -1 with the options when the
// command proceeds, and the exit code otherwise, having written the reason.
func parseImportArgs(args []string, getenv func(string) string, stderr io.Writer) (importOptions, int) {
	flags := flag.NewFlagSet("attest import", flag.ContinueOnError)
	setUsage(flags, "Copy signed bundles into a layer's attestation directory and prune it.")
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "directory the layer root is resolved from")
	root := flags.String("root", "", "the layer's registered root below --dir")
	commit := flags.String("commit", "", "read the layer's packages from this commit's tree")
	noPrune := flags.Bool("no-prune", false, "write the bundles and remove nothing")
	publicKey := flags.String("public-key", "", "file holding the PEM public key that key-signed bundles verify under, such as cosign.pub")
	ops, err := parseOperands(flags, args)
	if err != nil {
		return importOptions{}, parseExit(err)
	}
	opts := importOptions{files: ops, noPrune: *noPrune, layer: attestOptions{dir: *dir, commit: *commit}, keyFile: *publicKey}
	if len(ops) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: podium attest import <bundle-file>... [--dir <dir>] [--root <subpath>] [--commit <rev>] [--no-prune] [--public-key <file>]")
		return opts, 2
	}
	if opts.layer.root, opts.layer.layerName, err = parseLayerRoot(*root); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: --root: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	if opts.limits, err = readAttestLimits(getenv); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %s\n", oneLine(err.Error()))
		return opts, 2
	}
	return opts, -1
}

// readKey reads the --public-key file under the §4.7.11 public_key rules. A
// file that cannot be read or that the rules refuse fails the import before
// any input is read.
//
// Spec: §7.3.5, §4.7.11 (Key-based signers)
func (opts *importOptions) readKey() error {
	if opts.keyFile == "" {
		return nil
	}
	raw, err := os.ReadFile(opts.keyFile)
	if err != nil {
		return fmt.Errorf("read the --public-key file: %w", err)
	}
	key, err := sign.ParseAttestationPublicKey(string(raw))
	if err != nil {
		return fmt.Errorf("--public-key %s: %w", opts.keyFile, err)
	}
	opts.key = &key
	return nil
}

// readImportInputs reads every input file and returns its bundles with the
// timestamp certificates removed, one per distinct file name. It refuses a
// file that holds no bundle, a bundle that does not parse, a timestamp that
// does not parse, and a bundle above the size limit.
//
// Spec: §4.7.11, §7.3.5
func readImportInputs(opts importOptions) ([]importedBundle, error) {
	var out []importedBundle
	seen := map[string]bool{}
	for _, file := range opts.files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		values, err := attest.SplitBundles(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for i, v := range values {
			b, err := prepareBundle(v, opts.limits, opts.key)
			if err != nil {
				return nil, fmt.Errorf("%s: bundle %d: %w", file, i+1, err)
			}
			if seen[b.name] {
				continue
			}
			seen[b.name] = true
			b.source = file
			out = append(out, b)
		}
	}
	return out, nil
}

// prepareBundle checks one bundle, removes its timestamp certificates, and
// names it by the attestation digest of the bytes it will be written as. A
// key-signed bundle names its key in no signed byte, so it is accepted only
// when it meets the acceptance conditions of a key-based signer with key.
//
// Spec: §7.3.5, §4.7.11 (Key-based signers)
func prepareBundle(v []byte, limits attestLimits, key *sign.AttestationPublicKey) (importedBundle, error) {
	sum, err := sign.InspectAttestationBundle(v)
	if err != nil {
		return importedBundle{}, err
	}
	// The import groups the bundle by its signer, so a bundle whose
	// signature that signer's key did not make could remove a valid bundle
	// of that signer.
	if err := checkImportSignature(v, sum, key); err != nil {
		return importedBundle{}, err
	}
	out, _, err := attest.RemoveTimestampCertificates(v)
	if err != nil {
		return importedBundle{}, err
	}
	if int64(len(out)) > limits.MaxBytes {
		return importedBundle{}, fmt.Errorf("the bundle holds %d bytes, above %s (%d)", len(out), attestMaxBytesEnv, limits.MaxBytes)
	}
	// The removal changes only the signedTimestamp values, so the bundle
	// parses as before; the summary is read from the bytes written.
	summary, err := sign.InspectAttestationBundle(out)
	if err != nil {
		return importedBundle{}, err
	}
	if summary.KeySigned {
		summary.Key = key.ID()
	}
	return importedBundle{name: attest.BundleFileName(out), raw: out, summary: summary}, nil
}

// checkImportSignature verifies the signature of a bundle before an import
// groups it: under its own leaf certificate's key for a certificate-signed
// bundle, and under the --public-key key for a key-signed one, which the
// import refuses without that flag.
func checkImportSignature(v []byte, sum sign.BundleSummary, key *sign.AttestationPublicKey) error {
	if !sum.KeySigned {
		return sign.CheckAttestationSelfSignature(v)
	}
	if key == nil {
		return errors.New("the bundle is key-signed and names its key in no signed byte; name the key with --public-key")
	}
	_, err := sign.VerifyKeySignedAttestation(v, *key)
	return err
}

// keyGroup sets the key identifier of an existing key-signed bundle whose
// signature verifies under the --public-key key, so the pruning groups it
// with the imported bundles of that key. A key-signed bundle that does not
// verify under it groups with no imported bundle.
func keyGroup(b *dirBundle, key *sign.AttestationPublicKey) {
	if !b.summary.KeySigned || key == nil {
		return
	}
	if _, err := sign.VerifyKeySignedAttestation(b.raw, *key); err == nil {
		b.summary.Key = key.ID()
	}
}

// currentSubjects reads the layer with the walk of `podium attest` and
// returns its current subjects.
//
// Spec: §7.3.5
func currentSubjects(ctx context.Context, opts attestOptions, stderr io.Writer) (attest.CurrentSubjects, error) {
	records, files, err := readAttestLayer(ctx, opts, stderr)
	if err != nil {
		return nil, err
	}
	layer, content, err := attestLayerSubjects(ctx, opts.layerName, records, files, stderr)
	if err != nil {
		return nil, err
	}
	return attest.NewCurrentSubjects(layer, content.Subjects), nil
}

// writeImported writes each bundle to its name in dir, which
// attestationDir has checked or created. A regular
// file of that name that holds the same bytes is left as it is. Each file is
// written to a temporary file in dir and renamed over its name, so an entry
// of that name that is a link is replaced rather than followed.
//
// Spec: §7.3.5
func writeImported(dir string, imported []importedBundle, stdout io.Writer) error {
	for _, b := range imported {
		p := filepath.Join(dir, b.name)
		if sameRegularFile(p, b.raw) {
			_, _ = fmt.Fprintf(stdout, "unchanged %s\n", p)
			continue
		}
		if err := writeFileReplacing(dir, b.name, b.raw); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "wrote %s\n", p)
	}
	return nil
}

// sameRegularFile reports whether p is a regular file that holds b.
func sameRegularFile(p string, b []byte) bool {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(b)) {
		return false
	}
	got, err := os.ReadFile(p)
	return err == nil && bytes.Equal(got, b)
}

// writeFileReplacing writes b to name in dir through a temporary file and a
// rename. The temporary file is created with mode 0644 under the process
// umask, as os.WriteFile would create it, and with O_EXCL, so it replaces
// no entry and follows no link.
func writeFileReplacing(dir, name string, b []byte) error {
	target := filepath.Join(dir, name)
	tmp, err := createImportTemp(dir)
	if err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), target)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", target, werr)
	}
	return nil
}

// createImportTemp creates a new file .import-<random> in dir.
func createImportTemp(dir string) (*os.File, error) {
	for {
		var r [8]byte
		// crypto/rand.Read does not fail on the supported platforms.
		_, _ = rand.Read(r[:])
		f, err := os.OpenFile(filepath.Join(dir, ".import-"+hex.EncodeToString(r[:])), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
}

// pruneBundleDir removes each bundle file of dir that the run did not write
// and that the §7.3.5 rule describes: a file above the size limit or that
// does not parse, a bundle none of whose subjects matches the layer, and a
// bundle whose matching subjects the imported bundles of its signer and
// predicate type list together. An entry that is not a regular file stays,
// with a warning.
//
// Spec: §7.3.5
func pruneBundleDir(dir string, imported []importedBundle, cur attest.CurrentSubjects, opts importOptions, stdout, stderr io.Writer) error {
	limits := opts.limits
	existing, err := readBundleDir(dir, limits)
	if err != nil {
		return err
	}
	written := map[string]bool{}
	summaries := make([]sign.BundleSummary, len(imported))
	for i, b := range imported {
		written[b.name] = true
		summaries[i] = b.summary
	}
	for _, b := range existing {
		if written[b.name] {
			continue
		}
		reason := b.problem(limits)
		switch {
		case b.irregular:
			_, _ = fmt.Fprintf(stderr, "warning: %s %s; it stays\n", oneLine(b.path), reason)
			continue
		case reason == "":
			keyGroup(&b, opts.key)
			reason = string(attest.Prune(b.summary, summaries, cur))
		}
		if reason == "" {
			continue
		}
		if err := os.Remove(b.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", b.path, err)
		}
		_, _ = fmt.Fprintf(stdout, "removed %s: %s\n", oneLine(b.path), reason)
	}
	return nil
}

// warnBundleCount warns when dir holds more bundle files than the limit.
func warnBundleCount(dir string, limits attestLimits, stderr io.Writer) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	n := 0
	for _, e := range entries {
		if attest.IsBundleFileName(e.Name()) {
			n++
		}
	}
	if n > limits.MaxBundles {
		_, _ = fmt.Fprintf(stderr, "warning: %s holds %d bundle files, above %s (%d)\n", oneLine(dir), n, attestMaxBundlesEnv, limits.MaxBundles)
	}
}
