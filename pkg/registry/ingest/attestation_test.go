package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/spi"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.11 (Layer step, Attestation record) — a signed layer under
// require is stored, the record holds the status ok and the mark at the
// layer bundle's attested time, and layer.attestation_verified names the
// accepted bundle and the matched artifacts.
func TestAttestation_SignedLayerIsStored(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	rec := f.record()
	want := f.h.Clock().Add(time.Minute)
	if res.Accepted != 2 || rec.Status != "ok" || rec.Mark == nil || !rec.Mark.Equal(want) || rec.CheckedAt == nil || rec.Commit != "" {
		t.Fatalf("accepted %d, record %+v, want 2 and ok at %s", res.Accepted, rec, want)
	}
	ev, ok := f.lastEvent("layer.attestation_verified")
	if !ok || ev.target != "team" || ev.fields["matched"] != "2" || !strings.Contains(ev.fields["bundles"], `"instance":"github"`) || ev.fields["refused"] != "[]" {
		t.Fatalf("verified event = %+v", ev)
	}
	if o := res.Attestation.Outcomes["finance/pay"]; res.Attestation.Status != "ok" || o.Outcome != ingest.OutcomeVerified {
		t.Errorf("report = %+v", res.Attestation)
	}
}

// Spec: §4.7.11 (Layer step) — under require a layer-step failure refuses
// the cycle with its code: it stores no artifact and no domain record, it
// writes the failure to the record and leaves the mark, and it emits
// layer.attestation_failed.
func TestAttestation_RequireRefusesAnUnsignedLayer(t *testing.T) {
	f := newAttestFixture(t, "require")
	_, err := f.cycle()
	code, details := refusal(t, err)
	if code != ingest.CodeAttestationMissing || details["constraint"] != "no_bundle" {
		t.Fatalf("refusal %s %v", code, details)
	}
	domains, _ := f.st.ListDomains(context.Background(), "tenant-1")
	if rec := f.record(); len(f.stored()) != 0 || len(domains) != 0 || rec.Status != code || rec.Mark != nil {
		t.Fatalf("stored %v, domains %d, record %+v", f.stored(), len(domains), rec)
	}
	if ev, ok := f.lastEvent("layer.attestation_failed"); !ok || ev.fields["code"] != code || ev.fields["mode"] != "require" {
		t.Errorf("failed event = %+v", ev)
	}
}

// Spec: §4.7.11 (Layer step, Attestation outcome) — under warn the cycle
// carries on after a failed layer step: each artifact is stored with the
// outcome failed and the scope layer, and the record holds the failure.
func TestAttestation_WarnStoresWithAFailedOutcome(t *testing.T) {
	f := newAttestFixture(t, "warn")
	res, err := f.cycle()
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	o := res.Attestation.Outcomes["finance/close"]
	if res.Accepted != 2 || f.record().Status != ingest.CodeAttestationMissing || o.Outcome != ingest.OutcomeFailed || o.Scope != "layer" || o.Constraint != "no_bundle" {
		t.Fatalf("accepted %d, record %+v, outcome %+v", res.Accepted, f.record(), o)
	}
}

