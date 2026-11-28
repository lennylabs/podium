package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sync"
)

// reviewAgentDoc is the agent the layer_roots test signs under another root.
const reviewAgentDoc = "---\ntype: agent\nversion: 1.0.0\ndescription: pay\n---\n\nPay.\n"

// Spec: §7.5 (Author attestation), §13.11.3 (Layer subject) — layer_roots
// takes effect from ~/.podium/sync.yaml alone: a workspace sync.yaml that
// maps the user's layer to another root is ignored with a notice, so the
// complete signed content of that root stays refused under the user's
// require.
func TestLoadSyncAttestation_WorkspaceCannotOverrideUserLayerRoot(t *testing.T) {
	h := sh.New(t)
	home, ws := t.TempDir(), t.TempDir()
	policy := `{"version":1,"instances":[{"name":"github","trusted_root_file":"github.json","transparency_log":"none","certificate_transparency":"none"}],"rules":[` +
		`{"ids":["finance/**"],"mode":"require","requirements":[{"predicate_type":"https://slsa.dev/provenance/v1","signers":[{"issuer":"` + sh.DefaultIssuer +
		`","identity":"` + sh.DefaultSAN + `","instance":"github"}]}]}]}`
	writePolicyDir(t, h, filepath.Join(home, ".podium"), "user.json", policy)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"),
		[]byte("defaults:\n  attestation:\n    policy_file: user.json\n    layer_roots:\n      team: prod\n"), 0o600))

	// The layer directory holds the complete signed content of root staging.
	reg := filepath.Join(t.TempDir(), "team")
	testharness.WriteTree(t, reg, testharness.WriteTreeOption{Path: "finance/pay/ARTIFACT.md", Content: reviewAgentDoc})
	content, err := filesystem.LayerContent(filesystem.Layer{ID: "team", Path: reg})
	must(err)
	subs := []sh.Subject{{Name: ".podium/layer/staging", SHA256: strings.TrimPrefix(attest.LayerDigest(content), "sha256:")}}
	for _, s := range content.Subjects {
		subs = append(subs, sh.Subject{Name: s.Name, SHA256: strings.TrimPrefix(s.Digest, "sha256:")})
	}
	bundle := h.DSSEBundle(sh.Statement(t, "https://slsa.dev/provenance/v1", subs...),
		sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	must(os.MkdirAll(filepath.Join(reg, ".podium", "attestations"), 0o755))
	must(os.WriteFile(filepath.Join(reg, ".podium", "attestations", attest.BundleFileName(bundle)), bundle, 0o644))

	var stderr bytes.Buffer
	run := func() *sync.Result {
		t.Helper()
		att, err := loadSyncAttestation(syncInputs(nil, home, ws, &stderr))
		must(err)
		res, err := sync.Run(sync.Options{RegistryPath: reg, Target: t.TempDir(), Attestation: func() (*sync.Attestation, error) { return att, nil }})
		must(err)
		return res
	}
	// Control: the user's own setting refuses the staging content.
	if res := run(); len(res.Dropped) != 1 {
		t.Fatalf("control: want finance/pay refused, got %+v", res)
	}
	// The repository's sync.yaml re-points the layer to staging.
	must(os.MkdirAll(filepath.Join(ws, ".podium"), 0o755))
	must(os.WriteFile(filepath.Join(ws, ".podium", "sync.yaml"),
		[]byte("defaults:\n  attestation:\n    layer_roots:\n      team: staging\n"), 0o600))
	res := run()
	if len(res.Dropped) != 1 || res.Dropped[0].Details["constraint"] != attest.ConstraintLayerDigest || len(res.Artifacts) != 0 {
		t.Fatalf("the workspace layer_roots took effect: artifacts %+v dropped %+v", res.Artifacts, res.Dropped)
	}
	if !strings.Contains(stderr.String(), "defaults.attestation.layer_roots takes effect from ~/.podium/sync.yaml alone") {
		t.Fatalf("stderr = %q, want the ignored key named", stderr.String())
	}
}
