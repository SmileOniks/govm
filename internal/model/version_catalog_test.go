package model

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

var (
	themeCurrent = styles.NewTheme(config.ThemeCurrent)
	themeLight   = styles.NewTheme(config.ThemeLight)
)

func vcItem(version string) utils.GoVersion {
	return utils.GoVersion{Version: version, Filename: "go" + version + ".tar.gz"}
}

func asItem(t *testing.T, it list.Item) styles.Item {
	t.Helper()
	si, ok := it.(styles.Item)
	if !ok {
		t.Fatalf("item %v is not styles.Item", it)
	}
	return si
}

func TestVersionCatalogReplacePreservesSourceOrder(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	in := []utils.GoVersion{
		vcItem("1.22.0"),
		vcItem("1.21.0"),
		vcItem("1.23.0"),
	}
	changed, err := c.replace(in)
	if err != nil || !changed {
		t.Fatalf("replace: changed=%v err=%v", changed, err)
	}

	want := []string{"1.22.0", "1.21.0", "1.23.0"}
	if got := len(c.versions); got != len(want) {
		t.Fatalf("len = %d, want %d", got, len(want))
	}
	for i, w := range want {
		if c.versions[i].Version != w {
			t.Errorf("versions[%d] = %q, want %q", i, c.versions[i].Version, w)
		}
	}

	for _, v := range in {
		got, ok := c.lookup(v.Version)
		if !ok {
			t.Errorf("lookup(%q) not found", v.Version)
			continue
		}
		if got.Version != v.Version {
			t.Errorf("lookup(%q) = %q", v.Version, got.Version)
		}
	}
	if _, ok := c.lookup("9.9.9"); ok {
		t.Errorf("lookup of missing version should be false")
	}

	for v, idx := range c.index {
		if idx < 0 || idx >= len(c.versions) || c.versions[idx].Version != v {
			t.Errorf("index[%q] = %d inconsistent", v, idx)
		}
	}

	proj := c.projection()
	if len(proj.available) != 3 {
		t.Fatalf("projection available len = %d", len(proj.available))
	}
}

func TestVersionCatalogEmptyReplace(t *testing.T) {
	c := newVersionCatalog(themeCurrent)

	changed, err := c.replace(nil)
	if err != nil {
		t.Fatalf("nil replace on empty catalog: err=%v", err)
	}
	if changed {
		t.Fatalf("nil replace on empty catalog should be no-op")
	}

	if _, err := c.replace([]utils.GoVersion{vcItem("1.22.0")}); err != nil {
		t.Fatalf("seed replace: err=%v", err)
	}

	changed, err = c.replace(nil)
	if err != nil {
		t.Fatalf("empty replace: err=%v", err)
	}
	if !changed {
		t.Fatalf("empty replace on populated catalog should report changed")
	}
	if len(c.versions) != 0 {
		t.Fatalf("expected empty catalog, got len=%d", len(c.versions))
	}
	if _, ok := c.lookup("1.22.0"); ok {
		t.Fatalf("catalog should be empty after nil replace")
	}
	proj := c.projection()
	if len(proj.available) != 0 || len(proj.installed) != 0 {
		t.Fatalf("projection should be empty: available=%d installed=%d", len(proj.available), len(proj.installed))
	}
}

func TestVersionCatalogRejectsBadIdentity(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{vcItem("1.22.0")}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	if _, err := c.replace([]utils.GoVersion{vcItem("1.22.0"), vcItem("1.22.0")}); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("duplicate id: err=%v", err)
	}
	if _, err := c.replace([]utils.GoVersion{{Version: ""}}); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("empty id: err=%v", err)
	}

	// Atomic rollback: catalog keeps the seeded state untouched.
	if len(c.versions) != 1 {
		t.Fatalf("rollback failed: len=%d", len(c.versions))
	}
	if c.versions[0].Version != "1.22.0" {
		t.Fatalf("rollback failed: version=%q", c.versions[0].Version)
	}
	if _, ok := c.lookup("1.22.0"); !ok {
		t.Fatalf("rollback failed: lost seeded version")
	}
}

