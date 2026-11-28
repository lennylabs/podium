//go:build !unix && !windows

package sync

import (
	"errors"
	"os"
	"path/filepath"
)

// MachineConfigDir reports that the platform has no machine file
// directory.
func MachineConfigDir() (string, error) {
	return "", errors.New("this platform has no machine configuration directory")
}

// openMachineFile refuses a machine file on a platform whose ownership
// check is not implemented, so a file that exists is reported rather than
// read unchecked.
func openMachineFile(dir string, _ MachineFileOptions) (*os.File, string, error) {
	path := filepath.Join(dir, MachineConfigName)
	if _, err := os.Lstat(path); absentFile(err) {
		return nil, path, nil
	}
	return nil, path, unsafeFile(path, "this platform has no ownership check")
}
