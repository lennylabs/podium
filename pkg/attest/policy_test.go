package attest_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// emailSignerJSON is a signer of the harness's default email identity on
// the github instance.
const emailSignerJSON = `{"issuer":"` + sh.DefaultIssuer + `","identity":"` + sh.DefaultSAN + `","instance":"github"}`

// githubInstanceJSON lists the github instance with a relative root file.
const githubInstanceJSON = `{"name":"github","trusted_root_file":"github-root.json","transparency_log":"none","certificate_transparency":"none"}`

// policyJSON builds a policy file with the github instance and rules.
func policyJSON(rules ...string) string {
	return `{"version":1,"instances":[` + githubInstanceJSON + `],"rules":[` + strings.Join(rules, ",") + `]}`
}

// requireRule is a rule of the patterns under mode with one requirement of
// the email signer.
func requireRule(mode string, ids ...string) string {
	b, _ := json.Marshal(ids)
	m := ""
	if mode != "" {
		m = `"mode":"` + mode + `",`
	}
	return `{"ids":` + string(b) + `,` + m + `"requirements":[{"predicate_type":"` + slsaType + `","signers":[` + emailSignerJSON + `]}]}`
}

// slsaType is the predicate type the consumer fixtures sign.
const slsaType = "https://slsa.dev/provenance/v1"

// writePolicy writes a policy file and the harness's GitHub-style root
// beside it, and loads it with publicGood.
func writePolicy(t *testing.T, h *sh.Harness, text string, publicGood sign.AttestationInstance) *attest.PolicyFile {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "github-root.json"), h.TrustedRootJSON(sh.GitHubStyleRoot()...), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := attest.LoadPolicyFile(p, publicGood, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("LoadPolicyFile: %v", err)
	}
	return f
}

