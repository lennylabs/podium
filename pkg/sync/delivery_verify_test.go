package sync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	stdsync "sync"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// glossaryStub is the one-artifact effective view most delivery cases serve.
func glossaryStub() stubArtifact {
	return stubArtifact{
		typ:          "context",
		layer:        "team-shared",
		frontmatter:  contextArtifactSrc,
		manifestBody: "Glossary body.\n",
	}
}

// runStub runs a server-source sync of srv into a fresh target with delivery.
func runStub(t *testing.T, srvURL string, delivery DeliveryCheckFunc) (string, error) {
	t.Helper()
	target := t.TempDir()
	_, err := Run(Options{RegistryPath: srvURL, Target: target, AdapterID: "none", Delivery: delivery})
	return target, err
}

// assertNothingWritten fails when target holds a materialized file or a lock.
func assertNothingWritten(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("target holds %v after a refused sync, want nothing", names)
	}
}

// assertRefused fails unless err carries code and target holds nothing.
func assertRefused(t *testing.T, err error, code, target string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("Run error = %v, want %s", err, code)
	}
	assertNothingWritten(t, target)
}

// Spec: §4.7.10 — a server-source load with no delivery-check resolver fails
// closed before any request.
func TestRun_ServerSourceNilDeliveryFailsClosed(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	target, err := runStub(t, srv.URL, nil)
	if !errors.Is(err, errNoDeliveryCheck) {
		t.Fatalf("Run error = %v, want the guard error", err)
	}
	if err.Error() != "sync: server-source load has no delivery check configured" {
		t.Errorf("guard message = %q", err.Error())
	}
	if n := counts.all.Load(); n != 0 {
		t.Errorf("stub requests = %d, want 0", n)
	}
	assertNothingWritten(t, target)
}

// Spec: §4.7.10 — a resolver that returns no check and no error is refused
// like a missing resolver.
func TestRun_ServerSourceNilCheckFailsClosed(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	_, err := runStub(t, srv.URL, func() (*sign.DeliveryCheck, error) { return nil, nil })
	if !errors.Is(err, errNoDeliveryCheck) {
		t.Fatalf("Run error = %v, want the guard error", err)
	}
	if n := counts.all.Load(); n != 0 {
		t.Errorf("stub requests = %d, want 0", n)
	}
}

// Spec: §7.5 — an invalid policy refuses the sync with a *DeliveryConfigError
// before any request.
func TestRun_ServerSourceInvalidPolicyIsDeliveryConfigError(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	getenv := func(k string) string {
		if k == "PODIUM_VERIFY_SIGNATURES" {
			return "bogus"
		}
		return ""
	}
	target, err := runStub(t, srv.URL, NewDeliveryCheckFunc(t.TempDir(), t.TempDir(), getenv, nil))
	var cfgErr *DeliveryConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("Run error = %v, want a *DeliveryConfigError", err)
	}
	if n := counts.all.Load(); n != 0 {
		t.Errorf("stub requests = %d, want 0", n)
	}
	assertNothingWritten(t, target)
}

// Spec: §7.5 — deliveryCheck returns a resolver's plain error unchanged.
func TestRun_ServerSourceResolverErrorIsUnchanged(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	want := errors.New("resolver failed")
	target, err := runStub(t, srv.URL, func() (*sign.DeliveryCheck, error) { return nil, want })
	if err != want {
		t.Fatalf("Run error = %v, want the resolver's error unchanged", err)
	}
	if n := counts.all.Load(); n != 0 {
		t.Errorf("stub requests = %d, want 0", n)
	}
	assertNothingWritten(t, target)
}

