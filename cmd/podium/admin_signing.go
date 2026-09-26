package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/sign"
)

// adminSigningKeyCmd dispatches `podium admin signing-key generate|rotate`,
// which write the registry-managed key file (§4.7.9).
func adminSigningKeyCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("admin signing-key", "Generate or rotate the registry signing key file.", [][2]string{
			{"generate", "Write a new registry signing key file."},
			{"rotate", "Replace the signing key and keep the previous keys as verify: lines."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "generate":
		return signingKeyGenerate(args[1:], os.Stdout, os.Stderr)
	case "rotate":
		return signingKeyRotate(args[1:], os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "unknown signing-key subcommand: %s\n", args[0])
		return 2
	}
}

// signingKeyFlags parses the subcommand's flags. --key-file has no default:
// on an operator's shell PODIUM_SIGN_KEY_PATH is usually unset, and the
// standalone default path would then name the operator's personal key, so
// a bare invocation must not resolve one. ok is false on a parse or usage
// failure, and code is then the exit status.
func signingKeyFlags(name, description string, args []string, stderr io.Writer, staged *string) (path string, code int, ok bool) {
	flags := flag.NewFlagSet("admin signing-key "+name, flag.ContinueOnError)
	setUsage(flags, description)
	flags.SetOutput(stderr)
	keyFile := flags.String("key-file", "", "path to the registry signing key file (required)")
	if staged != nil {
		flags.StringVar(staged, "staged-out", "", "also write the previous file plus a verify: line for the new key, for a multi-replica roll")
	}
	if err := flags.Parse(args); err != nil {
		return "", parseExit(err), false
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", flags.Arg(0))
		flags.Usage()
		return "", 2, false
	}
	if *keyFile == "" {
		fmt.Fprintln(stderr, "error: --key-file is required")
		flags.Usage()
		return "", 2, false
	}
	return *keyFile, 0, true
}

// signingKeyGenerate writes a new key file and refuses to replace an existing
// one, because the key it holds may have signed stored rows.
//
//	podium admin signing-key generate --key-file F
//
// Spec: §4.7.9.
func signingKeyGenerate(args []string, stdout, stderr io.Writer) int {
	path, code, ok := signingKeyFlags("generate", "Write a new registry signing key file.", args, stderr, nil)
	if !ok {
		return code
	}
	_, err := os.Lstat(path)
	if err == nil {
		fmt.Fprintf(stderr, "error: %s already exists; use `podium admin signing-key rotate` to replace its signing key\n", path)
		return 1
	}
	if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	kf, err := newSigningKeyFile(nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := sign.WriteKeyFile(path, kf); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	printKeySet(stdout, kf)
	return 0
}

// signingKeyRotate replaces the signing key and keeps the previous public key
// and every previous verify: key as verify: lines, so rows signed under them
// stay admitted until sign-stored-rows re-signs them. With --staged-out it
// first writes the previous file plus a verify: line for the new key, which a
// multi-replica roll deploys to every replica before the rotated file.
//
//	podium admin signing-key rotate --key-file F [--staged-out S]
//
// Spec: §4.7.9.
func signingKeyRotate(args []string, stdout, stderr io.Writer) int {
	var staged string
	path, code, ok := signingKeyFlags("rotate", "Replace the signing key and keep the previous keys as verify: lines.", args, stderr, &staged)
	if !ok {
		return code
	}
	if staged != "" && filepath.Clean(staged) == filepath.Clean(path) {
		fmt.Fprintln(stderr, "error: --staged-out must name a file other than --key-file")
		return 1
	}
	prev, err := sign.ReadKeyFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	next, err := newSigningKeyFile(append([]ed25519.PublicKey{prev.Public}, prev.Verify...))
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if staged != "" {
		stagedFile := prev
		stagedFile.Verify = append(append([]ed25519.PublicKey(nil), prev.Verify...), next.Public)
		if err := sign.WriteKeyFile(staged, stagedFile); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	if err := sign.WriteKeyFile(path, next); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	printKeySet(stdout, next)
	return 0
}

// newSigningKeyFile generates a keypair and returns a key file that signs
// under it and lists verify as its verification-only keys.
func newSigningKeyFile(verify []ed25519.PublicKey) (sign.KeyFile, error) {
	// crypto/rand.Reader does not return an error on the supported
	// platforms, so the error branch has no test.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return sign.KeyFile{}, fmt.Errorf("generate signing key: %w", err)
	}
	return sign.KeyFile{Private: priv, Public: pub, Verify: verify}, nil
}

// printKeySet writes the key file's verification key set as one
// comma-separated line of base64 keys, signing key first, which is the value
// a consumer's PODIUM_SIGNATURE_VERIFY_KEY takes, and then one line per key
// naming its key_id and role.
func printKeySet(w io.Writer, kf sign.KeyFile) {
	keys := append([]ed25519.PublicKey{kf.Public}, kf.Verify...)
	encoded := make([]string, len(keys))
	for i, k := range keys {
		encoded[i] = base64.StdEncoding.EncodeToString(k)
	}
	fmt.Fprintln(w, strings.Join(encoded, ","))
	for i, k := range keys {
		role := "verify"
		if i == 0 {
			role = "signing"
		}
		fmt.Fprintf(w, "key_id=%s role=%s\n", sign.KeyIDFor(k), role)
	}
}
