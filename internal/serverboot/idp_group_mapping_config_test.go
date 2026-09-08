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
// storage backends validate requires already selected. set distinguishes an
// unset variable from one set to the empty string, which os.Getenv cannot.
func gmConfig(t *testing.T, spec string, set bool) *Config {
	t.Helper()
	noConfigFile(t)
	setEnvForTest(t, "PODIUM_IDP_GROUP_MAPPING", spec, set)
	c := LoadConfig()
	c.bind = "127.0.0.1:8080"
	c.storeType = "sqlite"
	c.objectStore = "filesystem"
	return c
}

// settingValue returns the value and source columns `podium config show`
// renders for the named row.
func settingValue(t *testing.T, c *Config, name string) (string, string) {
	t.Helper()
	for _, s := range c.Settings() {
		if s.Name == name {
			return s.Value, s.Source
		}
	}
	t.Fatalf("Settings() has no %q row", name)
	return "", ""
}

// Spec: §6.3.1 / §13.12 — a non-empty PODIUM_IDP_GROUP_MAPPING that carries a
// malformed entry, or that resolves to no claim=group entry at all, fails
// startup with config.invalid_idp_group_mapping, and the message names the
// setting the operator has to correct. An unset or empty variable is the
// legitimate no-mapping state. The whitespace-only value is the boundary the
// parse-site guard is written on: the guard tests the raw value, so a
// whitespace-only setting reaches the parser, resolves to an empty table, and
// is refused alongside the separators-only one, which is the same operator
// mistake.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestConfig_IdpGroupMapping(t *testing.T) {
	cases := []struct {
		name string
		// spec is the PODIUM_IDP_GROUP_MAPPING value, set only when
		// setEnv is true.
		spec   string
		setEnv bool
		// wantErrText is the fragment the refusal message carries
		// beside the error code; empty means the setting is accepted.
		wantErrText   string
		wantEntries   int
		wantNilTable  bool
		wantRowValue  string
		wantRowSource string
	}{
		{
			name:        "malformed entry with no separator",
			spec:        "00g1financeOID",
			setEnv:      true,
			wantErrText: `"00g1financeOID"`,
		},
		{
			name:        "malformed entry with no claim",
			spec:        "=finance",
			setEnv:      true,
			wantErrText: `"=finance"`,
		},
		{
			name:        "malformed entry with no group",
			spec:        "okta=",
			setEnv:      true,
			wantErrText: `"okta="`,
		},
		{
			name:        "separator only",
			spec:        ",",
			setEnv:      true,
			wantErrText: `","`,
		},
		{
			name:        "separators and whitespace only",
			spec:        " , ",
			setEnv:      true,
			wantErrText: `" , "`,
		},
		{
			name:        "whitespace only",
			spec:        "   ",
			setEnv:      true,
			wantErrText: `"   "`,
		},
		{
			// One typo discards every well-formed entry beside it, so
			// the refusal has to leave no partial table behind.
			name:         "one malformed entry beside a well-formed one",
			spec:         "00g1financeOID=finance,platformOID",
			setEnv:       true,
			wantErrText:  `"platformOID"`,
			wantNilTable: true,
		},
		{
			name:          "well-formed table",
			spec:          "00g1financeOID=finance, 00g2platformOID = platform ",
			setEnv:        true,
			wantEntries:   2,
			wantRowValue:  "2 mappings",
			wantRowSource: "PODIUM_IDP_GROUP_MAPPING",
		},
		{
			name:          "empty value",
			spec:          "",
			setEnv:        true,
			wantNilTable:  true,
			wantRowSource: "default",
		},
		{
			name:          "unset",
			wantNilTable:  true,
			wantRowSource: "default",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := gmConfig(t, tc.spec, tc.setEnv)
			err := c.validate()
			switch {
			case tc.wantErrText == "" && err != nil:
				t.Fatalf("validate() = %v, want nil", err)
			case tc.wantErrText != "":
				if err == nil || !strings.Contains(err.Error(), "config.invalid_idp_group_mapping") {
					t.Fatalf("validate() = %v, want config.invalid_idp_group_mapping", err)
				}
				if !strings.Contains(err.Error(), tc.wantErrText) {
					t.Errorf("validate() = %v, want the message to name %s", err, tc.wantErrText)
				}
			}
			if tc.wantNilTable && c.idpGroupMapping != nil {
				t.Errorf("idpGroupMapping = %v, want no table", c.idpGroupMapping)
			}
			if got := c.idpGroupMapping.Len(); got != tc.wantEntries {
				t.Errorf("idpGroupMapping.Len() = %d, want %d", got, tc.wantEntries)
			}
			if tc.wantRowSource == "" {
				return
			}
			value, source := settingValue(t, c, "idp_group_mapping")
			if value != tc.wantRowValue || source != tc.wantRowSource {
				t.Errorf("idp_group_mapping row = (%q, %q), want (%q, %q)", value, source, tc.wantRowValue, tc.wantRowSource)
			}
		})
	}
}
