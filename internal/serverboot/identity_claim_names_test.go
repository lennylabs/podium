package serverboot

// Coverage for the §6.3.3 / §13.12 oidc-jwt claim-name settings at the
// configuration level: LoadConfig reads PODIUM_OAUTH_SUBJECT_CLAIM and
// PODIUM_OAUTH_GROUPS_CLAIM, and Settings() reports both under their
// config-file keys so `podium config show --server` names the applied claims.

import (
	"path/filepath"
	"testing"
)

// adfsGroupsClaimURI is the full claim-type URI an AD FS issuance rule emits
// group membership under unless the rule is authored with a short name.
const adfsGroupsClaimURI = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"

// noConfigFile points PODIUM_CONFIG_FILE at an absent path so LoadConfig
// resolves from the environment alone, whatever registry.yaml the developer
// machine carries.
func noConfigFile(t *testing.T) {
	t.Helper()
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yaml"))
}

// Spec: §6.3.3 / §13.12 — PODIUM_OAUTH_SUBJECT_CLAIM names the claim read as
// the caller's subject and PODIUM_OAUTH_GROUPS_CLAIM names the claim read for
// group membership. LoadConfig carries both to the oidc-jwt construction site.
func TestLoadConfig_OAuthClaimNamesFromEnv(t *testing.T) {
	noConfigFile(t)
	t.Setenv("PODIUM_OAUTH_SUBJECT_CLAIM", "idsub")
	t.Setenv("PODIUM_OAUTH_GROUPS_CLAIM", adfsGroupsClaimURI)
	c := LoadConfig()
	if c.oauthSubjectClaim != "idsub" {
		t.Errorf("oauthSubjectClaim = %q, want idsub", c.oauthSubjectClaim)
	}
	if c.oauthGroupsClaim != adfsGroupsClaimURI {
		t.Errorf("oauthGroupsClaim = %q, want %q", c.oauthGroupsClaim, adfsGroupsClaimURI)
	}
}

// Spec: §13.12 — both settings are unset by default, which leaves the verifier
// on "sub" and "groups".
func TestLoadConfig_OAuthClaimNamesUnsetByDefault(t *testing.T) {
	noConfigFile(t)
	t.Setenv("PODIUM_OAUTH_SUBJECT_CLAIM", "")
	t.Setenv("PODIUM_OAUTH_GROUPS_CLAIM", "")
	c := LoadConfig()
	if c.oauthSubjectClaim != "" || c.oauthGroupsClaim != "" {
		t.Errorf("claim names = (%q, %q), want both empty", c.oauthSubjectClaim, c.oauthGroupsClaim)
	}
}

// setting returns the named row from Settings(), failing the test when the row
// is absent.
func setting(t *testing.T, c *Config, name string) Setting {
	t.Helper()
	for _, s := range c.Settings() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("Settings() missing %q", name)
	return Setting{}
}

// Spec: §13.12 — `podium config show --server` reports both claim names under
// their config-file keys, sourced to the env var when it is set.
func TestSettings_OAuthClaimNamesSourceEnv(t *testing.T) {
	t.Setenv("PODIUM_OAUTH_SUBJECT_CLAIM", "idsub")
	t.Setenv("PODIUM_OAUTH_GROUPS_CLAIM", adfsGroupsClaimURI)
	c := &Config{oauthSubjectClaim: "idsub", oauthGroupsClaim: adfsGroupsClaimURI}

	subj := setting(t, c, "identity_provider.subject_claim")
	if subj.Value != "idsub" || subj.Source != "PODIUM_OAUTH_SUBJECT_CLAIM" {
		t.Errorf("subject_claim = (%q, %q), want (idsub, PODIUM_OAUTH_SUBJECT_CLAIM)", subj.Value, subj.Source)
	}
	groups := setting(t, c, "identity_provider.groups_claim")
	if groups.Value != adfsGroupsClaimURI || groups.Source != "PODIUM_OAUTH_GROUPS_CLAIM" {
		t.Errorf("groups_claim = (%q, %q), want (%q, PODIUM_OAUTH_GROUPS_CLAIM)", groups.Value, groups.Source, adfsGroupsClaimURI)
	}
}

// Spec: §13.12 — with the env vars unset, both rows report registry.yaml as
// the source, because each key is also a config-file key.
func TestSettings_OAuthClaimNamesSourceYAMLWhenEnvUnset(t *testing.T) {
	t.Setenv("PODIUM_OAUTH_SUBJECT_CLAIM", "")
	t.Setenv("PODIUM_OAUTH_GROUPS_CLAIM", "")
	c := &Config{oauthSubjectClaim: "idsub", oauthGroupsClaim: "groups"}

	if got := setting(t, c, "identity_provider.subject_claim").Source; got != "registry.yaml" {
		t.Errorf("subject_claim source = %q, want registry.yaml", got)
	}
	if got := setting(t, c, "identity_provider.groups_claim").Source; got != "registry.yaml" {
		t.Errorf("groups_claim source = %q, want registry.yaml", got)
	}
}
