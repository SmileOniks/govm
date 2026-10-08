package model

// Catalog integration tests drive actions and load results through the model
// and check the shared Available/Installed projection.

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

// projectionVersions snapshots the model's catalog into a VersionsMsg,
// optionally appending extra versions. Used to simulate the result of a
// reconciliation fetch without network I/O.
func projectionVersions(m Model, extra ...utils.GoVersion) []utils.GoVersion {
	items := m.projection.availableModel().Items()
	out := make([]utils.GoVersion, 0, len(items)+len(extra))
	for _, item := range items {
		it, ok := item.(styles.Item)
		if !ok {
			continue
		}
		if v, found := m.projection.lookup(it.Name); found {
			out = append(out, v)
		}
	}
	return append(out, extra...)
}

// listItemName returns the Name field of the styles.Item at the given
// list index, or "" if the index is out of range or the item is not a
// styles.Item.
func listItemName(m Model, index int) string {
	items := m.projection.availableModel().Items()
	if index < 0 || index >= len(items) {
		return ""
	}
	it, ok := items[index].(styles.Item)
	if !ok {
		return ""
	}
	return it.Name
}

// Invalid VersionsMsg preserves prior projection and reports error.

// TestInvalidVersionsMsgPreservesPriorProjection verifies that an
// invalid VersionsMsg (containing a duplicate version id) dispatched
// through Update does NOT replace the catalog. The catalog's replace
// method rejects duplicate ids, so the prior projection must be
// preserved and an error must be surfaced to the user.
func TestInvalidVersionsMsgPreservesPriorProjection(t *testing.T) {
	m := newVersionCacheTestModel(t)
	priorLen := len(m.projection.projection().available)
	load := m.projection.startLoad(catalogLoadPurposeRefresh)

	// Duplicate version ids are rejected by the catalog as invalid input.
	updated, _ := m.Update(catalogLoadedMsg{
		RequestID: load.loadRequest.ID,
		Versions: []utils.GoVersion{
			{Version: "1.0.0", Filename: "go1.0.0.tar.gz"},
			{Version: "1.0.0", Filename: "go1.0.0.tar.gz"},
		},
	})
	got := updated.(Model)

	if gotLen := len(got.projection.projection().available); gotLen != priorLen {
		t.Fatalf("projection length = %d, want %d (prior preserved on invalid VersionsMsg)", gotLen, priorLen)
	}
	if got.Status.Kind() != "error" {
		t.Fatalf("status kind = %q, want error for invalid VersionsMsg", got.Status.Kind())
	}
}

func TestVersionsMsgAcceptsUnmanagedActiveVersion(t *testing.T) {
	m := newVersionCacheTestModel(t)
	load := m.projection.startLoad(catalogLoadPurposeRefresh)
	updated, _ := m.Update(catalogLoadedMsg{
		RequestID: load.loadRequest.ID,
		Versions: []utils.GoVersion{{
			Version: "1.26.0",
			Active:  true,
		}},
	})
	got := updated.(Model)

	v, ok := got.projection.lookup("1.26.0")
	if !ok || !v.Active || v.Installed {
		t.Fatalf("unmanaged active version = %+v, found=%v", v, ok)
	}
	if got.Status.Kind() == "error" {
		t.Fatalf("status = %q, want non-error", got.Status.Text())
	}
	if len(got.projection.installedModel().Rows()) != 0 {
		t.Fatalf("installed rows = %v, want none", got.projection.installedModel().Rows())
	}
}

