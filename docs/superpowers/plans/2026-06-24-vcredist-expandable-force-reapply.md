# VC++ Redistributables: expandable group + force-reapply — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let already-applied tweaks be force-reapplied via [Apply], and surface VC++ 2005-2022 as one expandable parent row with per-version children installable individually.

**Architecture:** `core.Tweak` gains `Children []Tweak`; a parent carries children, no own Actions. The ENGINE IS UNCHANGED — it only ever probes/applies LEAF tweaks (children or childless tweaks). The parent is a pure UI concept: its status is aggregated in the UI from its children's cached statuses, and applying a checked parent expands into its children's IDs through the existing sequential batch queue. The right pane renders a flattened "visible rows" list (parents, optionally their indented children) that drives rendering, cursor navigation, mouse hit-testing, and selection from one source.

**Tech Stack:** Go, Bubble Tea v2 (`charm.land/bubbletea/v2`), lipgloss v2, `golang.org/x/sys/windows/registry`. Tests are standard `go test`.

## Global Constraints

- Module path: `morgtweaker`. Go on Windows; tests run on Windows.
- Verify is FAIL-CLOSED: an installer never runs unless its `Verify` passes (`internal/action/downloadinstall.go`). Do not weaken this.
- No literals from memory: every download URL, SHA256 pin, registry detect path, MSI product GUID, and silent-install arg MUST be grounded (downloaded/inspected on a real machine) in Task 7 and recorded in `docs/superpowers/refs/vcredist-grounding.md` BEFORE being pasted into the catalog. Placeholder pins must fail isSHA256Hex (they do) so a forgotten value can never install.
- Run the full suite after each task: `go test ./...` from `D:\MorgDEV\wintweaker`. Expected: `ok` for every package.
- Commit after each task with the message shown in its final step.
- Comments/commits in English; keep the existing file's comment density and style.

---

## File map

- `internal/ui/render.go` — `statusAppliable` widens (Task 1); right pane renders flattened visible rows (Task 6).
- `internal/core/types.go` — `Tweak.Children`; `Catalog.Find` recurses; new `Catalog.Leaves()` / `Tweak.IsParent()` (Task 2).
- `internal/action/regpresent.go` (new) — `RegPresent` detect-only action (Task 3).
- `internal/catalog/redist.go` (new) — redist parent + 12 children, detects, verify modes (Task 4 + Task 7 values).
- `internal/catalog/prep.go` — remove `prep.install_vcredist`; add the redist category/parent (Task 4).
- `internal/catalog/catalog.go` (wherever categories are assembled) — register the redist parent under `prep` (Task 4).
- `internal/ui/ui.go` — model gains `expanded map[string]bool`; `allTweaks`→leaves for probe; `visibleRows`, `rowStatus` helpers (Task 5/6).
- `internal/ui/update.go` — navigation/hit-test/selection use `visibleRows`; Enter expands a parent; selection expands a parent into children at apply time (Task 5/6).

---

### Task 1: Force-reapply — widen the Apply filter

Smallest, fully independent. Already-applied (`StatusOn`) checked rows become appliable; `[Apply]` re-runs `Apply(on=true)` on them (RegSet rewrites same value; DownloadInstall reinstalls).

**Files:**
- Modify: `internal/ui/render.go:106-108` (`statusAppliable`)
- Test: `internal/ui/render_test.go` (create if absent) and `internal/ui/buttonbar_test.go`

**Interfaces:**
- Produces: `statusAppliable(core.Status) bool` now true for `StatusOff`, `StatusPartial`, `StatusOn`.
- `statusRollbackable`, `statusHasAction` UNCHANGED.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/render_test.go`:

```go
package ui

import (
	"testing"

	"morgtweaker/internal/core"
)

func TestStatusAppliableIncludesOn(t *testing.T) {
	cases := map[core.Status]bool{
		core.StatusOff:           true,
		core.StatusPartial:       true,
		core.StatusOn:            true, // force-reapply: applied rows are re-appliable
		core.StatusBlocked:       false,
		core.StatusAbsent:        false,
		core.StatusRebootPending: false,
		core.StatusUnknown:       false,
		core.StatusWorking:       false,
	}
	for st, want := range cases {
		if got := statusAppliable(st); got != want {
			t.Errorf("statusAppliable(%v) = %v, want %v", st, got, want)
		}
	}
}

