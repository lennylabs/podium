package version

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrLinkedHashMismatch signals that a fetched or decoded resource body does
// not match the content hash its link or batch reference carries. It carries
// no §6.10 code; each caller wraps it with the code its surface returns.
var ErrLinkedHashMismatch = errors.New("content hash mismatch")

// ResourceDigest returns "sha256:" followed by the lowercase hexadecimal
// SHA-256 digest of body, the form a resource content hash takes in the
// §4.7.10 delivery record and on a link object.
// Spec: §4.7.10
func ResourceDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ResourceHashes builds the resource map of a §4.7.10 delivery record. Every
// path in either map appears in the result. A path that linkHashes contains
// takes that value even when it is empty, and every other path takes the
// digest of its body.
//
// The membership test is deliberate. Falling back to the digest of the
// fetched body when a link's content_hash is empty would frame a hash the
// registry never attested, so a record that §4.7.10 step 5 frames with the
// empty value, and that the SDKs refuse, would verify here.
// Spec: §4.7.10
func ResourceHashes(bodies map[string][]byte, linkHashes map[string]string) map[string]string {
	out := make(map[string]string, len(bodies)+len(linkHashes))
	for path, body := range bodies {
		out[path] = ResourceDigest(body)
	}
	for path, hash := range linkHashes {
		out[path] = hash
	}
	return out
}

// CheckLinked applies the §4.7.10 step 6 check to one body: it compares the
// digest of body with want as exact strings. An empty want is not checked on
// its own, because the record frames the empty value and the delivery-hash
// comparison refuses it.
// Spec: §4.7.10
func CheckLinked(body []byte, want string) error {
	if want == "" {
		return nil
	}
	if got := ResourceDigest(body); got != want {
		return fmt.Errorf("%w: got %s want %s", ErrLinkedHashMismatch, got, want)
	}
	return nil
}