// Spec: §4.7.11 (Layer step) — the codes and constraints of layer steps 1 to
// 6, each from a layer state that a signing run did not produce.
func TestAttestation_LayerStepFailures(t *testing.T) {
	cases := []struct {
		name       string
		arrange    func(f *attestFixture)
		code       string
		constraint string
		check      func(t *testing.T, d map[string]any)
	}{
		{"another identity", func(f *attestFixture) {
			f.place(f.sign(f.subjects(), time.Minute, sh.WithLeaf(sh.LeafEmail("mallory@acme.com"))))
		}, ingest.CodeAttestationMissing, "no_accepted_bundle", func(t *testing.T, d map[string]any) {
			b := d["bundles"].([]map[string]any)[0]
			if b["reason"] != "identity" || b["identity"] != "mallory@acme.com" || b["instance"] != "github" {
				t.Errorf("bundle entry %v", b)
			}
		}},
		{"no layer subject", func(f *attestFixture) {
			f.place(f.sign(f.subjects()[1:], time.Minute))
		}, ingest.CodeAttestationMissing, "no_layer_subject", nil},
		{"changed artifact", func(f *attestFixture) {
			f.signLayer(time.Minute)
			f.write(map[string]string{"finance/pay/ARTIFACT.md": agentArtifact("Pay a changed invoice")})
		}, ingest.CodeAttestationMismatch, "layer_digest", func(t *testing.T, d map[string]any) {
			diff := d["different"].([]map[string]any)
			if len(diff) != 1 || diff[0]["subject"] != "finance/pay@1.0.0" || diff[0]["type"] != "agent" || d["subjects_agree"] != false {
				t.Errorf("details %v", d)
			}
		}},
		{"changed domain", func(f *attestFixture) {
			f.signLayer(time.Minute)
			f.write(map[string]string{"finance/DOMAIN.md": "---\ndescription: Changed\n---\n"})
		}, ingest.CodeAttestationMismatch, "layer_digest", func(t *testing.T, d map[string]any) {
			if d["subjects_agree"] != true {
				t.Errorf("details %v", d)
			}
		}},
		{"added package", func(f *attestFixture) {
			f.signLayer(time.Minute)
			f.write(map[string]string{"finance/extra/ARTIFACT.md": agentArtifact("Extra")})
		}, ingest.CodeAttestationMismatch, "layer_digest", func(t *testing.T, d map[string]any) {
			if fmt.Sprint(d["only_in_snapshot"]) != "[finance/extra@1.0.0]" {
				t.Errorf("details %v", d)
			}
		}},
		{"incomplete", func(f *attestFixture) {
			s := f.subjects()
			f.place(f.sign(s[:2], time.Minute))
		}, ingest.CodeAttestationMissing, "incomplete", func(t *testing.T, d map[string]any) {
			if fmt.Sprint(d["artifacts"]) != "[finance/pay@1.0.0]" {
				t.Errorf("details %v", d)
			}
		}},
		{"subject name", func(f *attestFixture) {
			f.signLayer(time.Minute)
			f.write(map[string]string{"finance/a b/ARTIFACT.md": agentArtifact("Spaced")})
		}, ingest.CodeAttestationInvalid, "subject_name", func(t *testing.T, d map[string]any) {
			if fmt.Sprint(d["ids"]) != "[finance/a b]" {
				t.Errorf("details %v", d)
			}
		}},
		{"too many bundles", func(f *attestFixture) {
			f.cfg.MaxBundles = 1
			f.place(f.sign(f.subjects(), time.Minute), f.sign(f.subjects(), 2*time.Minute))
		}, ingest.CodeAttestationInvalid, "limits", nil},
		{"bundle too large", func(f *attestFixture) {
			f.cfg.MaxBundleBytes = 100
			f.signLayer(time.Minute)
		}, ingest.CodeAttestationInvalid, "limits", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttestFixture(t, "require")
			tc.arrange(f)
			_, err := f.cycle()
			code, details := refusal(t, err)
			if code != tc.code || details["constraint"] != tc.constraint {
				t.Fatalf("refusal %s %v, want %s %s", code, details, tc.code, tc.constraint)
			}
			if tc.check != nil {
				tc.check(t, details)
			}
			if f.record().Status != tc.code || len(f.stored()) != 0 {
				t.Errorf("record %+v, stored %v", f.record(), f.stored())
			}
		})
	}
}

// Spec: §4.7.11 (Attestation mark) — a replayed older signed state is
// refused as stale against the mark, and a newer bundle for the same content
// passes and moves the mark forward.
func TestAttestation_MarkRefusesAReplay(t *testing.T) {
	f := newAttestFixture(t, "require")
	older := f.sign(f.subjects(), time.Minute)
	f.place(f.sign(f.subjects(), 3*time.Minute))
	if _, err := f.cycle(); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	f.place(older)
	_, err := f.cycle()
	if code, d := refusal(t, err); code != ingest.CodeAttestationStale || d["constraint"] != "mark" {
		t.Fatalf("replay: %s %v", code, d)
	}
	if rec := f.record(); !rec.Mark.Equal(f.h.Clock().Add(3*time.Minute)) || rec.Status != ingest.CodeAttestationStale {
		t.Fatalf("record after the replay %+v", rec)
	}
	// A newer bundle beside the replayed one passes, because the choice is
	// made among the candidates that pass.
	f.place(older, f.sign(f.subjects(), 5*time.Minute))
	if _, err := f.cycle(); err != nil {
		t.Fatalf("newer bundle: %v", err)
	}
	if rec := f.record(); !rec.Mark.Equal(f.h.Clock().Add(5*time.Minute)) || rec.Status != "ok" {
		t.Fatalf("record %+v", rec)
	}
}

