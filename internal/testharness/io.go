package testharness

import "os"

// readFileImpl wraps os.ReadFile so tests in this package can stub it.
func readFileImpl(path string) ([]byte, error) {
	return os.ReadFile(path)
}
