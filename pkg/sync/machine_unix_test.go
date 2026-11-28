//go:build unix

package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// machineDir creates a directory for a machine file that the process user
// owns, with mode 0755, and options that trust that user.
func machineDir(t *testing.T) (string, MachineFileOptions) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "podium")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, MachineFileOptions{Dir: dir, TrustedUIDs: []uint32{uint32(os.Getuid())}, AllowSticky: true}
}

// writeMachine writes the machine file with mode.
func writeMachine(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, MachineConfigName)
	if err := os.WriteFile(p, []byte("defaults:\n  attestation:\n    policy: require\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// wantUnsafe fails unless err wraps ErrMachineFileUnsafe and mentions part.
func wantUnsafe(t *testing.T, err error, part string) {
	t.Helper()
	if !errors.Is(err, ErrMachineFileUnsafe) || !strings.Contains(err.Error(), part) {
		t.Fatalf("err = %v, want ErrMachineFileUnsafe naming %q", err, part)
	}
}

// Spec: §4.7.11 (Machine file, Check on Unix) — a file that root or a
// trusted owner owns and no other user can write is read, an absent file
// raises nothing, and a file that does not parse is an error of its own.
func TestReadMachineConfig_ReadsACheckedFile(t *testing.T) {
	t.Parallel()
	dir, opts := machineDir(t)
	if mf, err := ReadMachineConfig(opts); mf != nil || err != nil {
		t.Fatalf("absent file: %v, %v", mf, err)
	}
	p := writeMachine(t, dir, 0o644)
	mf, err := ReadMachineConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	a, err := mf.Config.Defaults.AttestationDefaults()
	if err != nil || a.Policy != "require" || mf.Path != p {
		t.Fatalf("read %+v, %v", mf, err)
	}
	if err := os.WriteFile(p, []byte("defaults: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMachineConfig(opts); err == nil || errors.Is(err, ErrMachineFileUnsafe) {
		t.Fatalf("a file that does not parse: %v", err)
	}
	if err := os.WriteFile(p, make([]byte, maxMachineConfigBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	wantUnsafe(t, func() error { _, err := ReadMachineConfig(opts); return err }(), "larger than")
}

// Spec: §4.7.11 (Machine file, Check on Unix) — the production check trusts
// root alone, so a file the test user owns fails it.
func TestReadMachineConfig_ProductionTrustsRootAlone(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root owns every file this test creates")
	}
	dir, _ := machineDir(t)
	writeMachine(t, dir, 0o644)
	// The production walk refuses at the first untrusted directory, which
	// on Linux is the world-writable /tmp above the test's directory and on
	// macOS the user's own temporary directory; either reason fails closed.
	_, err := ReadMachineConfig(MachineFileOptions{Dir: dir})
	if !errors.Is(err, ErrMachineFileUnsafe) || !strings.Contains(err.Error(), "not by root") && !strings.Contains(err.Error(), "can write the directory") {
		t.Fatalf("err = %v, want ErrMachineFileUnsafe for an owner or a writable directory", err)
	}
	if d, err := MachineConfigDir(); err != nil || d != "/etc/podium" {
		t.Errorf("MachineConfigDir = %q, %v", d, err)
	}
}

// Spec: §4.7.11 (Machine file, Check on Unix) — a file another user can
// write, a directory that is group-writable for a group other than gid 0,
// a directory any user can write without the sticky bit, a link to a file
// another user can write, and an entry that is no regular file each fail
// the check.
func TestReadMachineConfig_Refusals(t *testing.T) {
	t.Parallel()
	t.Run("file writable by others", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		writeMachine(t, dir, 0o666)
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "can write the file")
	})
	t.Run("group-writable directory", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		writeMachine(t, dir, 0o644)
		var st syscall.Stat_t
		if err := syscall.Stat(dir, &st); err != nil || st.Gid == 0 {
			t.Skip("the directory's group is gid 0, whose write bit the check accepts")
		}
		if err := os.Chmod(dir, 0o775); err != nil {
			t.Fatal(err)
		}
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "can write the directory")
	})
	t.Run("directory writable by others without the sticky bit", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		writeMachine(t, dir, 0o644)
		if err := os.Chmod(dir, 0o757); err != nil {
			t.Fatal(err)
		}
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "can write the directory")
		if err := os.Chmod(dir, 0o757|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadMachineConfig(opts); err != nil {
			t.Errorf("a sticky directory under the test seam: %v", err)
		}
		opts.AllowSticky = false
		_, err = ReadMachineConfig(opts)
		wantUnsafe(t, err, "can write the directory")
	})
	t.Run("link to a file others can write", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		other := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.Mkdir(other, 0o755); err != nil {
			t.Fatal(err)
		}
		target := writeMachine(t, other, 0o666)
		if err := os.Symlink(target, filepath.Join(dir, MachineConfigName)); err != nil {
			t.Fatal(err)
		}
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "can write the file")
		if err := os.Chmod(target, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadMachineConfig(opts); err != nil {
			t.Errorf("a link to a checked file: %v", err)
		}
	})
	t.Run("relative link loop", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		if err := os.Symlink(MachineConfigName, filepath.Join(dir, MachineConfigName)); err != nil {
			t.Fatal(err)
		}
		_, err := ReadMachineConfig(opts)
		if err == nil {
			t.Fatal("a link loop was read")
		}
	})
	t.Run("directory at the file", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		if err := os.Mkdir(filepath.Join(dir, MachineConfigName), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "regular file")
	})
	t.Run("file on the way", func(t *testing.T) {
		t.Parallel()
		dir, opts := machineDir(t)
		if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		opts.Dir = filepath.Join(dir, "f", "sub")
		_, err := ReadMachineConfig(opts)
		wantUnsafe(t, err, "is not a directory")
	})
}

