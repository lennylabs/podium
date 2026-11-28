package vectors_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

type referencesVector struct {
	Name         string          `json:"name"`
	Attestations json.RawMessage `json:"attestations"`
	Digests      []string        `json:"digests"`
	Code         string          `json:"code"`
	Constraint   string          `json:"constraint"`
}

// fetchOutcomeVector gives what each reference's fetch yields: ok_garbage
// (bytes with the reference's digest that are no bundle), not_found,
// unavailable (a 5xx answer), unreachable, uncached (offline-only),
// too_large (over max_bytes), digest_mismatch, or invalid_response.
type fetchOutcomeVector struct {
	Name       string   `json:"name"`
	MaxBytes   int64    `json:"max_bytes"`
	Fetches    []string `json:"fetches"`
	Code       string   `json:"code"`
	Constraint string   `json:"constraint"`
}

type observationVector struct {
	Verified      bool   `json:"verified"`
	Reason        string `json:"reason"`
	Accepted      bool   `json:"accepted"`
	PredicateType bool   `json:"predicate_type"`
	Subject       bool   `json:"subject"`
}

type requirementCodeVector struct {
	Name         string              `json:"name"`
	Observations []observationVector `json:"observations"`
	Code         string              `json:"code"`
	Constraint   string              `json:"constraint"`
}

// referencesVectors declares the step 7 cases.
func referencesVectors(t *testing.T) []referencesVector {
	t.Helper()
	d := "sha256:" + strings.Repeat("a", 64)
	many := make([]string, 17)
	for i := range many {
		many[i] = `{"digest":"` + d + `"}`
	}
	cases := map[string]string{
		"usable and repeated":  `[{"digest":"` + d + `","verdict":"failed"},{"digest":"` + d + `"}]`,
		"unusable forms":       `[{"digest":"sha256:` + strings.Repeat("A", 64) + `"},{"digest":1},"x",{},{"digest":null}]`,
		"absent":               `null`,
		"not an array":         `{"digest":"` + d + `"}`,
		"more than sixteen":    `[` + strings.Join(many, ",") + `]`,
		"sixteen":              `[` + strings.Join(many[:16], ",") + `]`,
		"no usable after skip": `[]`,
	}
	var out []referencesVector
	for name, raw := range cases {
		digests, err := attest.ReadReferences(map[string]json.RawMessage{"attestations": json.RawMessage(raw)})
		v := referencesVector{Name: name, Attestations: json.RawMessage(raw), Digests: digests}
		v.Code, v.Constraint = failureParts(t, err)
		if v.Digests == nil {
			v.Digests = []string{}
		}
		out = append(out, v)
	}
	return out
}

// fetchSource answers each digest with its declared fetch result.
type fetchSource struct {
	results map[string]func() ([]byte, error)
}

// Bundle answers the declared result.
func (s fetchSource) Bundle(_ context.Context, digest string) ([]byte, error) {
	return s.results[digest]()
}

