//go:build windows

package serve

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

const fileDeleteChild = 0x40

// grantWindowsServiceAccess gives the virtual service account access to the
// selected installation and state, without making the service LocalSystem.
func grantWindowsServiceAccess(exe, home, config string) error {
	sid, err := serviceAccountSID()
	if err != nil {
		return err
	}
	if err := grantWindowsServiceExecutableAccess(sid, exe); err != nil {
		return err
	}
	if err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		return grantPathAccess(path, sid, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE|windows.DELETE|fileDeleteChild, entry.IsDir())
	}); err != nil {
		return fmt.Errorf("grant access to service home %s: %w", home, err)
	}
	if config != "" {
		if err := grantPathAccess(config, sid, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE, false); err != nil {
			return err
		}
		// Atomic config writes create a temporary file beside the destination
		// and rename it over the old file. Grant those operations on the parent
		// without FILE_DELETE_CHILD, which would let the service delete siblings.
		if err := grantPathAccess(filepath.Dir(config), sid, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE, false); err != nil {
			return fmt.Errorf("grant config directory access: %w", err)
		}
	}
	return nil
}

// GrantWindowsServiceExecutableAccess reapplies the executable ACE after an
// updater replaces the file. The parent grant is deliberately non-inheritable
// so unrelated sibling files do not become readable by the service account.
func GrantWindowsServiceExecutableAccess(exe string) error {
	sid, err := serviceAccountSID()
	if err != nil {
		return err
	}
	return grantWindowsServiceExecutableAccess(sid, exe)
}

func serviceAccountSID() (*windows.SID, error) {
	sid, _, _, err := windows.LookupSID("", WindowsServiceAccount)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", WindowsServiceAccount, err)
	}
	return sid, nil
}

func grantWindowsServiceExecutableAccess(sid *windows.SID, exe string) error {
	if err := grantPathAccess(exe, sid, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE, false); err != nil {
		return err
	}
	return grantPathAccess(filepath.Dir(exe), sid, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE, false)
}

func grantPathAccess(path string, sid *windows.SID, permissions windows.ACCESS_MASK, inherit bool) error {
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read permissions of %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL of %s: %w", path, err)
	}
	if dacl == nil {
		return fmt.Errorf("%s has no explicit DACL", path)
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if inherit {
		inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	merged, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return fmt.Errorf("merge DACL of %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, merged, nil); err != nil {
		return fmt.Errorf("write DACL of %s: %w", path, err)
	}
	return nil
}