func TestVersionCatalogRejectsInvariantViolations(t *testing.T) {
	cases := []struct {
		name string
		in   []utils.GoVersion
	}{
		{
			"two active",
			[]utils.GoVersion{
				{Version: "1.22.0", Installed: true, Path: "/a", Active: true},
				{Version: "1.21.0", Installed: true, Path: "/b", Active: true},
			},
		},
		{
			"installed empty path",
			[]utils.GoVersion{{Version: "1.22.0", Installed: true, Path: ""}},
		},
		{
			"uninstalled with path",
			[]utils.GoVersion{{Version: "1.22.0", Installed: false, Path: "/a"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newVersionCatalog(themeCurrent)
			if _, err := c.replace([]utils.GoVersion{vcItem("1.20.0")}); err != nil {
				t.Fatalf("seed: err=%v", err)
			}
			_, err := c.replace(tc.in)
			if !errors.Is(err, errCatalogInvalid) {
				t.Fatalf("expected invalid input, got %v", err)
			}
			// Atomic rollback.
			if len(c.versions) != 1 || c.versions[0].Version != "1.20.0" {
				t.Fatalf("rollback failed: versions=%v", c.versions)
			}
		})
	}
}

func TestVersionCatalogAcceptsUnmanagedActiveVersion(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	changed, err := c.replace([]utils.GoVersion{{Version: "1.22.0", Active: true}})
	if err != nil {
		t.Fatalf("replace unmanaged active version: %v", err)
	}
	if !changed {
		t.Fatal("replace unmanaged active version should report changed")
	}

	got, ok := c.lookup("1.22.0")
	if !ok {
		t.Fatal("unmanaged active version not found")
	}
	if !got.Active || got.Installed || got.Path != "" {
		t.Fatalf("unmanaged active version = %+v", got)
	}
}

func TestVersionCatalogIdempotentAndChangedPath(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{vcItem("1.22.0")}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	changed, err := c.markInstalled("1.22.0", "/path/a")
	if err != nil || !changed {
		t.Fatalf("first markInstalled: changed=%v err=%v", changed, err)
	}
	changed, err = c.markInstalled("1.22.0", "/path/a")
	if err != nil {
		t.Fatalf("idempotent markInstalled err=%v", err)
	}
	if changed {
		t.Fatalf("idempotent markInstalled should report changed=false")
	}

	changed, err = c.markInstalled("1.22.0", "/path/b")
	if err != nil || !changed {
		t.Fatalf("changed-path markInstalled: changed=%v err=%v", changed, err)
	}
	got, _ := c.lookup("1.22.0")
	if got.Path != "/path/b" || !got.Installed {
		t.Fatalf("after changed path: installed=%v path=%q", got.Installed, got.Path)
	}

	changed, err = c.activate("1.22.0")
	if err != nil || !changed {
		t.Fatalf("first activate: changed=%v err=%v", changed, err)
	}
	changed, err = c.activate("1.22.0")
	if err != nil {
		t.Fatalf("idempotent activate err=%v", err)
	}
	if changed {
		t.Fatalf("idempotent activate should report changed=false")
	}

	// markDeleted idempotency requires a non-active installed version.
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/p"},
		{Version: "1.21.0", Installed: true, Path: "/q", Active: true},
	}); err != nil {
		t.Fatalf("reseed: err=%v", err)
	}
	changed, err = c.markDeleted("1.22.0")
	if err != nil || !changed {
		t.Fatalf("first markDeleted: changed=%v err=%v", changed, err)
	}
	changed, err = c.markDeleted("1.22.0")
	if err != nil {
		t.Fatalf("idempotent markDeleted err=%v", err)
	}
	if changed {
		t.Fatalf("idempotent markDeleted should report changed=false")
	}
	got, _ = c.lookup("1.22.0")
	if got.Installed || got.Path != "" {
		t.Fatalf("after delete: installed=%v path=%q", got.Installed, got.Path)
	}

	// markInstalled empty path is rejected.
	if _, err := c.markInstalled("1.21.0", ""); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("empty path markInstalled: err=%v", err)
	}
	// lookup miss is not-found.
	if _, err := c.markInstalled("9.9.9", "/x"); !errors.Is(err, errCatalogNotFound) {
		t.Fatalf("not-found markInstalled: err=%v", err)
	}
}