func TestCatalogModelReconciliation(t *testing.T) {
	for _, action := range []string{"install", "activate", "delete"} {
		t.Run(action, func(t *testing.T) {
			m := newVersionCacheTestModel(t)
			const version = "1.30.0"
			target := utils.GoVersion{Version: version}
			if action != "install" {
				target.Installed = true
				target.Path = "/p/" + version
			}
			before := projectionVersions(m)
			seedVersions(t, &m, append(projectionVersions(m), target))
			nextSnapshot := before
			loads, mutations := 0, 0
			m = m.BindVersionOperations(VersionOperations{
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
					loads++
					return nextSnapshot, nil
				},
				Install: func(_ context.Context, r install.Request) (install.Result, error) {
					mutations++
					return install.Result{Version: r.Version, Path: "/p/" + r.Version}, nil
				},
				Activate: func(_ context.Context, v string) (lifecycle.ActivationResult, error) {
					mutations++
					return lifecycle.ActivationResult{Version: v}, nil
				},
				Delete: func(_ context.Context, v string) (lifecycle.DeletionResult, error) {
					mutations++
					return lifecycle.DeletionResult{Version: v}, nil
				},
				ShimInPath: func() bool { return true },
			})
			m = applyFilter(t, m, version)
			key := 'i'
			if action == "activate" {
				key = 'u'
			}
			if action == "delete" {
				m = press(t, m, tea.KeyPressMsg{Code: 'd'})
				key = 'y'
			}
			updated, operation := m.Update(tea.KeyPressMsg{Code: key})
			m = updated.(Model)
			if operation == nil {
				t.Fatal("action was not admitted")
			}
			completion := operation()
			updated, refresh := m.Update(tea.KeyPressMsg{Code: 'r'})
			m = runCatalogTestCmd(t, updated.(Model), refresh)
			if _, exists := m.projection.lookup(version); exists {
				t.Fatal("refresh did not remove the in-flight identity")
			}
			nextSnapshot = append([]utils.GoVersion(nil), before...)
			if action != "delete" {
				for i := range nextSnapshot {
					if action == "activate" {
						nextSnapshot[i].Active = false
					}
				}
				target.Installed = true
				target.Path = "/p/" + version
				target.Active = action == "activate"
				nextSnapshot = append(nextSnapshot, target)
			}
			updated, verify := m.Update(completion)
			m = runCatalogTestCmd(t, updated.(Model), verify)
			if loads != 2 || mutations != 1 || m.Status.Kind() != "success" {
				t.Fatalf("loads=%d mutations=%d status=%q", loads, mutations, m.Status.Text())
			}
			got, exists := m.projection.lookup(version)
			if action == "delete" {
				if exists {
					t.Fatalf("deleted identity remains: %+v", got)
				}
			} else if !exists || !got.Installed || got.Path != target.Path || got.Active != target.Active {
				t.Fatalf("reconciled target = %+v, found=%v, want %+v", got, exists, target)
			}
			assertVersionViewsConsistent(t, m)
		})
	}
}

// Theme toggle refreshes cached RenderedTitle.

// TestThemeToggleRefreshesRenderedTitle verifies that toggling the theme
// via applyRuntimeTheme refreshes the cached RenderedTitle on every list
// item. The Available list pre-renders item titles for performance; if
// applyRuntimeTheme does not rebuild them after a theme change, the
// titles would render with stale colours.
func TestThemeToggleRefreshesRenderedTitle(t *testing.T) {
	m := newVersionCacheTestModel(t)

	items := m.projection.availableModel().Items()
	if len(items) == 0 {
		t.Fatal("expected at least one list item")
	}
	it0, ok := items[0].(styles.Item)
	if !ok {
		t.Fatalf("list item 0 is %T, want styles.Item", items[0])
	}
	oldTitle := it0.RenderedTitle

	m.settings.values.Theme = config.ThemeLight
	m.applyRuntimeTheme()

	// The cached RenderedTitle must reflect the new theme.
	items = m.projection.availableModel().Items()
	it0, ok = items[0].(styles.Item)
	if !ok {
		t.Fatalf("list item 0 is %T after theme toggle", items[0])
	}
	if it0.RenderedTitle == oldTitle {
		t.Fatal("expected RenderedTitle to change after theme toggle")
	}
	// The refreshed title must still be correct for the source version.
	items = m.projection.availableModel().Items()
	if len(items) > 0 {
		it0, ok = items[0].(styles.Item)
		if !ok {
			t.Fatalf("list item 0 is %T after theme toggle", items[0])
		}
		v, found := m.projection.lookup(it0.Name)
		if !found {
			t.Fatalf("list item 0 version %q not in catalog after theme toggle", it0.Name)
		}
		want := styles.RenderItemTitle(m.theme, v.Version, v.Installed, v.Active)
		if it0.RenderedTitle != want {
			t.Fatalf("RenderedTitle = %q, want %q after theme toggle", it0.RenderedTitle, want)
		}
	}
}

// Selection preserved by version identity across reorder.

// TestSelectionPreservedByVersionIdentityAcrossReorder verifies that the
// catalog tracks list selection by version identity (version string),
// not by list index. After replaceVersions reorders the versions, the
// selected item must follow the version it pointed to before the
// reorder.
func TestSelectionPreservedByVersionIdentityAcrossReorder(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})

	// Select the middle version (1.21.0, index 1).
	m.projection.selectAvailable(1)
	if got := listItemName(m, m.projection.availableModel().Index()); got != "1.21.0" {
		t.Fatalf("pre-reorder selected = %q, want 1.21.0", got)
	}

	// Re-seed with a different order: 1.21.0 moves to index 2.
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.22.0"},
		{Version: "1.20.0"},
		{Version: "1.21.0"},
	})

	// Selection must follow 1.21.0 by identity, not stay at index 1.
	if got := listItemName(m, m.projection.availableModel().Index()); got != "1.21.0" {
		t.Fatalf("selected = %q after reorder, want 1.21.0 (identity preserved)", got)
	}
}

