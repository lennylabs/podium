package core

import (
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// TestVisibilityOf pins the projection of every §4.6 visibility field from a
// stored layer config onto the record the evaluator consumes.
//
// Spec: §4.6
func TestVisibilityOf(t *testing.T) {
	cases := []struct {
		name string
		cfg  store.LayerConfig
		want layer.Visibility
	}{
		{
			name: "public",
			cfg:  store.LayerConfig{ID: "base", Public: true},
			want: layer.Visibility{Public: true},
		},
		{
			name: "organization",
			cfg:  store.LayerConfig{ID: "org", Organization: true},
			want: layer.Visibility{Organization: true},
		},
		{
			name: "groups",
			cfg:  store.LayerConfig{ID: "eng", Groups: []string{"eng", "sre"}},
			want: layer.Visibility{Groups: []string{"eng", "sre"}},
		},
		{
			name: "users",
			cfg:  store.LayerConfig{ID: "alice-personal", Users: []string{"alice"}},
			want: layer.Visibility{Users: []string{"alice"}},
		},
		{
			name: "every field at once",
			cfg: store.LayerConfig{
				ID: "mixed", Public: true, Organization: true,
				Groups: []string{"eng"}, Users: []string{"alice", "bob"},
			},
			want: layer.Visibility{
				Public: true, Organization: true,
				Groups: []string{"eng"}, Users: []string{"alice", "bob"},
			},
		},
		{
			name: "no visibility set",
			cfg:  store.LayerConfig{ID: "closed"},
			want: layer.Visibility{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisibilityOf(tc.cfg); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("VisibilityOf(%+v) = %+v, want %+v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestLayerFromConfigStampsPrecedence confirms layerFromConfig still carries
// the ID and the caller-supplied precedence, and reads its visibility from the
// exported projection.
//
// Spec: §4.6
func TestLayerFromConfigStampsPrecedence(t *testing.T) {
	cfg := store.LayerConfig{
		ID: "eng", Organization: true, Groups: []string{"eng"}, Users: []string{"alice"},
	}
	got := layerFromConfig(cfg, 7)
	want := layer.Layer{ID: "eng", Precedence: 7, Visibility: VisibilityOf(cfg)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layerFromConfig = %+v, want %+v", got, want)
	}
}