func TestVersionCatalogActivateClearsPriorActive(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a"},
		{Version: "1.21.0", Installed: true, Path: "/b"},
	}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	if changed, err := c.activate("1.22.0"); err != nil || !changed {
		t.Fatalf("activate 1.22.0: changed=%v err=%v", changed, err)
	}
	if changed, err := c.activate("1.21.0"); err != nil || !changed {
		t.Fatalf("activate 1.21.0: changed=%v err=%v", changed, err)
	}

	a, _ := c.lookup("1.22.0")
	if a.Active {
		t.Fatalf("1.22.0 should no longer be active")
	}
	b, _ := c.lookup("1.21.0")
	if !b.Active {
		t.Fatalf("1.21.0 should be active")
	}

	activeCount := 0
	for _, v := range c.versions {
		if v.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active count = %d, want 1", activeCount)
	}

	// activate on uninstalled version is forbidden.
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a", Active: true},
		{Version: "1.21.0"},
	}); err != nil {
		t.Fatalf("reseed: err=%v", err)
	}
	if _, err := c.activate("1.21.0"); !errors.Is(err, errCatalogForbidden) {
		t.Fatalf("activate uninstalled: err=%v", err)
	}
}

func TestVersionCatalogDeleteRules(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a", Active: true},
	}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	if _, err := c.markDeleted("1.22.0"); !errors.Is(err, errCatalogForbidden) {
		t.Fatalf("delete active should be forbidden: err=%v", err)
	}
	// State untouched after forbidden delete.
	got, _ := c.lookup("1.22.0")
	if !got.Installed || !got.Active || got.Path != "/a" {
		t.Fatalf("forbidden delete mutated state: %+v", got)
	}

	// Ordinary delete clears installed state and path.
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a"},
		{Version: "1.21.0", Installed: true, Path: "/b", Active: true},
	}); err != nil {
		t.Fatalf("reseed: err=%v", err)
	}
	changed, err := c.markDeleted("1.22.0")
	if err != nil || !changed {
		t.Fatalf("ordinary delete: changed=%v err=%v", changed, err)
	}
	got, _ = c.lookup("1.22.0")
	if got.Installed || got.Path != "" || got.Active {
		t.Fatalf("delete should clear: installed=%v path=%q active=%v", got.Installed, got.Path, got.Active)
	}
	// Still present in the catalog as an available version.
	if len(c.versions) != 2 {
		t.Fatalf("delete should not remove version from catalog: len=%d", len(c.versions))
	}

	// not-found path.
	if _, err := c.markDeleted("9.9.9"); !errors.Is(err, errCatalogNotFound) {
		t.Fatalf("delete missing: err=%v", err)
	}
}

func TestVersionCatalogCopyOnWrite(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a", Active: true},
		{Version: "1.21.0"},
	}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}
	snapshot := c.projection()

	old := c // value copy; must keep observing the pre-mutation state.

	if _, err := c.markInstalled("1.21.0", "/b"); err != nil {
		t.Fatalf("markInstalled 1.21.0: err=%v", err)
	}
	if _, err := c.activate("1.21.0"); err != nil {
		t.Fatalf("activate 1.21.0: err=%v", err)
	}
	if _, err := c.replace([]utils.GoVersion{vcItem("1.20.0")}); err != nil {
		t.Fatalf("replace: err=%v", err)
	}

	if len(old.versions) != 2 {
		t.Fatalf("old copy len = %d, want 2", len(old.versions))
	}
	if old.versions[0].Version != "1.22.0" || old.versions[1].Version != "1.21.0" {
		t.Fatalf("old copy versions changed: %v", old.versions)
	}
	o22, _ := old.lookup("1.22.0")
	if !o22.Installed || !o22.Active || o22.Path != "/a" {
		t.Fatalf("old 1.22.0 mutated: %+v", o22)
	}
	o21, _ := old.lookup("1.21.0")
	if o21.Installed || o21.Path != "" || o21.Active {
		t.Fatalf("old 1.21.0 mutated: %+v", o21)
	}
	if _, ok := old.lookup("1.20.0"); ok {
		t.Fatalf("old copy should not contain 1.20.0")
	}

	// The snapshot taken before mutation also stays valid: its slices
	// are independent of the catalog's current storage.
	snap22 := asItem(t, snapshot.available[0])
	if snap22.Name != "1.22.0" {
		t.Fatalf("snapshot available[0] = %q", snap22.Name)
	}
	if len(snapshot.installed) != 1 || snapshot.installed[0][0] != "1.22.0" {
		t.Fatalf("snapshot installed changed: %v", snapshot.installed)
	}
}

