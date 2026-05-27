package server

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
)

func TestHeaderIdentityResolver_NoHeaders_ReturnsPublic(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	got := HeaderIdentityResolver(r)
	want := layer.Identity{IsPublic: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHeaderIdentityResolver_SubOnly_ReturnsAuthenticated(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(HeaderUserSub, "alice")
	got := HeaderIdentityResolver(r)
	want := layer.Identity{
		Sub:             "alice",
		IsAuthenticated: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHeaderIdentityResolver_AllFields(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(HeaderUserSub, "alice")
	r.Header.Set(HeaderUserEmail, "alice@acme.com")
	r.Header.Set(HeaderUserOrg, "acme")
	r.Header.Set(HeaderUserGroups, "engineering, sre,  platform-team")
	got := HeaderIdentityResolver(r)
	want := layer.Identity{
		Sub:             "alice",
		Email:           "alice@acme.com",
		OrgID:           "acme",
		Groups:          []string{"engineering", "sre", "platform-team"},
		IsAuthenticated: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHeaderIdentityResolver_EmailOnly_StillAuthenticated(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(HeaderUserEmail, "bob@acme.com")
	got := HeaderIdentityResolver(r)
	if !got.IsAuthenticated {
		t.Fatalf("expected IsAuthenticated=true, got %#v", got)
	}
	if got.IsPublic {
		t.Fatalf("expected IsPublic=false when email is set, got %#v", got)
	}
}

func TestHeaderIdentityResolver_WhitespaceOnlyHeaders_ReturnsPublic(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(HeaderUserSub, "   ")
	r.Header.Set(HeaderUserEmail, "")
	got := HeaderIdentityResolver(r)
	if !got.IsPublic {
		t.Fatalf("expected IsPublic=true when sub is whitespace-only, got %#v", got)
	}
}

func TestParseGroupsHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "engineering", []string{"engineering"}},
		{"trimmed", "  engineering  ", []string{"engineering"}},
		{"multi", "engineering,sre,platform-team", []string{"engineering", "sre", "platform-team"}},
		{"spaces_after_comma", "engineering, sre, platform-team", []string{"engineering", "sre", "platform-team"}},
		{"empty_segments_skipped", ",engineering,,sre,", []string{"engineering", "sre"}},
		{"only_commas", ",,,", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseGroupsHeader(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}
