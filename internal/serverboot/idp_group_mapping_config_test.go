package serverboot

// Coverage for the §6.3.1 / §13.12 group-mapping setting at the configuration
// level: LoadConfig records why a non-empty PODIUM_IDP_GROUP_MAPPING did not
// resolve to a table, and (*Config).validate reports it as
// config.invalid_idp_group_mapping.

import (
	"strings"
	"testing"
)

// gmConfig loads the configuration from the environment alone with the
// storage backends validate requires already selected.
func gmConfig(t *testing.T, spec string) *Config {
	t.Helper()
	noConfigFile(t)
	t.Setenv("PODIUM_IDP_GROUP_MAPPING", spec)
	c := LoadConfig()
	c.bind = "127.0.0.1:8080"
	c.storeType = "sqlite"
	c.objectStore = "filesystem"
	return c
}

// Spec: §6.3.1 / §13.12 — a non-empty PODIUM_IDP_GROUP_MAPPING that carries a
// malformed entry, or that resolves to no claim=group entry at all, fails
// startup with config.invalid_idp_group_mapping. The whitespace-only and
// separators-only values are the same operator mistake, so both are refused:
// the parse-site guard tests the raw value, which sends a whitespace-only
// setting through the parser to an empty table.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestValidate_IdpGroupMappingRefusesUnresolvable(t *testing.T) {
	for _, spec := range []string{"finance", "=finance", "oktaGroupOID=", "ok=fine,broken", " ", " , ", ","} {
		err := gmConfig(t, spec).validate()
		if err == nil || !strings.Contains(err.Error(), "config.invalid_idp_group_mapping") {
			t.Errorf("validate() with PODIUM_IDP_GROUP_MAPPING=%q = %v, want config.invalid_idp_group_mapping", spec, err)
		}
	}
}

// Spec: §6.3.1 / §13.12 — an unset or empty value configures no table and
// startup proceeds, and a value that resolves to a table is accepted and
// reaches the adapter.
func TestValidate_IdpGroupMappingAccepted(t *testing.T) {
	c := gmConfig(t, "")
	if err := c.validate(); err != nil {
		t.Fatalf("validate() with an empty setting = %v, want nil", err)
	}
	if !c.idpGroupMapping.Empty() {
		t.Errorf("idpGroupMapping = %v, want no table", c.idpGroupMapping)
	}
	c = gmConfig(t, " 00g1a2b3c4d5 = finance , 00g9z8y7 = eng ")
	if err := c.validate(); err != nil {
		t.Fatalf("validate() with a well-formed table = %v, want nil", err)
	}
	if got := c.idpGroupMapping.Len(); got != 2 {
		t.Errorf("idpGroupMapping.Len() = %d, want 2", got)
	}
	if got := c.idpGroupMapping.Map([]string{"00g1a2b3c4d5", "other"}); strings.Join(got, ",") != "finance,other" {
		t.Errorf("Map() = %v, want [finance other]", got)
	}
}