func TestVersionCatalogAliasingSafety(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	in := []utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a"},
		{Version: "1.21.0"},
	}
	if _, err := c.replace(in); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	// Mutate the caller's input slice after replace.
	in[0].Version = "9.9.9"
	in[1].Installed = true
	in[1].Path = "/leaked"
	in = append(in, vcItem("1.20.0"))

	if _, ok := c.lookup("9.9.9"); ok {
		t.Fatalf("input element mutation leaked into catalog")
	}
	if _, ok := c.lookup("1.20.0"); ok {
		t.Fatalf("input append leaked into catalog")
	}
	got22, ok := c.lookup("1.22.0")
	if !ok {
		t.Fatalf("input mutation removed 1.22.0")
	}
	if got22.Version != "1.22.0" || got22.Path != "/a" {
		t.Fatalf("input mutation altered 1.22.0: %+v", got22)
	}
	got21, _ := c.lookup("1.21.0")
	if got21.Installed || got21.Path != "" {
		t.Fatalf("input mutation altered 1.21.0: %+v", got21)
	}

	// Projection aliasing: mutating the returned slices/rows must not
	// reach the catalog.
	proj := c.projection()
	proj.available[0] = nil
	proj.available = proj.available[:0]
	if len(proj.installed) > 0 {
		proj.installed[0][0] = "MUTATED"
		proj.installed = proj.installed[:0]
	}

	proj2 := c.projection()
	if len(proj2.available) != 2 {
		t.Fatalf("catalog available corrupted by projection aliasing: len=%d", len(proj2.available))
	}
	if proj2.available[0] == nil {
		t.Fatalf("catalog available[0] nilled by projection aliasing")
	}
	if len(proj2.installed) != 1 {
		t.Fatalf("catalog installed corrupted by projection aliasing: len=%d", len(proj2.installed))
	}
	wantRow := table.Row{"1.22.0", "/a", "0 B", ""}
	for i, cell := range wantRow {
		if proj2.installed[0][i] != cell {
			t.Fatalf("catalog installed row corrupted: %v", proj2.installed[0])
		}
	}
}

func TestVersionCatalogSetThemeUpdatesRenderedTitle(t *testing.T) {
	c := newVersionCatalog(themeCurrent)
	if _, err := c.replace([]utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/a", Active: true},
	}); err != nil {
		t.Fatalf("seed: err=%v", err)
	}

	before := asItem(t, c.projection().available[0]).RenderedTitle
	if before == "" {
		t.Fatalf("RenderedTitle should not be empty")
	}

	if !c.setTheme(themeLight) {
		t.Fatalf("setTheme should report a rebuild")
	}

	after := asItem(t, c.projection().available[0]).RenderedTitle
	if after == "" {
		t.Fatalf("RenderedTitle should not be empty after setTheme")
	}
	if before == after {
		t.Fatalf("setTheme did not change RenderedTitle")
	}

	// Non-theme data (name) is preserved across the rebuild.
	if asItem(t, c.projection().available[0]).Name != "1.22.0" {
		t.Fatalf("setTheme rebuild lost item name")
	}
	// Installed projection is unaffected by theme change.
	proj := c.projection()
	if len(proj.installed) != 1 || proj.installed[0][0] != "1.22.0" || proj.installed[0][3] != "active" {
		t.Fatalf("installed projection changed unexpectedly: %v", proj.installed)
	}
}
