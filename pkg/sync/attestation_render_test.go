package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
)

// Spec: §7.8 (Author attestation) — a render whose plugin selects a record
// that the attestation check refuses under require fails before it writes a
// file; a record that no plugin selects is not checked, so it cannot fail the
// render; under warn the render writes the record and reports the warning.
func TestRender_AttestationRefusalFailsBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	testharness.WriteTree(t, reg, testharness.WriteTreeOption{Path: "personal/notes/ARTIFACT.md", Content: agentArtifactSrc})
	signLayer(t, h, reg, ".podium/layer")
	workdir := t.TempDir()
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/ap/**"),
		signerRule("require", "bob@acme.com", "finance/close/**", "personal/**"))
	opts := RenderOptions{OutputID: "acme", Registry: reg, Workdir: workdir, Harnesses: []string{"claude-code"},
		Plugins: []PluginFilter{{Name: "pack", Include: []string{"finance/**"}}}, Attestation: f}
	_, err := Render(context.Background(), opts)
	var refused *RecordRefusedError
	if !errors.As(err, &refused) || refused.ID != "finance/close/books" || !strings.Contains(err.Error(), attest.CodeAttestationUntrusted) {
		t.Fatalf("Render = %v, want finance/close/books refused", err)
	}
	if entries, _ := os.ReadDir(workdir); len(entries) != 0 {
		t.Fatalf("the failed render wrote %v", entries)
	}

	opts.Plugins = []PluginFilter{{Name: "pack", Include: []string{"finance/ap/**"}}}
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("records no plugin selects must not fail the render: %v", err)
	}

	_, warn := testAttestation(t, h, "", signerRule("warn", "bob@acme.com", "finance/**"))
	opts.Attestation = warn
	opts.Plugins = []PluginFilter{{Name: "pack", Include: []string{"finance/**"}}}
	res, err := Render(context.Background(), opts)
	if err != nil || len(res.Warnings) != 2 {
		t.Fatalf("warn render = %+v, %v", res, err)
	}
}

// Spec: §7.8 (Author attestation) — a record the well-formed rule refuses
// fails the render whether or not a plugin selects it.
func TestRender_WellFormedRefusalFailsTheTarget(t *testing.T) {
	t.Parallel()
	reg := twoAgentLayer(t)
	testharness.WriteTree(t, reg, testharness.WriteTreeOption{Path: "personal/con/ARTIFACT.md", Content: agentArtifactSrc})
	workdir := t.TempDir()
	_, err := Render(context.Background(), RenderOptions{OutputID: "acme", Registry: reg, Workdir: workdir,
		Harnesses: []string{"claude-code"}, Plugins: []PluginFilter{{Name: "pack", Include: []string{"finance/**"}}}})
	if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeRequestMismatch || fl.Constraint != attest.ConstraintIDForm {
		t.Fatalf("Render = %v, want request_mismatch (id_form)", err)
	}
	if _, _, err := FetchRecords(Options{RegistryPath: reg}); err == nil {
		t.Fatal("FetchRecords must refuse a record the well-formed rule refuses")
	}
}

// Spec: §7.8 (Author attestation) — RunMarketplace runs no publish command
// after a render that a refusal fails.
func TestRunMarketplace_NoPublishAfterARefusedRender(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	reg := twoAgentLayer(t)
	_, f := testAttestation(t, h, attest.ModeRequire, signerRule("require", sh.DefaultSAN, "finance/**"))
	workdir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "published")
	_, err := RunMarketplace(context.Background(), RunOptions{
		Output: ResolvedOutput{ID: "acme", Registry: reg, Harnesses: []string{"claude-code"},
			Plugins:  []PluginFilter{{Name: "pack", Include: []string{"finance/**"}}},
			Workflow: Workflow{Publish: []Command{{Run: []string{"touch", marker}}}}},
		Workdir: workdir, Attestation: f, Stdout: os.Stderr,
	})
	if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeAttestationMissing {
		t.Fatalf("RunMarketplace = %v, want attestation_missing", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publish ran after a refused render: %v", err)
	}
}

// Spec: §7.8 (Plugin names) — config validation refuses a plugin name that
// is empty, holds a slash, a backslash, or a control character, is a dot
// segment, begins with a dot, or is not a portable name, and a name two
// plugins share without regard to case.
func TestValidatePluginNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "../x", ".h", "a/b", `a\b`, "con", "a.", "x~1", ".", "..", "a\x01b", "CON.txt"} {
		err := ValidatePluginNames("acme", []PluginFilter{{Name: name}})
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("name %q = %v, want config.invalid", name, err)
		}
	}
	if err := ValidatePluginNames("acme", []PluginFilter{{Name: "Finance"}, {Name: "finance"}}); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("a shared name = %v", err)
	}
	if err := ValidatePluginNames("acme", []PluginFilter{{Name: "finance-pack"}, {Name: "security"}}); err != nil {
		t.Errorf("valid names = %v", err)
	}
	if err := ValidateOutput(ResolvedOutput{ID: "acme", Plugins: []PluginFilter{{Name: "con"}}}); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("ValidateOutput = %v", err)
	}
	cfg := &SyncConfig{Defaults: Defaults{Registry: "/r"}, Targets: []TargetEntry{{ID: "m", Kind: KindMarketplace, Target: "/t",
		Harnesses: []string{"claude-code"}, Plugins: []PluginFilter{{Name: "nul"}}}}}
	if _, err := PlanMultiTarget(cfg, PlanInput{}); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("PlanMultiTarget = %v", err)
	}
}
