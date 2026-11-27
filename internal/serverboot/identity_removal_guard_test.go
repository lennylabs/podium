package serverboot

import (
	"context"
	"errors"
	"strings"
	"testing"

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

// Spec: §13.10 (Identity provider removed between starts) — a start with a
// request-time verifier records verified identity whatever the store held,
// and the acknowledgement does not change that.
func TestIdentityProviderRemovalGuard_VerifiedStartRecords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		recorded bool
		allow    bool
	}{
		{name: "first verified start"},
		{name: "later verified start", recorded: true},
		{name: "acknowledgement set", allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &recordStore{recorded: tc.recorded}
			cfg := &Config{identityProvider: "trusted-headers", allowIdentityProviderRemoval: tc.allow}
			if err := identityProviderRemovalGuard(context.Background(), st, cfg, true); err != nil {
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
// config.identity_provider_removed. The message names why the start has no
// verifier, what it would admit, and both remedies, and the admin clause
// appears only where the §4.7.2 admin arm would admit every caller.
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
		{name: "label resolving to no provider", cfg: &Config{identityProvider: "external"}, cause: `PODIUM_IDENTITY_PROVIDER="external" resolves to no provider`},
		{name: "public mode", cfg: &Config{publicMode: true}, cause: "public mode (PODIUM_PUBLIC_MODE) is on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &recordStore{recorded: true}
			err := identityProviderRemovalGuard(context.Background(), st, tc.cfg, false)
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
// never recorded verified identity admits a start without a verifier, with or
// without the acknowledgement, and the start writes nothing.
func TestIdentityProviderRemovalGuard_NeverVerifiedStoreUnaffected(t *testing.T) {
	t.Parallel()
	for _, allow := range []bool{false, true} {
		st := &recordStore{}
		cfg := &Config{allowIdentityProviderRemoval: allow}
		if err := identityProviderRemovalGuard(context.Background(), st, cfg, false); err != nil {
			t.Errorf("allow=%v: never-verified store refused: %v", allow, err)
		}
		if len(st.writes) != 0 {
			t.Errorf("allow=%v: start wrote %v, want no write", allow, st.writes)
		}
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the
// acknowledgement admits the start, clears the record, and logs a warning
// that names the removal and asks for the setting to be removed.
func TestIdentityProviderRemovalGuard_AcknowledgementClears(t *testing.T) {
	logs := captureLogs(t)
	st := &recordStore{recorded: true}
	cfg := &Config{allowIdentityProviderRemoval: true}
	if err := identityProviderRemovalGuard(context.Background(), st, cfg, false); err != nil {
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
			err := identityProviderRemovalGuard(context.Background(), tc.st, tc.cfg, tc.verifier)
			if !errors.Is(err, fault) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one wrapping the store fault and naming %q", err, tc.want)
			}
		})
	}
}

// Spec: §13.10 (Identity provider removed between starts) — the boot sequence
// wires the guard: a trusted-headers start records verified identity in the
// SQLite store, a later start without a provider is refused before it serves,
// the acknowledged start succeeds and clears the record, and the next start
// without the acknowledgement succeeds.
func TestRun_IdentityProviderRemovalAcrossStarts(t *testing.T) {
	f := newBootFixture(t)
	t.Setenv("PODIUM_PUBLIC_MODE", "")
	t.Setenv("PODIUM_MULTI_TENANT", "")
	t.Setenv("PODIUM_TRUSTED_PROXY_SECRET", "")
	t.Setenv("PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL", "")

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
