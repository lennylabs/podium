package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// The §13.12 attestation directory limits.
const (
	attestMaxBytesEnv   = attest.MaxBytesSetting
	attestMaxBundlesEnv = attest.MaxBundlesSetting
)

// attestLimits are the bounds of one attestation directory.
type attestLimits = attest.Limits

// readAttestLimits reads PODIUM_ATTESTATION_MAX_BYTES and
// PODIUM_ATTESTATION_MAX_BUNDLES, whose invalid values the commands report
// as usage errors.
func readAttestLimits(getenv func(string) string) (attestLimits, error) {
	return attest.ReadLimits(getenv)
}

// dirBundle is one entry of an attestation directory whose name ends in
// .sigstore.json.
type dirBundle struct {
	name string
	path string
	// irregular marks an entry that is not a regular file, which is
	// neither read nor removed.
	irregular bool
	// size is the size of a file over the limit, which is not read.
	size int64
	// oversize marks a file above PODIUM_ATTESTATION_MAX_BYTES.
	oversize bool
	// raw holds the bytes of a file within the limit.
	raw []byte
	// parseErr is the refusal of a file that does not parse as a bundle.
	parseErr error
	// summary is the statement and signer of a file that parses.
	summary sign.BundleSummary
}

// problem names why the entry counts as no bundle, or "" for a bundle that
// parsed.
func (b dirBundle) problem(limits attestLimits) string {
	switch {
	case b.irregular:
		return "is not a regular file"
	case b.oversize:
		return fmt.Sprintf("holds %d bytes, above %s (%d)", b.size, attestMaxBytesEnv, limits.MaxBytes)
	case b.parseErr != nil:
		reason, _ := sign.AttestationReasonOf(b.parseErr)
		return fmt.Sprintf("does not parse as a bundle (%s)", reason)
	}
	return ""
}

// readBundleDir reads every entry of dir whose name ends in .sigstore.json,
// in name order. A dir that does not exist holds no bundle. Each file is read
// up to the size limit, so a file above it is never loaded.
//
// Spec: §4.7.11, §7.3.5
func readBundleDir(dir string, limits attestLimits) ([]dirBundle, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the attestation directory %s: %w", dir, err)
	}
	var out []dirBundle
	for _, e := range entries {
		if !attest.IsBundleFileName(e.Name()) {
			continue
		}
		b, err := readDirBundle(filepath.Join(dir, e.Name()), limits)
		if err != nil {
			return nil, err
		}
		b.name = e.Name()
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// readDirBundle reads one bundle file. It checks the entry with Lstat before
// it opens it, opens it without blocking, and checks that the open file is
// the same regular file, so a named pipe or a link is not read.
func readDirBundle(p string, limits attestLimits) (dirBundle, error) {
	b := dirBundle{path: p}
	before, err := os.Lstat(p)
	// The entry was listed a moment ago; this branch runs only when it is
	// removed between the listing and the Lstat.
	if err != nil {
		return b, fmt.Errorf("read %s: %w", p, err)
	}
	if !before.Mode().IsRegular() {
		b.irregular = true
		return b, nil
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return b, fmt.Errorf("read %s: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	// This branch runs only when the entry is replaced between Lstat and
	// open, a race no test arranges deterministically.
	if err != nil || !os.SameFile(before, after) {
		return b, fmt.Errorf("read %s: the file changed while it was read", p)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limits.MaxBytes+1))
	if err != nil {
		return b, fmt.Errorf("read %s: %w", p, err)
	}
	if int64(len(raw)) > limits.MaxBytes {
		b.oversize, b.size = true, after.Size()
		return b, nil
	}
	b.raw = raw
	b.summary, b.parseErr = sign.InspectAttestationBundle(raw)
	return b, nil
}

// attestationDir returns the attestation directory of the layer root that
// dir and root give.
func attestationDir(dir, root string) string {
	return filepath.Join(dir, filepath.FromSlash(root), filepath.FromSlash(attest.AttestationDir))
}
