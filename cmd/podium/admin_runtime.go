package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lennylabs/podium/pkg/identity"
)

// adminRuntimeCmd dispatches `podium admin runtime register`
// for §6.3.2 trusted-runtime management.
func adminRuntimeCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("admin runtime", "Manage trusted runtime signing keys.", [][2]string{
			{"register", "Write a trusted runtime signing key into the registry's keys file."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "register":
		return adminRuntimeRegister(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown runtime subcommand: %s\n", args[0])
		return 2
	}
}

// adminRuntimeRegister writes a runtime trust key into the local
// keys file the registry reads at startup (§6.3.2). The registry
// exposes no request-time registration API, so this command talks
// to the file rather than to a server, and the registry picks the
// record up on its next boot.
//
//	podium admin runtime register --keys-file path/to/runtimes.json
//	  --issuer ... --algorithm RS256 --public-key-file path/to/key.pem
//
// --keys-file takes no environment default. PODIUM_RUNTIME_KEYS_PATH
// is a registry-process variable (§13.12), so defaulting to it on the
// operator's shell would usually resolve to an empty path and report
// the library's error in place of the flag-validation message below.
func adminRuntimeRegister(args []string) int {
	fs := flag.NewFlagSet("admin runtime register", flag.ContinueOnError)
	setUsage(fs, "Write a trusted runtime signing key into the registry's keys file.")
	keysFile := fs.String("keys-file", "", "path to the registry's runtime keys file (required)")
	issuer := fs.String("issuer", "", "issuer name (required)")
	algorithm := fs.String("algorithm", "", "JWS algorithm (RS256, ES256, EdDSA, ...)")
	keyFile := fs.String("public-key-file", "", "path to PEM-encoded public key (required)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *keysFile == "" || *issuer == "" || *algorithm == "" || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "error: --keys-file, --issuer, --algorithm, and --public-key-file are required")
		return 2
	}
	pemBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read %s: %v\n", *keyFile, err)
		return 1
	}
	// Parsing here rather than at boot means a key that does not match
	// its declared algorithm fails at authoring time, where the operator
	// can still fix it, instead of aborting the registry's next start.
	pub, err := identity.ParsePublicKeyPEM(string(pemBytes), *algorithm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse %s: %v\n", *keyFile, err)
		return 1
	}
	// LoadFilePersistedRuntimeKeyRegistry reads the existing records and
	// Register rewrites the whole file, so the command is a load-modify-write
	// over a file with a single writer.
	reg, err := identity.LoadFilePersistedRuntimeKeyRegistry(*keysFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load %s: %v\n", *keysFile, err)
		return 1
	}
	if err := reg.Register(identity.RuntimeKey{
		Issuer:    *issuer,
		Algorithm: *algorithm,
		Key:       pub,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: register %s: %v\n", *issuer, err)
		return 1
	}
	fmt.Printf("Registered runtime %s (%s) in %s. Restart the registry to load it.\n", *issuer, *algorithm, *keysFile)
	return 0
}
