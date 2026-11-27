package ghactions

import (
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/store"
)

// PresetName is the one value of --trust-preset.
const PresetName = "github-actions"

// Issuer is the OIDC issuer of a GitHub Actions certificate.
const Issuer = "https://token.actions.githubusercontent.com"

// WorkflowPath is the path of the workflow `podium attest init` writes,
// relative to the top of the repository.
const WorkflowPath = ".github/workflows/podium-attest.yml"

// SignedRef is the ref the workflow pushes signed states to and the preset
// registers the layer at.
const SignedRef = "podium/signed"

// PrivateInstance is the Sigstore instance that signs for a private or
// internal repository.
const PrivateInstance = "github"

// WorkflowIdentity returns the certificate identity of the workflow at
// WorkflowPath in the repository fullName, run from branch: the URI GitHub
// writes into the subject alternative name of the signing certificate.
func WorkflowIdentity(fullName, branch string) string {
	return "https://" + Host + "/" + fullName + "/" + WorkflowPath + "@refs/heads/" + branch
}

// Preset is the trust policy that --trust-preset github-actions resolves.
type Preset struct {
	// Ref is the ref the layer is registered at.
	Ref string
	// Mode is the layer's trust_mode.
	Mode string
	// Requirement is the layer's one trust requirement.
	Requirement store.TrustRequirement
}

// Overrides are the explicit flags that replace part of the preset.
type Overrides struct {
	// Mode replaces the require mode when it is not empty.
	Mode string
	// Ref replaces the podium/signed ref when it is not empty.
	Ref string
	// Signers, as <issuer>=<identity> pairs, replace the preset's issuer
	// and identity. Each signer keeps the preset's source and ref binding.
	Signers []string
}

// Resolve builds the preset's policy from the record of a public
// repository. The signer has the GitHub Actions issuer and the identity of
// the repository's podium-attest workflow on its default branch, and its
// binding names the repository's URL, its identifier, and its owner's
// identifier, with the ref of the default branch. An explicit signer
// replaces the issuer and the identity alone, so a certificate of another
// caller of a shared workflow, or of another branch, is still refused.
//
// Spec: §7.3.1 (Trust preset)
func Resolve(rec Record, o Overrides) (Preset, error) {
	p := Preset{Ref: SignedRef, Mode: "require"}
	if o.Mode != "" {
		p.Mode = o.Mode
	}
	if o.Ref != "" {
		p.Ref = o.Ref
	}
	pairs := o.Signers
	if len(pairs) == 0 {
		pairs = []string{Issuer + "=" + WorkflowIdentity(rec.FullName, rec.DefaultBranch)}
	}
	for _, pair := range pairs {
		issuer, identity, ok := strings.Cut(pair, "=")
		if !ok || issuer == "" || identity == "" {
			return Preset{}, fmt.Errorf("--trust-signer %q is not <issuer>=<identity>", pair)
		}
		s := bindings(rec)
		s.Issuer, s.Identity = issuer, identity
		p.Requirement.Signers = append(p.Requirement.Signers, s)
	}
	return p, nil
}

// bindings returns a signer with the source and ref binding of rec.
func bindings(rec Record) store.TrustSigner {
	return store.TrustSigner{
		SourceRepository:        rec.Repo().URL(),
		SourceRepositoryID:      formatID(rec.ID),
		SourceRepositoryOwnerID: formatID(rec.OwnerID),
		SourceRepositoryRef:     "refs/heads/" + rec.DefaultBranch,
	}
}

// GenericFlags names the explicit trust flags of `podium layer register`
// for a repository the preset does not cover, with a placeholder for each
// value.
const GenericFlags = "--trust require \\\n    --trust-signer '<issuer>=<identity>' \\\n    --trust-source-repository <repository-url> \\\n    --trust-source-repository-ref refs/heads/<branch>"

// ExplicitFlags returns the trust flags of `podium layer register` that
// state the preset's policy without the preset, for a registration the
// preset refuses. A zero record writes placeholders for the values the
// command could not read. instance names the Sigstore instance, and an empty
// one leaves --trust-instance out. signer, when not empty, is the
// <issuer>=<identity> of a shared workflow. Each value is shell-quoted.
//
// Spec: §7.3.1 (Trust preset)
func ExplicitFlags(repo Repo, rec Record, signer, instance string) string {
	branch, repoID, ownerID, name := "<default-branch>", "<repository-id>", "<owner-id>", repo.FullName()
	if rec.ID > 0 {
		branch, repoID, ownerID, name = rec.DefaultBranch, formatID(rec.ID), formatID(rec.OwnerID), rec.FullName
	}
	if signer == "" {
		signer = Issuer + "=" + WorkflowIdentity(name, branch)
	}
	flags := []string{
		"--ref " + ShellQuote(SignedRef),
		"--trust require",
		"--trust-signer " + ShellQuote(signer),
		"--trust-source-repository " + ShellQuote(repoNamed(name).URL()),
		"--trust-source-repository-id " + ShellQuote(repoID),
		"--trust-source-owner-id " + ShellQuote(ownerID),
		"--trust-source-repository-ref " + ShellQuote("refs/heads/"+branch),
	}
	if instance != "" {
		flags = append(flags, "--trust-instance "+ShellQuote(instance))
	}
	return strings.Join(flags, " \\\n    ")
}
