// Command minttoken generates a runtime RSA signing key and mints RS256
// injected-session-token JWTs for the manual validation scenarios in
// test/manual-validation.md. A standalone registry started with
// PODIUM_IDENTITY_PROVIDER=injected-session-token verifies these tokens once
// the public key is registered with `podium admin runtime register`.
//
// First run, which writes a keypair under --keys and names the register
// command to run:
//
//	go run ./tools/minttoken --keys "$WORK/keys" --register-cmd "$PODIUM_REGISTRY"
//
// Mint a caller token (the signed JWT is the only thing printed to stdout, so
// it can be captured directly):
//
//	TOKEN=$(go run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --groups engineering)
//
// The claims mirror the §6.3.2 set the server verifies: iss, aud, sub, act,
// exp, and the optional groups, email, org_id, and scope claims. The issuer
// and audience defaults match the values the manual validation doc uses; pass
// --iss / --aud to change them, and keep --aud equal to the server's
// PODIUM_OAUTH_AUDIENCE.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	keys := flag.String("keys", "mint-keys", "directory holding the runtime keypair (created on first use)")
	sub := flag.String("sub", "", "token subject (OIDC sub); when set, a token is minted and printed to stdout")
	groups := flag.String("groups", "", "comma-separated groups claim")
	org := flag.String("org", "", "org_id claim")
	email := flag.String("email", "", "email claim")
	scope := flag.String("scope", "", "space-separated scope claim")
	iss := flag.String("iss", "manual-runtime", "issuer (must match the registered runtime)")
	aud := flag.String("aud", "https://podium.manual", "audience (must equal the server's PODIUM_OAUTH_AUDIENCE)")
	ttl := flag.Duration("ttl", 30*time.Minute, "token lifetime")
	registerFor := flag.String("register-cmd", "", "print the `podium admin runtime register` command for this registry URL, then exit")
	flag.Parse()

	priv, pubPath, created, err := loadOrCreateKey(*keys)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if created {
		fmt.Fprintf(os.Stderr, "Generated a runtime keypair under %s\n", *keys)
	}

	registerCmd := fmt.Sprintf(
		"podium admin runtime register --registry %s --issuer %s --algorithm RS256 --public-key-file %s",
		"$PODIUM_REGISTRY", *iss, pubPath)
	if *registerFor != "" {
		registerCmd = strings.Replace(registerCmd, "$PODIUM_REGISTRY", *registerFor, 1)
		fmt.Println(registerCmd)
		return
	}
	if *sub == "" {
		fmt.Fprintf(os.Stderr, "Public key: %s\nRegister it, then re-run with --sub to mint a token:\n  %s\n", pubPath, registerCmd)
		return
	}

	claims := jwt.MapClaims{
		"iss": *iss,
		"aud": *aud,
		"sub": *sub,
		"act": *iss,
		"exp": time.Now().Add(*ttl).Unix(),
	}
	if *groups != "" {
		claims["groups"] = splitComma(*groups)
	}
	if *org != "" {
		claims["org_id"] = *org
	}
	if *email != "" {
		claims["email"] = *email
	}
	if *scope != "" {
		claims["scope"] = *scope
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(priv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: sign:", err)
		os.Exit(1)
	}
	fmt.Println(signed)
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// loadOrCreateKey returns the RSA private key under dir, generating and
// persisting a fresh RSA-2048 keypair (PKCS#8 private, PKIX public) when none
// exists yet. The bool reports whether a new key was created.
func loadOrCreateKey(dir string) (*rsa.PrivateKey, string, bool, error) {
	privPath := filepath.Join(dir, "runtime-priv.pem")
	pubPath := filepath.Join(dir, "runtime-pub.pem")

	if b, err := os.ReadFile(privPath); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, "", false, fmt.Errorf("decode %s", privPath)
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", false, err
		}
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, "", false, fmt.Errorf("%s is not an RSA key", privPath)
		}
		return rk, pubPath, false, nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", false, err
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", false, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, "", false, err
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil {
		return nil, "", false, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, "", false, err
	}
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600); err != nil {
		return nil, "", false, err
	}
	return priv, pubPath, true, nil
}
