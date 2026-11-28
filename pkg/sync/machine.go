package sync

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// MachineConfigName is the name of the machine file in its directory.
const MachineConfigName = "sync.yaml"

// maxMachineConfigBytes bounds the read of the machine file. A sync.yaml is
// a few hundred bytes; the bound keeps a device or a huge file from being
// read whole.
const maxMachineConfigBytes = 1 << 20

// ErrMachineFileUnsafe reports a machine file that exists and fails its
// ownership check. A consumer ignores such a file and reports it.
var ErrMachineFileUnsafe = errors.New("the machine sync.yaml fails its ownership check")

// MachineFileOptions selects the machine file and the owners its check
// trusts.
type MachineFileOptions struct {
	// Dir is the directory of the machine file. Empty selects the platform
	// directory: /etc/podium on Unix, and podium under the ProgramData
	// known folder on Windows.
	Dir string
	// TrustedUIDs are the Unix owners the check accepts besides root. A
	// production consumer passes none; the test seam of the MCP server
	// passes the user ID of the process, because a test cannot create
	// files that root owns.
	TrustedUIDs []uint32
	// AllowSticky accepts, on Unix, a directory on the way that is writable
	// by other users when its sticky bit is set and root or a trusted user
	// owns it, which a temporary directory under /tmp needs. Only the test
	// seam sets it.
	AllowSticky bool
}

// MachineFile is the machine file a consumer read: its path and its parsed
// content.
type MachineFile struct {
	Path   string
	Config *SyncConfig
}

// ReadMachineConfig reads the machine-level sync.yaml, a user source of the
// §4.7.11 consumer trust policy that a repository cannot write. It returns
// nil and no error when the file is absent. A file that exists and fails
// the ownership check of the platform returns an error that wraps
// ErrMachineFileUnsafe; the check reads the content from the descriptor it
// checked, so a file replaced between the check and the read is not read.
// A file that does not parse is an error that does not wrap it.
//
// Spec: §4.7.11 (Machine file)
func ReadMachineConfig(opts MachineFileOptions) (*MachineFile, error) {
	dir := opts.Dir
	if dir == "" {
		d, err := MachineConfigDir()
		if err != nil {
			return nil, fmt.Errorf("%w: locate the machine configuration directory: %v", ErrMachineFileUnsafe, err)
		}
		dir = d
	}
	f, path, err := openMachineFile(dir, opts)
	if err != nil || f == nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMachineConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrMachineFileUnsafe, path, err)
	}
	if len(data) > maxMachineConfigBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrMachineFileUnsafe, path, maxMachineConfigBytes)
	}
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := CheckKnownKeys(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &MachineFile{Path: path, Config: cfg}, nil
}

// unsafeFile wraps a check failure of path in ErrMachineFileUnsafe.
func unsafeFile(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrMachineFileUnsafe, path, fmt.Sprintf(format, args...))
}

// absentFile reports whether err says a path does not exist.
func absentFile(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
