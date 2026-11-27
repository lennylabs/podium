package serverboot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// recordStore is an identityRecordStore whose read and write can fail.
type recordStore struct {
	recorded bool
	readErr  error
	writeErr error
	writes   []bool
}

func (r *recordStore) DataMigrationApplied(_ context.Context, name string) (bool, error) {
	if name != store.DeploymentMarkerVerifiedIdentity {
		return false, errors.New("unexpected marker " + name)
	}
	return r.recorded, r.readErr
}

func (r *recordStore) SetDataMigrationApplied(_ context.Context, name string, applied bool) error {
	if name != store.DeploymentMarkerVerifiedIdentity {
		return errors.New("unexpected marker " + name)
	}
	if r.writeErr != nil {
		return r.writeErr
	}
	r.writes = append(r.writes, applied)
	r.recorded = applied
	return nil
}

// guardStart runs both halves of the guard for one start, as run does: the
// removal check after the store opens, then the record once the verifier
// phase has installed a verifier or not.
func guardStart(st identityRecordStore, cfg *Config, verifierInstalled bool) error {
	ctx := context.Background()
	check, err := refuseIdentityProviderRemoval(ctx, st, cfg)
	if err != nil {
		return err
	}
	return recordVerifiedIdentity(ctx, st, cfg, verifierInstalled, check)
}

// Spec: §13.10 (Identity provider removed between starts) — the prediction the
// removal check runs on matches the providers whose boot arm installs a
// verifier, and public mode, an unset provider, `none`, `external`, and a
// registered provider without a verifier all predict none.
func TestExpectsRequestVerifier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cfg  *Config
		want bool
	}{
		{&Config{identityProvider: "injected-session-token"}, true},
		{&Config{identityProvider: "oidc-jwt"}, true},
		{&Config{identityProvider: "trusted-headers"}, true},
		{&Config{identityProvider: "oauth-device-code"}, false},
		{&Config{identityProvider: "external"}, false},
		{&Config{identityProvider: "none"}, false},
		{&Config{}, false},
		{&Config{publicMode: true}, false},
	} {
		if got := expectsRequestVerifier(tc.cfg); got != tc.want {
			t.Errorf("expectsRequestVerifier(provider=%q, public=%v) = %v, want %v", tc.cfg.identityProvider, tc.cfg.publicMode, got, tc.want)
		}
	}
}

// Spec: §13.10 (Identity provider removed between starts) — a start with a
// request-time verifier records verified identity whatever the store held,
// and the acknowledgement does not change that.
func TestIdentityProviderRemovalGuard_VerifiedStartRecords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		recorded bool
	}{
		{name: "first verified start"},
		{name: "later verified start", recorded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &recordStore{recorded: tc.recorded}
			cfg := &Config{identityProvider: "trusted-headers"}
			if err := guardStart(st, cfg, true); err != nil {
				t.Fatalf("verified start refused: %v", err)
			}
			if !st.recorded || len(st.writes) != 1 {
				t.Errorf("record = %v after writes %v, want one write that sets it", st.recorded, st.writes)
			}
		})
	}
}

// Spec: §13.10 (Identity provider removed between starts) — a start without a
// verifier over a store that recorded verified identity is refused with
// config.identity_provider_removed, before it writes anything. The message
// names why the start has no verifier, what it would admit, and both
// remedies, and the admin clause appears only where the §4.7.2 admin arm
// would admit every caller.
// Matrix: §6.10 (config.identity_provider_removed)
func TestIdentityProviderRemovalGuard_RefusesWithoutVerifier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		cfg       *Config
		cause     string
		wantAdmin bool
	}{
		{name: "no identity provider", cfg: &Config{}, cause: "no identity provider is configured", wantAdmin: true},
		{name: "none label", cfg: &Config{identityProvider: "none"}, cause: "no identity provider is configured", wantAdmin: true},
		{name: "external authentication", cfg: &Config{identityProvider: "external"}, cause: `PODIUM_IDENTITY_PROVIDER="external" leaves authentication to whatever is in front of the registry`},
		{name: "public mode", cfg: &Config{publicMode: true}, cause: "public mode (PODIUM_PUBLIC_MODE) is on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &recordStore{recorded: true}
			_, err := refuseIdentityProviderRemoval(context.Background(), st, tc.cfg)
			if err == nil {
				t.Fatal("start without a verifier over a recorded store was admitted")
			}
			msg := err.Error()
			for _, want := range []string{
				"config.identity_provider_removed",
				"previously ran with verified identity",
				tc.cause,
				"show every restricted layer to every caller",
				"Restore the identity provider",
				"--allow-identity-provider-removal",
				"PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal missing %q:\n%s", want, msg)
				}
			}
			if got := strings.Contains(msg, "admit every caller as an admin"); got != tc.wantAdmin {
				t.Errorf("admin clause present = %v, want %v:\n%s", got, tc.wantAdmin, msg)
			}
			if !st.recorded || len(st.writes) != 0 {
				t.Errorf("refused start changed the record: recorded %v, writes %v", st.recorded, st.writes)
			}
		})
	}
}