func TestUnchangedReplaceDoesNotRepublishProjection(t *testing.T) {
	m := newVersionCacheTestModel(t)
	priorItems := projectionVersions(m)
	priorSelection := selectedListVersion(m)

	cmd, err := replaceVersions(&m, projectionVersions(m))
	if err != nil {
		t.Fatalf("replace unchanged catalog: %v", err)
	}
	if cmd != nil {
		t.Fatal("unchanged replace returned a projection command")
	}
	if !reflect.DeepEqual(projectionVersions(m), priorItems) {
		t.Fatalf("unchanged replace altered available items: got %+v, want %+v", projectionVersions(m), priorItems)
	}
	if selected := selectedListVersion(m); selected != priorSelection {
		t.Fatalf("unchanged replace selected %q, want %q", selected, priorSelection)
	}
}

// Fallback index after selected identity removal.

// TestFallbackIndexAfterSelectedIdentityRemoval verifies that when the
// selected version is removed from the catalog (replaceVersions without
// it), the list selection falls back to a valid index rather than
// pointing past the end of the list.
func TestFallbackIndexAfterSelectedIdentityRemoval(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})
	m.projection.selectAvailable(0) // Select 1.20.0.

	// Remove the selected version from the catalog.
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})

	idx := m.projection.availableModel().Index()
	availLen := len(m.projection.projection().available)
	if idx < 0 || idx >= availLen {
		t.Fatalf("selection index %d out of range [0, %d) after selected identity removal", idx, availLen)
	}
}

func TestFilteredSelectionRestoredAfterProjectionRefilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})
	m.projection.setAvailableFilteringEnabled(true)
	m.projection.setAvailableFilterText("1.2")
	m.projection.selectAvailable(1)

	cmd, err := replaceVersions(&m, []utils.GoVersion{
		{Version: "1.22.0"},
		{Version: "1.20.0"},
		{Version: "1.21.0"},
	})
	if err != nil {
		t.Fatalf("replace filtered catalog: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected deferred refilter command")
	}
	msg, ok := cmd().(catalogProjectionRefilterMsg)
	if !ok {
		t.Fatalf("refilter command returned %T", cmd())
	}
	updated, _ := m.Update(msg)
	got := updated.(Model)

	if selected := selectedListVersion(got); selected != "1.21.0" {
		t.Fatalf("selected version = %q, want 1.21.0", selected)
	}
}

func TestStaleProjectionRefilterGenerationIsIgnored(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})
	m.projection.setAvailableFilterText("1.2")
	m.projection.selectAvailable(1)

	staleCmd, err := replaceVersions(&m, []utils.GoVersion{
		{Version: "1.22.0"},
		{Version: "1.21.0"},
		{Version: "1.20.0"},
	})
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	currentCmd, err := replaceVersions(&m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.22.0"},
		{Version: "1.21.0"},
	})
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}
	currentSelection := selectedListVersion(m)
	currentItems := projectionVersions(m)

	updated, _ := m.Update(staleCmd())
	m = updated.(Model)
	if !reflect.DeepEqual(projectionVersions(m), currentItems) {
		t.Fatalf("stale result changed current items: got %+v, want %+v", projectionVersions(m), currentItems)
	}
	if selected := selectedListVersion(m); selected != currentSelection {
		t.Fatalf("stale result selected %q, want %q", selected, currentSelection)
	}

	updated, _ = m.Update(currentCmd())
	m = updated.(Model)
	if selected := selectedListVersion(m); selected != "1.21.0" {
		t.Fatalf("selected version = %q, want 1.21.0", selected)
	}
}

func TestStaleProjectionRefilterTextIsIgnored(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.30.0"},
	})
	m.projection.setAvailableFilterText("1.2")

	cmd, err := replaceVersions(&m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.30.0"},
		{Version: "1.31.0"},
	})
	if err != nil {
		t.Fatalf("replace filtered catalog: %v", err)
	}
	m.projection.setAvailableFilterText("1.3")

	updated, _ := m.Update(cmd())
	got := updated.(Model)
	for _, item := range got.projection.availableModel().VisibleItems() {
		version := item.(styles.Item).Name
		if version != "1.30.0" && version != "1.31.0" {
			t.Fatalf("visible version = %q after stale filter result", version)
		}
	}
}
