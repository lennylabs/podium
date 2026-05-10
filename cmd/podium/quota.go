package main

import (
	"flag"
	"fmt"
	"os"
)

// quotaCmd queries /v1/quota and prints the limits + current usage
// for the configured tenant.
//
//	podium quota [--registry URL]
func quotaCmd(args []string) int {
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	setUsage(fs, "Print the tenant's quotas and current usage.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/quota", "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "quota failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