// Spec: §13.10 (Identity provider removed between starts) — a store that
// never recorded verified identity admits a start without a verifier and the
// start writes nothing.
func TestIdentityProviderRemovalGuard_NeverVerifiedStoreUnaffected(t *testing.T) {
	t.Parallel()
	st := &recordStore{}
	if err := guardStart(st, &Config{}, false); err != nil {
		t.Errorf("never-verified store refused: %v", err)
	}
	if len(st.writes) != 0 {
		t.Errorf("start wrote %v, want no write", st.writes)
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the
// acknowledgement admits the start, clears the record, and logs a warning
// that names the removal and asks for the setting to be removed.
func TestIdentityProviderRemovalGuard_AcknowledgementClears(t *testing.T) {
	logs := captureLogs(t)
	st := &recordStore{recorded: true}
	cfg := &Config{allowIdentityProviderRemoval: true}
	if err := guardStart(st, cfg, false); err != nil {
		t.Fatalf("acknowledged removal refused: %v", err)
	}
	out := logs.String()
	if st.recorded || len(st.writes) != 1 {
		t.Errorf("record = %v after writes %v, want one write that clears it", st.recorded, st.writes)
	}
	for _, want := range []string{"warning: identity provider removed", "no identity provider is configured", "remove PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q:\n%s", want, out)
		}
	}
}

// Spec: §13.10 (Identity provider removed between starts), §13.12 — the
// acknowledgement on a start where it has no effect, a verified start or a
// start over a store with no record, changes nothing and logs that it should
// be removed, because while it stays set it admits every later removal.
func TestIdentityProviderRemovalGuard_UnusedAcknowledgementWarns(t *testing.T) {
	logs := captureLogs(t)
	verified := &recordStore{}
	if err := guardStart(verified, &Config{identityProvider: "oidc-jwt", allowIdentityProviderRemoval: true}, true); err != nil {
		t.Fatalf("verified start with the acknowledgement refused: %v", err)
	}
	unrecorded := &recordStore{}
	if err := guardStart(unrecorded, &Config{allowIdentityProviderRemoval: true}, false); err != nil {
		t.Fatalf("start without a record refused: %v", err)
	}
	out := logs.String()
	for _, want := range []string{
		"has no effect on this start because this start installs a request-time identity verifier",
		"has no effect on this start because the metadata store records no earlier start with verified identity",
		"while it stays set a later start that drops the identity provider is admitted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q:\n%s", want, out)
		}
	}
	if !verified.recorded || len(unrecorded.writes) != 0 {
		t.Errorf("verified record %v, unrecorded writes %v; want the record set and no write", verified.recorded, unrecorded.writes)
	}
}

