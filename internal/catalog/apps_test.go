package catalog

import (
	"testing"

	"morgtweaker/internal/action"
)

// TestAppsCategoryPresent proves the assembled catalog exposes the "apps"
// category.
func TestAppsCategoryPresent(t *testing.T) {
	var found bool
	for _, c := range Build() {
		if c.ID == "apps" {
			found = true
			if c.Name.RU == "" || c.Name.EN == "" {
				t.Errorf("apps category missing RU/EN name: %+v", c.Name)
			}
		}
	}
	if !found {
		t.Fatal("expected category with ID \"apps\" in the catalog")
	}
}

// TestAppsParent proves the single apps parent is an expandable group with the
// five children in declaration order, winget first.
func TestAppsParent(t *testing.T) {
	parent, ok := Build().Find("apps.programs")
	if !ok {
		t.Fatal("apps.programs missing")
	}
	if !parent.IsParent() {
		t.Fatal("apps.programs must be a parent (have children)")
	}
	if len(parent.Children) != 5 {
		t.Fatalf("apps.programs has %d children, want 5", len(parent.Children))
	}
	want := []string{
		"apps.winget",
		"apps.powershell",
		"apps.7zip",
		"apps.terminal",
		"apps.vlc",
	}
	for i, id := range want {
		if parent.Children[i].ID != id {
			t.Errorf("child[%d].ID = %q, want %q", i, parent.Children[i].ID, id)
		}
	}
	if parent.Children[0].ID != "apps.winget" {
		t.Errorf("winget must be the FIRST child so it installs before the app installs run; got %q", parent.Children[0].ID)
	}
}

// TestAppsWingetChild proves the first child bootstraps winget.
func TestAppsWingetChild(t *testing.T) {
	w, ok := Build().Find("apps.winget")
	if !ok {
		t.Fatal("apps.winget missing")
	}
	if len(w.Actions) == 0 {
		t.Fatal("apps.winget has no actions")
	}
	if _, ok := w.Actions[0].(action.WingetBootstrap); !ok {
		t.Errorf("apps.winget action[0] is %T, want action.WingetBootstrap", w.Actions[0])
	}
	if w.Gate == nil {
		t.Error("apps.winget must carry a BuildGate (Gate != nil)")
	}
}

// TestAppsInstallChildren proves each app child installs the right package via
// WingetInstall with reboot-aware accepted exit codes.
func TestAppsInstallChildren(t *testing.T) {
	cases := []struct {
		id  string
		pkg string
	}{
		{"apps.powershell", "Microsoft.PowerShell"},
		{"apps.7zip", "7zip.7zip"},
		{"apps.terminal", "Microsoft.WindowsTerminal"},
		{"apps.vlc", "VideoLAN.VLC"},
	}
	for _, tc := range cases {
		ch, ok := Build().Find(tc.id)
		if !ok {
			t.Errorf("%s missing", tc.id)
			continue
		}
		if len(ch.Actions) == 0 {
			t.Errorf("%s has no actions", tc.id)
			continue
		}
		wi, ok := ch.Actions[0].(action.WingetInstall)
		if !ok {
			t.Errorf("%s action[0] is %T, want action.WingetInstall", tc.id, ch.Actions[0])
			continue
		}
		if wi.ID != tc.pkg {
			t.Errorf("%s package ID = %q, want %q", tc.id, wi.ID, tc.pkg)
		}
		if !containsInt(wi.AcceptExit, 0) {
			t.Errorf("%s AcceptExit %v must contain 0 (success)", tc.id, wi.AcceptExit)
		}
		if !containsInt(wi.AcceptExit, -1978334967) {
			t.Errorf("%s AcceptExit %v must contain the reboot-required code -1978334967", tc.id, wi.AcceptExit)
		}
		if ch.Gate == nil {
			t.Errorf("%s must carry a BuildGate (Gate != nil)", tc.id)
		}
	}
}

// TestAppsEveryChildGated proves every child of the apps parent is build-gated.
func TestAppsEveryChildGated(t *testing.T) {
	parent, ok := Build().Find("apps.programs")
	if !ok {
		t.Fatal("apps.programs missing")
	}
	for _, ch := range parent.Children {
		if ch.Gate == nil {
			t.Errorf("child %q must carry a Gate (Gate != nil)", ch.ID)
		}
		if _, ok := ch.Gate.(action.BuildGate); !ok {
			t.Errorf("child %q gate is %T, want action.BuildGate", ch.ID, ch.Gate)
		}
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