func TestStatusRollbackableUnchanged(t *testing.T) {
	if !statusRollbackable(core.StatusOn) || !statusRollbackable(core.StatusRebootPending) {
		t.Fatal("rollbackable must still include On and RebootPending")
	}
	if statusRollbackable(core.StatusOff) || statusRollbackable(core.StatusPartial) {
		t.Fatal("rollbackable must not include Off/Partial")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestStatusAppliableIncludesOn -v`
Expected: FAIL — `statusAppliable(on) = false, want true`.

- [ ] **Step 3: Implement the change**

In `internal/ui/render.go` replace `statusAppliable`:

```go
// statusAppliable reports whether a tweak in this status can be APPLIED now.
// StatusOn is included so an already-applied (grey) row that the user explicitly
// checks is RE-applied on [Apply] (RegSet rewrites the same value; an install
// re-downloads + reinstalls). Row COLOUR is unaffected — only this filter widens.
func statusAppliable(st core.Status) bool {
	return st == core.StatusOff || st == core.StatusPartial || st == core.StatusOn
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: PASS (all ui tests, including existing buttonbar/async).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/render_test.go
git commit -m "feat(ui): force-reapply checked applied tweaks via [Apply]"
```

---

### Task 2: `core.Tweak.Children` + recursive `Catalog.Find` + `Leaves`

Adds the parent/child data shape with zero behaviour change for existing leaf tweaks. A parent has `Children` set and (normally) empty `Actions`.

**Files:**
- Modify: `internal/core/types.go` (Tweak struct, Find, add helpers)
- Test: `internal/core/types_test.go`

**Interfaces:**
- Produces:
  - `Tweak.Children []Tweak` (zero value nil = leaf).
  - `func (t Tweak) IsParent() bool` → `len(t.Children) > 0`.
  - `func (c Catalog) Find(id string) (Tweak, bool)` — now also searches children (depth 1).
  - `func (c Catalog) Leaves() []Tweak` — every childless tweak across all categories, with parents replaced by their children (used for the startup probe; parents are never probed by the engine).

- [ ] **Step 1: Write the failing test**

Append to `internal/core/types_test.go`:

```go
func TestFindLocatesChild(t *testing.T) {
	cat := Catalog{{ID: "prep", Tweaks: []Tweak{
		{ID: "prep.group", Children: []Tweak{
			{ID: "prep.group.a"},
			{ID: "prep.group.b"},
		}},
		{ID: "prep.leaf"},
	}}}
	if _, ok := cat.Find("prep.group.b"); !ok {
		t.Fatal("Find must locate a child tweak by ID")
	}
	if _, ok := cat.Find("prep.group"); !ok {
		t.Fatal("Find must still locate the parent")
	}
	if _, ok := cat.Find("prep.leaf"); !ok {
		t.Fatal("Find must still locate a normal leaf")
	}
	if _, ok := cat.Find("nope"); ok {
		t.Fatal("Find must return ok=false for a missing id")
	}
}

func TestLeavesExpandsParents(t *testing.T) {
	cat := Catalog{{ID: "prep", Tweaks: []Tweak{
		{ID: "prep.group", Children: []Tweak{{ID: "prep.group.a"}, {ID: "prep.group.b"}}},
		{ID: "prep.leaf"},
	}}}
	var ids []string
	for _, l := range cat.Leaves() {
		ids = append(ids, l.ID)
	}
	want := []string{"prep.group.a", "prep.group.b", "prep.leaf"}
	if len(ids) != len(want) {
		t.Fatalf("Leaves ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("Leaves ids = %v, want %v", ids, want)
		}
	}
}

func TestIsParent(t *testing.T) {
	if (Tweak{}).IsParent() {
		t.Fatal("a tweak with no children is not a parent")
	}
	if !(Tweak{Children: []Tweak{{ID: "x"}}}).IsParent() {
		t.Fatal("a tweak with children is a parent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'TestFindLocatesChild|TestLeaves|TestIsParent' -v`
Expected: FAIL — `Children`/`IsParent`/`Leaves` undefined.

- [ ] **Step 3: Implement**

In `internal/core/types.go`, add the field to `Tweak` (after `Actions   []Action`):

```go
	Actions   []Action
	// Children, when non-empty, makes this Tweak an expandable PARENT: it has no
	// own Actions; its children are individually-applicable leaf tweaks and its
	// status is aggregated (in the UI) from theirs. Zero value (nil) = leaf.
	Children  []Tweak
	Gate      Gate
```

Add helpers (anywhere in the file, e.g. after `NeedsAdmin`):

```go
// IsParent reports whether this tweak is an expandable group (has children).
func (t Tweak) IsParent() bool { return len(t.Children) > 0 }
```

Replace `Catalog.Find` to recurse one level into children:

```go
// Find returns the tweak with the given id, ok=false if absent. It searches both
// top-level tweaks AND their children (so a child tweak applied via the batch
// queue resolves by ID).
func (c Catalog) Find(id string) (Tweak, bool) {
	for _, cat := range c {
		for _, t := range cat.Tweaks {
			if t.ID == id {
				return t, true
			}
			for _, ch := range t.Children {
				if ch.ID == id {
					return ch, true
				}
			}
		}
	}
	return Tweak{}, false
}
```

Add `Leaves`:

```go
// Leaves returns every applicable LEAF tweak across all categories in order: a
// parent is replaced by its children; a childless tweak yields itself. The engine
// only ever probes/applies leaves — a parent has no Actions, so probing it would
// read StatusAbsent. The UI aggregates a parent's status from its children.
func (c Catalog) Leaves() []Tweak {
	var out []Tweak
	for _, cat := range c {
		for _, t := range cat.Tweaks {
			if t.IsParent() {
				out = append(out, t.Children...)
			} else {
				out = append(out, t)
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/core/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/types.go internal/core/types_test.go
git commit -m "feat(core): Tweak.Children + recursive Find + Leaves for parent/child"
```

---

### Task 3: `RegPresent` detect-only action (for 2005/2008)

VC++ 2005/2008 have no `Installed` dword. They are detected by the existence of their MSI `Uninstall\{ProductGUID}` key. `RegPresent` probes whether a registry value exists at a path (ignoring its content); Apply/Snapshot/Restore are honest no-ops (it is used ONLY as a `DownloadInstall.Detect`).

**Files:**
- Create: `internal/action/regpresent.go`
- Test: `internal/action/regpresent_test.go`
- Reference: `internal/action/regset.go` (mirror style), `internal/action/regcommon.go` (`readRawView`, `RegView`, `ViewWow6432`, `ValueKind`)

**Interfaces:**
- Consumes: `readRawView(root registry.Key, path, value string, kind ValueKind, view RegView) (existed bool, typ uint32, v any, err error)` (existing in regcommon.go — confirm signature before use).
- Produces:
  ```go
  type RegPresent struct {
      Root  registry.Key
      Path  string
      Value string       // value whose existence signals "installed" (e.g. "DisplayName")
      Elev  core.Elevation
      View  RegView
  }
  ```
  Implements `core.Action`: `Probe` → `PointOn` if the value exists, else `PointOff`; `Apply`/`Snapshot`/`Restore` no-ops.

- [ ] **Step 1: Write the failing test**

Create `internal/action/regpresent_test.go`. Use a real HKCU scratch key (the test creates and cleans it) so the probe exercises the real registry path:

```go
package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

func TestRegPresentProbe(t *testing.T) {
	const sub = `Software\morgtweaker_test\regpresent`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, sub, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("create scratch key: %v", err)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, sub)
	defer k.Close()

	rp := RegPresent{Root: registry.CURRENT_USER, Path: sub, Value: "Marker", Elev: core.ElevUser}

	// Absent value → PointOff.
	if ps, err := rp.Probe(core.ActionContext{}); err != nil || ps != core.PointOff {
		t.Fatalf("absent probe = (%v,%v), want (PointOff,nil)", ps, err)
	}
	// Present value → PointOn.
	if err := k.SetStringValue("Marker", "anything"); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	if ps, err := rp.Probe(core.ActionContext{}); err != nil || ps != core.PointOn {
		t.Fatalf("present probe = (%v,%v), want (PointOn,nil)", ps, err)
	}
}

func TestRegPresentApplyIsNoop(t *testing.T) {
	rp := RegPresent{Root: registry.CURRENT_USER, Path: `Software\nope`, Value: "x"}
	if err := rp.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply must be a no-op, got %v", err)
	}
	if _, err := rp.Snapshot(core.ActionContext{}); err != nil {
		t.Fatalf("Snapshot must be a no-op, got %v", err)
	}
	if err := rp.Restore(core.ActionContext{}, core.Backup{}); err != nil {
		t.Fatalf("Restore must be a no-op, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/action/ -run TestRegPresent -v`
Expected: FAIL — `RegPresent` undefined.

- [ ] **Step 3: Implement**

Create `internal/action/regpresent.go`. NOTE: confirm `readRawView`'s exact signature in `regcommon.go` first; the `kind` argument below assumes `readRawView(root, path, value, kind, view)`. For presence we read as `KindString` but ignore the returned value — only `existed` matters. If `readRawView` requires a matching kind to read, pass `KindAny`/the simplest kind that returns `existed` regardless; adjust to the real helper.

```go
package action

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// RegPresent is a DETECT-ONLY action: its Probe reports PointOn when a given
// registry value EXISTS (content ignored), else PointOff. It is used as a
// DownloadInstall.Detect for components that have no clean "Installed" flag — e.g.
// VC++ 2005/2008, detected by the existence of their MSI Uninstall\{GUID} key's
// DisplayName value. Apply/Snapshot/Restore are honest no-ops: presence is not
// something this action writes, only reads.
type RegPresent struct {
	Root  registry.Key
	Path  string
	Value string
	Elev  core.Elevation
	View  RegView
}

func (a RegPresent) Level() core.Elevation { return a.Elev }

func (a RegPresent) Apply(core.ActionContext, bool) error          { return nil }
func (a RegPresent) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}
func (a RegPresent) Restore(core.ActionContext, core.Backup) error { return nil }

func (a RegPresent) Probe(core.ActionContext) (core.PointState, error) {
	existed, _, _, err := readRawView(a.Root, a.Path, a.Value, KindString, a.View)
	if err != nil {
		return core.PointOff, err
	}
	if existed {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}

var _ core.Action = RegPresent{}
```

If `readRawView` returns an error for a missing KEY (vs a missing VALUE), wrap so a missing key reads as `existed=false, err=nil`. Verify against `regcommon.go` and `regset.go`'s `Probe` (which treats a missing value as PointOff without error) and mirror that behaviour exactly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/action/ -run TestRegPresent -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/action/regpresent.go internal/action/regpresent_test.go
git commit -m "feat(action): RegPresent detect-only action for keyed presence"
```

---

### Task 4: Redist catalog — parent + 12 children (structure; values from Task 7)

Build the catalog shape. URLs/hashes/GUIDs/args are GROUNDED in Task 7 — until then use the existing 2015-2022 evergreen values (already known) for the 2022 children, and clearly-marked placeholder constants for legacy that FAIL CLOSED (a non-hex SHA256 placeholder makes `DownloadInstall.Apply` refuse before the network — see `downloadinstall.go:88`). Task 7 replaces placeholders with grounded values.

**Files:**
- Create: `internal/catalog/redist.go`
- Modify: `internal/catalog/prep.go` (remove `prep.install_vcredist`; it becomes the two 2022 children under the redist parent)
- Modify: wherever `prep(tc)` is consumed to assemble the catalog — append the redist parent into the `prep` category's tweak list (confirm the assembly site; likely `internal/catalog/catalog.go`).
- Test: `internal/catalog/redist_test.go`, update `internal/catalog/prep_test.go`

**Interfaces:**
- Consumes: `action.DownloadInstall`, `action.RegSet` (as detect), `action.RegPresent` (Task 3), `action.VerifyAuthenticodeMicrosoft`, `action.VerifySHA256`, `vcredistAcceptExit`, `vcRuntimeDetect` (existing in prep.go — MOVE to redist.go).
- Produces:
  - `func redistParent() core.Tweak` — `ID: "prep.vcredist"`, `IsParent()==true`, 12 children, no Actions.
  - Child IDs: `prep.vcredist.vc{2005,2008,2010,2012,2013,2022}_{x64,x86}`.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/redist_test.go`:

```go
package catalog

import (
	"strings"
	"testing"

	"morgtweaker/internal/core"
)

func TestRedistParentShape(t *testing.T) {
	p := redistParent()
	if !p.IsParent() {
		t.Fatal("redist parent must have children")
	}
	if len(p.Actions) != 0 {
		t.Fatal("redist parent must have NO own actions")
	}
	if len(p.Children) != 12 {
		t.Fatalf("want 12 children (6 versions x 2 arch), got %d", len(p.Children))
	}
	for _, ch := range p.Children {
		if !strings.HasPrefix(ch.ID, "prep.vcredist.vc") {
			t.Errorf("child id %q has wrong prefix", ch.ID)
		}
		if len(ch.Actions) != 1 {
			t.Errorf("child %q must have exactly one DownloadInstall action", ch.ID)
		}
		if ch.Category != "prep" {
			t.Errorf("child %q category = %q, want prep", ch.ID, ch.Category)
		}
	}
}

func TestRedistParentRegisteredUnderPrep(t *testing.T) {
	// Build the full catalog the app uses and assert the parent is present.
	cat := Build() // confirm the real builder name/signature
	if _, ok := cat.Find("prep.vcredist"); !ok {
		t.Fatal("prep.vcredist parent not registered in catalog")
	}
	if _, ok := cat.Find("prep.vcredist.vc2022_x64"); !ok {
		t.Fatal("2022 x64 child not findable")
	}
	if _, ok := cat.Find("prep.install_vcredist"); ok {
		t.Fatal("old combined prep.install_vcredist must be removed")
	}
}
```

(Confirm the catalog builder's real name — grep `func.*core.Catalog` in `internal/catalog`. Replace `Build()` accordingly.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/catalog/ -run TestRedist -v`
Expected: FAIL — `redistParent` undefined / old tweak still present.

- [ ] **Step 3: Implement**

Create `internal/catalog/redist.go`. Move `vcredistX64URL`, `vcredistX86URL`, `vcredistAcceptExit`, `vcRuntimeDetect` here from `prep.go`. Add a `redistChild` helper and the per-version detect builders. Placeholders for legacy until Task 7:

```go
package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// Evergreen 2015-2022 permalinks (verified by Microsoft Authenticode, no pin).
const (
	vcredistX64URL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"
	vcredistX86URL = "https://aka.ms/vs/17/release/vc_redist.x86.exe"
)

// Installer exit codes treated as success: 0 ok, 3010 reboot-required, 1638 newer
// already installed (satisfied), 1641 reboot initiated.
var vcredistAcceptExit = []int{0, 3010, 1638, 1641}

// PLACEHOLDER legacy values — REPLACED in Task 7 by grounded URL+SHA256. A non-hex
// SHA256 here makes DownloadInstall.Apply refuse before the network (fail-closed),
// so a forgotten value can never install. DO NOT ship with these.
const sha256TODO = "TODO_GROUND_THIS_SHA256_IN_TASK_7"

// vcRuntimeDetect reads a version's runtime "Installed" dword in the correct view.
// keyVer is the VisualStudio key version ("14.0","12.0","11.0"); sub is the runtime
// subkey family ("VC\\Runtimes" for 2012/2013/2022, "VC\\VCRedist" for 2010).
func vcRuntimeDetect(keyVer, sub, arch string) action.RegSet {
	view := action.ViewDefault64
	if arch == "x86" {
		view = action.ViewWow6432
	}
	return action.RegSet{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\VisualStudio\` + keyVer + `\` + sub + `\` + arch,
		Value: "Installed", Kind: action.KindDword, On: uint64(1), Off: uint64(0),
		Elev: core.ElevUser, View: view,
	}
}

// vcUninstallDetect detects 2005/2008 by the existence of their MSI Uninstall key's
// DisplayName value. guid is the product GUID (grounded in Task 7). x86 GUIDs live
// under the WOW6432Node view on 64-bit Windows.
func vcUninstallDetect(guid, arch string) action.RegPresent {
	view := action.ViewDefault64
	if arch == "x86" {
		view = action.ViewWow6432
	}
	return action.RegPresent{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + guid,
		Value: "DisplayName", Elev: core.ElevUser, View: view,
	}
}

// redistChild builds one version+arch install leaf.
func redistChild(id string, name core.I18n, url string, verify action.VerifyMode, sha string, detect core.Action) core.Tweak {
	return core.Tweak{
		ID: id, Category: "prep", Name: name, Elevation: core.ElevAdmin,
		Actions: []core.Action{action.DownloadInstall{
			URL:        url,
			Verify:     verify,
			SHA256:     sha,
			Args:       []string{"/install", "/quiet", "/norestart"}, // 2010+; legacy MSI args grounded in Task 7
			AcceptExit: vcredistAcceptExit,
			Detect:     detect,
			Elev:       core.ElevAdmin,
		}},
	}
}

// redistParent is the expandable group surfaced in the prep category.
func redistParent() core.Tweak {
	n := func(ru, en string) core.I18n { return core.I18n{RU: ru, EN: en} }
	return core.Tweak{
		ID: "prep.vcredist", Category: "prep", Elevation: core.ElevAdmin,
		Name: n("Visual C++ Redistributable (все версии)", "Visual C++ Redistributable (all versions)"),
		Desc: n("Скачать и тихо установить VC++ 2005-2022 (x64/x86) с проверкой подписи Microsoft.",
			"Download and silently install VC++ 2005-2022 (x64/x86), verifying the Microsoft signature."),
		Children: []core.Tweak{
			// 2015-2022: evergreen, Authenticode-verified (existing behaviour).
			redistChild("prep.vcredist.vc2022_x64", n("VC++ 2015-2022 x64", "VC++ 2015-2022 x64"), vcredistX64URL, action.VerifyAuthenticodeMicrosoft, "", vcRuntimeDetect("14.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2022_x86", n("VC++ 2015-2022 x86", "VC++ 2015-2022 x86"), vcredistX86URL, action.VerifyAuthenticodeMicrosoft, "", vcRuntimeDetect("14.0", `VC\Runtimes`, "x86")),
			// 2013 (12.0) — VC\Runtimes. URL+SHA grounded in Task 7.
			redistChild("prep.vcredist.vc2013_x64", n("VC++ 2013 x64", "VC++ 2013 x64"), "TODO_URL_2013_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("12.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2013_x86", n("VC++ 2013 x86", "VC++ 2013 x86"), "TODO_URL_2013_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("12.0", `VC\Runtimes`, "x86")),
			// 2012 (11.0) — VC\Runtimes.
			redistChild("prep.vcredist.vc2012_x64", n("VC++ 2012 x64", "VC++ 2012 x64"), "TODO_URL_2012_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("11.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2012_x86", n("VC++ 2012 x86", "VC++ 2012 x86"), "TODO_URL_2012_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("11.0", `VC\Runtimes`, "x86")),
			// 2010 (10.0) — VC\VCRedist (NOT Runtimes).
			redistChild("prep.vcredist.vc2010_x64", n("VC++ 2010 x64", "VC++ 2010 x64"), "TODO_URL_2010_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("10.0", `VC\VCRedist`, "x64")),
			redistChild("prep.vcredist.vc2010_x86", n("VC++ 2010 x86", "VC++ 2010 x86"), "TODO_URL_2010_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("10.0", `VC\VCRedist`, "x86")),
			// 2008 (9.0) — MSI; detect via Uninstall\{GUID}. GUID+URL+SHA grounded in Task 7.
			redistChild("prep.vcredist.vc2008_x64", n("VC++ 2008 x64", "VC++ 2008 x64"), "TODO_URL_2008_X64", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2008_X64", "x64")),
			redistChild("prep.vcredist.vc2008_x86", n("VC++ 2008 x86", "VC++ 2008 x86"), "TODO_URL_2008_X86", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2008_X86", "x86")),
			// 2005 (8.0) — MSI; detect via Uninstall\{GUID}.
			redistChild("prep.vcredist.vc2005_x64", n("VC++ 2005 x64", "VC++ 2005 x64"), "TODO_URL_2005_X64", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2005_X64", "x64")),
			redistChild("prep.vcredist.vc2005_x86", n("VC++ 2005 x86", "VC++ 2005 x86"), "TODO_URL_2005_X86", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2005_X86", "x86")),
		},
	}
}
```

In `internal/catalog/prep.go`: delete the `prep.install_vcredist` tweak literal AND the now-moved consts/helpers (`vcredistX64URL`, `vcredistX86URL`, `vcredistAcceptExit`, `vcRuntimeDetect`). In the catalog assembly site, append `redistParent()` to the `prep` category's tweak slice (after the existing prep tweaks). Confirm `action.KindDword`, `action.ViewDefault64`, `action.ViewWow6432`, `action.KindString` names against `regcommon.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -v`
Expected: PASS for `TestRedistParentShape` / `TestRedistParentRegisteredUnderPrep`; update any `prep_test.go` assertion that referenced `prep.install_vcredist`.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/redist.go internal/catalog/prep.go internal/catalog/*_test.go internal/catalog/catalog.go
git commit -m "feat(catalog): redist parent + 12 version/arch children (values TBD Task 7)"
```

---

### Task 5: UI — `expanded` state, `visibleRows` flattening, parent aggregate status

Introduce the flattened visible-row model the right pane uses everywhere, plus parent status aggregation and the leaves-only startup probe. No rendering change yet beyond routing through the new helpers; behaviour for categories WITHOUT parents is identical.

**Files:**
- Modify: `internal/ui/ui.go` (model field `expanded`; `New` initialises it; `Init`/`allTweaks` use `Leaves`; add `visibleRows`, `rowStatus`)
- Modify: `internal/ui/update.go` (navigation/hit-test counts use `visibleRows`; `curTweak` uses it)
- Test: `internal/ui/visible_test.go` (new)

**Interfaces:**
- Produces:
  - model field `expanded map[string]bool` (parent ID → expanded).
  - `type visRow struct { tw core.Tweak; child bool }` — a flattened right-pane row; `child` marks an indented child.
  - `func (m model) visibleRows() []visRow` — for the current category: each tweak as a row; if it `IsParent()` and `m.expanded[tw.ID]`, its children follow as `child:true` rows.
  - `func (m model) rowStatus(tw core.Tweak) core.Status` — for a parent, the aggregate of children's cached statuses (all-On→On, all-Off→Off, any unknown→Unknown, mix→Partial); for a leaf, `m.statusOf(tw.ID)`.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/visible_test.go`:

```go
package ui

import (
	"testing"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

func redistFixture() core.Catalog {
	return core.Catalog{{ID: "prep", Tweaks: []core.Tweak{
		{ID: "prep.leaf"},
		{ID: "prep.group", Children: []core.Tweak{
			{ID: "prep.group.a"}, {ID: "prep.group.b"},
		}},
	}}}
}

func TestVisibleRowsCollapsedThenExpanded(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// collapsed: parent shows as a single row, no children.
	rows := m.visibleRows()
	if len(rows) != 2 || rows[1].tw.ID != "prep.group" || rows[1].child {
		t.Fatalf("collapsed rows wrong: %+v", rows)
	}
	// expand the parent.
	m.expanded["prep.group"] = true
	rows = m.visibleRows()
	if len(rows) != 4 {
		t.Fatalf("expanded want 4 rows, got %d", len(rows))
	}
	if rows[2].tw.ID != "prep.group.a" || !rows[2].child {
		t.Fatalf("row 2 should be child a, got %+v", rows[2])
	}
	if rows[3].tw.ID != "prep.group.b" || !rows[3].child {
		t.Fatalf("row 3 should be child b, got %+v", rows[3])
	}
}

func TestRowStatusAggregatesParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	parent, _ := m.catalog.Find("prep.group")
	// all children On → parent On.
	m.statuses["prep.group.a"] = core.StatusOn
	m.statuses["prep.group.b"] = core.StatusOn
	if got := m.rowStatus(parent); got != core.StatusOn {
		t.Fatalf("all-on aggregate = %v, want On", got)
	}
	// mix → Partial.
	m.statuses["prep.group.b"] = core.StatusOff
	if got := m.rowStatus(parent); got != core.StatusPartial {
		t.Fatalf("mixed aggregate = %v, want Partial", got)
	}
	// all off → Off.
	m.statuses["prep.group.a"] = core.StatusOff
	if got := m.rowStatus(parent); got != core.StatusOff {
		t.Fatalf("all-off aggregate = %v, want Off", got)
	}
	// any unknown → Unknown (still probing).
	delete(m.statuses, "prep.group.a")
	if got := m.rowStatus(parent); got != core.StatusUnknown {
		t.Fatalf("unknown-present aggregate = %v, want Unknown", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run 'TestVisibleRows|TestRowStatus' -v`
Expected: FAIL — `expanded`/`visibleRows`/`rowStatus` undefined.

- [ ] **Step 3: Implement**

In `internal/ui/ui.go` model struct add (near `selected`):

```go
	// expanded marks expandable PARENT tweak IDs whose children are shown inline.
	// Missing entry = collapsed. Independent of selection/status.
	expanded map[string]bool
```

In `New(...)` add `expanded: map[string]bool{},` to the returned literal.

Change `Init` to probe leaves only:

```go
func (m model) Init() tea.Cmd {
	tws := m.catalog.Leaves()
	if len(tws) == 0 {
		return nil
	}
	for _, t := range tws {
		m.probing[t.ID] = true
	}
	return m.engine.ProbeBatchCmd(tws)
}
```

(Leave `allTweaks` as-is if other callers use it, but the startup probe now uses `Leaves`.)

Add the flattening + aggregation helpers (in ui.go, near the accessors):

```go
// visRow is one flattened right-pane row: a tweak plus whether it is an indented
// child of an expanded parent. visibleRows is the single source the renderer,
// cursor, hit-test, and selection all index, so they never disagree on geometry.
type visRow struct {
	tw    core.Tweak
	child bool
}

// visibleRows flattens the current category's tweaks: each tweak is a row; an
// expanded parent is immediately followed by its children as child rows.
func (m model) visibleRows() []visRow {
	var rows []visRow
	for _, tw := range m.curTweaks() {
		rows = append(rows, visRow{tw: tw})
		if tw.IsParent() && m.expanded[tw.ID] {
			for _, ch := range tw.Children {
				rows = append(rows, visRow{tw: ch, child: true})
			}
		}
	}
	return rows
}

// rowStatus is the status to render/act on for a row: a parent aggregates its
// children's cached statuses (any unknown → Unknown so it shows "…" until all
// children resolve; all-on → On; all-off → Off; otherwise Partial); a leaf is its
// own cached status.
func (m model) rowStatus(tw core.Tweak) core.Status {
	if !tw.IsParent() {
		return m.statusOf(tw.ID)
	}
	on, off := 0, 0
	for _, ch := range tw.Children {
		switch m.statusOf(ch.ID) {
		case core.StatusOn:
			on++
		case core.StatusOff:
			off++
		default:
			return core.StatusUnknown // a child not yet resolved (or blocked/absent)
		}
	}
	switch {
	case on == 0 && off == 0:
		return core.StatusUnknown
	case off == 0:
		return core.StatusOn
	case on == 0:
		return core.StatusOff
	default:
		return core.StatusPartial
	}
}
```

Reroute the right-pane geometry to `visibleRows()`:
- `internal/ui/ui.go` `curTweak`:
  ```go
  func (m model) curTweak() (core.Tweak, bool) {
  	rows := m.visibleRows()
  	if m.twCursor < 0 || m.twCursor >= len(rows) {
  		return core.Tweak{}, false
  	}
  	return rows[m.twCursor].tw, true
  }
  ```
- `internal/ui/update.go`: replace `len(m.curTweaks())` with `len(m.visibleRows())` in: the wheel-scroll handler (`twScroll` clamp), `rowAtClick` right-pane `total` (`internal/ui/render.go:530`), `moveCursor`/`moveCursorTo`/`moveCursorToEnd` right-pane `n`, and `clampScrolls` `nTw`. (Search the package for `curTweaks()` and switch the COUNT/INDEX sites — but keep `curTweaks()` itself for the category tweak list source used inside `visibleRows`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: PASS — new visible/rowStatus tests and all existing ui tests (collapsed categories without parents behave exactly as before, since a parentless category yields one visRow per tweak).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/ui.go internal/ui/update.go internal/ui/render.go internal/ui/visible_test.go
git commit -m "feat(ui): flattened visibleRows + parent aggregate status + leaves probe"
```

---

### Task 6: UI — render children, expand/collapse keys+click, parent-apply expansion

Wire the visible rows into rendering (indented children, parent ▸/▾ glyph), Enter/click to expand a parent, and apply-time expansion of a checked parent into its appliable children.

**Files:**
- Modify: `internal/ui/render.go` (`rightBody` iterates `visibleRows`, uses `rowStatus`, indents children, draws expand glyph)
- Modify: `internal/ui/update.go` (`toggleCurrent` expands a parent; click on a parent expands; `selectedByStatus`/`applySelected` expand a checked parent into children)
- Test: `internal/ui/expand_test.go` (new), extend `buttonbar_test.go`

**Interfaces:**
- Consumes: `visibleRows`, `rowStatus` (Task 5); `statusAppliable` (Task 1); `core.Tweak.Children` (Task 2).
- Produces:
  - `toggleCurrent`: on a parent row → flips `m.expanded[id]` (no checkbox); on a leaf/child → toggles `m.selected[id]` (unchanged behaviour).
  - `applySelected`: a checked parent contributes its appliable children's IDs to the batch queue (never the parent ID — the parent has no Actions).

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/expand_test.go`:

```go
package ui

import (
	"testing"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

func TestEnterExpandsParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1 // the parent row (row 0 is prep.leaf)
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatal("toggleCurrent on a parent must expand it")
	}
	if gm.selected["prep.group"] {
		t.Fatal("expanding a parent must NOT check it")
	}
	// toggling again collapses.
	gm.twCursor = 1
	out2, _ := gm.toggleCurrent()
	if out2.(model).expanded["prep.group"] {
		t.Fatal("second toggle must collapse")
	}
}

func TestApplySelectedExpandsParentToChildren(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// parent checked; children appliable (Off).
	m.selected["prep.group"] = true
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	ids := m.applyQueueIDs() // helper the impl exposes for testing the expansion
	want := map[string]bool{"prep.group.a": true, "prep.group.b": true}
	if len(ids) != 2 || !want[ids[0]] || !want[ids[1]] {
		t.Fatalf("queue = %v, want the two children", ids)
	}
	// the parent's own ID must never be queued (it has no actions).
	for _, id := range ids {
		if id == "prep.group" {
			t.Fatal("parent id must not be in the apply queue")
		}
	}
}
```

(Refactor `applySelected` to build its queue via a small testable helper `applyQueueIDs() []string` that does the parent→children expansion; `applySelected` then consumes it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestEnterExpands|TestApplySelectedExpands' -v`
Expected: FAIL — parent toggles checkbox (not expand); `applyQueueIDs` undefined.

- [ ] **Step 3: Implement**

`toggleCurrent` in `internal/ui/update.go` — branch on parent:

```go
func (m model) toggleCurrent() (model, tea.Cmd) {
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	if tw.IsParent() {
		m.expanded[tw.ID] = !m.expanded[tw.ID] // Enter/space/click expands, never checks
		return m, nil
	}
	if !statusHasAction(m.rowStatus(tw)) {
		return m, nil
	}
	m.selected[tw.ID] = !m.selected[tw.ID]
	return m, nil
}
```

Add `applyQueueIDs` and use it in `applySelected` (replace its `selectedByStatus(statusAppliable)` call):

```go
// applyQueueIDs returns the leaf tweak IDs to apply: every checked appliable leaf
// in catalog order, with a checked PARENT expanded into its appliable children
// (the parent itself, having no actions, is never queued).
func (m model) applyQueueIDs() []string {
	var ids []string
	for _, tw := range m.allTweaks() {
		if tw.IsParent() {
			if m.selected[tw.ID] {
				for _, ch := range tw.Children {
					if statusAppliable(m.statusOf(ch.ID)) {
						ids = append(ids, ch.ID)
					}
				}
			}
			// also allow individually-checked children (when expanded/selected).
			for _, ch := range tw.Children {
				if m.selected[ch.ID] && statusAppliable(m.statusOf(ch.ID)) {
					ids = append(ids, ch.ID)
				}
			}
			continue
		}
		if m.selected[tw.ID] && statusAppliable(m.statusOf(tw.ID)) {
			ids = append(ids, tw.ID)
		}
	}
	return dedupeStrings(ids) // a child checked AND under a checked parent appears once
}
```

Add a tiny `dedupeStrings` helper (order-preserving) in update.go. Update `applySelected`:

```go
func (m model) applySelected() (model, tea.Cmd) {
	if m.batchKind != batchNone {
		return m, nil
	}
	q := m.applyQueueIDs()
	if len(q) == 0 {
		return m, nil
	}
	m.batchKind = batchApply
	m.batchQueue = q
	m = m.enterProgress(len(q))
	return m.advanceBatch()
}
```

`rightBody` in `internal/ui/render.go` — iterate `visibleRows`, use `rowStatus`, indent children, draw a ▸/▾ glyph on parents:

```go
func (m model) rightBody(innerW int) []string {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return []string{dimStyle.Render(truncDisplay(T(m.lang, kNoTweaks), innerW))}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		tw := r.tw
		st := m.rowStatus(tw)

		rowStyle := appliableStyle
		switch st {
		case core.StatusOn, core.StatusBlocked, core.StatusAbsent,
			core.StatusRebootPending, core.StatusUnknown:
			rowStyle = appliedStyle
		}

		// Parents show an expand caret instead of a checkbox; leaves/children show a
		// checkbox when they have an action.
		var glyphCh string
		switch {
		case tw.IsParent():
			if m.expanded[tw.ID] {
				glyphCh = "▾"
			} else {
				glyphCh = "▸"
			}
		case statusHasAction(st):
			glyphCh = glyphOff
			if m.selected[tw.ID] {
				glyphCh = glyphOn
			}
		default:
			glyphCh = strings.Repeat(" ", lipgloss.Width(glyphOff))
		}

		name := tweakName(m.lang, tw)
		indent := ""
		if r.child {
			indent = "  "
		}

		marker := m.rowMarker(tw, st) // extract the existing marker switch into a helper

		glyph := rowStyle.Render(glyphCh)
		styledName := rowStyle.Render(name)
		raw := indent + glyph + " " + styledName + marker
		lines[i] = truncDisplay(raw, innerW)
	}
	return lines
}
```

Extract the existing marker `switch` (render.go:161-179) into `func (m model) rowMarker(tw core.Tweak, st core.Status) string` so both the old logic and child/parent rows reuse it. For children, add a child-specific installed/not marker if desired (e.g. `kStatusOn`/`kStatusOff` i18n) — minimal: reuse the existing markers (Partial/Blocked/etc.) which already cover the cases. Confirm `countOn` (render.go:425) still iterates leaves correctly — it reads `m.statusOf(tw.ID)` over `c.Tweaks`; update it to count over `m.catalog.Leaves()` so parents (Unknown aggregate) are not miscounted:

```go
func (m model) countOn() int {
	n := 0
	for _, tw := range m.catalog.Leaves() {
		if m.statusOf(tw.ID).IsOn() {
			n++
		}
	}
	return n
}
```

Mouse: clicking a parent row should expand it. `rowAtClick` already returns the visible-row index (after Task 5 it counts `visibleRows`); the right-pane click path calls `toggleCurrent`, which now expands parents — so click-to-expand works with no extra change. Verify the click handler (update.go:162-166) sets `m.twCursor = idx` then calls `toggleCurrent`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -v`
Expected: PASS — expand/apply-expansion tests plus all existing ui tests.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/render.go internal/ui/update.go internal/ui/expand_test.go internal/ui/buttonbar_test.go
git commit -m "feat(ui): inline expand of redist group + parent-apply expansion"
```

---

### Task 7: Grounding — real URLs, SHA256 pins, GUIDs, args; replace placeholders

Replace every `TODO_*` placeholder in `redist.go` with grounded values, recorded first in a reference doc. This task is RESEARCH + verification, not TDD; its deliverable is real values + a green build + a live install smoke test.

**Files:**
- Create: `docs/superpowers/refs/vcredist-grounding.md` (the recorded values + provenance)
- Modify: `internal/catalog/redist.go` (paste grounded values)

- [ ] **Step 1: Gather download URLs (per version × arch)**

For 2005, 2008, 2010, 2012, 2013 (x64 + x86), obtain the official Microsoft installer URLs. Prefer stable `aka.ms` permalinks where they exist (2012/2013 have `https://aka.ms/...` or `download.microsoft.com` links); otherwise the `download.microsoft.com/.../vcredist_x{64,86}.exe` static URLs. Record each URL + where it came from in `vcredist-grounding.md`. Use Context7/official Microsoft docs or the Microsoft "latest supported VC++ downloads" page — do NOT guess.

- [ ] **Step 2: Download each installer and compute its SHA256**

For each URL:
```bash
curl -L -o vc.exe "<URL>"
sha256sum vc.exe   # or: certutil -hashfile vc.exe SHA256
```
Record the 64-hex digest in `vcredist-grounding.md`. If a URL is a redirect to a frequently-updated build (not static), mark that child `VerifyAuthenticodeMicrosoft` instead and DROP its SHA pin (set `SHA256:""`, `Verify: action.VerifyAuthenticodeMicrosoft`) — the hybrid fallback.

- [ ] **Step 3: Confirm registry detection on this machine**

For 2010/2012/2013, confirm the `Installed` dword path+view actually exists when the runtime is installed (install one, then check):
```powershell
Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\VisualStudio\12.0\VC\Runtimes\x64' -Name Installed
Get-ItemProperty 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\12.0\VC\Runtimes\x86' -Name Installed
Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\VisualStudio\10.0\VC\VCRedist\x64' -Name Installed
```
For 2005/2008, find the actual `Uninstall\{GUID}` keys present after install:
```powershell
Get-ChildItem 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall',
              'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall' |
  Where-Object { (Get-ItemProperty $_.PSPath).DisplayName -match 'Visual C\+\+ (2005|2008)' } |
  Select-Object PSChildName, @{n='Name';e={(Get-ItemProperty $_.PSPath).DisplayName}}
```
Record the exact GUIDs (x64 in the 64-bit view, x86 under WOW6432Node) in `vcredist-grounding.md`. Note the GUID-fragility risk (a different minor/SP may differ → at worst a harmless idempotent re-install).

- [ ] **Step 4: Confirm silent-install args per legacy installer**

2010+ accept `/install /quiet /norestart`. 2005/2008 are MSI-wrapped: confirm whether they accept `/q` or `/quiet` (run `vcredist_x86.exe /?` if available, or use the documented `/q`). Record per-version args; adjust each child's `Args` in `redistChild` if they differ from the 2010+ default (thread an `args []string` param into `redistChild` if needed).

- [ ] **Step 5: Paste grounded values + build**

Replace every `TODO_URL_*`, `TODO_GUID_*`, and `sha256TODO` usage with the recorded values (or switch the child to Authenticode per Step 2). Remove the `sha256TODO` const once unused.
Run: `go build ./... && go test ./...`
Expected: build clean; all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/redist.go docs/superpowers/refs/vcredist-grounding.md
git commit -m "feat(catalog): ground legacy VC++ redist URLs, SHA256 pins, detect keys"
```

---

### Task 8: Live integration smoke test

Verify the whole feature end-to-end on the real machine. The x64/x86 2015-2022 `Installed` flags are currently ZEROED (set to 0 by the operator) so the 2022 children render as not-installed/appliable — ideal for exercising the install path.

- [ ] **Step 1: Build and run the app**

Run: `go build -o morgtweaker.exe . && ./morgtweaker.exe` (confirm the main package path).
Navigate to Prep → the "Visual C++ Redistributable (all versions)" row. Confirm it renders with a ▸ caret and aggregate colour (bright, since some are not installed).

- [ ] **Step 2: Expand and verify per-child status**

Press Enter on the parent → children appear indented, each with its installed/not status. Confirm the 2022 x64/x86 children show as not-installed (flags zeroed), legacy as appropriate.

- [ ] **Step 3: Install one child**

Check the 2022 x64 child, press [Apply]. Confirm: download progress streams, Authenticode verify passes, installer runs, exit code accepted, and the child re-probes to installed (the installer rewrites `Installed=1`). Confirm the row is NOT falsely flagged Blocked.

- [ ] **Step 4: Force-reapply check**

With a now-installed (grey) child checked, press [Apply] again. Confirm it re-runs (1638 "already installed" → accepted as success, not an error).

- [ ] **Step 5: Record the result**

Append the observed outcomes (per-child status, install exit codes, any 1638/3010) to `docs/superpowers/refs/vcredist-grounding.md`. If verify-after falsely flags Blocked on 1638 (installer did not rewrite the flag), note it and apply the spec's fallback: trust the accepted install exit code for that child by giving its `DownloadInstall` a `SkipVerifyAfter`-returning condition — revisit only if observed.

- [ ] **Step 6: Commit (docs only)**

```bash
git add docs/superpowers/refs/vcredist-grounding.md
git commit -m "docs: record vcredist live integration results"
```

---

## Self-review notes

- Spec coverage: force-reapply (T1), parent/child data (T2), RegPresent (T3), catalog parent+children (T4), inline-expand UI + aggregation (T5/T6), hybrid verify + full detect + grounding (T4 structure, T7 values), live verify-after/1638 (T8). All spec sections map to a task.
- Deviation from spec, intentional and simpler: the ENGINE is NOT modified to recurse into children. Parent aggregation and parent-apply expansion live in the UI; the engine only ever sees leaf tweaks. This meets every requirement with less risk. Recorded here so a reviewer expecting engine changes knows it was deliberate.
- Type consistency: `visRow{tw, child}`, `visibleRows()`, `rowStatus()`, `applyQueueIDs()`, `redistChild(...)`, `redistParent()`, `vcRuntimeDetect(keyVer, sub, arch)`, `vcUninstallDetect(guid, arch)`, `RegPresent{Root,Path,Value,Elev,View}` are used consistently across tasks.
- Placeholder discipline: the ONLY placeholders are the grounded legacy values, deliberately fail-closed (non-hex SHA256) and resolved in Task 7 before any real install. No `TODO` survives Task 7.
- Confirm-before-use list (grep first, the plan cites likely names): the catalog builder function name (Task 4 `Build()`), `readRawView` signature (Task 3), `action.KindDword/KindString/ViewDefault64/ViewWow6432` (Tasks 3-4), the main package path (Task 8).