// Spec: §4.7.11 (Policy that became invalid) — a cycle whose resolved
// policy has no requirement, or names an instance the registry does not
// configure, is refused before the snapshot with
// ingest.attestation_policy_invalid, and the record holds the refusal.
func TestAttestation_InvalidPolicyRefusesTheCycle(t *testing.T) {
	for _, tc := range []struct {
		name, constraint string
		arrange          func(f *attestFixture)
	}{
		{"no requirement", "no_requirements", func(f *attestFixture) { f.setPolicy("", nil...); f.cfg.DefaultMode = "warn" }},
		{"unknown instance", "unknown_instance", func(f *attestFixture) {
			s := emailSigner()
			s.Instance = "staging"
			f.setPolicy("require", s)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttestFixture(t, "require")
			tc.arrange(f)
			_, err := f.cycle()
			if code, d := refusal(t, err); code != ingest.CodeAttestationPolicyInvalid || d["constraint"] != tc.constraint {
				t.Fatalf("refusal %s %v", code, d)
			}
			if f.record().Status != ingest.CodeAttestationPolicyInvalid || len(f.stored()) != 0 {
				t.Fatalf("record %+v, stored %v", f.record(), f.stored())
			}
		})
	}
}

// Spec: §4.7.11 (Ingest verification) — a layer under the mode none runs no
// attestation step and writes no record, so the mark survives a period in
// that mode.
func TestAttestation_ModeNoneWritesNoRecord(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	before := f.record()
	f.setPolicy("none", emailSigner())
	f.place()
	res, err := f.cycle()
	if err != nil || res.Attestation != nil || !f.record().CheckedAt.Equal(*before.CheckedAt) || !f.record().Mark.Equal(*before.Mark) {
		t.Fatalf("err %v, report %v, record %+v, before %+v", err, res.Attestation, f.record(), before)
	}
}

// staleRoot is a trusted root source past its maximum age.
type staleRoot struct{}

func (staleRoot) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, sign.ErrAttestationRootStale
}

// Spec: §4.7.11 (Maximum age) — a policy that names the public-good
// instance whose root is past its maximum age fails the layer step with
// ingest.attestation_root_stale, after the cycle asked for the refresh.
func TestAttestation_StalePublicGoodRoot(t *testing.T) {
	f := newAttestFixture(t, "require")
	refreshed := 0
	f.cfg.Instances["public-good"] = sign.AttestationInstance{Name: "public-good", Root: staleRoot{}}
	f.cfg.OnPublicGood = func() { refreshed++ }
	s := emailSigner()
	s.Instance = ""
	f.setPolicy("require", s)
	f.signLayer(time.Minute)
	_, err := f.cycle()
	if code, _ := refusal(t, err); code != ingest.CodeAttestationRootStale || refreshed != 1 {
		t.Fatalf("refusal %s, refreshed %d", code, refreshed)
	}
}

