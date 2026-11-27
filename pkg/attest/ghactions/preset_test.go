package ghactions

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

var publicRecord = Record{ID: 123456, OwnerID: 7890, FullName: "acme/finance", DefaultBranch: "trunk", Visibility: "public"}

// Spec: §7.3.1 (Trust preset) — the preset resolves mode require, the ref
// podium/signed, and one signer with the GitHub Actions issuer, the
// identity of the repository's podium-attest workflow on its default
// branch, and a binding by URL, repository identifier, owner identifier,
// and the default branch's ref.
func TestResolve_Defaults(t *testing.T) {
	t.Parallel()
	p, err := Resolve(publicRecord, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	want := store.TrustSigner{
		Issuer:                  "https://token.actions.githubusercontent.com",
		Identity:                "https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/trunk",
		SourceRepository:        "https://github.com/acme/finance",
		SourceRepositoryID:      "123456",
		SourceRepositoryOwnerID: "7890",
		SourceRepositoryRef:     "refs/heads/trunk",
	}
	if p.Mode != "require" || p.Ref != "podium/signed" || len(p.Requirement.Signers) != 1 || p.Requirement.Signers[0] != want {
		t.Errorf("preset = %+v", p)
	}
}

// Spec: §7.3.1 (Trust preset) — an explicit --trust or --ref replaces the
// mode or the ref, and each explicit --trust-signer replaces the issuer and
// the identity and keeps the binding.
func TestResolve_Overrides(t *testing.T) {
	t.Parallel()
	shared := Issuer + "=" + SharedIdentity(Repo{"acme", ".github"})
	p, err := Resolve(publicRecord, Overrides{Mode: "warn", Ref: "signed", Signers: []string{shared, "https://accounts.google.com=alice@acme.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "warn" || p.Ref != "signed" || len(p.Requirement.Signers) != 2 {
		t.Fatalf("preset = %+v", p)
	}
	s := p.Requirement.Signers[0]
	if s.Identity != "https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main" ||
		s.SourceRepositoryID != "123456" || s.SourceRepositoryRef != "refs/heads/trunk" {
		t.Errorf("shared signer = %+v", s)
	}
	if s := p.Requirement.Signers[1]; s.Issuer != "https://accounts.google.com" || s.Identity != "alice@acme.com" || s.SourceRepositoryOwnerID != "7890" {
		t.Errorf("second signer = %+v", s)
	}
	for _, bad := range []string{"no-equals", "=x", "x="} {
		if _, err := Resolve(publicRecord, Overrides{Signers: []string{bad}}); err == nil {
			t.Errorf("signer %q accepted", bad)
		}
	}
}

// Spec: §7.3.1 (Trust preset) — a refusal names the explicit flags: with
// the record's values when it was read, with placeholders when it was not,
// and with the github instance for a private repository.
func TestExplicitFlags(t *testing.T) {
	t.Parallel()
	got := ExplicitFlags(Repo{"acme", "finance"}, publicRecord, "", PrivateInstance)
	for _, want := range []string{
		"--ref podium/signed", "--trust require",
		"--trust-signer 'https://token.actions.githubusercontent.com=https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/trunk'",
		"--trust-source-repository https://github.com/acme/finance", "--trust-source-repository-id 123456",
		"--trust-source-owner-id 7890", "--trust-source-repository-ref refs/heads/trunk", "--trust-instance github",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("flags lack %q:\n%s", want, got)
		}
	}
	got = ExplicitFlags(Repo{"acme", "finance"}, Record{}, "i=x", "")
	for _, want := range []string{"<repository-id>", "<owner-id>", "refs/heads/<default-branch>", "--trust-signer 'i=x'", "https://github.com/acme/finance"} {
		if !strings.Contains(got, want) {
			t.Errorf("placeholder flags lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--trust-instance") {
		t.Errorf("flags name an instance:\n%s", got)
	}
	if !strings.Contains(GenericFlags, "<issuer>=<identity>") {
		t.Errorf("generic flags = %s", GenericFlags)
	}
}
