package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// The §13.12 attestation directory limits.
const (
	attestMaxBytesEnv   = "PODIUM_ATTESTATION_MAX_BYTES"
	attestMaxBundlesEnv = "PODIUM_ATTESTATION_MAX_BUNDLES"
)

// attestLimits are the bounds of one attestation directory.
type attestLimits struct {
	maxBytes   int64
	maxBundles int
}

// readAttestLimits reads PODIUM_ATTESTATION_MAX_BYTES and
// PODIUM_ATTESTATION_MAX_BUNDLES. An unset or blank value takes the default,
// and a value that is not plain decimal digits in the setting's range is an
// error the commands report as a usage error.
//
// Spec: §13.12
func readAttestLimits(getenv func(string) string) (attestLimits, error) {
	maxBytes, err := boundedSetting(getenv, attestMaxBytesEnv, attest.DefaultMaxBundleBytes, attest.MaxBundleBytesLimit)
	if err != nil {
		return attestLimits{}, err
	}
	maxBundles, err := boundedSetting(getenv, attestMaxBundlesEnv, attest.DefaultMaxBundles, attest.MaxBundlesLimit)
	if err != nil {
		return attestLimits{}, err
	}
	return attestLimits{maxBytes: maxBytes, maxBundles: int(maxBundles)}, nil
}

// plainDecimal is a decimal integer of at least 1 written in digits alone,
// with no sign and no leading zero.
var plainDecimal = regexp.MustCompile(`\A[1-9][0-9]*\z`)

// boundedSetting parses the setting name as a decimal integer from 1 to
// most, or returns def when it is unset or blank.
func boundedSetting(getenv func(string) string, name string, def, most int64) (int64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if !plainDecimal.MatchString(raw) || err != nil || n > most {
		return 0, fmt.Errorf("%s must be a decimal integer from 1 to %d, written in digits with no sign or leading zero, got %q", name, most, raw)
	}
	return n, nil
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
		return fmt.Sprintf("holds %d bytes, above %s (%d)", b.size, attestMaxBytesEnv, limits.maxBytes)
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
	raw, err := io.ReadAll(io.LimitReader(f, readLimit(limits.maxBytes)))
	if err != nil {
		return b, fmt.Errorf("read %s: %w", p, err)
	}
	if int64(len(raw)) > limits.maxBytes {
		b.oversize, b.size = true, after.Size()
		return b, nil
	}
	b.raw = raw
	b.summary, b.parseErr = sign.InspectAttestationBundle(raw)
	return b, nil
}

// readLimit is one byte above maxBytes, so a read tells a file at the
// limit from a larger one, without overflowing at the largest int64.
func readLimit(maxBytes int64) int64 {
	if maxBytes == math.MaxInt64 {
		return math.MaxInt64
	}
	return maxBytes + 1
}

// errAttestationDirEntry reports a symbolic link or an entry other than a
// directory at .podium or .podium/attestations.
var errAttestationDirEntry = errors.New("is a symbolic link or no directory; the attestation directory must lie inside the layer root")

// attestationDir returns the attestation directory of the layer root that
// dir and root give. It refuses a symbolic link or an entry other than a
// directory at .podium or .podium/attestations, so a command never reads,
// writes, or removes a file outside the layer root through one (§4.7.11).
// With create, it creates each absent component with Mkdir, which follows no
// link at the new name, and checks the component again afterwards, so a
// link placed there in between is refused too. Without create, exists is
// false when a component is absent. The check and the later file operations
// are separate calls, so a party who can replace the directory between them
// is outside what the check closes.
//
// Spec: §4.7.11, §7.3.5
func attestationDir(dir, root string, create bool) (path string, exists bool, err error) {
	path = filepath.Join(dir, filepath.FromSlash(root))
	for _, seg := range strings.Split(attest.AttestationDir, "/") {
		path = filepath.Join(path, seg)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return "", false, fmt.Errorf("create %s: %w", path, err)
			}
			info, err = os.Lstat(path)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return filepath.Join(dir, filepath.FromSlash(root), filepath.FromSlash(attest.AttestationDir)), false, nil
		case err != nil:
			return "", false, fmt.Errorf("read %s: %w", path, err)
		case !info.IsDir():
			return "", false, fmt.Errorf("%s %w", path, errAttestationDirEntry)
		}
	}
	return path, true, nil
}
