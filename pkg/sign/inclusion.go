package sign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// checkpoint is a parsed signed-note checkpoint.
type checkpoint struct {
	body string // the signed text, origin through the last body line, newline-terminated
	size uint64
	root []byte
	sigs [][]byte // each signature line's bytes after the 4-byte key hint
}

// signatureLinePrefix opens every signed-note signature line: an em dash
// (U+2014) and a space.
const signatureLinePrefix = "— "

// parseCheckpoint parses checkpoint text: the origin, the decimal tree
// size, and the base64 32-byte root hash, optional extension lines, a
// blank line, then one or more "— <name> <base64>" signature lines.
func parseCheckpoint(text string) (checkpoint, error) {
	bodyText, sigText, found := strings.Cut(text, "\n\n")
	if !found {
		return checkpoint{}, errors.New("checkpoint does not parse: no blank line before the signatures")
	}
	lines := strings.Split(bodyText, "\n")
	if len(lines) < 3 {
		return checkpoint{}, errors.New("checkpoint does not parse: body has fewer than three lines")
	}
	size, err := strconv.ParseUint(lines[1], 10, 64)
	if err != nil {
		return checkpoint{}, fmt.Errorf("checkpoint does not parse: tree size: %w", err)
	}
	root, err := base64.StdEncoding.DecodeString(lines[2])
	if err != nil || len(root) != sha256.Size {
		return checkpoint{}, errors.New("checkpoint does not parse: root hash is not 32 base64 bytes")
	}
	cp := checkpoint{body: bodyText + "\n", size: size, root: root}
	for _, line := range strings.Split(strings.TrimSuffix(sigText, "\n"), "\n") {
		sig, err := parseSignatureLine(line)
		if err != nil {
			return checkpoint{}, err
		}
		cp.sigs = append(cp.sigs, sig)
	}
	return cp, nil
}

// parseSignatureLine returns the signature bytes of one signature line.
// The 4-byte key hint is not interpreted: every trusted key is tried.
func parseSignatureLine(line string) ([]byte, error) {
	rest, ok := strings.CutPrefix(line, signatureLinePrefix)
	fields := strings.Fields(rest)
	if !ok || len(fields) != 2 {
		return nil, errors.New("checkpoint does not parse: malformed signature line")
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(raw) <= 4 {
		return nil, errors.New("checkpoint does not parse: malformed signature line")
	}
	return raw[4:], nil
}

// verifyInclusion checks that the entry body is included at its log index
// in the tree the checkpoint states, and that the checkpoint verifies
// under a log key valid at the attested time t.
//
// Spec: §4.7.9.
func verifyInclusion(root trustedRoot, tl *tlogEntry, t time.Time) error {
	cp, err := parseCheckpoint(tl.Checkpoint)
	if err != nil {
		return err
	}
	body, err := base64.StdEncoding.DecodeString(tl.Body)
	if err != nil {
		return fmt.Errorf("inclusion proof does not verify: entry body: %w", err)
	}
	hashes := make([][]byte, len(tl.Hashes))
	for i, h := range tl.Hashes {
		if hashes[i], err = base64.StdEncoding.DecodeString(h); err != nil {
			return fmt.Errorf("inclusion proof does not verify: hash %d: %w", i, err)
		}
	}
	if tl.LogIndex < 0 || uint64(tl.LogIndex) >= cp.size {
		return errors.New("inclusion proof does not verify")
	}
	leaf := sha256.Sum256(append([]byte{0x00}, body...))
	computed, ok := rootFromInclusionProof(uint64(tl.LogIndex), cp.size, leaf[:], hashes)
	if !ok || !bytes.Equal(computed, cp.root) {
		return errors.New("inclusion proof does not verify")
	}
	if !cp.verifiesUnder(root.logKeysAt(t)) {
		return errors.New("checkpoint signature does not verify under a trusted log key")
	}
	return nil
}

// rootFromInclusionProof recomputes the tree root from a leaf hash at
// index of a size-leaf tree and its audit path, by the RFC 9162
// §2.1.3.2 algorithm. ok is false when the path does not fit the tree,
// including a path with hashes left over. The caller guarantees
// index < size.
func rootFromInclusionProof(index, size uint64, leaf []byte, path [][]byte) ([]byte, bool) {
	fn, sn, r := index, size-1, leaf
	for _, p := range path {
		if sn == 0 {
			return nil, false
		}
		if fn&1 == 1 || fn == sn {
			r = interiorHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = interiorHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	return r, sn == 0
}

// interiorHash is the RFC 6962 interior node hash, SHA-256(0x01 || l || r).
func interiorHash(l, r []byte) []byte {
	buf := make([]byte, 0, 1+len(l)+len(r))
	buf = append(append(append(buf, 0x01), l...), r...)
	sum := sha256.Sum256(buf)
	return sum[:]
}

// verifiesUnder reports whether any signature line verifies under any
// of keys: ECDSA over SHA-256 of the note body, Ed25519 over the body.
func (cp checkpoint) verifiesUnder(keys []crypto.PublicKey) bool {
	digest := sha256.Sum256([]byte(cp.body))
	for _, sig := range cp.sigs {
		for _, key := range keys {
			switch k := key.(type) {
			case *ecdsa.PublicKey:
				if ecdsa.VerifyASN1(k, digest[:], sig) {
					return true
				}
			case ed25519.PublicKey:
				if ed25519.Verify(k, []byte(cp.body), sig) {
					return true
				}
			}
		}
	}
	return false
}