// Spec: §4.7.10 — each tampered or unverifiable record refuses the whole sync
// with its §6.10 code, and no file or lock is written.
func TestRun_ServerSourceRefusesUnverifiedRecords(t *testing.T) {
	t.Parallel()
	signer := newDeliverySigner(t)
	other := newDeliverySigner(t)
	withLarge := func(a stubArtifact) stubArtifact {
		a.largeResource = map[string]string{"data/big.bin": "BIGDATA"}
		return a
	}
	cases := []struct {
		name     string
		art      stubArtifact
		signer   *deliverySigner
		delivery DeliveryCheckFunc
		code     string
	}{
		{
			name: "tampered frontmatter byte",
			art: func() stubArtifact {
				a := glossaryStub()
				a.mutate = func(r map[string]any) {
					r["frontmatter"] = strings.Replace(contextArtifactSrc, "glossary", "Glossary", 1)
				}
				return a
			}(),
			delivery: neverDelivery,
			code:     "materialize.content_hash_mismatch",
		},
		{
			name: "tampered large resource",
			art: func() stubArtifact {
				a := withLarge(glossaryStub())
				a.blobOverride = map[string]string{"data/big.bin": "BADDATA"}
				return a
			}(),
			delivery: neverDelivery,
			code:     "materialize.content_hash_mismatch: large resource data/big.bin",
		},
		{
			name: "tampered signature",
			art: func() stubArtifact {
				a := glossaryStub()
				a.mutate = func(r map[string]any) {
					sig := r["delivery_signature"].(string)
					r["delivery_signature"] = strings.Replace(sig, `"signature":"`, `"signature":"AA`, 1)
				}
				return a
			}(),
			signer:   &signer,
			delivery: signer.check(),
			code:     "materialize.signature_invalid",
		},
		{
			name:     "missing signature under always",
			art:      glossaryStub(),
			delivery: signer.check(),
			code:     "materialize.signature_missing",
		},
		{
			name:     "wrong key",
			art:      glossaryStub(),
			signer:   &other,
			delivery: signer.check(),
			code:     "materialize.signature_invalid",
		},
		{
			// The registry frames the body's true digest; a link that serves an
			// empty content_hash frames the empty value, so the recompute
			// differs from the served hash.
			name: "large resource link with an empty content_hash",
			art: func() stubArtifact {
				a := withLarge(glossaryStub())
				a.mutate = func(r map[string]any) {
					r["large_resources"].(map[string]any)["data/big.bin"].(map[string]any)["content_hash"] = ""
				}
				return a
			}(),
			delivery: neverDelivery,
			code:     "materialize.content_hash_mismatch: recomputed delivery hash",
		},
		{
			name: "missing delivery_hash",
			art: func() stubArtifact {
				a := glossaryStub()
				a.mutate = func(r map[string]any) { delete(r, "delivery_hash") }
				return a
			}(),
			delivery: neverDelivery,
			code:     "materialize.content_hash_mismatch: the response carries no delivery_hash",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newSignedStubRegistry(t, map[string]stubArtifact{"team/glossary": tc.art}, tc.signer)
			target, err := runStub(t, srv.URL, tc.delivery)
			assertRefused(t, err, tc.code, target)
		})
	}
}

// Spec: §4.7.10 — a signed record verifies under always and materializes.
func TestRun_ServerSourceSignedRecordVerifies(t *testing.T) {
	t.Parallel()
	signer := newDeliverySigner(t)
	art := glossaryStub()
	art.largeResource = map[string]string{"data/big.bin": "BIGDATA"}
	srv := newSignedStubRegistry(t, map[string]stubArtifact{"team/glossary": art}, &signer)
	target, err := runStub(t, srv.URL, signer.check())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readFileT(t, filepath.Join(target, "team", "glossary", "data", "big.bin")); got != "BIGDATA" {
		t.Errorf("large resource = %q, want BIGDATA", got)
	}
}

// Spec: §4.7.10 — a path served both inline and as a large-resource link is
// refused before any object-store request.
func TestRun_ServerSourceRefusesSharedResourcePath(t *testing.T) {
	t.Parallel()
	art := glossaryStub()
	art.resources = map[string]string{"data/big.bin": "inline"}
	art.largeResource = map[string]string{"data/big.bin": "BIGDATA"}
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": art}, nil)
	target, err := runStub(t, srv.URL, neverDelivery)
	assertRefused(t, err, "materialize.content_hash_mismatch", target)
	if n := counts.blob.Load(); n != 0 {
		t.Errorf("object-store requests = %d, want 0", n)
	}
}