// Spec: §4.7.11 (Layer step 5), §9.1 (Lineage query) — the attested commit
// of a candidate must be in the lineage of the snapshot: the answer
// in_lineage passes and records the commit, not_descendant and
// not_in_history are stale with the answer as the constraint, a query the
// source cannot answer fails the cycle with no record, and a fetch deadline
// is recorded.
func TestAttestation_Lineage(t *testing.T) {
	const identity = "https://github.com/acme/layer/.github/workflows/attest.yml@refs/heads/main"
	commit := strings.Repeat("a", 40)
	signer := store.TrustSigner{Issuer: sh.GitHubActionsIssuer, Identity: identity, Instance: "github",
		SourceRepository: "https://github.com/acme/layer", SourceRepositoryRef: "refs/heads/main"}
	leaf := sh.WithLeaf(sh.GitHubActionsLeaf(identity, sh.Source{Repository: "https://github.com/acme/layer", Ref: "refs/heads/main", Digest: commit}))
	answer := func(a source.LineageAnswer, err error) (ingest.LineageFunc, *[]string) {
		var bases []string
		return func(_ context.Context, base string, commits []string) ([]source.LineageAnswer, error) {
			bases = append(bases, base+"|"+strings.Join(commits, ","))
			if err != nil {
				return nil, err
			}
			return []source.LineageAnswer{a}, nil
		}, &bases
	}
	f := newAttestFixture(t, "require")
	f.setPolicy("require", signer)
	f.place(f.sign(f.subjects(), time.Minute, leaf))
	pass, bases := answer(source.InLineage, nil)
	if _, err := f.cycleWith(pass); err != nil || f.record().Commit != commit || (*bases)[0] != "|"+commit {
		t.Fatalf("in lineage: err %v, record %+v, queries %v", err, f.record(), *bases)
	}
	f.place(f.sign(f.subjects(), 2*time.Minute, leaf))
	notDesc, bases := answer(source.NotDescendant, nil)
	if _, err := f.cycleWith(notDesc); err == nil || (*bases)[0] != commit+"|"+commit {
		t.Fatalf("not descendant: err %v, queries %v", err, *bases)
	} else if code, d := refusal(t, err); code != ingest.CodeAttestationStale || d["constraint"] != "not_descendant" {
		t.Fatalf("not descendant: %s %v", code, d)
	}
	before := f.record()
	failing, _ := answer("", fmt.Errorf("%w: down", source.ErrSourceUnreachable))
	if _, err := f.cycleWith(failing); !errors.Is(err, source.ErrSourceUnreachable) || !f.record().CheckedAt.Equal(*before.CheckedAt) {
		t.Fatalf("unreachable: err %v, record %+v", err, f.record())
	}
	deadline := &spi.Error{Code: "ingest.source_unreachable", Message: "deadline", Details: map[string]any{"constraint": "fetch_deadline", "depth": 64}}
	late, _ := answer("", deadline)
	if _, err := f.cycleWith(late); !errors.Is(err, deadline) || f.record().Status != "ingest.source_unreachable" || f.record().Details["constraint"] != "fetch_deadline" {
		t.Fatalf("deadline: err %v, record %+v", err, f.record())
	}
}

// Spec: §4.7.11 (Attestation record) — a cycle whose claim a later cycle
// took writes no record and stores nothing, and a record write that would
// move a mark the record no longer holds does not apply.
func TestAttestation_SupersededCycleWritesNothing(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	ctx := context.Background()
	claim, _, claimed, err := ingest.ClaimLayer(ctx, f.st, "tenant-1", "team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	mark := f.record()
	f.signLayer(2 * time.Minute)
	res, err := ingest.Ingest(ctx, f.st, ingest.Request{TenantID: "tenant-1", LayerID: "team", IngestClaim: claim,
		Files: source.ConfinedFS(f.dir), Attestation: f.cfg.Cycle(claimed, claim)})
	if err != nil || !res.Superseded || !f.record().Mark.Equal(*mark.Mark) {
		t.Fatalf("err %v, result %+v, record %+v", err, res, f.record())
	}
}

// Spec: §4.7.11 (Mark reset) — a reingest that carries a reset clears the
// mark in the write that claims the layer, records the named commit, emits
// layer.attestation_mark_reset with the earlier values, and keeps its claim
// when its cycle fails.
func TestAttestation_MarkResetWithTheClaim(t *testing.T) {
	f := newAttestFixture(t, "require")
	// A recorded commit with a line feed, as an earlier reset in a store
	// edited by hand could hold, reaches the event as a reported string.
	if _, _, _, err := f.st.ClaimLayerIngestWithReset(context.Background(), "tenant-1", "team", "seed", "bad\ncommit"); err != nil {
		t.Fatal(err)
	}
	f.signLayer(3 * time.Minute)
	if _, err := f.cycle(); err != nil {
		t.Fatal(err)
	}
	f.signLayer(time.Minute)
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		t.Fatal(err)
	}
	var events []auditRecord
	commit := strings.Repeat("b", 40)
	_, err = ingest.SourceIngestWithOptions(context.Background(), f.st, source.Local{}, lc, ingest.SourceIngestOptions{
		Attestation: f.cfg, MarkReset: &ingest.MarkReset{ToCommit: commit}, CallerID: "alice@acme.com",
		AuditEmit: func(typ, target string, fields map[string]string) {
			events = append(events, auditRecord{typ, target, fields})
		},
	})
	if err != nil {
		t.Fatalf("reset cycle: %v", err)
	}
	rec := f.record()
	if !rec.Mark.Equal(f.h.Clock().Add(time.Minute)) || rec.Commit != commit {
		t.Fatalf("record %+v", rec)
	}
	i := slices.IndexFunc(events, func(e auditRecord) bool { return e.typ == "layer.attestation_mark_reset" })
	if i < 0 || events[i].fields["to_commit"] != commit || events[i].fields["previous_mark"] == "" || events[i].fields["caller"] != "alice@acme.com" ||
		events[i].fields["previous_commit"] != "bad\ufffdcommit" {
		t.Fatalf("events %+v", events)
	}
}

