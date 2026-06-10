package server

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §4.6 / §13.10 — bootstrapVisibility honors a non-empty per-layer
// .layer-config declaration and falls back to the all-public standalone
// default when the layer declares nothing. A present-but-empty visibility
// block carries no filters, so it falls back like an absent file rather than
// booting visible to no one.
func TestBootstrapVisibility(t *testing.T) {
	cases := []struct {
		name string
		in   filesystem.Layer
		want layer.Visibility
	}{
		{
			name: "no layer-config defaults public",
			in:   filesystem.Layer{ID: "a", HasVisibility: false},
			want: layer.Visibility{Public: true},
		},
		{
			name: "declared groups honored",
			in:   filesystem.Layer{ID: "b", HasVisibility: true, Visibility: filesystem.Visibility{Groups: []string{"finance"}}},
			want: layer.Visibility{Groups: []string{"finance"}},
		},
		{
			name: "present but empty block defaults public",
			in:   filesystem.Layer{ID: "c", HasVisibility: true, Visibility: filesystem.Visibility{}},
			want: layer.Visibility{Public: true},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := bootstrapVisibility(c.in)
			if got.Public != c.want.Public || got.Organization != c.want.Organization ||
				len(got.Groups) != len(c.want.Groups) || len(got.Users) != len(c.want.Users) {
				t.Fatalf("bootstrapVisibility(%+v) = %+v, want %+v", c.in, got, c.want)
			}
			for i := range c.want.Groups {
				if got.Groups[i] != c.want.Groups[i] {
					t.Errorf("group[%d] = %q, want %q", i, got.Groups[i], c.want.Groups[i])
				}
			}
		})
	}
}
