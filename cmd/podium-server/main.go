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
package main

import (
	"log"

	"github.com/lennylabs/podium/internal/serverboot"
)

func main() {
	if err := serverboot.Run(); err != nil {
		log.Fatal(err)
	}
}
