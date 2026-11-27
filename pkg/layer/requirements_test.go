package layer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// boundSigner is a signer with a source and ref binding.
func boundSigner() store.TrustSigner {
	s := signer()
	s.SourceRepository, s.SourceRepositoryRef = "https://github.com/acme/finance", "refs/heads/main"
	return s
}

// Spec: §4.7.11 (Requirements file) — the file written for a layer reads
// back to the same requirements, and a file with a member of another name,
// no trust_requirements array, or data after the object is refused.
func TestRequirementsFile_RoundTrip(t *testing.T) {
	reqs := []store.TrustRequirement{{Signers: []store.TrustSigner{boundSigner()}, PredicateType: "https://slsa.dev/provenance/v1"}}
	b, err := layer.MarshalRequirementsFile(reqs)
	if err != nil {
		t.Fatalf("MarshalRequirementsFile: %v", err)
	}
	if !strings.HasSuffix(string(b), "}\n") || strings.Contains(string(b), "layer_roots") {
		t.Fatalf("file = %s", b)
	}
	f, err := layer.ParseRequirementsFile(b)
	if err != nil {
		t.Fatalf("ParseRequirementsFile: %v", err)
	}
	if len(f.TrustRequirements) != 1 || f.TrustRequirements[0].Signers[0] != boundSigner() || f.TrustRequirements[0].PredicateType != reqs[0].PredicateType {
		t.Fatalf("parsed = %+v", f)
	}
	// layer_roots is kept as written and not read.
	if f, err := layer.ParseRequirementsFile([]byte(`{"trust_requirements":[],"layer_roots":{"x":5}}`)); err != nil || string(f.LayerRoots) != `{"x":5}` {
		t.Fatalf("layer_roots: %+v, %v", f, err)
	}
	for name, in := range map[string]string{
		"unknown member":        `{"trust_requirements":[],"trust_mode":"require"}`,
		"misspelled signer":     `{"trust_requirements":[{"signers":[{"issuer":"i","identity":"a@b","instnace":"github"}]}]}`,
		"no requirements":       `{}`,
		"null requirements":     `{"trust_requirements":null}`,
		"not an object":         `[]`,
		"data after the object": `{"trust_requirements":[]} {}`,
		"empty":                 ``,
	} {
		if _, err := layer.ParseRequirementsFile([]byte(in)); !errors.Is(err, layer.ErrTrustPolicy) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

// Spec: §4.7.11 (Policy rules) — the rules that read a requirement or a
// signer apply to a requirements file, with the instance known to the
// caller.
func TestCheckRequirements(t *testing.T) {
	github := func(name string) bool { return name == "github" }
	ok := []store.TrustRequirement{{Signers: []store.TrustSigner{boundSigner()}}}
	if err := layer.CheckRequirements(ok, nil); err != nil {
		t.Fatalf("valid: %v", err)
	}
	gh := boundSigner()
	gh.Instance = "github"
	if err := layer.CheckRequirements([]store.TrustRequirement{{Signers: []store.TrustSigner{gh}}}, github); err != nil {
		t.Fatalf("known instance: %v", err)
	}
	for name, reqs := range map[string][]store.TrustRequirement{
		"none":             nil,
		"two":              {ok[0], ok[0]},
		"no signer":        {{}},
		"no binding":       {{Signers: []store.TrustSigner{signer()}}},
		"unknown instance": {{Signers: []store.TrustSigner{gh}}},
	} {
		if err := layer.CheckRequirements(reqs, nil); !errors.Is(err, layer.ErrTrustPolicy) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