// fakeSource is a source provider over a local directory that counts its
// snapshots, fails them with snapErr when set, and answers lineage queries
// with answer.
type fakeSource struct {
	dir       string
	snapErr   error
	snapshots int
	answer    source.LineageAnswer
	queries   []string
}

func (s *fakeSource) ID() string                   { return "fake" }
func (s *fakeSource) Trigger() source.TriggerModel { return source.TriggerManual }
func (s *fakeSource) AnswersLineage() bool         { return true }

func (s *fakeSource) Snapshot(context.Context, source.LayerConfig) (*source.Snapshot, error) {
	s.snapshots++
	if s.snapErr != nil {
		return nil, s.snapErr
	}
	return &source.Snapshot{Reference: strings.Repeat("c", 40), Files: source.ConfinedFS(s.dir)}, nil
}

func (s *fakeSource) Lineage(_ context.Context, _ source.LayerConfig, reference, base string, commits []string) ([]source.LineageAnswer, error) {
	s.queries = append(s.queries, reference+"|"+base+"|"+strings.Join(commits, ","))
	out := make([]source.LineageAnswer, len(commits))
	for i := range out {
		out[i] = s.answer
	}
	return out, nil
}

// runSource runs one orchestrated cycle of the fixture's layer over src.
func (f *attestFixture) runSource(src source.Provider) (*ingest.Result, error) {
	f.t.Helper()
	lc, err := f.st.GetLayerConfig(context.Background(), "tenant-1", "team")
	if err != nil {
		f.t.Fatal(err)
	}
	return ingest.SourceIngestWithOptions(context.Background(), f.st, src, lc, ingest.SourceIngestOptions{Attestation: f.cfg, Clock: f.clock})
}

// Spec: §4.7.11 (Ingest verification, Attestation record), §7.3.1 (Removed
// artifacts) — the orchestrated cycle refuses an invalid policy before it
// takes a snapshot, records a snapshot that reached the Git fetch deadline
// and keeps its claim, and sends the lineage query of a provider that
// answers one with the snapshot reference.
func TestAttestation_OrchestratedCycle(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.setPolicy("require")
	src := &fakeSource{dir: f.dir, answer: source.InLineage}
	if _, err := f.runSource(src); src.snapshots != 0 {
		t.Fatalf("invalid policy: err %v after %d snapshots", err, src.snapshots)
	}
	const identity = "https://github.com/acme/layer/.github/workflows/attest.yml@refs/heads/main"
	commit := strings.Repeat("d", 40)
	f.setPolicy("require", store.TrustSigner{Issuer: sh.GitHubActionsIssuer, Identity: identity, Instance: "github",
		SourceRepository: "https://github.com/acme/layer", SourceRepositoryRef: "refs/heads/main"})
	f.place(f.sign(f.subjects(), time.Minute, sh.WithLeaf(sh.GitHubActionsLeaf(identity,
		sh.Source{Repository: "https://github.com/acme/layer", Ref: "refs/heads/main", Digest: commit}))))
	if _, err := f.runSource(src); err != nil || len(src.queries) != 1 || src.queries[0] != strings.Repeat("c", 40)+"||"+commit || f.record().Commit != commit {
		t.Fatalf("lineage: err %v, queries %v, record %+v", err, src.queries, f.record())
	}
	src.snapErr = &spi.Error{Code: "ingest.source_unreachable", Message: "deadline", Details: map[string]any{"constraint": "fetch_deadline"}}
	if _, err := f.runSource(src); err == nil || f.record().Status != "ingest.source_unreachable" {
		t.Fatalf("deadline: err %v, record %+v", err, f.record())
	}
}

