package action

import (
	"fmt"

	"golang.org/x/sys/windows"

	"golang.org/x/sys/windows/registry"
)

// This file holds a small, reusable take-ownership-on-a-registry-key helper used
// by DefenderServiceDisable when a plain Start-DWORD write is rejected with
// ERROR_ACCESS_DENIED even under TrustedInstaller (WdFilter / WdBoot are extra
// hardened: their service key DACL denies even TI write access). The fallback is
// the classic Win32 take-ownership dance, done through golang.org/x/sys/windows
// security APIs (verified against x/sys v0.46.0 security_windows.go):
//
//  1. GetNamedSecurityInfo(name, SE_REGISTRY_KEY, OWNER|DACL)  -> snapshot SDDL
//  2. SetNamedSecurityInfo(name, OWNER, Administrators SID)    -> seize ownership
//  3. SetNamedSecurityInfo(name, DACL, <Administrators:FullControl>) -> grant write
//
// regSecObjectName maps a Services\<Svc> subkey to the named-object form the
// SetNamedSecurityInfo/GetNamedSecurityInfo APIs expect for HKLM: the "MACHINE\"
// prefix (NOT "HKEY_LOCAL_MACHINE\") per the Win32 SE_REGISTRY_KEY convention.
func regSecObjectName(svcKeyPath string) string {
	return `MACHINE\` + svcKeyPath
}

// regOwnership reads and writes a registry key's owner+DACL. It is the seam the
// action injects so tests never touch the live ACL subsystem. The real
// implementation (realRegOwnership) calls the Win32 security APIs; tests inject a
// fake. All methods take the canonical Services\<Svc> subkey path (NOT the
// MACHINE\-prefixed object name — the implementation prefixes internally).
type regOwnership interface {
	// snapshotSecurity returns the current owner+DACL of the key as an SDDL string,
	// suitable for exact restore. err is non-nil only on a genuine read failure.
	snapshotSecurity(svcKeyPath string) (sddl string, err error)
	// takeOwnership seizes ownership for the local Administrators group and grants
	// it FullControl, so a subsequent Start-DWORD write succeeds.
	takeOwnership(svcKeyPath string) error
	// restoreSecurity writes the given SDDL (owner+DACL) back onto the key, exactly
	// reverting a prior takeOwnership. An empty sddl is a no-op (we never changed it).
	restoreSecurity(svcKeyPath, sddl string) error
}

// realRegOwnership is the production regOwnership backed by the Win32 security APIs.
type realRegOwnership struct{}

// secInfoOwnerDACL is the SECURITY_INFORMATION mask we snapshot and restore: owner
// + DACL (the only two facets take-ownership mutates).
const secInfoOwnerDACL = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

func (realRegOwnership) snapshotSecurity(svcKeyPath string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(regSecObjectName(svcKeyPath),
		windows.SE_REGISTRY_KEY, secInfoOwnerDACL)
	if err != nil {
		return "", fmt.Errorf("regown: GetNamedSecurityInfo(%s): %w", svcKeyPath, err)
	}
	// String() emits SDDL for the facets present; SecurityDescriptorFromString
	// round-trips it back on restore.
	return sd.String(), nil
}

func (realRegOwnership) takeOwnership(svcKeyPath string) error {
	name := regSecObjectName(svcKeyPath)

	// 1. Seize ownership for the local Administrators group.
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("regown: CreateWellKnownSid(Administrators): %w", err)
	}
	if err := windows.SetNamedSecurityInfo(name, windows.SE_REGISTRY_KEY,
		windows.OWNER_SECURITY_INFORMATION, admins, nil, nil, nil); err != nil {
		return fmt.Errorf("regown: SetNamedSecurityInfo(owner) %s: %w", svcKeyPath, err)
	}

	// 2. Grant Administrators FullControl so the Start write goes through. Build a
	// one-ACE DACL via ACLFromEntries (KEY_ALL_ACCESS = 0xF003F) and apply it.
	ea := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.KEY_ALL_ACCESS,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(admins),
		},
	}}
	acl, err := windows.ACLFromEntries(ea, nil)
	if err != nil {
		return fmt.Errorf("regown: ACLFromEntries: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(name, windows.SE_REGISTRY_KEY,
		windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("regown: SetNamedSecurityInfo(dacl) %s: %w", svcKeyPath, err)
	}
	return nil
}

func (realRegOwnership) restoreSecurity(svcKeyPath, sddl string) error {
	if sddl == "" {
		return nil // we never changed this key's security
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("regown: SecurityDescriptorFromString: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("regown: read snapshot owner: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("regown: read snapshot DACL: %w", err)
	}
	// Restore owner+DACL atomically in a SINGLE SetNamedSecurityInfo call (both
	// facets in one shot) so the key returns to EXACTLY the snapshot owner+DACL.
	if err := windows.SetNamedSecurityInfo(regSecObjectName(svcKeyPath),
		windows.SE_REGISTRY_KEY, secInfoOwnerDACL, owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("regown: SetNamedSecurityInfo(restore) %s: %w", svcKeyPath, err)
	}
	return nil
}

// regWriter is the registry seam for the Start DWORD: a tiny interface so tests
// inject an in-memory store instead of touching HKLM. realRegWriter delegates to
// the package's existing writeRaw/readRaw helpers under HKLM.
type regWriter interface {
	readStart(svcKeyPath string) (present bool, start uint64, err error)
	writeStart(svcKeyPath string, start uint64) error
}

// realRegWriter writes the Start DWORD under HKLM via the shared raw helpers.
type realRegWriter struct{}

func (realRegWriter) readStart(svcKeyPath string) (bool, uint64, error) {
	existed, _, v, err := readRaw(registry.LOCAL_MACHINE, svcKeyPath, "Start", KindDword)
	if err != nil {
		return false, 0, err
	}
	if !existed {
		return false, 0, nil
	}
	n, _ := toU64(v)
	return true, n, nil
}

func (realRegWriter) writeStart(svcKeyPath string, start uint64) error {
	return writeRaw(registry.LOCAL_MACHINE, svcKeyPath, "Start", KindDword, start)
}