// Spec: §4.7.10 — a manifest_body_url link with an empty content_hash frames
// the empty value: the authentic document loads, and a tampered one is
// refused by the delivery-hash comparison.
func TestRun_ServerSourceManifestLinkWithEmptyContentHash(t *testing.T) {
	t.Parallel()
	emptyLinkHash := func(r map[string]any) {
		r["manifest_body_url"].(map[string]any)["content_hash"] = ""
	}
	t.Run("authentic", func(t *testing.T) {
		t.Parallel()
		art := glossaryStub()
		art.manifestURL = true
		art.mutate = emptyLinkHash
		srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": art})
		target, err := runStub(t, srv.URL, neverDelivery)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := readFileT(t, filepath.Join(target, "team", "glossary", "ARTIFACT.md")); got != contextArtifactSrc {
			t.Errorf("ARTIFACT.md = %q, want the linked document", got)
		}
	})
	t.Run("tampered", func(t *testing.T) {
		t.Parallel()
		art := glossaryStub()
		art.manifestURL = true
		art.mutate = emptyLinkHash
		art.blobOverride = map[string]string{stubManifestPath: strings.Replace(contextArtifactSrc, "glossary", "Glossary", 1)}
		srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": art})
		target, err := runStub(t, srv.URL, neverDelivery)
		assertRefused(t, err, "materialize.content_hash_mismatch: recomputed delivery hash", target)
	})
}

// Spec: §4.7.10 — sync decodes through version.ParseLoadResponse: a
// resources_base64 value holding an escaped CR LF is refused before any
// object-store request, and a case variant of frontmatter is not read, so the
// recompute differs from the served hash.
func TestRun_ServerSourceDecodesByTheProcedure(t *testing.T) {
	t.Parallel()
	t.Run("non-canonical base64", func(t *testing.T) {
		t.Parallel()
		art := glossaryStub()
		art.resources = map[string]string{"data/a.txt": "QUJD"}
		art.resourcesB64 = true
		art.largeResource = map[string]string{"data/big.bin": "BIGDATA"}
		art.raw = func(r map[string]any) string {
			return strings.Replace(mustJSON(t, r), `"QUJD"`, `"QU\r\nJD"`, 1)
		}
		srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": art}, nil)
		target, err := runStub(t, srv.URL, neverDelivery)
		assertRefused(t, err, "materialize.content_hash_mismatch", target)
		if n := counts.blob.Load(); n != 0 {
			t.Errorf("object-store requests = %d, want 0", n)
		}
	})
	t.Run("case-variant frontmatter", func(t *testing.T) {
		t.Parallel()
		art := glossaryStub()
		art.raw = func(r map[string]any) string {
			return strings.Replace(mustJSON(t, r), `"frontmatter":`, `"FRONTMATTER":`, 1)
		}
		srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": art})
		target, err := runStub(t, srv.URL, neverDelivery)
		assertRefused(t, err, "materialize.content_hash_mismatch: recomputed delivery hash", target)
	})
}

