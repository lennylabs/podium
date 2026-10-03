package sigstoreharness

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

// checkpointBody is the signed-note body of a checkpoint: the origin, the
// decimal tree size, and the base64 root hash, each ending in a newline.
func checkpointBody(size int, root []byte) string {
	return fmt.Sprintf("%s\n%d\n%s\n", checkpointOrigin, size, base64.StdEncoding.EncodeToString(root))
}

// signNote appends a signed-note signature line by key to body. The line
// carries a 4-byte key hint taken from SHA-256 of the PKIX public key;
// the §4.7.9 verifier tries every trusted key and does not read the hint.
// ECDSA signs SHA-256(body) and Ed25519 signs body, as Rekor v2 does.
func signNote(body string, key crypto.Signer) (string, error) {
	pkixPub, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return "", fmt.Errorf("checkpoint key: %w", err)
	}
	hint := sha256.Sum256(pkixPub)
	var sig []byte
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		digest := sha256.Sum256([]byte(body))
		sig, err = ecdsa.SignASN1(rand.Reader, k, digest[:])
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, []byte(body))
	default:
		err = fmt.Errorf("unsupported checkpoint key %T", key)
	}
	if err != nil {
		return "", fmt.Errorf("sign checkpoint: %w", err)
	}
	line := base64.StdEncoding.EncodeToString(append(hint[:4], sig...))
	return body + "\n" + "— " + checkpointOrigin + " " + line + "\n", nil
}

// signedInclusion proves body at index of a size-leaf tree and signs the
// checkpoint with key.
func signedInclusion(body []byte, size, index int, key crypto.Signer) (inclusion, error) {
	inc, err := prove(body, size, index)
	if err != nil {
		return inclusion{}, err
	}
	inc.checkpoint, err = signNote(checkpointBody(size, inc.root), key)
	return inc, err
}
