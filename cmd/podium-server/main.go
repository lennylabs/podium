// Command podium-server runs the Podium registry as a long-lived
// HTTP server. The standalone deployment (§13.10) bundles SQLite +
// filesystem object storage; the standard deployment (§13.1) wires
// Postgres + S3-compatible object storage + an OIDC IdP via env
// vars per §13.12.
//
// The bootstrap lives in `internal/serverboot` so the same binary
// can run as `podium serve`. Default behavior matches §13.10: zero
// flags, SQLite + filesystem object store + no auth bound on
// 127.0.0.1:8080.
//
// `podium-server sign-stored-rows [--include-unsigned] [--dry-run]` runs the
// §13.4 sign-stored-rows command and exits without binding a listener.
package main

import (
	"context"
	"log"
	"os"

	"github.com/lennylabs/podium/internal/serverboot"
)

func main() {
	// sign-stored-rows is the §13.4 one-shot command. The image ships only
	// this binary, so the command runs here as well as under podium admin.
	if len(os.Args) > 1 && os.Args[1] == "sign-stored-rows" {
		err := serverboot.RunSignStoredRows(context.Background(), os.Args[2:])
		os.Exit(serverboot.SignStoredRowsExitCode(os.Stderr, err))
	}
	if err := serverboot.Run(); err != nil {
		log.Fatal(err)
	}
}
