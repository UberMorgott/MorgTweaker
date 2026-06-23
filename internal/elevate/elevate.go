// Package elevate handles privilege concerns: detecting admin, enabling
// SeDebugPrivilege, and impersonating SYSTEM / TrustedInstaller for the few
// tweaks that need a higher token than an elevated admin process has.
//
// Impersonation is thread-local: a duplicated token is attached to the CURRENT
// OS thread, so every impersonated call MUST run on a locked thread and revert
// before unlocking. RunAs encapsulates that contract.
package elevate

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Level selects which identity RunAs impersonates.
type Level int

const (
	User             Level = iota // run as the current (elevated) user — no impersonation
	System                        // impersonate winlogon.exe (NT AUTHORITY\SYSTEM)
	TrustedInstaller              // impersonate the TrustedInstaller service token
)

// IsAdmin reports whether the current process token is elevated.
func IsAdmin() bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return tok.IsElevated()
}

// EnableSeDebugPrivilege turns on SeDebugPrivilege for the current process so we
// can open privileged processes (winlogon, TrustedInstaller) to steal a token.
func EnableSeDebugPrivilege() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()

	var luid windows.LUID
	// SE_DEBUG_NAME is not exported by x/sys — use the literal privilege name.
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	if err := windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil); err != nil {
		return err
	}
	// AdjustTokenPrivileges returns success even when it assigned NOTHING: it sets
	// LastError to ERROR_NOT_ALL_ASSIGNED, and the x/sys wrapper only fails on a
	// zero return (golang/go#64170). So a non-elevated or policy-blocked run would
	// otherwise falsely report the privilege enabled and then fail confusingly at
	// OpenProcess(winlogon). Check GetLastError explicitly.
	if err := windows.GetLastError(); err == windows.ERROR_NOT_ALL_ASSIGNED {
		return fmt.Errorf("elevate: SeDebugPrivilege not assigned (not elevated or blocked by policy)")
	}
	return nil
}

// RunAs runs fn under the requested identity. For User it just calls fn. For
// System/TrustedInstaller it locks the OS thread, attaches a duplicated token,
// runs fn, then reverts and unlocks — always, even on error.
func RunAs(level Level, fn func() error) error {
	if level == User {
		return fn()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var (
		dup windows.Token
		err error
	)
	switch level {
	case System:
		dup, err = systemToken()
	case TrustedInstaller:
		dup, err = trustedInstallerToken()
	default:
		return fmt.Errorf("elevate: unknown level %d", level)
	}
	if err != nil {
		return err
	}
	defer dup.Close()

	if err := windows.SetThreadToken(nil, dup); err != nil {
		return fmt.Errorf("elevate: SetThreadToken: %w", err)
	}
	defer windows.RevertToSelf()

	return fn()
}

// systemToken returns a duplicated impersonation token for NT AUTHORITY\SYSTEM,
// stolen from winlogon.exe.
func systemToken() (windows.Token, error) {
	pid, err := findPID("winlogon.exe")
	if err != nil {
		return 0, err
	}
	return dupTokenFromPID(pid)
}

// trustedInstallerToken starts the TrustedInstaller service if needed, waits for
// it to run, then duplicates its process token.
func trustedInstallerToken() (windows.Token, error) {
	m, err := mgr.Connect()
	if err != nil {
		return 0, err
	}
	defer m.Disconnect()

	s, err := m.OpenService("TrustedInstaller")
	if err != nil {
		return 0, err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return 0, err
	}
	if st.State != svc.Running {
		if err := s.Start(); err != nil {
			return 0, err
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			st, err = s.Query()
			if err != nil {
				return 0, err
			}
			if st.State == svc.Running {
				break
			}
			if time.Now().After(deadline) {
				return 0, fmt.Errorf("elevate: TrustedInstaller did not reach running state")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return dupTokenFromPID(st.ProcessId)
}

// dupTokenFromPID opens the process, opens its token, and duplicates it into an
// impersonation token usable with SetThreadToken.
func dupTokenFromPID(pid uint32) (windows.Token, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("elevate: OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var procTok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &procTok); err != nil {
		return 0, fmt.Errorf("elevate: OpenProcessToken: %w", err)
	}
	defer procTok.Close()

	var dup windows.Token
	if err := windows.DuplicateTokenEx(procTok, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenImpersonation, &dup); err != nil {
		return 0, fmt.Errorf("elevate: DuplicateTokenEx: %w", err)
	}
	return dup, nil
}

// findPID returns the PID of the first process matching name (case-insensitive)
// via the toolhelp snapshot.
func findPID(name string) (uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0, err
	}
	for {
		if eqName(pe.ExeFile[:], name) {
			return pe.ProcessID, nil
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			return 0, fmt.Errorf("elevate: process %q not found", name)
		}
	}
}

// eqName compares a UTF-16 exe-file field against an ASCII process name,
// case-insensitively.
func eqName(field []uint16, name string) bool {
	got := windows.UTF16ToString(field)
	if len(got) != len(name) {
		return false
	}
	for i := 0; i < len(name); i++ {
		a, b := got[i], name[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
