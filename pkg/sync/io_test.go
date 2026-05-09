package sync_test

import "os"

// readFileShim wraps os.ReadFile so override_test.go can read files
// without importing os directly.
func readFileShim(path string) ([]byte, error) {
	return os.ReadFile(path)
}
