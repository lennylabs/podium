package conformance

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/hook"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/spi"
	"github.com/lennylabs/podium/pkg/store"
)

// spiCodeNamespaces are the §6.10 error-code namespaces a structured SPI error
// may use.
var spiCodeNamespaces = map[string]bool{
	"registry": true, "ingest": true, "materialize": true, "config": true,
	"auth": true, "quota": true, "domain": true, "visibility": true,
	"network": true, "mcp": true,
}

// TestSPIStructuredErrors is the §9.3 "Structured errors" conformance guard.
// §9.3 states the built-in SPIs (RegistryStore, HarnessAdapter,
// LayerSourceProvider, etc.) "conform to these constraints today", where one
// constraint is that failures "use a structured envelope ({code, message,
// retryable, details}) rather than opaque Go error chains" with codes from the
// §6.10 namespacing. This test asserts every error a built-in SPI returns is a
// structured *spi.Error carrying a non-empty, §6.10-namespaced code and a
// message, so the conformance claim is verifiable and guarded against
// regression.
//
// spec: §9.3 "Structured errors"; §6.10 error-code namespacing.
func TestSPIStructuredErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"store.ErrNotFound", store.ErrNotFound},
		{"store.ErrImmutableViolation", store.ErrImmutableViolation},
		{"store.ErrTenantNotFound", store.ErrTenantNotFound},
		{"source.ErrSourceUnreachable", source.ErrSourceUnreachable},
		{"source.ErrInvalidConfig", source.ErrInvalidConfig},
		{"sign.ErrSignatureInvalid", sign.ErrSignatureInvalid},
		{"sign.ErrSignatureMissing", sign.ErrSignatureMissing},
		{"sign.ErrSigstoreUnavailable", sign.ErrSigstoreUnavailable},
		{"sign.ErrRegistryManagedUnavailable", sign.ErrRegistryManagedUnavailable},
		{"hook.ErrSandboxViolation", hook.ErrSandboxViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertStructuredSPIError(t, tc.name, tc.err)
		})
	}

	// The HarnessAdapter SPI surfaces its failure through Registry.Get. The
	// built-in adapters' Adapt methods return nil on success, so the lookup is
	// the adapter package's representative structured failure.
	t.Run("adapter.Registry.Get", func(t *testing.T) {
		_, err := adapter.DefaultRegistry().Get("not-a-real-harness")
		if e := assertStructuredSPIError(t, "adapter.Registry.Get", err); e != nil && e.Code != "config.unknown_harness" {
			t.Errorf("code = %q, want config.unknown_harness", e.Code)
		}
	})
}

// assertStructuredSPIError fails when err is not a structured *spi.Error with a
// non-empty §6.10-namespaced code and message. It returns the recovered error
// so the caller can assert the specific code.
func assertStructuredSPIError(t *testing.T, name string, err error) *spi.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: nil error", name)
	}
	e, ok := spi.AsError(err)
	if !ok {
		t.Fatalf("%s is not a structured *spi.Error; §9.3 requires SPI failures to use the {code,message,retryable,details} envelope: %T", name, err)
	}
	ns, _, found := strings.Cut(e.Code, ".")
	if !found || !spiCodeNamespaces[ns] {
		t.Errorf("%s: code %q is not §6.10-namespaced (namespace %q)", name, e.Code, ns)
	}
	if e.Message == "" {
		t.Errorf("%s: empty message", name)
	}
	return e
}