// fetchOutcomeVectors runs steps 8 to 10 over declared fetch results under
// a key-based require rule, with a rule record that passes steps 2 to 6.
func fetchOutcomeVectors(t *testing.T) []fetchOutcomeVector {
	t.Helper()
	pem, _ := json.Marshal(sh.PublicKeyPEM(t, &sh.NewSigningKey(t).PublicKey))
	doc, err := attest.ParsePolicyDocument([]byte(`{"version":1,"rules":[{"ids":["**"],"mode":"require","requirements":[{"signers":[{"public_key":` + string(pem) + `}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	file, err := attest.NewPolicyFile("vector", doc, sign.AttestationInstance{}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{file}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []fetchOutcomeVector{
		{Name: "only not found", Fetches: []string{"not_found"}},
		{Name: "only unavailable", Fetches: []string{"unavailable", "unavailable"}},
		{Name: "only uncached", Fetches: []string{"uncached"}},
		{Name: "only unreachable", Fetches: []string{"unreachable"}},
		{Name: "not found beside unreachable", Fetches: []string{"not_found", "unreachable"}},
		{Name: "uncached beside a bundle", Fetches: []string{"uncached", "ok_garbage"}},
		{Name: "unavailable beside a bundle", Fetches: []string{"unavailable", "ok_garbage"}},
		{Name: "unreachable beside a bundle", Fetches: []string{"unreachable", "ok_garbage"}},
		{Name: "too large beside a bundle", MaxBytes: 64, Fetches: []string{"too_large", "ok_garbage"}},
		{Name: "digest mismatch", Fetches: []string{"ok_garbage", "digest_mismatch"}},
		{Name: "invalid response beside a bundle", Fetches: []string{"invalid_response", "ok_garbage"}},
	}
	rec := version.DeliveryRecord{ID: "team/apply", Version: "1.0.0", Type: "rule", Frontmatter: consumerRuleMD, ManifestBody: "Apply.\n"}
	for i, c := range cases {
		src := fetchSource{results: map[string]func() ([]byte, error){}}
		var refs []map[string]string
		for j, kind := range c.Fetches {
			body := []byte("no bundle " + kind + string(rune('a'+j)))
			if kind == "too_large" {
				body = []byte(strings.Repeat("x", 65))
			}
			sum := sha256.Sum256(body)
			d := "sha256:" + hex.EncodeToString(sum[:])
			refs = append(refs, map[string]string{"digest": d})
			src.results[d] = fetchResult(kind, body)
		}
		raw, _ := json.Marshal(refs)
		served := version.Served{Record: rec, Members: map[string]json.RawMessage{"attestations": raw}}
		chk := &attest.Checker{Policy: policy, Bundles: src, MaxBytes: c.MaxBytes}
		_, err := chk.Verify(context.Background(), attest.Delivery{Request: attest.Request{ID: rec.ID}, Served: served})
		cases[i].Code, cases[i].Constraint = failureParts(t, err)
	}
	return cases
}

// fetchResult returns the fetch behaviour of kind, which serves body when
// the fetch succeeds.
func fetchResult(kind string, body []byte) func() ([]byte, error) {
	return func() ([]byte, error) {
		switch kind {
		case "not_found":
			return nil, attest.ErrBundleNotFound
		case "unavailable":
			return nil, attest.ErrBundleUnavailable
		case "unreachable":
			return nil, errors.New("dial tcp: connection refused")
		case "uncached":
			return nil, &attest.Failure{Code: attest.CodeAttestationMissing}
		case "invalid_response":
			return nil, &attest.Failure{Code: attest.CodeAttestationInvalid, Constraint: attest.ConstraintFetch}
		case "digest_mismatch":
			return append([]byte("other "), body...), nil
		}
		return body, nil
	}
}

// requirementCodeVectors declares the step 10 code selection cases.
func requirementCodeVectors(t *testing.T) []requirementCodeVector {
	t.Helper()
	met := observationVector{Verified: true, Accepted: true, PredicateType: true, Subject: true}
	noSubject := observationVector{Verified: true, Accepted: true, PredicateType: true}
	otherType := observationVector{Verified: true, Accepted: true}
	untrusted := observationVector{Verified: true}
	chain := observationVector{Reason: string(sign.ReasonChain)}
	timeR := observationVector{Reason: string(sign.ReasonTime)}
	cases := []requirementCodeVector{
		{Name: "met beside failures", Observations: []observationVector{chain, untrusted, met}},
		{Name: "no subject outranks the rest", Observations: []observationVector{otherType, noSubject, untrusted, chain}},
		{Name: "predicate type outranks untrusted", Observations: []observationVector{untrusted, otherType}},
		{Name: "untrusted outranks invalid", Observations: []observationVector{chain, untrusted}},
		{Name: "latest refusal reason", Observations: []observationVector{chain, timeR}},
		{Name: "no observation", Observations: []observationVector{}},
	}
	for i, c := range cases {
		var obs []attest.SignerObservation
		for _, o := range c.Observations {
			obs = append(obs, attest.SignerObservation{Verified: o.Verified, Reason: sign.AttestationReason(o.Reason),
				Accepted: o.Accepted, PredicateType: o.PredicateType, Subject: o.Subject})
		}
		cases[i].Code, cases[i].Constraint = failureParts(t, attest.RequirementFailure(obs))
	}
	return cases
}
