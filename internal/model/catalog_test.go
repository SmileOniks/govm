package model

// This file contains integration tests for the private version catalog
// and its interaction with the Available/Installed widgets. These tests
// exercise the catalog through the agreed production contract:
//
//   - Model.replaceVersions([]utils.GoVersion) (tea.Cmd, error) applies
//     the catalog snapshot and rebuilds both widgets.
//   - catalog.projection() returns one ordered snapshot for both
//     widgets.
//   - catalog.lookup(version) returns (utils.GoVersion, bool).
//   - The Available list and Installed table are private widget fields
//     (m.list, m.installedTable) accessible from same-package tests.
//
// Each test documents the observable behaviour it asserts. If a test
// fails due to a production contract mismatch, the failure message
// identifies which catalog behaviour diverged.

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
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

// ---------------------------------------------------------------------------
// 1. Invalid VersionsMsg preserves prior projection and reports error.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// 2. Completion for unknown version starts reconciliation.
// ---------------------------------------------------------------------------

// TestUnknownCompletionStartsReconciliation verifies that an install
// completion for a version absent from the catalog triggers a
// reconciliation: a fetch command is issued (asserted non-nil, not
// invoked to avoid network) and a warning surfaces to the user.
func TestUnknownCompletionStartsReconciliation(t *testing.T) {
	m := newVersionCacheTestModel(t)
	operation := m.projection.startMutation(catalogMutationInstall, "9.9.9-nonexistent")

	updated, cmd := m.Update(installSuccessMsg{
		OperationID: operation.id,
		Version:     "9.9.9-nonexistent",
		Path:        "/p/9.9.9",
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("expected non-nil reconciliation fetch command for unknown completion version")
	}
	if got.Status.Kind() != "warning" {
		t.Fatalf("status kind = %q, want warning for reconciliation", got.Status.Kind())
	}
}

// ---------------------------------------------------------------------------
// 3. Successful reconciliation confirms pending operation.
// ---------------------------------------------------------------------------

// TestReconciliationConfirmsPendingCompletion verifies the full
// reconciliation flow: after an unknown completion triggers a fetch,
// the returned VersionsMsg that includes the completed version must
// confirm the pending operation — the version is marked installed with
// the path from the original completion message.
func TestReconciliationConfirmsPendingCompletion(t *testing.T) {
	m := newVersionCacheTestModel(t)
	operation := m.projection.startMutation(catalogMutationInstall, "1.30.0")

	// 1. Trigger reconciliation for a version not yet in the catalog.
	updated, cmd := m.Update(installSuccessMsg{
		OperationID: operation.id,
		Version:     "1.30.0",
		Path:        "/p/1.30.0",
	})
	m = updated.(Model)

	// 2. The reconciliation fetch returns a catalog that now includes
	//    the completed version, installed on disk.
	fresh := projectionVersions(m, utils.GoVersion{
		Version:   "1.30.0",
		Filename:  "go1.30.0.darwin-arm64.tar.gz",
		Installed: true,
		Path:      "/p/1.30.0",
	})
	updated, _ = m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, cmd),
		Versions:  fresh,
	})
	got := updated.(Model)

	v, ok := got.projection.lookup("1.30.0")
	if !ok {
		t.Fatal("expected 1.30.0 in catalog after reconciliation")
	}
	if !v.Installed {
		t.Fatalf("1.30.0 Installed = false, want true (pending completion confirmed)")
	}
	if v.Path != "/p/1.30.0" {
		t.Fatalf("1.30.0 Path = %q, want /p/1.30.0", v.Path)
	}
}

func TestSwitchReconciliationConfirmsActiveVersion(t *testing.T) {
	m := newVersionCacheTestModel(t)
	operation := m.projection.startMutation(catalogMutationActivation, "1.30.0")
	updated, cmd := m.Update(activationSuccessMsg{
		OperationID: operation.id,
		Result:      lifecycle.ActivationResult{Version: "1.30.0"},
		ShimInPath:  true,
	})
	m = updated.(Model)

	fresh := projectionVersions(m)
	for i := range fresh {
		fresh[i].Active = false
	}
	fresh = append(fresh, utils.GoVersion{
		Version:   "1.30.0",
		Installed: true,
		Active:    true,
		Path:      "/p/1.30.0",
	})
	updated, _ = m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, cmd),
		Versions:  fresh,
	})
	got := updated.(Model)

	v, ok := got.projection.lookup("1.30.0")
	if !ok || !v.Active {
		t.Fatalf("reconciled switch version = %+v, found=%v", v, ok)
	}
	if got.Status.Kind() != "success" || got.projection.operationPhase() != catalogOperationPhaseIdle {
		t.Fatalf("status=%q phase=%v", got.Status.Kind(), got.projection.operationPhase())
	}
}

