// Command sigstoreroot fetches the public-good Sigstore trusted root through
// the Sigstore TUF repository and writes it over the copy that pkg/sign
// embeds, or with -check compares the two and exits non-zero when they
// differ. The release workflow runs the check, because a release binary
// counts the age of its embedded root from its build date (§4.7.11), which
// holds only when the embedded root was current at that date.
//
// Usage, from the module root:
//
//	go run ./tools/sigstoreroot          # refresh the embedded copy
//	go run ./tools/sigstoreroot -check   # fail when it is not current
//
// The exit status is 0 on success, 1 when the copy differs from the target
// or cannot be read or written, 2 on a usage error, and 3 when the target
// cannot be fetched, so a caller retries only a fetch that failed.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/lennylabs/podium/pkg/sign"
)

// The exit statuses.
const (
	exitOK       = 0
	exitMismatch = 1
	exitUsage    = 2
	exitFetch    = 3
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is main with its arguments and streams injected for the tests.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sigstoreroot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "compare the embedded copy with the current target and exit 1 when they differ")
	out := fs.String("file", sign.EmbeddedPublicGoodRootPath, "the embedded copy")
	mirror := fs.String("mirror", tuf.DefaultMirror, "the TUF repository")
	timeout := fs.Duration("timeout", 2*time.Minute, "deadline of the fetch")
	tufRoot := fs.String("tuf-root", "", "a TUF root.json to start from instead of the one sigstore-go embeds")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	current, err := fetchCurrent(ctx, *mirror, *tufRoot, *timeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sigstoreroot: %v\n", err)
		return exitFetch
	}
	if *check {
		return compare(*out, current, stdout, stderr)
	}
	if err := os.WriteFile(*out, current, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "sigstoreroot: write %s: %v\n", *out, err)
		return exitMismatch
	}
	r, _ := sign.ParseAttestationRoot(current)
	_, _ = fmt.Fprintf(stdout, "wrote %s (%s)\n", *out, r.Digest())
	return exitOK
}

// fetchCurrent returns the verified trusted_root.json target, checked
// against the load rules the registry applies to a public-good root.
func fetchCurrent(ctx context.Context, mirror, tufRootPath string, timeout time.Duration) ([]byte, error) {
	root := tuf.DefaultRoot()
	if tufRootPath != "" {
		raw, err := os.ReadFile(tufRootPath)
		if err != nil {
			return nil, fmt.Errorf("read TUF root: %w", err)
		}
		root = raw
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return sign.FetchPublicGoodRoot(ctx, mirror, root, http.DefaultClient)
}

// compare reports whether the embedded copy at path equals current.
func compare(path string, current []byte, stdout, stderr io.Writer) int {
	have, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sigstoreroot: read %s: %v\n", path, err)
		return exitMismatch
	}
	if !bytes.Equal(have, current) {
		_, _ = fmt.Fprintf(stderr, "sigstoreroot: %s differs from the current public-good trusted root; run `go run ./tools/sigstoreroot` and commit the result\n", path)
		return exitMismatch
	}
	_, _ = fmt.Fprintf(stdout, "%s is current\n", path)
	return exitOK
}
