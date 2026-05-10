package serverboot

import "os"

// readBytes is a tiny helper used by tests across this package
// without dragging in os in every test file.
func readBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