// Spec: §13.10 (Identity provider removed between starts) — store failures
// fail closed: an unreadable record refuses a start without a verifier, with
// or without the acknowledgement, and a record that cannot be written refuses
// a verified start and an acknowledged removal alike.
func TestIdentityProviderRemovalGuard_StoreFailuresRefuse(t *testing.T) {
	t.Parallel()
	fault := errors.New("disk I/O error")
	for _, tc := range []struct {
		name     string
		st       *recordStore
		cfg      *Config
		verifier bool
		want     string
	}{
		{name: "unreadable record", st: &recordStore{readErr: fault}, cfg: &Config{}, want: "read the verified-identity record"},
		{name: "unreadable record with acknowledgement", st: &recordStore{readErr: fault}, cfg: &Config{allowIdentityProviderRemoval: true}, want: "read the verified-identity record"},
		{name: "unwritable record on verified start", st: &recordStore{writeErr: fault}, cfg: &Config{identityProvider: "oidc-jwt"}, verifier: true, want: "record verified identity"},
		{name: "uncleared record on acknowledged removal", st: &recordStore{recorded: true, writeErr: fault}, cfg: &Config{allowIdentityProviderRemoval: true}, want: "clear the verified-identity record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guardStart(tc.st, tc.cfg, tc.verifier)
			if !errors.Is(err, fault) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one wrapping the store fault and naming %q", err, tc.want)
			}
		})
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the record step
// refuses a start whose installed verifier disagrees with the prediction the
// removal check ran on. The boot arms and verifiedProviders agree today, so
// the mismatch is constructed directly.
func TestRecordVerifiedIdentity_PredictionMismatchRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cfg      *Config
		verifier bool
	}{
		{&Config{identityProvider: "oauth-device-code"}, true},
		{&Config{identityProvider: "oidc-jwt"}, false},
	} {
		st := &recordStore{}
		err := recordVerifiedIdentity(context.Background(), st, tc.cfg, tc.verifier, identityRemovalCheck{})
		if err == nil || !strings.Contains(err.Error(), "update verifiedProviders") || len(st.writes) != 0 {
			t.Errorf("provider %q, verifier %v: err = %v, writes %v; want a refusal and no write", tc.cfg.identityProvider, tc.verifier, err, st.writes)
		}
	}
}

