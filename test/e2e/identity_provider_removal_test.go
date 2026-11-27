package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// identityStartEnv returns the environment for one standalone start over the
// SQLite store under home. Each variable the guard reads is pinned, so a value
// inherited from the developer's shell cannot change which start is verified.
func identityStartEnv(home, provider string, extra ...string) []string {
	return append([]string{
		"HOME=" + home,
		"PODIUM_IDENTITY_PROVIDER=" + provider,
		"PODIUM_PUBLIC_MODE=",
		"PODIUM_MULTI_TENANT=",
		"PODIUM_TRUSTED_PROXY_SECRET=",
		"PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=",
		"PODIUM_SQLITE_PATH=",
		"PODIUM_REGISTRY_STORE=",
	}, extra...)
}

// verifiedIdentityRecorded reports whether the standalone SQLite store under
// home holds the verified-identity record.
func verifiedIdentityRecorded(t *testing.T, home string) bool {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(home, ".podium", "standalone", "podium.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	recorded, err := st.DataMigrationApplied(context.Background(), store.DeploymentMarkerVerifiedIdentity)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	return recorded
}

// storeRows renders the tenants of the standalone SQLite store under home with
// each tenant's layer configuration and admin grants, the rows a start's
// bootstrap writes.
func storeRows(t *testing.T, home string) string {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(home, ".podium", "standalone", "podium.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	var b strings.Builder
	for _, tenant := range tenants {
		layers, err := st.ListLayerConfigs(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list layers: %v", err)
		}
		grants, err := st.ListAdminGrants(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list grants: %v", err)
		}
		fmt.Fprintf(&b, "tenant %+v\nlayers %+v\ngrants %+v\n", tenant, layers, grants)
	}
	return b.String()
}

// startAndStop starts the registry, waits for /healthz, stops it, and returns
// its log.
func startAndStop(t *testing.T, env []string, args ...string) string {
	t.Helper()
	srv := startServerArgs(t, env, append([]string{"serve", "--standalone"}, args...)...)
	stopProc(srv.cmd)
	return srv.log()
}

// Spec: §13.10 (Identity provider removed between starts), §13.2.3, §4.6 — a
// registry that ran under trusted-headers and restarts on the same SQLite
// store without an identity provider is refused with
// config.identity_provider_removed, and the refused start leaves the tenants,
// the layer configuration, and the admin grants as they were, although its
// --layer-path bootstrap would store the layer as public and it names a
// bootstrap admin. The acknowledged start serves, logs the removal, and clears
// the record, and the next start without the acknowledgement serves.
// Matrix: §6.10 (config.identity_provider_removed)
func TestIdentityProviderRemoval_RefusedThenAcknowledged(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	layers := "PODIUM_LAYER_PATH=" + writeRegistry(t, map[string]string{
		"ops/acme/alpha/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\nsensitivity: low\ndescription: alpha\n---\n\nalpha body\n",
	})
	startAndStop(t, identityStartEnv(home, "trusted-headers", layers))
	if !verifiedIdentityRecorded(t, home) {
		t.Fatal("a trusted-headers start recorded no verified identity")
	}
	before := storeRows(t, home)

	refusedStart(t, identityStartEnv(home, "", layers, "PODIUM_BOOTSTRAP_ADMINS=bob@acme.com"),
		"config.identity_provider_removed",
		"previously ran with verified identity",
		"no identity provider is configured",
		"show every restricted layer to every caller and admit every caller as an admin",
		"Restore the identity provider",
		"--allow-identity-provider-removal",
		"PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true",
	)
	if !verifiedIdentityRecorded(t, home) {
		t.Fatal("the refused start cleared the record")
	}
	if after := storeRows(t, home); after != before {
		t.Fatalf("the refused start changed the store\nbefore:\n%s\nafter:\n%s", before, after)
	}

	ack := startAndStop(t, identityStartEnv(home, ""), "--allow-identity-provider-removal")
	if !strings.Contains(ack, "warning: identity provider removed") {
		t.Errorf("acknowledged start logged no removal warning:\n%s", ack)
	}
	if verifiedIdentityRecorded(t, home) {
		t.Fatal("the acknowledged start left the record in place")
	}
	startAndStop(t, identityStartEnv(home, ""))
}

// Spec: §13.10 (Identity provider removed between starts) — public mode
// installs no request-time verifier, so a public-mode start after a verified
// start on the same store is refused, and the environment variable form of
// the acknowledgement admits it.
func TestIdentityProviderRemoval_PublicModeAfterVerifiedStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	startAndStop(t, identityStartEnv(home, "trusted-headers"))

	refusedStart(t, identityStartEnv(home, "", "PODIUM_PUBLIC_MODE=true"),
		"config.identity_provider_removed",
		"public mode (PODIUM_PUBLIC_MODE) is on",
	)
	startAndStop(t, identityStartEnv(home, "", "PODIUM_PUBLIC_MODE=true", "PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true"))
	if verifiedIdentityRecorded(t, home) {
		t.Fatal("the acknowledged public-mode start left the record in place")
	}
}

// Spec: §13.10 (Identity provider removed between starts) — a registry that
// never installed a verifier never holds the record, so repeated starts
// without an identity provider, with a --layer-path bootstrap, with the
// acknowledgement, and in public mode all serve, and none of them writes the
// record. The start with the acknowledgement logs that it has no effect.
func TestIdentityProviderRemoval_NeverVerifiedRegistryUnaffected(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	layers := writeRegistry(t, map[string]string{
		"ops/acme/alpha/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\nsensitivity: low\ndescription: alpha\n---\n\nalpha body\n",
	})

	startAndStop(t, identityStartEnv(home, ""), "--layer-path", layers)
	startAndStop(t, identityStartEnv(home, ""), "--layer-path", layers)
	unused := startAndStop(t, identityStartEnv(home, ""), "--allow-identity-provider-removal")
	if !strings.Contains(unused, "has no effect on this start") {
		t.Errorf("a start with the acknowledgement and no record logged no warning to remove it:\n%s", unused)
	}
	startAndStop(t, identityStartEnv(home, "", "PODIUM_PUBLIC_MODE=true"))
	if verifiedIdentityRecorded(t, home) {
		t.Fatal("a registry that never installed a verifier holds the verified-identity record")
	}
}
