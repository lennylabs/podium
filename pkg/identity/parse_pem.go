package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedKey is returned when ParsePublicKeyPEM is asked
// to validate a key whose type doesn't match algorithm.
var ErrUnsupportedKey = errors.New("identity: unsupported public key type")

// ParsePublicKeyPEM decodes a PKIX public key PEM block and
// confirms it matches algorithm. algorithm follows JWS naming
// (RS256 / RS384 / RS512 → *rsa.PublicKey, ES256 / ES384 / ES512
// → *ecdsa.PublicKey, EdDSA → ed25519.PublicKey). The matched key
// is returned with the strict type the JWT verifier wants.
func ParsePublicKeyPEM(pemData, algorithm string) (any, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("identity: no PEM block found")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// Fall back to RSA-only PKCS#1 PEMs the operator may paste.
		rsaPub, err2 := x509.ParsePKCS1PublicKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("identity: parse PKIX key: %w", err)
		}
		pub = rsaPub
	}
	switch alg := strings.ToUpper(algorithm); {
	case strings.HasPrefix(alg, "RS"):
		k, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: want RSA, got %T", ErrUnsupportedKey, pub)
		}
		return k, nil
	case strings.HasPrefix(alg, "ES"):
		k, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: want ECDSA, got %T", ErrUnsupportedKey, pub)
		}
		return k, nil
	case alg == "EDDSA":
		k, ok := pub.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: want Ed25519, got %T", ErrUnsupportedKey, pub)
		}
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown algorithm %q", ErrUnsupportedKey, algorithm)
}