// Spec: §4.7.11 (Policy that became invalid) — a stored trust_mode the
// registry cannot read, in any letter case or with white space, does not read
// as none: the cycle is refused with policy_rule, stores nothing, and writes
// the refusal to the record. A nil configuration refuses a layer whose own
// mode is above none, because it configures no instance.
func TestAttestation_UnreadableModeFailsClosed(t *testing.T) {
	for _, mode := range []string{"REQUIRE", "required", "Require", " require"} {
		t.Run(mode, func(t *testing.T) {
			f := newAttestFixture(t, mode)
			_, err := f.cycle()
			if code, d := refusal(t, err); code != ingest.CodeAttestationPolicyInvalid || d["constraint"] != "policy_rule" {
				t.Fatalf("refusal %s %v", code, d)
			}
			if f.record().Status != ingest.CodeAttestationPolicyInvalid || len(f.stored()) != 0 {
				t.Fatalf("record %+v, stored %v", f.record(), f.stored())
			}
		})
	}
	f := newAttestFixture(t, "require")
	f.cfg = nil
	f.signLayer(time.Minute)
	_, err := f.cycle()
	if code, d := refusal(t, err); code != ingest.CodeAttestationPolicyInvalid || d["constraint"] != "unknown_instance" || len(f.stored()) != 0 {
		t.Fatalf("nil configuration: %s %v, stored %v", code, d, f.stored())
	}
}

// Spec: §4.7.11 (Reported strings) — a replayed signed state beside packages
// whose IDs and fields a sender chose to be escaped by HTML-safe encoders
// gives a layer_digest failure whose stored attestation_details stays below
// 32 KiB, measured with <, >, and & escaped, and is marked truncated.
func TestAttestation_DetailsStayBelow32KiB(t *testing.T) {
	f := newAttestFixture(t, "require")
	f.signLayer(time.Minute)
	seg := strings.Repeat("<", 200)
	for i := range 25 {
		f.write(map[string]string{fmt.Sprintf("%s/%s%02d/ARTIFACT.md", seg, strings.Repeat(">", 120), i): agentArtifact("x")})
	}
	rb := strings.Repeat("&", 300)
	for _, id := range []string{"finance/pay", "finance/close"} {
		f.write(map[string]string{id + "/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: d\nsensitivity: low\ndeprecated: true\nreplaced_by: \"" + rb + "\"\n---\n\nbody\n"})
	}
	_, err := f.cycle()
	if code, d := refusal(t, err); code != ingest.CodeAttestationMismatch || d["constraint"] != "layer_digest" {
		t.Fatalf("refusal %s %v", code, d)
	}
	rec := f.record()
	b, _ := json.Marshal(rec.Details)
	if len(b) >= 32*1024 || rec.Details["truncated"] != true {
		t.Fatalf("attestation_details is %d bytes, truncated %v", len(b), rec.Details["truncated"])
	}
}

// Spec: §4.7.11 (Layer step) — a bundle entry the cycle cannot read, a
// dangling link or a link to a directory, fails the layer step with
// ingest.attestation_invalid and the constraint unreadable under require, is
// recorded, and under warn the cycle stores the layer with that status.
func TestAttestation_UnreadableBundleEntry(t *testing.T) {
	for _, tc := range []struct{ name, target string }{{"dangling", "missing-target"}, {"directory link", "."}} {
		for _, mode := range []string{"require", "warn"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				f := newAttestFixture(t, mode)
				f.signLayer(time.Minute)
				if err := os.Symlink(tc.target, filepath.Join(f.dir, ".podium", "attestations", "zz.sigstore.json")); err != nil {
					t.Fatal(err)
				}
				res, err := f.cycle()
				if mode == "require" {
					if code, d := refusal(t, err); code != ingest.CodeAttestationInvalid || d["constraint"] != "unreadable" || d["file"] != "zz.sigstore.json" {
						t.Fatalf("refusal %s %v", code, d)
					}
				} else if err != nil || res.Accepted != 2 {
					t.Fatalf("warn: err %v, result %+v", err, res)
				}
				if rec := f.record(); rec.Status != ingest.CodeAttestationInvalid || rec.Details["constraint"] != "unreadable" {
					t.Fatalf("record %+v", rec)
				}
			})
		}
	}
}
