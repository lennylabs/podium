package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// substituted answers a request for one ID with the sealed record of
// another, as a registry that labels a record with another ID would.
func substituted(t *testing.T, srvURL *string, otherID string, other stubArtifact) func(map[string]any) {
	return func(resp map[string]any) {
		repl := stubLoadResponse(t, *srvURL, otherID, other, nil)
		for k := range resp {
			delete(resp, k)
		}
		for k, v := range repl {
			resp[k] = v
		}
	}
}

// Spec: §4.7.11 (Request binding, Well-formed requests and records), §7.5 —
// a server-source sync refuses a record the registry served for another ID
// and does not request an ID that breaks the well-formed rule; each refused
// record is listed with its code, keeps its previous output, and every other
// record is written, under no policy.
func TestRun_ServerSourceBindingRefusals(t *testing.T) {
	t.Parallel()
	agent := stubArtifact{typ: "agent", layer: "team", frontmatter: agentArtifactSrc, manifestBody: "Glossary body.\n"}
	var url string
	arts := map[string]stubArtifact{
		"finance/close-books": agent,
		"team/glossary":       agent,
		"personal/CLOSE-~1":   agent,
	}
	srv, counts := newCountedStubRegistry(t, arts, nil)
	url = srv.URL
	target := t.TempDir()
	opts := Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: target, HTTPClient: srv.Client()}
	// The ill-formed ID is refused before any request, so the first run
	// already reports it and writes the well-formed records.
	if res, err := Run(opts); err != nil || len(res.Dropped) != 1 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	for _, r := range counts.seen() {
		if strings.Contains(r.path, "CLOSE") {
			t.Fatalf("the ill-formed ID was requested: %+v", r)
		}
	}
	before := lockEntriesOf(t, target, "finance/close-books")
	if len(before) == 0 {
		t.Fatal("no entry for finance/close-books after the first run")
	}

	a := arts["finance/close-books"]
	a.mutate = substituted(t, &url, "team/glossary", agent)
	arts["finance/close-books"] = a
	res, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	d := dropOf(t, res, "finance/close-books")
	if d.Code != attest.CodeRequestMismatch || d.Details["constraint"] != attest.ConstraintID || !d.PreviousOutputKept {
		t.Fatalf("substituted = %+v", d)
	}
	if d := dropOf(t, res, "personal/CLOSE-~1"); d.Details["constraint"] != attest.ConstraintIDForm {
		t.Fatalf("ill-formed = %+v", d)
	}
	if got := lockEntriesOf(t, target, "finance/close-books"); len(got) != len(before) {
		t.Fatalf("the refused record's entries = %+v, want %+v", got, before)
	}
	if got := lockEntriesOf(t, target, "team/glossary"); len(got) == 0 {
		t.Fatal("the other record was not written")
	}
}

// Spec: §7.5 (Author attestation), §4.7.11 (Attestation check, step 7) — a
// server-source record that carries no attestation reference is refused
// under require with attestation_missing, and a resource path that breaks
// the well-formed rule refuses the record under every mode.
func TestRun_ServerSourceAttestationMissingAndResourcePath(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	agent := stubArtifact{typ: "agent", layer: "team", frontmatter: agentArtifactSrc, manifestBody: "Glossary body.\n"}
	bad := agent
	bad.resources = map[string]string{`notes\x.md`: "n\n"}
	srv := newStubRegistry(t, map[string]stubArtifact{"finance/pay": agent, "personal/x": bad})
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	res, err := Run(Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: t.TempDir(), HTTPClient: srv.Client(), Attestation: f})
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "finance/pay"); d.Code != attest.CodeAttestationMissing {
		t.Fatalf("finance/pay = %+v", d)
	}
	if d := dropOf(t, res, "personal/x"); d.Code != attest.CodeRequestMismatch || d.Details["constraint"] != attest.ConstraintResourcePath {
		t.Fatalf("personal/x = %+v", d)
	}
}

// Spec: §7.5 (Author attestation) — the defaults.attestation block decodes
// each key, resolves relative paths against the file's directory, treats a
// null as unset, and refuses a block that is not a mapping, an unknown or
// repeated key, and a value of another type or outside its values.
func TestReadAttestationBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(body string) string {
		t.Helper()
		p := filepath.Join(dir, "sync.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	b, err := ReadAttestationBlock(write(`defaults:
  registry: x
  attestation:
    policy: require
    policy_file: p.json
    trusted_root_file: /abs/root.json
    root_max_age: 24h
    overlay: deny
    pin_signers: true
    layer_roots:
      team: agents
    policy_file_unused: ~
`))
	if err == nil {
		t.Fatalf("an unknown key = %+v", b)
	}
	b, err = ReadAttestationBlock(write(`defaults:
  attestation: &a
    policy: require
    policy_file: p.json
    trusted_root_file: /abs/root.json
    root_max_age: 24h
    overlay: deny
    pin_signers: true
    layer_roots:
      team: agents
`))
	if err != nil || b.Policy != attest.ModeRequire || b.PolicyFile != filepath.Join(dir, "p.json") || b.TrustedRootFile != "/abs/root.json" ||
		!b.RootMaxAgeSet || b.RootMaxAge.Hours() != 24 || b.Overlay != OverlayDeny || !b.PinSigners || b.LayerRoots["team"] != "agents" {
		t.Fatalf("block = %+v, %v", b, err)
	}
	for _, body := range []string{"defaults:\n  registry: x\n", "x: 1\n", "", "defaults:\n  attestation: ~\n", "defaults: 3\n", "- a\n"} {
		if b, err := ReadAttestationBlock(write(body)); err != nil || b != nil {
			t.Errorf("%q = %+v, %v, want no block", body, b, err)
		}
	}
	for _, body := range []string{
		"defaults:\n  attestation: [a]\n",
		"defaults:\n  attestation:\n    policy: strict\n",
		"defaults:\n  attestation:\n    policy: 3\n",
		"defaults:\n  attestation:\n    pin_signers: yes please\n",
		"defaults:\n  attestation:\n    root_max_age: soon\n",
		"defaults:\n  attestation:\n    overlay: maybe\n",
		"defaults:\n  attestation:\n    layer_roots: [a]\n",
		"defaults:\n  attestation:\n    layer_roots:\n      team: ../x\n",
		"defaults:\n  attestation:\n    layer_roots:\n      team: [a]\n",
		"defaults:\n  attestation:\n    policy: warn\n    policy: none\n",
		"defaults: [unclosed\n",
	} {
		if _, err := ReadAttestationBlock(write(body)); err == nil || !strings.Contains(err.Error(), attest.CodePolicyUnavailable) {
			t.Errorf("%q = %v, want a refusal", body, err)
		}
	}
	if b, err := ReadAttestationBlock(filepath.Join(dir, "absent.yaml")); err != nil || b != nil {
		t.Errorf("an absent file = %+v, %v", b, err)
	}
	if _, err := ReadAttestationBlock(dir); err == nil {
		t.Error("a directory must not read as a file")
	}
}
