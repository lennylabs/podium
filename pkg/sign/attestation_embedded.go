package sign

import _ "embed"

// embeddedPublicGoodRoot is the public-good trusted_root.json that was
// current when the binary was built. `go run ./tools/sigstoreroot` replaces
// it from the Sigstore TUF repository, and the release workflow refuses a
// tag whose copy differs from the current target.
//
//go:embed embedded/public-good.trusted_root.json
var embeddedPublicGoodRoot []byte

// EmbeddedPublicGoodRootPath is the repository path of the embedded copy,
// relative to the module root.
const EmbeddedPublicGoodRootPath = "pkg/sign/embedded/public-good.trusted_root.json"

// EmbeddedPublicGoodRoot returns a copy of the embedded public-good root.
// The public-good source uses it when no verified copy is on disk.
//
// Spec: §4.7.11.
func EmbeddedPublicGoodRoot() []byte {
	return append([]byte(nil), embeddedPublicGoodRoot...)
}
