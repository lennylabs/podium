package main

import (
	"flag"
	"fmt"
	"os"
)

// adminRuntimeCmd dispatches `podium admin runtime <register|list>`
// for §6.3.2 trusted-runtime management.
func adminRuntimeCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin runtime <register|list> [flags]")
		return 2
	}
	switch args[0] {
	case "register":
		return adminRuntimeRegister(args[1:])
	case "list":
		return adminRuntimeList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown runtime subcommand: %s\n", args[0])
		return 2
	}
}

// adminRuntimeRegister POSTs a runtime trust key.
//
//	podium admin runtime register --issuer ... --algorithm RS256
//	  --public-key-file path/to/key.pem [--registry URL]
func adminRuntimeRegister(args []string) int {
	fs := flag.NewFlagSet("admin runtime register", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	issuer := fs.String("issuer", "", "issuer name (required)")
	algorithm := fs.String("algorithm", "", "JWS algorithm (RS256, ES256, EdDSA, ...)")
	keyFile := fs.String("public-key-file", "", "path to PEM-encoded public key (required)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" || *issuer == "" || *algorithm == "" || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "error: --registry, --issuer, --algorithm, and --public-key-file are required")
		return 2
	}
	pemBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read %s: %v\n", *keyFile, err)
		return 1
	}
	body := map[string]any{
		"issuer":         *issuer,
		"algorithm":      *algorithm,
		"public_key_pem": string(pemBytes),
	}
	out, status := doJSON(*registry+"/v1/admin/runtime", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "register failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func adminRuntimeList(args []string) int {
	fs := flag.NewFlagSet("admin runtime list", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/admin/runtime", "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "list failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