// Spec: §4.7.11 (Consumer trust policy) — the policy file of the design's
// example parses, with covers_layer and optional admitted and not read, a
// rule without a mode left unset, and a certificate signer without an
// instance on public-good.
func TestParsePolicyDocument_Accepts(t *testing.T) {
	t.Parallel()
	doc, err := attest.ParsePolicyDocument([]byte(`{
  "version": 1,
  "instances": [{"name": "github", "trusted_root_file": "/etc/podium/github-trusted-root.json", "transparency_log": "none", "certificate_transparency": "none"},
                {"name": "corp", "trusted_root_file": "corp.json", "transparency_log": "required"}],
  "rules": [
    {"ids": ["personal/**"], "mode": "none"},
    {"ids": ["**"], "mode": "require", "requirements": [{"covers_layer": true, "optional": false, "signers": [{
      "issuer": "https://token.actions.githubusercontent.com",
      "identity": "https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main",
      "source_repository_owner": "https://github.com/acme", "source_repository_ref": "refs/heads/main", "instance": "github"}]}]},
    {"ids": ["x/*"], "requirements": [{"signers": [{"issuer": "i", "identity": "alice@acme.com"}]}]}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Instances) != 2 || !doc.Instances[1].TransparencyLog || doc.Instances[1].CertificateTransparency {
		t.Fatalf("instances = %+v", doc.Instances)
	}
	if len(doc.Rules) != 3 || doc.Rules[2].Mode != "" || doc.Rules[2].Requirements[0].Signers[0].Instance != sign.PublicGoodInstance {
		t.Fatalf("rules = %+v", doc.Rules)
	}
	if doc.Rules[1].Requirements[0].Signers[0].Signer.SourceRepositoryOwner != "https://github.com/acme" {
		t.Fatalf("binding lost: %+v", doc.Rules[1].Requirements[0])
	}
}

// Spec: §4.7.11 (Consumer trust policy, Policy load) — every breach of the
// file format is config.attestation_policy_unavailable.
// Matrix: §6.10 (config.attestation_policy_unavailable)
func TestParsePolicyDocument_Refusals(t *testing.T) {
	t.Parallel()
	sig := emailSignerJSON
	rule := func(body string) string {
		return `{"version":1,"instances":[` + githubInstanceJSON + `],"rules":[` + body + `]}`
	}
	cases := map[string]string{
		"invalid utf-8":          "{\"version\":1,\"rules\":[],\"x\":\"\xff\"}",
		"byte order mark":        "\xef\xbb\xbf{\"version\":1,\"rules\":[]}",
		"not an object":          `[]`,
		"no version":             `{"rules":[]}`,
		"version 2":              `{"version":2,"rules":[]}`,
		"version string":         `{"version":"1","rules":[]}`,
		"unknown top member":     `{"version":1,"rules":[],"rule":[]}`,
		"member twice":           `{"version":1,"rules":[],"rules":[]}`,
		"case variant":           `{"Version":1,"rules":[]}`,
		"no rules":               `{"version":1}`,
		"rules not array":        `{"version":1,"rules":{}}`,
		"text after":             `{"version":1,"rules":[]} {}`,
		"truncated":              `{"version":1,"rules":[`,
		"rule unknown member":    rule(`{"ids":["a"],"modes":"none"}`),
		"rule no ids":            rule(`{"mode":"none"}`),
		"rule empty ids":         rule(`{"ids":[]}`),
		"pattern partial":        rule(`{"ids":["a/b*"]}`),
		"pattern empty":          rule(`{"ids":[""]}`),
		"pattern empty segment":  rule(`{"ids":["a//b"]}`),
		"pattern not string":     rule(`{"ids":[1]}`),
		"mode unknown":           rule(`{"ids":["a"],"mode":"always"}`),
		"mode empty":             rule(`{"ids":["a"],"mode":""}`),
		"require no requirement": rule(`{"ids":["a"],"mode":"require"}`),
		"requirements not array": rule(`{"ids":["a"],"requirements":{}}`),
		"requirement no signers": rule(`{"ids":["a"],"requirements":[{}]}`),
		"requirement empty":      rule(`{"ids":["a"],"requirements":[{"signers":[]}]}`),
		"requirement member":     rule(`{"ids":["a"],"requirements":[{"signers":[` + sig + `],"predicate":"x"}]}`),
		"covers_layer string":    rule(`{"ids":["a"],"requirements":[{"signers":[` + sig + `],"covers_layer":"yes"}]}`),
		"predicate not string":   rule(`{"ids":["a"],"requirements":[{"signers":[` + sig + `],"predicate_type":1}]}`),
		"signer case variant":    rule(`{"ids":["a"],"requirements":[{"signers":[{"Issuer":"i","identity":"a@b"}]}]}`),
		"signer no issuer":       rule(`{"ids":["a"],"requirements":[{"signers":[{"identity":"a@b"}]}]}`),
		"uri without binding":    rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"https://x/y"}]}]}`),
		"binding without ref":    rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"https://x/y","source_repository":"https://x"}]}]}`),
		"any_source with member": rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"https://x/y","any_source":true,"source_repository":"https://x","any_ref":true}]}]}`),
		"any_ref with ref":       rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"https://x/y","source_repository":"https://x","any_ref":true,"source_repository_ref":"refs/heads/main"}]}]}`),
		"any_ref not bool":       rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"a@b","any_ref":1}]}]}`),
		"issuer not string":      rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":1,"identity":"a@b"}]}]}`),
		"unknown instance":       rule(`{"ids":["a"],"requirements":[{"signers":[{"issuer":"i","identity":"a@b","instance":"corp"}]}]}`),
		"key with instance":      rule(`{"ids":["a"],"requirements":[{"signers":[{"public_key":"x","instance":"github"}]}]}`),
		"instance public-good":   `{"version":1,"instances":[{"name":"public-good","trusted_root_file":"r","transparency_log":"required"}],"rules":[]}`,
		"instance bad name":      `{"version":1,"instances":[{"name":"Corp","trusted_root_file":"r","transparency_log":"none"}],"rules":[]}`,
		"instance twice":         `{"version":1,"instances":[` + githubInstanceJSON + `,` + githubInstanceJSON + `],"rules":[]}`,
		"instance no root":       `{"version":1,"instances":[{"name":"corp","transparency_log":"none"}],"rules":[]}`,
		"instance empty root":    `{"version":1,"instances":[{"name":"corp","trusted_root_file":" ","transparency_log":"none"}],"rules":[]}`,
		"instance no tlog":       `{"version":1,"instances":[{"name":"corp","trusted_root_file":"r"}],"rules":[]}`,
		"instance tlog value":    `{"version":1,"instances":[{"name":"corp","trusted_root_file":"r","transparency_log":"yes"}],"rules":[]}`,
		"instance tlog type":     `{"version":1,"instances":[{"name":"corp","trusted_root_file":"r","transparency_log":true}],"rules":[]}`,
		"instance ct value":      `{"version":1,"instances":[{"name":"corp","trusted_root_file":"r","transparency_log":"none","certificate_transparency":"x"}],"rules":[]}`,
		"github with a log":      `{"version":1,"instances":[{"name":"github","trusted_root_file":"r","transparency_log":"required"}],"rules":[]}`,
		"instances not array":    `{"version":1,"instances":{},"rules":[]}`,
		"instance member":        `{"version":1,"instances":[{"name":"corp","trusted_root_file":"r","transparency_log":"none","root":"x"}],"rules":[]}`,
		"instance name number":   `{"version":1,"instances":[{"name":1}],"rules":[]}`,
	}
	for name, text := range cases {
		_, err := attest.ParsePolicyDocument([]byte(text))
		f, ok := attest.FailureOf(err)
		if !ok || f.Code != attest.CodePolicyUnavailable {
			t.Errorf("%s: err = %v, want %s", name, err, attest.CodePolicyUnavailable)
		}
	}
}

