package serverboot

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// layerRepoFormStub is a store whose MigrateLayerRepoForm returns a fixed
// count and error.
type layerRepoFormStub struct {
	store.Store
	n   int
	err error
}

func (s layerRepoFormStub) MigrateLayerRepoForm(context.Context) (int, error) { return s.n, s.err }

// Spec: §13.4 — the start-time rewrite runs on a store that implements it,
// wraps its failure so the start refuses with the cause, and logs the count of
// rewritten rows only when it is above 0. A store with no such method, as the
// memory store, is left alone.
// Spec: §7.3.1 (Repository credentials)
//
// Each case replaces the process-wide log writer, so the test does not run in
// parallel.
func TestMigrateLayerRepoForm(t *testing.T) {
	refused := errors.New("rewrite refused")
	const rewrote2 = "layer repo migration: rewrote 2 stored layer(s) into the two-column form"
	cases := []struct {
		name    string
		st      store.Store
		wantErr error
		wantLog string
	}{
		{"error", layerRepoFormStub{err: refused}, refused, ""},
		{"two rows", layerRepoFormStub{n: 2}, nil, rewrote2},
		{"no row", layerRepoFormStub{}, nil, ""},
		{"memory store", store.NewMemory(), nil, ""},
	}
	for _, tc := range cases {
		var logs bytes.Buffer
		prev := log.Writer()
		log.SetOutput(&logs)
		err := migrateLayerRepoForm(context.Background(), tc.st)
		log.SetOutput(prev)

		errOK := err == nil && tc.wantErr == nil ||
			err != nil && errors.Is(err, tc.wantErr) && strings.HasPrefix(err.Error(), "layer repo migration: ")
		logOK := strings.Contains(logs.String(), tc.wantLog) &&
			(tc.wantLog != "" || !strings.Contains(logs.String(), "layer repo migration"))
		if !errOK || !logOK {
			t.Errorf("%s: migrateLayerRepoForm = %v, want %v under the layer repo migration prefix; log %q, want %q",
				tc.name, err, tc.wantErr, logs.String(), tc.wantLog)
		}
	}
}
