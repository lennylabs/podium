//go:build windows

package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// MachineConfigDir returns the directory of the machine file on Windows:
// podium under the ProgramData known folder, which SHGetKnownFolderPath
// resolves. The PROGRAMDATA variable is never read, because a project
// configuration can set the environment of the MCP server.
//
// Spec: §4.7.11 (Machine file)
func MachineConfigDir() (string, error) {
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, "podium"), nil
}

// trustedInstallerSID is the service SID of TrustedInstaller.
const trustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

// The rights a DACL may grant only to a trusted SID. A folder also grants
// no right to delete its children, which deletes or replaces the file.
const (
	fileDeleteChild   = 0x0040
	fileAllAccess     = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF
	fileWriteRights   = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER
	folderWriteRights = fileWriteRights | fileDeleteChild
)

// The ACE types that grant access. An allow ACE of another type, such as
// an object ACE, is refused rather than read, because its SID lies at
// another offset.
const (
	accessAllowedACE         = 0x0
	accessAllowedCallbackACE = 0x9
	accessAllowedObjectACE   = 0x5
	accessAllowedCbObjectACE = 0xB
)

// openMachineFile checks the podium folder and dir\sync.yaml by their
// security descriptors: the owner is SYSTEM or Administrators, the DACL
// grants no write, append, delete, WRITE_DAC, or WRITE_OWNER right to a SID
// other than SYSTEM, Administrators, and TrustedInstaller, the folder's
// DACL also grants no FILE_DELETE_CHILD and is protected, and a reparse
// point at the folder or the file meets the same conditions as its target.
// It then opens the file, reads its descriptor from the handle, and
// returns that handle for the read. A nil file and no error mean the file
// is absent.
//
// Spec: §4.7.11 (Machine file, Check on Windows)
func openMachineFile(dir string, _ MachineFileOptions) (*os.File, string, error) {
	path := filepath.Join(dir, MachineConfigName)
	if _, err := os.Lstat(path); absentFile(err) {
		return nil, path, nil
	}
	if err := checkObject(dir, true); err != nil {
		return nil, path, err
	}
	if err := checkObject(path, false); err != nil {
		return nil, path, err
	}
	h, err := openHandle(path, 0)
	if err != nil {
		return nil, path, unsafeFile(path, "open: %v", err)
	}
	if err := checkHandle(h, false); err != nil {
		_ = windows.CloseHandle(h)
		return nil, path, unsafeFile(path, "%v", err)
	}
	return os.NewFile(uintptr(h), path), path, nil
}

// openHandle opens path for reading its content and security descriptor.
func openHandle(path string, flags uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|flags, 0)
}

// checkObject checks one path. A reparse point is checked itself and then
// as the target it resolves to.
func checkObject(path string, folder bool) error {
	h, err := openHandle(path, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if err != nil {
		return unsafeFile(path, "open: %v", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := checkHandle(h, folder); err != nil {
		return unsafeFile(path, "%v", err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return unsafeFile(path, "%v", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return nil
	}
	target, err := openHandle(path, 0)
	if err != nil {
		return unsafeFile(path, "open the target: %v", err)
	}
	defer func() { _ = windows.CloseHandle(target) }()
	if err := checkHandle(target, folder); err != nil {
		return unsafeFile(path, "target: %v", err)
	}
	return nil
}

// checkHandle reads the owner and the DACL of an open handle and applies
// the conditions of the file or of the folder.
func checkHandle(h windows.Handle, folder bool) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the security descriptor: %v", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("has no owner")
	}
	if !owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return fmt.Errorf("is owned by %s, not by SYSTEM or Administrators", owner)
	}
	if folder {
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return fmt.Errorf("the folder's DACL inherits from its parent; protect it")
		}
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("has no DACL, which grants every right to everyone")
	}
	bad := uint32(fileWriteRights)
	if folder {
		bad = folderWriteRights
	}
	return checkACEs(dacl, bad)
}

// checkACEs refuses an allow ACE that grants one of the bad rights, after
// generic rights are mapped, to an untrusted SID. An inherit-only ACE, which
// grants nothing on the object itself, is refused as well: the check reads
// every allow ACE as a grant, which fails closed on a DACL that an installer
// wrote with such an entry (§4.7.11 Check on Windows).
func checkACEs(dacl *windows.ACL, bad uint32) error {
	trustedInstaller, err := windows.StringToSid(trustedInstallerSID)
	if err != nil {
		return err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("read ACE %d: %v", i, err)
		}
		switch ace.Header.AceType {
		case accessAllowedACE, accessAllowedCallbackACE:
		case accessAllowedObjectACE, accessAllowedCbObjectACE:
			return fmt.Errorf("ACE %d is an object ACE that grants access", i)
		default:
			continue
		}
		if mapGeneric(uint32(ace.Mask))&bad == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.Equals(trustedInstaller) {
			continue
		}
		return fmt.Errorf("the DACL grants a write right to %s", sid)
	}
	return nil
}

// mapGeneric maps the generic rights of an ACE mask to the file rights
// they stand for.
func mapGeneric(m uint32) uint32 {
	if m&windows.GENERIC_ALL != 0 {
		m |= fileAllAccess
	}
	if m&windows.GENERIC_WRITE != 0 {
		m |= windows.FILE_GENERIC_WRITE
	}
	if m&windows.GENERIC_READ != 0 {
		m |= windows.FILE_GENERIC_READ
	}
	if m&windows.GENERIC_EXECUTE != 0 {
		m |= windows.FILE_GENERIC_EXECUTE
	}
	return m
}