// Spec: §4.7.11 (Machine file, Check on Unix) — the file is read from the
// descriptor whose owner and mode were checked, so a file replaced between
// the path check and the read is either the checked one or refused: a
// replacement another user can write fails the check on the descriptor.
func TestReadMachineConfig_ChecksTheOpenDescriptor(t *testing.T) {
	t.Parallel()
	dir, opts := machineDir(t)
	p := writeMachine(t, dir, 0o644)
	f, path, err := openMachineFile(dir, opts)
	if err != nil || f == nil || path != p {
		t.Fatalf("open: %v", err)
	}
	// Replace the file after the open: the descriptor still reads the
	// checked content.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("defaults:\n  attestation:\n    policy: none\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	_ = f.Close()
	if !strings.Contains(string(buf[:n]), "require") {
		t.Errorf("read %q from the checked descriptor", buf[:n])
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	_, err = ReadMachineConfig(opts)
	wantUnsafe(t, err, "can write the file")
}

// Spec: §4.7.11 (Machine file) — with no directory named, the reader uses
// /etc/podium, where an absent file raises nothing.
func TestReadMachineConfig_PlatformDirectory(t *testing.T) {
	t.Parallel()
	if _, err := os.Lstat("/etc/podium/sync.yaml"); err == nil {
		t.Skip("this host has a machine file")
	}
	if mf, err := ReadMachineConfig(MachineFileOptions{}); mf != nil || err != nil {
		t.Fatalf("ReadMachineConfig = %v, %v", mf, err)
	}
}

// Spec: §4.7.11 (Machine file, Check on Unix) — a file the process cannot
// open, and a file below a directory it cannot search, fail the check
// rather than read as absent.
func TestReadMachineConfig_UnreadableFails(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	dir, opts := machineDir(t)
	p := writeMachine(t, dir, 0o644)
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	_, err := ReadMachineConfig(opts)
	wantUnsafe(t, err, "open")

	dir2, opts2 := machineDir(t)
	sub := filepath.Join(dir2, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMachine(t, sub, 0o644)
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
	opts2.Dir = sub
	_, err = ReadMachineConfig(opts2)
	wantUnsafe(t, err, "permission denied")
}
