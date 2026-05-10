package main

import (
	"flag"
	"fmt"
	"os"
)

// impactCmd queries /v1/dependents and prints the reverse-dependency
// edges for the given artifact, mapping to spec §4.7.5 impact analysis.
//
//	podium impact <artifact-id>
func impactCmd(args []string) int {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	setUsage(fs, "List artifacts that depend on a given artifact.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium impact <artifact-id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/dependents?id="+fs.Arg(0), "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "impact failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}