// Spec: §4.7.11 (Consumer trust policy) — * matches one segment, ** zero
// or more, and every other segment byte for byte.
func TestMatchPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		p, id string
		want  bool
	}{
		{"**", "a", true}, {"**", "a/b/c", true},
		{"finance/**", "finance", true}, {"finance/**", "finance/a/b", true}, {"finance/**", "financex", false},
		{"finance/*", "finance/a", true}, {"finance/*", "finance", false}, {"finance/*", "finance/a/b", false},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/y/b", true}, {"a/**/b", "a/x/c", false},
		{"*/x", "a/x", true}, {"*/x", "x", false},
		{"Finance/a", "finance/a", false}, {"a", "a", true}, {"a", "a/b", false},
	}
	for _, c := range cases {
		if got := attest.MatchPattern(c.p, c.id); got != c.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", c.p, c.id, got, c.want)
		}
	}
}

// Spec: §4.7.11 (Policy load, Trusted roots of a consumer) — a file loads
// its instances' roots relative to its directory, and an unreadable file,
// an unreadable root, a root that lacks a log its instance requires, and a
// public-good signer with no public-good root refuse the load.
func TestLoadPolicyFile(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	f := writePolicy(t, h, policyJSON(requireRule("require", "finance/**")), sign.AttestationInstance{})
	if inst, ok := f.Instance("github"); !ok || inst.TransparencyLog {
		t.Fatalf("github instance = %+v, %v", inst, ok)
	}
	if _, ok := f.Instance(sign.PublicGoodInstance); ok {
		t.Fatal("a public-good instance without a root")
	}

	dir := t.TempDir()
	load := func(text string, pg sign.AttestationInstance) error {
		p := filepath.Join(dir, "p.json")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := attest.LoadPolicyFile(p, pg, time.Unix(0, 0))
		return err
	}
	wantUnavailable := func(name string, err error) {
		t.Helper()
		if f, ok := attest.FailureOf(err); !ok || f.Code != attest.CodePolicyUnavailable || !strings.Contains(f.Error(), "p.json") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, err := attest.LoadPolicyFile(filepath.Join(dir, "absent.json"), sign.AttestationInstance{}, time.Unix(0, 0))
	if f, ok := attest.FailureOf(err); !ok || f.Code != attest.CodePolicyUnavailable {
		t.Errorf("absent file: %v", err)
	}
	wantUnavailable("bad json", load(`{`, sign.AttestationInstance{}))
	wantUnavailable("absent root", load(policyJSON(), sign.AttestationInstance{}))
	if err := os.WriteFile(filepath.Join(dir, "github-root.json"), h.TrustedRootJSON(sh.GitHubStyleRoot()...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := load(policyJSON(), sign.AttestationInstance{}); err != nil {
		t.Fatalf("relative root: %v", err)
	}
	corp := `{"version":1,"instances":[{"name":"corp","trusted_root_file":"github-root.json","transparency_log":"required"}],"rules":[]}`
	wantUnavailable("tlog required", load(corp, sign.AttestationInstance{}))
	corpCT := `{"version":1,"instances":[{"name":"corp","trusted_root_file":"github-root.json","transparency_log":"none","certificate_transparency":"required"}],"rules":[]}`
	wantUnavailable("ct required", load(corpCT, sign.AttestationInstance{}))
	pgRule := `{"version":1,"rules":[{"ids":["**"],"requirements":[{"signers":[{"issuer":"i","identity":"a@b"}]}]}]}`
	wantUnavailable("no public-good root", load(pgRule, sign.AttestationInstance{}))
	root, err := sign.ParseAttestationRoot(h.TrustedRootJSON())
	if err != nil {
		t.Fatal(err)
	}
	if err := load(pgRule, sign.AttestationInstance{Root: root}); err != nil {
		t.Fatalf("public-good root given: %v", err)
	}
}

// fileOf returns a PolicyFile of text with no instance roots.
func fileOf(t *testing.T, text string) *attest.PolicyFile {
	t.Helper()
	doc, err := attest.ParsePolicyDocument([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	f, err := attest.NewPolicyFile("mem", doc, sign.AttestationInstance{}, func(string) (*sign.AttestationRoot, error) {
		return nil, errors.New("no roots in this test")
	}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// keyRule is a rule with a key-based signer, which needs no root.
func keyRule(t *testing.T, mode string, ids ...string) string {
	t.Helper()
	pem, _ := json.Marshal(sh.PublicKeyPEM(t, &sh.NewSigningKey(t).PublicKey))
	b, _ := json.Marshal(ids)
	m := ""
	if mode != "" {
		m = `"mode":"` + mode + `",`
	}
	return `{"ids":` + string(b) + `,` + m + `"requirements":[{"signers":[{"public_key":` + string(pem) + `}]}]}`
}

// Spec: §4.7.11 (Policy sources) — every source contributes, the default
// mode is the strictest any source sets, a user default floors the rules of
// repository-source files and not those of user-source files, the user mode
// comes from the user sources alone, and the rule key names the matched
// rules.
func TestPolicy_Resolve(t *testing.T) {
	t.Parallel()
	user := fileOf(t, `{"version":1,"rules":[{"ids":["personal/**"],"mode":"none"},`+keyRule(t, "", "**")+`]}`)
	repo := fileOf(t, `{"version":1,"rules":[{"ids":["**"],"mode":"none"},`+keyRule(t, "require", "finance/**")+`]}`)
	p, err := attest.NewPolicy(
		attest.Source{Class: attest.UserSource, Name: "home", DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{user}},
		attest.Source{Class: attest.RepositorySource, Name: "workspace", DefaultMode: attest.ModeWarn, Files: []*attest.PolicyFile{repo}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AboveNone() {
		t.Fatal("a require default is not above none")
	}
	// personal/x: the user's none rule keeps none, the repository's none
	// rule is floored to require.
	r := p.Resolve("personal/x")
	if r.Mode != attest.ModeRequire || r.UserMode != attest.ModeNone || len(r.Matches) != 2 {
		t.Fatalf("personal/x = %v", r)
	}
	if r.Matches[1].Mode != attest.ModeRequire || r.Matches[0].Mode != attest.ModeNone {
		t.Fatalf("matches = %+v", r.Matches)
	}
	f := p.Resolve("finance/pay")
	if f.Mode != attest.ModeRequire || f.UserMode != attest.ModeRequire || f.RuleKey() == r.RuleKey() {
		t.Fatalf("finance/pay = %v key %q", f, f.RuleKey())
	}
	if g := p.Resolve("finance/close"); g.RuleKey() != f.RuleKey() {
		t.Fatalf("two IDs of one rule: %q != %q", g.RuleKey(), f.RuleKey())
	}
	if f.String() == "" || f.Matches[0].Rule().IDs[0] != "**" {
		t.Fatal("rule accessor")
	}
}

// Spec: §4.7.11 (Policy sources) — an ID that no rule matches takes the
// default mode; with no default it is none; a source with no class or an
// invalid default mode is refused.
func TestPolicy_DefaultAndRefusals(t *testing.T) {
	t.Parallel()
	only := fileOf(t, `{"version":1,"rules":[`+keyRule(t, "warn", "only/this")+`]}`)
	p, err := attest.NewPolicy(attest.Source{Class: attest.RepositorySource, Files: []*attest.PolicyFile{only}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.AboveNone() {
		t.Fatal("a warn rule is not above none")
	}
	a, b := p.Resolve("x/a"), p.Resolve("x/b")
	if a.Mode != attest.ModeNone || a.UserMode != attest.ModeNone || a.RuleKey() != b.RuleKey() || a.RuleKey() != "default" {
		t.Fatalf("unmatched = %v key %q", a, a.RuleKey())
	}
	empty, _ := attest.NewPolicy()
	if empty.AboveNone() || empty.Resolve("a").Mode != attest.ModeNone {
		t.Fatal("an empty policy is above none")
	}
	if _, err := attest.NewPolicy(attest.Source{Name: "x"}); err == nil {
		t.Fatal("a source with no class")
	}
	if _, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, DefaultMode: "always"}); err == nil {
		t.Fatal("an invalid default mode")
	}
	if _, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{nil}}); err == nil {
		t.Fatal("a nil file")
	}
	if _, err := attest.ParseMode("x"); err == nil {
		t.Fatal("ParseMode(x)")
	}
	if attest.ModeWarn.Stricter(attest.ModeNone) != attest.ModeWarn || attest.Mode("").Stricter(attest.ModeNone) != attest.ModeNone {
		t.Fatal("Stricter")
	}
}

// Spec: §4.7.11 (Consumer trust policy) — matching collapses runs of ** and
// runs in polynomial time, so a pattern a repository commits cannot stall a
// consumer.
func TestMatchPattern_Bounded(t *testing.T) {
	t.Parallel()
	pat := strings.Repeat("**/", 40) + "z"
	id := strings.TrimSuffix(strings.Repeat("a/", 200), "/")
	start := time.Now()
	if attest.MatchPattern(pat, id) {
		t.Fatal("matched")
	}
	if !attest.MatchPattern(pat, id+"/z") || !attest.MatchPattern("**/**/a/**", "a") {
		t.Fatal("did not match")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("matching took %v", d)
	}
}

// Spec: §4.7.11 (Consumer trust policy) — a literal pattern segment that no
// well-formed requested ID can hold is refused at load.
func TestParsePolicyDocument_UnmatchablePatterns(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"finance/./**", "finance/../**", "finance/.hidden", "finance/a@1.0.0", "GIT~1/**", "a\\\\b", "con/x"} {
		_, err := attest.ParsePolicyDocument([]byte(`{"version":1,"rules":[{"ids":["` + p + `"],"mode":"none"}]}`))
		if f, ok := attest.FailureOf(err); !ok || f.Code != attest.CodePolicyUnavailable {
			t.Errorf("pattern %q: %v", p, err)
		}
	}
	if _, err := attest.ParsePolicyDocument([]byte(`{"version":1,"rules":[{"ids":["Finance/**","*/x"],"mode":"none"}]}`)); err != nil {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (Attestation check, steps 1 and 10) — the plan of an ID
// and the code a failed requirement reports.
func TestPlanAndRequirementFailure(t *testing.T) {
	t.Parallel()
	user := fileOf(t, `{"version":1,"rules":[`+keyRule(t, "warn", "**")+`]}`)
	repo := fileOf(t, `{"version":1,"rules":[`+keyRule(t, "require", "finance/**")+`,{"ids":["bare/**"]}]}`)
	p, _ := attest.NewPolicy(
		attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{user}},
		attest.Source{Class: attest.RepositorySource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{repo}},
	)
	plan, err := p.Resolve("finance/pay").Plan()
	if err != nil || len(plan.Enforced) != 1 || len(plan.Reported) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	alone, _ := attest.NewPolicy(attest.Source{Class: attest.RepositorySource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{repo}})
	_, err = alone.Resolve("other/x").Plan()
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintNoRule)
	_, err = p.Resolve("bare/x").Plan()
	if f, ok := attest.FailureOf(err); !ok || f.Constraint != attest.ConstraintNoRequirements {
		t.Fatalf("bare: %v", err)
	}
	if err := attest.RequirementFailure([]attest.SignerObservation{{Reason: sign.ReasonChain}, {Verified: true, Accepted: true, PredicateType: true, Subject: true}}); err != nil {
		t.Fatal(err)
	}
	wantFailure(t, attest.RequirementFailure(nil), attest.CodeAttestationInvalid, string(sign.ReasonSignature))
	wantFailure(t, attest.RequirementFailure([]attest.SignerObservation{{Reason: sign.ReasonChain}, {Reason: sign.ReasonTime}}), attest.CodeAttestationInvalid, string(sign.ReasonChain))
}
