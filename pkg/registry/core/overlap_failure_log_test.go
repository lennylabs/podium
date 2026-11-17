package core

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/vector"
)

// failingScorer is the memory backend whose exact cosine scoring fails.
type failingScorer struct{ *vector.Memory }

func (failingScorer) CosineSimilarities(context.Context, string, []float32, []vector.RowKey) (map[vector.RowKey]float64, error) {
	return nil, errors.New("scoring backend unreachable")
}

// captureLog sends the standard logger to a buffer for the rest of the test.
// The test that calls it does not run in parallel, so no other test's lines
// land in the buffer and the logger's output is restored before the parallel
// tests resume.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// Spec: §7.3.1 — a failed computation is logged with the artifact identifier
// and names the call that failed: the stored-vector read, the neighbour
// query, or the scoring of the rows.
func TestComputeOverlap_FailureLogNamesTheStep(t *testing.T) {
	buf := captureLog(t)
	key := func(id string) []vector.RowKey { return []vector.RowKey{{ArtifactID: id, Version: "1.0.0"}} }

	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", true)
	f.put("b", "1.0.0", "L", "bravo", "", true)
	f.put("n", "1.0.0", "L", "novector", "", false)
	if res, _ := f.reg.ComputeOverlap(f.ctx, key("n")); res.Failed != 1 {
		t.Fatalf("missing vector: %+v, want one failure", res)
	}
	f.idx.fail = true
	if res, _ := f.reg.ComputeOverlap(f.ctx, key("a")); res.Failed != 1 {
		t.Fatalf("failing query: %+v, want one failure", res)
	}

	g := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	g.reg.WithVectorSearch(failingScorer{vector.NewMemory(4)}, overlapEmbedder{table: overlapVectors, model: "m1"})
	g.put("a", "1.0.0", "L", "alpha", "", true)
	g.put("b", "1.0.0", "L", "bravo", "", true)
	if res, _ := g.reg.ComputeOverlap(g.ctx, key("a")); res.Failed != 1 {
		t.Fatalf("failing scorer: %+v, want one failure", res)
	}

	out := buf.String()
	for _, want := range []string{
		"lint.semantic_overlap: stored-vector read for n@1.0.0 failed: " + errNoStoredVector.Error(),
		"lint.semantic_overlap: neighbour query for a@1.0.0 failed: index unreachable",
		"lint.semantic_overlap: scoring for a@1.0.0 failed: scoring backend unreachable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "index query for") {
		t.Errorf("log still calls a failure an index query:\n%s", out)
	}
	stepErr := error(&overlapStepError{step: stepStoredVector, err: errNoStoredVector})
	if !errors.Is(stepErr, errNoStoredVector) || stepErr.Error() != "stored-vector read: "+errNoStoredVector.Error() {
		t.Errorf("step error = %q, want it to wrap the cause after the step name", stepErr)
	}
	logOverlapFailure(f.put("z", "1.0.0", "L", "zulu", "", false), errors.New("plain"))
	if !strings.Contains(buf.String(), "computation for z@1.0.0 failed: plain") {
		t.Errorf("an error without a step is not logged as a computation failure:\n%s", buf.String())
	}
}