// Spec: §4.7.10 — layer is read by exact name: a mistyped case variant is
// ignored, the later of two repeated members is read, and a mistyped layer is
// refused.
func TestRun_ServerSourceReadsLayerByExactName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		rewrite   string
		wantLayer string
		wantErr   bool
	}{
		{name: "case variant ignored", rewrite: `"LAYER":5,"layer":"team-shared"`, wantLayer: "team-shared"},
		{name: "later member read", rewrite: `"layer":5,"layer":"team-shared"`, wantLayer: "team-shared"},
		{name: "mistyped layer refused", rewrite: `"layer":5`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			art := glossaryStub()
			art.layer = "team-shared"
			art.raw = func(r map[string]any) string {
				return strings.Replace(mustJSON(t, r), `"layer":"team-shared"`, tc.rewrite, 1)
			}
			srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": art})
			target, err := runStub(t, srv.URL, neverDelivery)
			if tc.wantErr {
				assertRefused(t, err, "materialize.content_hash_mismatch", target)
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			lock, err := ReadLock(target)
			if err != nil || lock == nil || len(lock.Artifacts) != 1 {
				t.Fatalf("ReadLock = %+v, %v", lock, err)
			}
			if lock.Artifacts[0].Layer != tc.wantLayer {
				t.Errorf("lock layer = %q, want %q", lock.Artifacts[0].Layer, tc.wantLayer)
			}
		})
	}
}

// Spec: §6.5, §4.7.10 — sync verifies a record whose artifact_revision is
// lower than an earlier load's and compares it with nothing.
func TestRun_ServerSourceDeliversLowerArtifactRevision(t *testing.T) {
	t.Parallel()
	signer := newDeliverySigner(t)
	target := t.TempDir()
	for _, step := range []struct{ version, revision string }{
		{"1.1.0", "2025-02-01T00:00:00.000000Z"},
		{"1.0.0", "2025-01-01T00:00:00.000000Z"},
	} {
		art := glossaryStub()
		art.raw = func(r map[string]any) string {
			r["version"] = step.version
			r["artifact_revision"] = step.revision
			delete(r, "delivery_signature")
			seal(t, r, &signer)
			return mustJSON(t, r)
		}
		srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": art})
		if _, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", Delivery: signer.check()}); err != nil {
			t.Fatalf("Run at %s: %v", step.version, err)
		}
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil || len(lock.Artifacts) != 1 {
		t.Fatalf("ReadLock = %+v, %v", lock, err)
	}
	if lock.Artifacts[0].Version != "1.0.0" {
		t.Errorf("lock version = %q, want 1.0.0", lock.Artifacts[0].Version)
	}
}

// Spec: §7.5 — Render and RunMarketplace pass Delivery through to the
// server-source load.
func TestRender_PassesDeliveryThrough(t *testing.T) {
	t.Parallel()
	srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()})
	opts := RenderOptions{OutputID: "acme-agents", Registry: srv.URL, Workdir: t.TempDir(), Harnesses: []string{"claude-code"}, Plugins: financePlugins()}
	if _, err := Render(context.Background(), opts); !errors.Is(err, errNoDeliveryCheck) {
		t.Errorf("Render without Delivery = %v, want the guard error", err)
	}
	opts.Delivery = neverDelivery
	if _, err := Render(context.Background(), opts); err != nil {
		t.Errorf("Render with Delivery: %v", err)
	}
	out := runOutput(srv.URL, []string{"claude-code"}, Workflow{})
	run := RunOptions{Output: out, DryRun: true, Now: fixedNow(), Stdout: discardBuf(), Stderr: discardBuf()}
	if _, err := RunMarketplace(context.Background(), run); !errors.Is(err, errNoDeliveryCheck) {
		t.Errorf("RunMarketplace without Delivery = %v, want the guard error", err)
	}
	run.Delivery = neverDelivery
	if _, err := RunMarketplace(context.Background(), run); err != nil {
		t.Errorf("RunMarketplace with Delivery: %v", err)
	}
}

