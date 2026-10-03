package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/scim"
)

// TestOpenSCIMStore pins the SCIM store selection: an empty path keeps the
// directory in memory, a usable path loads a file-backed store, and a path
// that cannot hold the directory refuses with config.scim_store_unavailable
// rather than falling back to memory.
//
// Spec: §6.3.1, §13.12
func TestOpenSCIMStore(t *testing.T) {
	t.Run("empty path keeps the directory in memory", func(t *testing.T) {
		st, err := openSCIMStore("")
		if err != nil {
			t.Fatalf("openSCIMStore: %v", err)
		}
		if _, ok := st.(*scim.Memory); !ok {
			t.Errorf("store = %T, want *scim.Memory", st)
		}
	})

	t.Run("usable path loads a file store", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scim", "directory.json")
		st, err := openSCIMStore(path)
		if err != nil {
			t.Fatalf("openSCIMStore: %v", err)
		}
		if _, ok := st.(*scim.FileStore); !ok {
			t.Errorf("store = %T, want *scim.FileStore", st)
		}
	})

	t.Run("malformed file refuses startup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "directory.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		st, err := openSCIMStore(path)
		if err == nil {
			t.Fatalf("openSCIMStore returned %T and no error", st)
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "config.scim_store_unavailable:") || !strings.Contains(msg, path) {
			t.Errorf("error = %q, want config.scim_store_unavailable naming %s", msg, path)
		}
	})
}
