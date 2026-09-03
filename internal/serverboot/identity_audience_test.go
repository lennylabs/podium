package serverboot

// Coverage for the §6.3.3 accepted-audience set at the configuration level:
// PODIUM_OAUTH_AUDIENCE carries a comma-separated list, the config-file key
// identity_provider.audience accepts a scalar and a sequence, and both resolve
// into one normalized set on Config.

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Spec: §13.12 — PODIUM_OAUTH_AUDIENCE is a comma-separated list, and
// LoadConfig resolves it into the §6.3.3 accepted-audience set: entries are
// trimmed, blank entries are dropped, and duplicates collapse keeping the
// first occurrence.
func TestLoadConfig_OAuthAudiencesFromEnv(t *testing.T) {
	noConfigFile(t)
	t.Setenv("PODIUM_OAUTH_AUDIENCE", " https://a , , https://b , https://a ")
	c := LoadConfig()
	if got := strings.Join(c.oauthAudiences, "|"); got != "https://a|https://b" {
		t.Errorf("oauthAudiences = %q, want [https://a https://b]", c.oauthAudiences)
	}
}

// Spec: §13.12 — a single value resolves to a one-entry set, which is the
// configuration every deployment carried before the variable took a list.
func TestLoadConfig_OAuthAudiencesSingleValue(t *testing.T) {
	noConfigFile(t)
	t.Setenv("PODIUM_OAUTH_AUDIENCE", "https://podium.acme.com")
	c := LoadConfig()
	if len(c.oauthAudiences) != 1 || c.oauthAudiences[0] != "https://podium.acme.com" {
		t.Errorf("oauthAudiences = %q, want [https://podium.acme.com]", c.oauthAudiences)
	}
}

// Spec: §13.12 — a value carrying only separators and whitespace resolves to
// no entry, so the startup guards refuse to start rather than leave the
// required aud claim unchecked.
func TestLoadConfig_OAuthAudiencesBlankResolvesToNone(t *testing.T) {
	noConfigFile(t)
	t.Setenv("PODIUM_OAUTH_AUDIENCE", " , ")
	c := LoadConfig()
	if len(c.oauthAudiences) != 0 {
		t.Errorf("oauthAudiences = %q, want none", c.oauthAudiences)
	}
	if err := oidcJWTConfigGuard("oidc-jwt", "https://acme.okta.com", c.oauthAudiences); err == nil {
		t.Error("a set with no entry must refuse startup under oidc-jwt")
	}
}

// Spec: §13.12 / §6.3.3 — identity_provider.audience decodes from a scalar
// naming one audience and from a sequence naming several. A scalar is one
// audience verbatim and is never split on a separator, because the
// comma-separated form belongs to PODIUM_OAUTH_AUDIENCE alone. Any other YAML
// kind is a decode error naming the key.
func TestAudienceList_UnmarshalYAML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		doc     string
		want    []string
		wantErr bool
	}{
		{"scalar yields one entry", "audience: https://podium.acme.com", []string{"https://podium.acme.com"}, false},
		{"scalar carrying a comma is one entry", "audience: https://a,https://b", []string{"https://a,https://b"}, false},
		{"sequence yields one entry per element in order", "audience: [https://b, https://a]", []string{"https://b", "https://a"}, false},
		{"sequence drops blanks and duplicates", "audience: [\" https://a \", \"\", https://b, https://a]", []string{"https://a", "https://b"}, false},
		{"empty sequence yields none", "audience: []", nil, false},
		{"null yields none", "audience:", nil, false},
		{"mapping is refused", "audience:\n  value: https://a", nil, true},
		{"non-string scalar is one entry verbatim", "audience: 5", []string{"5"}, false},
		{"sequence of mappings is refused", "audience:\n  - value: https://a", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Audience audienceList `yaml:"audience"`
			}
			err := yaml.Unmarshal([]byte(tc.doc), &got)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("decode of %q: want an error, got %q", tc.doc, got.Audience)
				}
				if !strings.Contains(err.Error(), "identity_provider.audience") {
					t.Errorf("error should name the key, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode of %q: %v", tc.doc, err)
			}
			if strings.Join(got.Audience, "|") != strings.Join(tc.want, "|") {
				t.Errorf("audience = %q, want %q", got.Audience, tc.want)
			}
		})
	}
}

// Spec: §13.12 — the environment beats the config file, so a registry.yaml
// sequence does not widen the set an operator pinned through
// PODIUM_OAUTH_AUDIENCE.
func TestApplyYAML_OAuthAudiencesEnvWins(t *testing.T) {
	t.Parallel()
	c := &Config{oauthAudiences: []string{"https://env.acme.com"}}
	applyYAML(c, &yamlConfig{Identity: yamlIdentityCfg{
		Audience: audienceList{"https://yaml.acme.com"},
	}})
	if len(c.oauthAudiences) != 1 || c.oauthAudiences[0] != "https://env.acme.com" {
		t.Errorf("oauthAudiences = %q, want the environment value alone", c.oauthAudiences)
	}
}

// Spec: §6.3.4 — the first entry is canonical and is the audience the registry
// asks for when it initiates a browser sign-in itself. A set with no entry
// yields the empty string.
func TestCanonicalAudience(t *testing.T) {
	t.Parallel()
	if got := canonicalAudience([]string{"https://a", "https://b"}); got != "https://a" {
		t.Errorf("canonicalAudience = %q, want https://a", got)
	}
	if got := canonicalAudience(nil); got != "" {
		t.Errorf("canonicalAudience(nil) = %q, want empty", got)
	}
}