// Spec: §7.5 — Override passes Delivery to its Run. A refused
// re-materialization keeps the toggle change and writes no artifact file.
func TestOverride_PassesDeliveryThrough(t *testing.T) {
	t.Parallel()
	tampered := glossaryStub()
	tampered.mutate = func(r map[string]any) { r["frontmatter"] = "tampered" }
	cases := []struct {
		name     string
		art      stubArtifact
		delivery DeliveryCheckFunc
		wantErr  string
	}{
		{name: "verified", art: glossaryStub(), delivery: neverDelivery},
		{name: "no resolver", art: glossaryStub(), wantErr: "override: materialize: sync: server-source load has no delivery check configured"},
		{name: "tampered", art: tampered, delivery: neverDelivery, wantErr: "materialize.content_hash_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": tc.art})
			target := t.TempDir()
			_, err := Override(OverrideOptions{Target: target, Add: []string{"team/glossary"}, RegistryPath: srv.URL, AdapterID: "none", Delivery: tc.delivery})
			artifactFile := filepath.Join(target, "team", "glossary", "ARTIFACT.md")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Override: %v", err)
				}
				if _, serr := os.Stat(artifactFile); serr != nil {
					t.Errorf("added artifact not materialized: %v", serr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Override error = %v, want %q", err, tc.wantErr)
			}
			assertToggleOnly(t, target, "team/glossary")
			if _, serr := os.Stat(artifactFile); !os.IsNotExist(serr) {
				t.Errorf("refused override wrote %s", artifactFile)
			}
		})
	}
}

// assertToggleOnly fails unless target's lock names id in toggles.add and
// lists no artifact.
func assertToggleOnly(t *testing.T, target, id string) {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = %+v, %v", lock, err)
	}
	if len(lock.Toggles.Add) != 1 || lock.Toggles.Add[0].ID != id {
		t.Errorf("toggles.add = %+v, want %s", lock.Toggles.Add, id)
	}
	if len(lock.Artifacts) != 0 {
		t.Errorf("lock artifacts = %+v, want none", lock.Artifacts)
	}
}

// Spec: §7.5 — the delivery check resolves at the one point a server-source
// effective view is requested: before GET /v1/sync/manifest, and never on a
// path that loads nothing from a server.
func TestDeliveryResolution_ResolvesAtEffectiveViewRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		call      func(registry string, delivery DeliveryCheckFunc) error
		server    bool
		wantCalls int
		wantLock  bool
	}{
		{name: "Run", server: true, wantCalls: 1, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := Run(Options{RegistryPath: reg, Target: t.TempDir(), Delivery: d})
			return err
		}},
		{name: "Run offline-only", server: true, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := Run(Options{RegistryPath: reg, Target: t.TempDir(), CacheMode: "offline-only", Delivery: d})
			return err
		}},
		{name: "Run filesystem", call: func(_ string, d DeliveryCheckFunc) error {
			_, err := Run(Options{RegistryPath: writeFilesystemRegistry(t), Target: t.TempDir(), Delivery: d})
			return err
		}},
		{name: "ResolveEffectiveView", server: true, wantCalls: 1, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := ResolveEffectiveView(Options{RegistryPath: reg, Target: t.TempDir(), Delivery: d})
			return err
		}},
		{name: "Render", server: true, wantCalls: 1, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := Render(context.Background(), RenderOptions{OutputID: "o", Registry: reg, Workdir: t.TempDir(), Harnesses: []string{"claude-code"}, Delivery: d})
			return err
		}},
		{name: "RunMarketplace check", server: true, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := RunMarketplace(context.Background(), RunOptions{Output: runOutput(reg, []string{"claude-code"}, Workflow{}), Check: true, Stdout: discardBuf(), Stderr: discardBuf(), Delivery: d})
			return err
		}},
		{name: "Override dry-run", server: true, call: func(reg string, d DeliveryCheckFunc) error {
			_, err := Override(OverrideOptions{Target: t.TempDir(), Add: []string{"team/glossary"}, DryRun: true, RegistryPath: reg, Delivery: d})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{}, nil)
			calls := 0
			resolver := func() (*sign.DeliveryCheck, error) {
				calls++
				return nil, &DeliveryConfigError{Err: errors.New("config.signature_provider_unavailable: test")}
			}
			err := tc.call(srv.URL, resolver)
			if calls != tc.wantCalls {
				t.Errorf("resolver calls = %d, want %d", calls, tc.wantCalls)
			}
			if n := counts.all.Load(); n != 0 {
				t.Errorf("stub requests = %d, want 0", n)
			}
			var cfgErr *DeliveryConfigError
			if got := errors.As(err, &cfgErr); got != (tc.wantCalls == 1) {
				t.Errorf("error = %v; DeliveryConfigError found = %v, want %v", err, got, tc.wantCalls == 1)
			}
		})
	}
	t.Run("Override add", func(t *testing.T) {
		t.Parallel()
		srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{}, nil)
		calls := 0
		resolver := func() (*sign.DeliveryCheck, error) {
			calls++
			return nil, &DeliveryConfigError{Err: errors.New("config.signature_provider_unavailable: test")}
		}
		target := t.TempDir()
		_, err := Override(OverrideOptions{Target: target, Add: []string{"team/glossary"}, RegistryPath: srv.URL, Delivery: resolver})
		var cfgErr *DeliveryConfigError
		if !errors.As(err, &cfgErr) {
			t.Fatalf("Override error = %v, want a *DeliveryConfigError", err)
		}
		if calls != 1 || counts.all.Load() != 0 {
			t.Errorf("(resolver calls, stub requests) = (%d, %d), want (1, 0)", calls, counts.all.Load())
		}
		assertToggleOnly(t, target, "team/glossary")
	})
}

