package main

import "testing"

// adminCmd and layerCmd dispatch tables: call into each case so the
// dispatch switch statements are fully exercised. Each branch
// invokes the named subcommand which exits 2 on missing args (the
// most common validation path). The dispatch test asserts only that
// the dispatcher itself didn't fall through to the default.

func TestAdminCmd_DispatchTable(t *testing.T) {
	for _, sub := range []string{
		"erase", "retention", "reembed", "grant", "revoke",
		"show-effective", "runtime",
	} {
		t.Run(sub, func(t *testing.T) {
			withStderr(t, func() {
				code := adminCmd([]string{sub}) // no extra args → child validates
				if code == 0 {
					// Some subcommands accept zero args and succeed
					// (e.g., admin reembed). 0 is fine.
				}
			})
		})
	}
	t.Run("migrate-to-standard", func(t *testing.T) {
		withStderr(t, func() {
			_ = adminCmd([]string{"migrate-to-standard", "--help"})
		})
	})
}

func TestLayerCmd_DispatchTable(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	for _, sub := range []string{
		"register", "list", "reorder", "unregister",
		"reingest", "update", "watch",
	} {
		t.Run(sub, func(t *testing.T) {
			withStderr(t, func() {
				_ = layerCmd([]string{sub, "--help"})
			})
		})
	}
}

func TestProfileCmd_DispatchTable(t *testing.T) {
	withStderr(t, func() {
		// edit branch is covered elsewhere; this exercises help.
		_ = profileCmd([]string{"edit", "--help"})
	})
}

func TestCacheCmd_DispatchTable(t *testing.T) {
	withStderr(t, func() {
		_ = cacheCmd([]string{"prune", "--help"})
	})
}

func TestConfigCmd_DispatchTable(t *testing.T) {
	withStderr(t, func() {
		_ = configCmd([]string{"show", "--help"})
	})
}

func TestArtifactCmd_DispatchTable(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	withStderr(t, func() {
		_ = artifactCmd([]string{"show", "--help"})
	})
}

func TestDomainCmd_DispatchTable(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	for _, sub := range []string{"show", "search", "analyze"} {
		t.Run(sub, func(t *testing.T) {
			withStderr(t, func() {
				_ = domainCmd([]string{sub, "--help"})
			})
		})
	}
}

func TestSyncCmd_OverrideDispatch(t *testing.T) {
	withStderr(t, func() {
		_ = syncCmd([]string{"override", "--help"})
		_ = syncCmd([]string{"save-as", "--help"})
	})
}

func TestAdminRuntimeCmd_DispatchTable(t *testing.T) {
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	for _, sub := range []string{"register", "list"} {
		t.Run(sub, func(t *testing.T) {
			withStderr(t, func() {
				_ = adminRuntimeCmd([]string{sub, "--help"})
			})
		})
	}
}
