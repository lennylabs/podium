package store_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// Spec: §9.1 RegistryStore SPI / §9.3 forward compatibility — every
// built-in backend passes the same conformance suite.
func TestStore_Memory_Conformance(t *testing.T) {
	storetest.Suite(t, func(*testing.T) store.Store {
		return store.NewMemory()
	})
}

// Spec: §13.10 Standalone Deployment — the standalone backend is
// SQLite. The same conformance suite that runs against Memory must
// pass against SQLite, ensuring the two are interchangeable.
func TestStore_SQLite_Conformance(t *testing.T) {
	storetest.Suite(t, func(t *testing.T) store.Store {
		s, err := store.OpenSQLite(":memory:")
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