// Spec: §7.5 — a watcher whose delivery check cannot be resolved emits the
// refusal and closes without subscribing to /v1/events.
func TestWatch_ServerSourceStopsOnDeliveryConfigError(t *testing.T) {
	t.Parallel()
	srv, counts := newCountedStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()}, nil)
	resolver := func() (*sign.DeliveryCheck, error) {
		return nil, &DeliveryConfigError{Err: errors.New("config.signature_provider_unavailable: test")}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := Watch(ctx, WatchOptions{Sync: Options{RegistryPath: srv.URL, Target: t.TempDir(), Delivery: resolver}})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	var got []WatchEvent
	for ev := range events {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1 before the channel closes", len(got))
	}
	var cfgErr *DeliveryConfigError
	if !errors.As(got[0].Err, &cfgErr) {
		t.Errorf("event error = %v, want a *DeliveryConfigError", got[0].Err)
	}
	if n := counts.all.Load(); n != 0 {
		t.Errorf("stub requests = %d, want 0", n)
	}
}

// mustJSON encodes v as JSON.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// Spec: §4.7.9 — across two Run calls one resolver reads each signing
// variable once and warns once, and concurrent calls share the one result.
func TestNewDeliveryCheckFunc_MemoizedAcrossRuns(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	syncYAML := filepath.Join(ws, ".podium", "sync.yaml")
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{Path: ".podium/sync.yaml", Content: "defaults:\n  verify_signatures: never\n"})
	var mu stdsync.Mutex
	reads := map[string]int{}
	getenv := func(k string) string {
		mu.Lock()
		defer mu.Unlock()
		reads[k]++
		return ""
	}
	var warnings []string
	delivery := NewDeliveryCheckFunc(ws, t.TempDir(), getenv, func(s string) { warnings = append(warnings, s) })

	var wg stdsync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = delivery()
		}()
	}
	wg.Wait()

	srv := newStubRegistry(t, map[string]stubArtifact{"team/glossary": glossaryStub()})
	target := t.TempDir()
	for i := range 2 {
		if _, err := Run(Options{RegistryPath: srv.URL, Target: target, AdapterID: "none", Delivery: delivery}); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	for _, k := range []string{"PODIUM_VERIFY_SIGNATURES", "PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGN_KEY_PATH"} {
		if reads[k] > 1 {
			t.Errorf("%s read %d times, want at most once", k, reads[k])
		}
	}
	if reads["PODIUM_VERIFY_SIGNATURES"] != 1 {
		t.Errorf("PODIUM_VERIFY_SIGNATURES read %d times, want 1", reads["PODIUM_VERIFY_SIGNATURES"])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], syncYAML) {
		t.Errorf("warnings = %q, want one naming %s", warnings, syncYAML)
	}
}
