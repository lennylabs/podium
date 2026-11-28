package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// consumerPolicy builds a consumer policy whose one user-source file holds
// rules and the github instance of the serving fixture's root.
func consumerPolicy(t *testing.T, f *servingFixture, rules string) *attest.Policy {
	t.Helper()
	doc, err := attest.ParsePolicyDocument([]byte(`{"version":1,"instances":[{"name":"github","trusted_root_file":"github.json","transparency_log":"none"}],"rules":` + rules + `}`))
	if err != nil {
		t.Fatal(err)
	}
	root, ok := f.cfg.Instances["github"].Root.(*sign.AttestationRoot)
	if !ok {
		t.Fatal("the fixture's github root is not a parsed root")
	}
	file, err := attest.NewPolicyFile("consumer.json", doc, sign.AttestationInstance{},
		func(string) (*sign.AttestationRoot, error) { return root, nil }, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, Name: "test", Files: []*attest.PolicyFile{file}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// consumerRequireRule is a require rule for finance/** of the fixture's
// signer, optionally under another identity.
func consumerRequireRule(identity string) string {
	return `[{"ids":["finance/**"],"mode":"require","requirements":[{"predicate_type":"` + asPredicateType +
		`","signers":[{"issuer":"` + sh.DefaultIssuer + `","identity":"` + identity + `","instance":"github"}]}]}]`
}

// bundlesAs is the registry's bundle endpoint, fetched as user.
func bundlesAs(f *servingFixture, user string) attest.HTTPBundleSource {
	return attest.HTTPBundleSource{BaseURL: f.ts.URL, Client: f.ts.Client(),
		Prepare: func(r *http.Request) { r.Header.Set(asUserHeader, user) }}
}

// loadServed loads id as alice and runs the §4.7.10 decoding and hash
// comparison.
func loadServed(t *testing.T, f *servingFixture, id string) version.Served {
	t.Helper()
	status, _, raw := f.do(t, http.MethodGet, "/v1/load_artifact?id="+id, "alice", nil)
	if status != http.StatusOK {
		t.Fatalf("load = %d: %s", status, raw)
	}
	served, err := version.ParseLoadResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if version.DeliveryHash(served.Record) != served.Hash {
		t.Fatal("the delivery hash does not verify")
	}
	return served
}

// Spec: §4.7.11 (Attestation check, Request binding), §7.3.5 — a consumer
// verifies a record the registry served under its own policy: it binds the
// request, frames the served fields into a candidate equal to the package
// digest the registry stored, fetches the linked bundle from the bundle
// endpoint, and accepts it for its own signer; a bundle it cannot fetch is
// missing, a signer it does not trust is untrusted, and a bundle kept in
// its cache verifies offline.
func TestAttestConsumer_VerifiesServedRecords(t *testing.T) {
	t.Parallel()
	for name, open := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			f := newServingFixture(t, open(t))
			ctx := context.Background()
			served := loadServed(t, f, "finance/pay")
			var stored string
			if err := json.Unmarshal(served.Members["package_digest"], &stored); err != nil || stored == "" {
				t.Fatalf("package_digest member: %v", err)
			}
			cache := attest.DirCache{Dir: filepath.Join(t.TempDir(), "attestations")}
			c := &attest.Checker{Policy: consumerPolicy(t, f, consumerRequireRule(sh.DefaultSAN)),
				Bundles: attest.CachedBundles{Cache: cache, Source: bundlesAs(f, "alice")}}
			res, err := c.Verify(ctx, attest.Delivery{Request: attest.Request{ID: "finance/pay", Pin: "1.0.0"}, Served: served})
			if err != nil {
				t.Fatal(err)
			}
			if res.PackageDigest != stored || len(res.Accepted) != 1 || res.Accepted[0].Digest != attest.AttestationDigest(f.bundle) {
				t.Fatalf("result = %+v, stored digest %s", res, stored)
			}

			offline := &attest.Checker{Policy: c.Policy, Bundles: attest.CachedBundles{Cache: cache, OfflineOnly: true}}
			if _, err := offline.Verify(ctx, attest.Delivery{Request: attest.Request{ID: "finance/pay"}, Served: served}); err != nil {
				t.Fatalf("offline from the cache: %v", err)
			}

			// bob sees no linked version, so the endpoint answers not found.
			hidden := &attest.Checker{Policy: c.Policy, Bundles: bundlesAs(f, "bob")}
			_, err = hidden.Verify(ctx, attest.Delivery{Request: attest.Request{ID: "finance/pay"}, Served: served})
			if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeAttestationMissing {
				t.Fatalf("hidden bundle: %v", err)
			}

			untrusted := &attest.Checker{Policy: consumerPolicy(t, f, consumerRequireRule("bob@acme.com")), Bundles: bundlesAs(f, "alice")}
			_, err = untrusted.Verify(ctx, attest.Delivery{Request: attest.Request{ID: "finance/pay"}, Served: served})
			if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeAttestationUntrusted || fl.Constraint != attest.ConstraintSigner {
				t.Fatalf("untrusted signer: %v", err)
			}

			// The record of another ID never answers this request.
			other := loadServed(t, f, "finance/close")
			_, err = c.Verify(ctx, attest.Delivery{Request: attest.Request{ID: "finance/pay"}, Served: other})
			if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeRequestMismatch || fl.Constraint != attest.ConstraintID {
				t.Fatalf("substituted record: %v", err)
			}
		})
	}
}

// Spec: §4.7.11 (Batch entries), §7.6.2 — each ok entry of a batch
// response is matched to the request by its ID and verified under the
// consumer's policy.
func TestAttestConsumer_BatchEntries(t *testing.T) {
	t.Parallel()
	f := newServingFixture(t, storesUnderTest(t)["memory"](t))
	status, _, raw := f.do(t, http.MethodPost, "/v1/artifacts:batchLoad", "alice", []byte(`{"ids":["finance/pay","finance/close"]}`))
	if status != http.StatusOK {
		t.Fatalf("batch = %d: %s", status, raw)
	}
	entries, err := version.ParseBatchResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	errs := attest.BindBatch([]string{"finance/pay", "finance/close"}, nil, entries)
	c := &attest.Checker{Policy: consumerPolicy(t, f, consumerRequireRule(sh.DefaultSAN)), Bundles: bundlesAs(f, "alice")}
	for i, e := range entries {
		if errs[i] != nil || e.Err != nil {
			t.Fatalf("entry %d: %v, %v", i, errs[i], e.Err)
		}
		if version.DeliveryHash(e.Served.Record) != e.Served.Hash {
			t.Fatalf("entry %d: the delivery hash does not verify", i)
		}
		if _, err := c.Verify(context.Background(), attest.Delivery{Request: attest.Request{ID: e.Served.Record.ID}, Served: e.Served}); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	if errs := attest.BindBatch([]string{"finance/pay"}, nil, entries); errs[0] == nil && errs[1] == nil {
		t.Fatal("an entry the request does not list was bound")
	}
}