// pinIdentityEnv clears every variable the guard reads so a value inherited
// from the developer's shell cannot change which start is verified.
func pinIdentityEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PODIUM_PUBLIC_MODE", "PODIUM_MULTI_TENANT", "PODIUM_TRUSTED_PROXY_SECRET", "PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL", "PODIUM_BOOTSTRAP_ADMINS", "PODIUM_OPERATOR_ADMINS", "PODIUM_DEFAULT_LAYER_VISIBILITY"} {
		t.Setenv(k, "")
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the boot sequence
// wires the guard: a trusted-headers start records verified identity in the
// SQLite store, a later start without a provider is refused before it serves,
// the acknowledged start succeeds and clears the record, and the next start
// without the acknowledgement succeeds.
func TestRun_IdentityProviderRemovalAcrossStarts(t *testing.T) {
	f := newBootFixture(t)
	pinIdentityEnv(t)

	t.Setenv("PODIUM_IDENTITY_PROVIDER", "trusted-headers")
	if _, err := f.boot(t); err != nil {
		t.Fatalf("trusted-headers start: %v", err)
	}
	t.Setenv("PODIUM_IDENTITY_PROVIDER", "")
	_, refused := f.boot(t)
	t.Setenv("PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL", "true")
	_, acknowledged := f.boot(t)
	t.Setenv("PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL", "")
	_, after := f.boot(t)

	recorded, err := f.openStoreDirect(t).DataMigrationApplied(context.Background(), store.DeploymentMarkerVerifiedIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if refused == nil || !strings.Contains(refused.Error(), "config.identity_provider_removed") || acknowledged != nil || after != nil || recorded {
		t.Errorf("starts: refused %v, acknowledged %v, after %v, record left %v; want the code, nil, nil, false",
			refused, acknowledged, after, recorded)
	}
}

// storeSnapshot renders the rows a start's bootstrap writes: every tenant,
// and each tenant's layer configuration and admin grants.
func storeSnapshot(t *testing.T, st store.Store) string {
	t.Helper()
	ctx := context.Background()
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, tenant := range tenants {
		layers, err := st.ListLayerConfigs(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		grants, err := st.ListAdminGrants(ctx, tenant.ID)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "tenant %+v\nlayers %+v\ngrants %+v\n", tenant, layers, grants)
	}
	return b.String()
}

// anonymousLoad returns the status of an anonymous load of the fixture's
// artifact from the registry at addr.
func anonymousLoad(t *testing.T, addr string) int {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/v1/load_artifact?id=alpha")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Spec: §13.10 (Identity provider removed between starts), §13.2.3 — a start
// without an identity provider, refused while a verified replica serves the
// same store, writes nothing: the tenants, the layer configuration, and the
// admin grants are unchanged, and the replica keeps refusing the anonymous
// caller the private layer it served before. The refused start carries a
// --layer-path bootstrap, which without a provider would store the layer as
// public, and a bootstrap admin, which it would grant.
func TestRun_RefusedStartLeavesStoreAndLiveReplicaUnchanged(t *testing.T) {
	f := newBootFixture(t)
	pinIdentityEnv(t)
	t.Setenv("PODIUM_IDENTITY_PROVIDER", "trusted-headers")

	var before, after string
	var statusBefore, statusAfter int
	var refused error
	_, err := f.bootWith(t, func() {
		st := f.openStoreDirect(t)
		before = storeSnapshot(t, st)
		statusBefore = anonymousLoad(t, f.addr)

		t.Setenv("PODIUM_IDENTITY_PROVIDER", "")
		t.Setenv("PODIUM_BOOTSTRAP_ADMINS", "bob@acme.com")
		t.Setenv("PODIUM_BIND", "127.0.0.1:0")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		refused = run(ctx, func() {})

		after = storeSnapshot(t, st)
		statusAfter = anonymousLoad(t, f.addr)
	})
	if err != nil {
		t.Fatalf("verified replica: %v", err)
	}
	if refused == nil || !strings.Contains(refused.Error(), "config.identity_provider_removed") {
		t.Fatalf("second start = %v, want config.identity_provider_removed", refused)
	}
	if before != after || statusBefore != http.StatusNotFound || statusAfter != http.StatusNotFound {
		t.Errorf("anonymous load %d then %d, want 404 both times; store changed = %v\nbefore:\n%s\nafter:\n%s",
			statusBefore, statusAfter, before != after, before, after)
	}
}

// Spec: §13.10 (Identity provider removed between starts), §6.3.2 — a
// provider the registry recognises but does not verify at request time is
// refused with config.identity_provider_unverified whether or not the store
// holds the record, because acknowledging a removal cannot admit it. The
// refusal reads and writes nothing.
func TestRefuseIdentityProviderRemoval_UnverifiedProviderTakesPrecedence(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		st := &recordStore{recorded: recorded}
		cfg := &Config{identityProvider: "oauth-device-code", allowIdentityProviderRemoval: true}
		_, err := refuseIdentityProviderRemoval(context.Background(), st, cfg)
		if err == nil || !strings.Contains(err.Error(), "config.identity_provider_unverified") || strings.Contains(err.Error(), "config.identity_provider_removed") || len(st.writes) != 0 {
			t.Errorf("record %v: err = %v, writes %v; want config.identity_provider_unverified and no write", recorded, err, st.writes)
		}
	}
}

// Spec: §13.10 (Identity provider removed between starts), §6.3.2 — after a
// verified start, a start naming oauth-device-code over the same store is
// refused with config.identity_provider_unverified before its bootstrap, so
// the tenants, the layer configuration, the admin grants, and the record are
// unchanged.
func TestRun_UnverifiedProviderOverRecordedStoreWritesNothing(t *testing.T) {
	f := newBootFixture(t)
	pinIdentityEnv(t)
	t.Setenv("PODIUM_IDENTITY_PROVIDER", "trusted-headers")
	if _, err := f.boot(t); err != nil {
		t.Fatalf("trusted-headers start: %v", err)
	}
	st := f.openStoreDirect(t)
	before := storeSnapshot(t, st)

	t.Setenv("PODIUM_IDENTITY_PROVIDER", "oauth-device-code")
	t.Setenv("PODIUM_BOOTSTRAP_ADMINS", "bob@acme.com")
	_, refused := f.boot(t)

	recorded, err := st.DataMigrationApplied(context.Background(), store.DeploymentMarkerVerifiedIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if refused == nil || !strings.Contains(refused.Error(), "config.identity_provider_unverified") {
		t.Fatalf("oauth-device-code start = %v, want config.identity_provider_unverified", refused)
	}
	if after := storeSnapshot(t, st); after != before || !recorded {
		t.Errorf("record kept %v; store changed = %v\nbefore:\n%s\nafter:\n%s", recorded, after != before, before, after)
	}
}
