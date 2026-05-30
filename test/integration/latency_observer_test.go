package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

type obsRec struct {
	op      string
	status  int
	elapsed time.Duration
}

// Spec: §7.1 Latency budgets — the latency observer fires once per served
// meta-tool request through the real server.Handler() chain (latency ->
// identity verification -> audit -> handler), keyed by operation name, so a
// deployment can compare observed latency against the SLO budgets. The
// liveness probe carries no SLO budget and must not be observed.
//
// The observer feeds a buffered channel rather than a slice so the test
// reads each observation without racing the server goroutine that records
// it after the handler returns.
func TestLatencyObserver_RecordsPerOperationOverHTTP(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID:    "default",
		ArtifactID:  "finance/variance",
		Version:     "1.0.0",
		ContentHash: "sha256:" + "0000000000000000000000000000000000000000000000000000000000000001",
		Type:        "context",
		Description: "Variance analysis reference for vendor payments here today.",
		Tags:        []string{"finance"},
		Layer:       "shared",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})

	obsCh := make(chan obsRec, 16)
	srv := server.New(reg, server.WithLatencyObserver(
		func(op string, status int, elapsed time.Duration) {
			obsCh <- obsRec{op, status, elapsed}
		}))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	get := func(path string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}
	next := func() obsRec {
		select {
		case o := <-obsCh:
			return o
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a latency observation")
			return obsRec{}
		}
	}

	// /healthz is excluded: it never enqueues an observation. The next
	// observed request must therefore be the one we make after it, which
	// proves the probe was skipped.
	get("/healthz")
	get("/v1/search_artifacts?q=variance")
	if o := next(); o.op != "search_artifacts" {
		t.Fatalf("first observation op = %q, want search_artifacts (was /healthz observed?)", o.op)
	} else if o.status != http.StatusOK {
		t.Fatalf("search_artifacts status = %d, want 200", o.status)
	} else if o.elapsed < 0 {
		t.Fatalf("search_artifacts elapsed = %v, want >= 0", o.elapsed)
	}

	get("/v1/load_artifact?id=finance/variance")
	if o := next(); o.op != "load_artifact" {
		t.Fatalf("observation op = %q, want load_artifact", o.op)
	} else if o.status != http.StatusOK {
		t.Fatalf("load_artifact status = %d, want 200", o.status)
	}

	// The channel must now be empty: exactly two observations for two
	// observed requests (the /healthz probe added none).
	select {
	case extra := <-obsCh:
		t.Fatalf("unexpected extra observation %+v; /healthz must not be observed", extra)
	default:
	}
}
