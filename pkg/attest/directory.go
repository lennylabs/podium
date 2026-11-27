package attest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// AttestationDir is the attestation directory of a layer, relative to the
// layer root, as a slash path.
//
// Spec: §4.6, §4.7.11
const AttestationDir = ".podium/attestations"

// BundleFileSuffix ends the name of every bundle file of an attestation
// directory. Every file with the suffix is a bundle file, whatever the rest
// of its name.
//
// Spec: §4.7.11
const BundleFileSuffix = ".sigstore.json"

// The §13.12 defaults of the attestation directory limits.
const (
	// DefaultMaxBundleBytes is the default of PODIUM_ATTESTATION_MAX_BYTES.
	// A statement of 1024 subjects gives a bundle of about 200 KB.
	DefaultMaxBundleBytes = 1 << 20
	// DefaultMaxBundles is the default of PODIUM_ATTESTATION_MAX_BUNDLES.
	DefaultMaxBundles = 64
)

// ErrNoBundle reports a bundle file that holds no JSON value.
var ErrNoBundle = errors.New("attest: the file holds no bundle")

// AttestationDigest returns the §4.7.11 attestation digest of a bundle: the
// SHA-256 digest of its bytes as stored, written sha256:<hex>.
//
// Spec: §4.7.11
func AttestationDigest(bundle []byte) string {
	return FileDigest(bundle)
}

// BundleFileName returns the name `podium attest import` gives a bundle:
// the 64 lowercase hexadecimal digits of its attestation digest followed by
// .sigstore.json. The name leaves out the sha256: prefix, because : is not
// valid in a Windows file name.
//
// Spec: §4.7.11, §7.3.5
func BundleFileName(bundle []byte) string {
	sum := sha256.Sum256(bundle)
	return hex.EncodeToString(sum[:]) + BundleFileSuffix
}

// IsBundleFileName reports whether name is the name of a bundle file.
//
// Spec: §4.7.11
func IsBundleFileName(name string) bool {
	return strings.HasSuffix(name, BundleFileSuffix)
}

// SplitBundles returns the bundles that raw holds: one bundle, or several in
// the JSON Lines format that GitHub's attestation action writes. It reads raw
// as a sequence of JSON values and returns each value's bytes without the
// white space around it, which is the form `podium attest import` writes. A
// file that is not such a sequence, and one that holds no value, returns an
// error. Each value is returned as it was written, so a caller applies the
// §4.7.10 JSON rule through the bundle parse.
//
// Spec: §7.3.5
func SplitBundles(raw []byte) ([][]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var out [][]byte
	for {
		var v json.RawMessage
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("attest: value %d is not JSON: %w", len(out)+1, err)
		}
		out = append(out, bytes.TrimSpace(v))
	}
	if len(out) == 0 {
		return nil, ErrNoBundle
	}
	return out, nil
}
