package identity

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestIdentityFromTrustedHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		sub             string
		email           string
		groups          string
		org             string
		proxySecret     string
		presentedSecret string
		want            Identity
	}{
		{
			name:   "full identity, no secret configured",
			sub:    "alice@acme.com",
			email:  "alice@acme.com",
			groups: "engineering, finance",
			org:    "acme",
			want: Identity{
				Sub: "alice@acme.com", Email: "alice@acme.com", OrgID: "acme",
				Groups: []string{"engineering", "finance"}, IsAuthenticated: true,
			},
		},
		{
			name: "missing sub is anonymous",
			sub:  "",
			want: Identity{},
		},
		{
			name:   "blank groups yields no groups",
			sub:    "bob@acme.com",
			groups: " , ,",
			want:   Identity{Sub: "bob@acme.com", IsAuthenticated: true},
		},
		{
			name:            "secret configured and matches",
			sub:             "alice@acme.com",
			proxySecret:     "s3cr3t",
			presentedSecret: "s3cr3t",
			want:            Identity{Sub: "alice@acme.com", IsAuthenticated: true},
		},
		{
			name:            "secret configured but mismatched is anonymous",
			sub:             "alice@acme.com",
			proxySecret:     "s3cr3t",
			presentedSecret: "wrong",
			want:            Identity{},
		},
		{
			name:        "secret configured but none presented is anonymous",
			sub:         "alice@acme.com",
			proxySecret: "s3cr3t",
			want:        Identity{},
		},
		{
			name:            "secret empty ignores presented secret",
			sub:             "alice@acme.com",
			presentedSecret: "anything",
			want:            Identity{Sub: "alice@acme.com", IsAuthenticated: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IdentityFromTrustedHeaders(tc.sub, tc.email, tc.groups, tc.org, tc.proxySecret, tc.presentedSecret)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestServerSideProviders_ResolveIsInboundOnly(t *testing.T) {
	t.Parallel()
	for _, p := range []Provider{OIDCJWT{}, TrustedHeaders{}} {
		if _, err := p.Resolve(context.Background()); !errors.Is(err, errServerSideProvider) {
			t.Errorf("%s.Resolve err = %v, want errServerSideProvider", p.ID(), err)
		}
	}
	if (OIDCJWT{}).ID() != "oidc-jwt" {
		t.Errorf("OIDCJWT.ID = %q", (OIDCJWT{}).ID())
	}
	if (TrustedHeaders{}).ID() != "trusted-headers" {
		t.Errorf("TrustedHeaders.ID = %q", TrustedHeaders{}.ID())
	}
}

func TestDefaultRegistry_RegistersServerSideProviders(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"oidc-jwt", "trusted-headers"} {
		if !Default.Has(id) {
			t.Errorf("Default registry missing %q", id)
		}
		prov, err := Default.New(id, Config{})
		if err != nil {
			t.Errorf("Default.New(%q): %v", id, err)
			continue
		}
		if prov.ID() != id {
			t.Errorf("provider ID = %q, want %q", prov.ID(), id)
		}
	}
}