func TestDeleteReconciliationAcceptsAbsentVersion(t *testing.T) {
	m := newVersionCacheTestModel(t)
	operation := m.projection.startMutation(catalogMutationDeletion, "1.30.0")
	updated, cmd := m.Update(deletionSuccessMsg{
		OperationID: operation.id,
		Result:      lifecycle.DeletionResult{Version: "1.30.0"},
	})
	m = updated.(Model)

	updated, _ = m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, cmd),
		Versions:  projectionVersions(m),
	})
	got := updated.(Model)
	if got.Status.Kind() != "success" {
		t.Fatalf("status kind = %q, want success", got.Status.Kind())
	}
	if got.projection.operationPhase() != catalogOperationPhaseIdle {
		t.Fatalf("operation phase = %v, want idle", got.projection.operationPhase())
	}
}

// ---------------------------------------------------------------------------
// 4. Valid refresh that cannot confirm pending completion applies
//    snapshot and reports error.
// ---------------------------------------------------------------------------

// TestValidRefreshWithoutPendingConfirmationReportsError verifies that
// when a reconciliation fetch returns a catalog that does NOT contain
// the pending completion version, the snapshot is still applied but an
// error is reported about the unconfirmable pending operation.
func TestValidRefreshWithoutPendingConfirmationReportsError(t *testing.T) {
	m := newVersionCacheTestModel(t)
	operation := m.projection.startMutation(catalogMutationInstall, "1.30.0")

	// 1. Trigger reconciliation for an unknown completion.
	updated, cmd := m.Update(installSuccessMsg{
		OperationID: operation.id,
		Version:     "1.30.0",
		Path:        "/p/1.30.0",
	})
	m = updated.(Model)

	// 2. The fetch returns a catalog WITHOUT the completed version —
	//    the pending operation cannot be confirmed.
	fresh := projectionVersions(m)
	updated, _ = m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, cmd),
		Versions:  fresh,
	})
	got := updated.(Model)

	// The snapshot must still be applied (catalog is non-empty).
	if len(got.projection.projection().available) == 0 {
		t.Fatal("expected non-empty catalog after valid refresh despite unconfirmable completion")
	}
	// The unconfirmable pending completion must be reported as an error.
	if got.Status.Kind() != "error" {
		t.Fatalf("status kind = %q, want error for unconfirmable pending completion", got.Status.Kind())
	}
}

// ---------------------------------------------------------------------------
// 5. Theme toggle refreshes cached RenderedTitle.
// ---------------------------------------------------------------------------

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

	// Toggle to the light theme.
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

// ---------------------------------------------------------------------------
// 6. Selection preserved by version identity across reorder.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// 7. Fallback index after selected identity removal.
// ---------------------------------------------------------------------------

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

// TestMissingDeleteConfirmLookupNeverInvokesDeletion verifies that when
// the user presses Y to confirm a delete whose target version is no
// longer in the catalog, the handler must NOT enter the Loading state
// and must NOT issue a deletion command. The handler must short-circuit
// via catalog lookup before dispatching the delete operation.
func TestMissingDeleteConfirmLookupNeverInvokesDeletion(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = AvailableTab
	m.ConfirmingDelete = true
	m.DeleteVersion = "9.9.9-nonexistent"

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'Y'})
	got := updated.(Model)

	if activity := got.projection.activityState(); activity.kind != catalogActivityIdle {
		t.Fatalf("activity = %+v, want idle when delete target is missing", activity)
	}
	if cmd != nil {
		t.Fatal("expected nil command when delete target is missing from catalog")
	}
	// The pending delete confirmation must be cleared.
	if got.ConfirmingDelete {
		t.Fatal("expected ConfirmingDelete=false after missing-lookup delete confirm")
	}
}
