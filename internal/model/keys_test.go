package model

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	coredeps "github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestTabSwitchingCyclesThroughFourTabs(t *testing.T) {
	m := newTestModel(t)

	if m.CurrentTab != AvailableTab {
		t.Fatalf("expected initial tab %d, got %d", AvailableTab, m.CurrentTab)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	if m.CurrentTab != InstalledTab {
		t.Fatalf("expected installed tab after first switch, got %d", m.CurrentTab)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	if m.CurrentTab != DepsTab {
		t.Fatalf("expected deps tab after second switch, got %d", m.CurrentTab)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	if m.CurrentTab != SettingsTab {
		t.Fatalf("expected settings tab after third switch, got %d", m.CurrentTab)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	if m.CurrentTab != AvailableTab {
		t.Fatalf("expected available tab after fourth switch, got %d", m.CurrentTab)
	}
}

func TestHandleTabKeyClearsScreenWhenSwitchingToSettings(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = DepsTab

	updated, cmd := m.handleTabKey()
	if got := updated.(*Model).CurrentTab; got != SettingsTab {
		t.Fatalf("current tab = %d, want %d", got, SettingsTab)
	}
	if cmd == nil {
		t.Fatal("expected clear screen command when switching to settings")
	}
	if got, want := reflect.TypeOf(cmd()), reflect.TypeOf(tea.ClearScreen()); got != want {
		t.Fatalf("command message type = %v, want %v", got, want)
	}
}

// shiftTab constructs a KeyPressMsg for Shift+Tab, mirroring the wire
// format produced by bubbletea v2 (KeyTab code + ModShift modifier).
func shiftTab() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}

func TestShiftTabSwitchesInReverseCycle(t *testing.T) {
	m := newTestModel(t)

	if m.CurrentTab != AvailableTab {
		t.Fatalf("expected initial tab %d, got %d", AvailableTab, m.CurrentTab)
	}

	// Available -> Settings (wraps backwards).
	updated, _ := m.Update(shiftTab())
	m = updated.(Model)
	if m.CurrentTab != SettingsTab {
		t.Fatalf("expected settings tab after first reverse switch, got %d", m.CurrentTab)
	}

	// Settings -> Deps.
	updated, _ = m.Update(shiftTab())
	m = updated.(Model)
	if m.CurrentTab != DepsTab {
		t.Fatalf("expected deps tab after second reverse switch, got %d", m.CurrentTab)
	}

	// Deps -> Installed.
	updated, _ = m.Update(shiftTab())
	m = updated.(Model)
	if m.CurrentTab != InstalledTab {
		t.Fatalf("expected installed tab after third reverse switch, got %d", m.CurrentTab)
	}

	// Installed -> Available.
	updated, _ = m.Update(shiftTab())
	m = updated.(Model)
	if m.CurrentTab != AvailableTab {
		t.Fatalf("expected available tab after fourth reverse switch, got %d", m.CurrentTab)
	}
}

func TestHandleShiftTabKeyClearsScreenWhenSwitchingToSettings(t *testing.T) {
	m := newTestModel(t)
	// On Available tab, reverse navigation wraps to Settings.
	m.CurrentTab = AvailableTab

	updated, cmd := m.handleShiftTabKey()
	if got := updated.(*Model).CurrentTab; got != SettingsTab {
		t.Fatalf("current tab = %d, want %d", got, SettingsTab)
	}
	if cmd == nil {
		t.Fatal("expected clear screen command when switching to settings")
	}
	if got, want := reflect.TypeOf(cmd()), reflect.TypeOf(tea.ClearScreen()); got != want {
		t.Fatalf("command message type = %v, want %v", got, want)
	}
}

// TestShiftTabCancelsPendingDelete mirrors the forward-Tab contract:
// switching tabs tears down the pending delete-confirmation context,
// not just for Tab but for the reverse direction too.
func TestShiftTabCancelsPendingDelete(t *testing.T) {
	m := newTestModel(t)
	deletes := 0
	m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
		deletes++
		return lifecycle.DeletionResult{Version: version}, nil
	}})
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"}})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: 'd'})
	if m.inputContext() != inputDeleteConfirm {
		t.Fatal("expected pending Installed delete confirmation")
	}
	m = press(t, m, shiftTab())
	if m.inputContext() == inputDeleteConfirm {
		t.Fatal("expected pending delete confirmation to be cancelled on reverse tab switch")
	}
	if got, want := m.CurrentTab, AvailableTab; got != want {
		t.Fatalf("current tab = %d, want %d (reverse of Installed)", got, want)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if deletes != 0 || m.inputContext() == inputDeleteConfirm {
		t.Fatal("returning to Installed must not restore the cancelled deletion")
	}
}

// TestConfirmDialogShiftTabTogglesChoice mirrors Tab inside the Deps
// confirm dialog: Shift+Tab flips the Yes/No selection rather than
// escaping the dialog, matching desktop conventions for 2-button dialogs.
func TestConfirmDialogShiftTabTogglesChoice(t *testing.T) {
	d := depsDialog{kind: dialogUpdate, choiceYes: true}

	got, action := d.handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if action != dialogNoop {
		t.Fatalf("expected dialogNoop, got %v", action)
	}
	if got.choiceYes {
		t.Fatal("expected Shift+Tab to flip ChoiceYes from true to false")
	}

	got, _ = got.handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !got.choiceYes {
		t.Fatal("expected second Shift+Tab to flip ChoiceYes back to true")
	}
}

func TestAvailableTabArrowKeysMoveListSelection(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)

	if got := m.projection.availableModel().Index(); got != 1 {
		t.Fatalf("expected list selection to move down to index 1, got %d", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)

	if got := m.projection.availableModel().Index(); got != 0 {
		t.Fatalf("expected list selection to move up to index 0, got %d", got)
	}
}

func TestInstalledTabArrowKeysMoveTableCursor(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = InstalledTab
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
		{Version: "1.25.0", Installed: true, Path: "/p/1.25.0"},
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)

	if got := m.projection.installedModel().Cursor(); got != 1 {
		t.Fatalf("expected installed table cursor to move down to index 1, got %d", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)

	if got := m.projection.installedModel().Cursor(); got != 0 {
		t.Fatalf("expected installed table cursor to move up to index 0, got %d", got)
	}
}

func TestInstalledTabDeleteUsesTableSelection(t *testing.T) {
	m := newTestModel(t)
	var deleted string
	m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
		deleted = version
		return lifecycle.DeletionResult{Version: version}, nil
	}})
	m.CurrentTab = InstalledTab
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
		{Version: "1.25.0", Installed: true, Path: "/p/1.25.0"},
	})
	setInstalledCursor(&m, 1)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	got := updated.(Model)
	if got.inputContext() != inputDeleteConfirm || !strings.Contains(got.Status.Text(), "delete Go 1.25.0?") {
		t.Fatalf("delete prompt = %q, context=%v", got.Status.Text(), got.inputContext())
	}
	// Navigation remains available, but confirmation must retain its original target.
	got = press(t, got, tea.KeyPressMsg{Code: tea.KeyUp})
	updated, cmd := got.Update(tea.KeyPressMsg{Code: 'y'})
	got = runCatalogTestCmd(t, updated.(Model), cmd)
	if deleted != "1.25.0" || got.inputContext() == inputDeleteConfirm {
		t.Fatalf("confirmed delete target=%q, context=%v", deleted, got.inputContext())
	}
}

func TestRefreshOnDepsTabTriggersCheckCmd(t *testing.T) {
	m := newTestModel(t)

	// Switch to deps tab
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	updated, _ = m.Update(dependenciesMsg{})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)

	if m.deps.phase != depsChecking {
		t.Fatal("expected CheckingDependencies to be true after pressing r on deps tab")
	}

	if cmd == nil {
		t.Fatal("expected a command to be returned")
	}
}

func TestRefreshWhileCatalogLoadIsInFlightIsIgnored(t *testing.T) {
	m := newTestModel(t)
	calls := 0
	m = m.BindVersionOperations(VersionOperations{
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
			calls++
			return []utils.GoVersion{{Version: "1.30.0"}}, nil
		},
	})
	updated, first := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	updated, second := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	if second != nil {
		t.Fatal("repeated refresh dispatched another load")
	}
	m = runCatalogTestCmd(t, m, first)
	if _, ok := m.projection.lookup("1.30.0"); calls != 1 || !ok {
		t.Fatalf("loader calls=%d, first snapshot published=%v", calls, ok)
	}
}

func TestRefreshWhileCatalogRefilterIsInFlightIsIgnored(t *testing.T) {
	m := applyFilter(t, newTestModel(t), "1.24")
	calls := 0
	m = m.BindVersionOperations(VersionOperations{
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
			calls++
			return []utils.GoVersion{{Version: "1.25.0"}, {Version: "1.24.4"}}, nil
		},
	})
	updated, first := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	updated, refilter := m.Update(first())
	m = updated.(Model)
	updated, second := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	if second != nil {
		t.Fatal("refresh interrupted the pending refilter")
	}
	m = runCatalogTestCmd(t, m, refilter)
	if calls != 1 || selectedListVersion(m) != "1.24.4" {
		t.Fatalf("loader calls=%d, selection=%q", calls, selectedListVersion(m))
	}
}

func TestFilterProgramMessageDropsRepeatedRefresh(t *testing.T) {
	m := newTestModel(t)
	program := newProgramModel(m)

	if got := FilterProgramMessage(program, tea.KeyPressMsg{Code: 'r'}); got == nil {
		t.Fatal("expected idle refresh to reach Update")
	}
	if got := FilterProgramMessage(program, tea.KeyPressMsg{Code: 'r'}); got != nil {
		t.Fatalf("rapid repeated message = %T, want nil", got)
	}

	program.lastRefreshKey = time.Time{}
	_, refresh := program.Update(tea.KeyPressMsg{Code: 'r'})
	if got := FilterProgramMessage(program, tea.KeyPressMsg{Code: 'r'}); got != nil {
		t.Fatalf("filtered message = %T, want nil while refresh is active", got)
	}
	program.Update(refresh())
	program.lastRefreshKey = time.Time{}
	if got := FilterProgramMessage(program, tea.KeyPressMsg{Code: 'r', IsRepeat: true}); got != nil {
		t.Fatalf("key repeat message = %T, want nil", got)
	}
	if got := FilterProgramMessage(program, tea.KeyPressMsg{Code: tea.KeyDown}); got == nil {
		t.Fatal("expected non-refresh key to reach Update")
	}
}

func TestProgramModelUpdateMutatesInPlace(t *testing.T) {
	program := newProgramModel(newTestModel(t))

	updated, _ := program.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	if updated != program {
		t.Fatal("expected the program model identity to remain stable")
	}
	if program.model.CurrentTab != InstalledTab {
		t.Fatalf("current tab = %d, want %d", program.model.CurrentTab, InstalledTab)
	}
}

func TestPressBOnDepsTabTriggersBackupListCmd(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)
	updated, _ = m.Update(dependenciesMsg{})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'b'})
	m = updated.(Model)

	if m.deps.phase != depsLoadingBackups {
		t.Fatal("expected LoadingBackups to be true after pressing b on deps tab")
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned")
	}
}

func TestPressUOnDepsOpensConfirmDialog(t *testing.T) {
	m := newTestModel(t)

	// Switch to deps tab.
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)

	// Load deps with one direct update.
	deps := dependenciesMsg{
		{Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0"},
	}
	updated, _ = m.Update(deps)
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'u'})
	m = updated.(Model)
	updated, _ = m.Update(coredeps.CheckUpdatesDoneEvent{Dependencies: []coredeps.ModuleDependency(deps)})
	m = updated.(Model)

	if m.deps.dialog.kind != dialogUpdate {
		t.Fatal("expected update dialog after fresh preflight")
	}
	if !m.deps.dialog.choiceYes {
		t.Fatal("expected default choice to be Yes")
	}
}

func TestPressUOnDepsWithoutUpdatesShowsMessage(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)

	// Load deps with no updates.
	deps := dependenciesMsg{
		{Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.0.0"},
	}
	updated, _ = m.Update(deps)
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'u'})
	m = updated.(Model)
	updated, _ = m.Update(coredeps.CheckUpdatesDoneEvent{Dependencies: []coredeps.ModuleDependency(deps)})
	m = updated.(Model)

	if m.deps.dialog.active() {
		t.Fatal("expected dialog to stay closed when no updates available")
	}
	if m.Status.Kind() != "warning" {
		t.Fatalf("expected warning message, got type %q", m.Status.Kind())
	}
}

func TestCatalogFlowBlocksDepsUpdateUntilMutationCompletes(t *testing.T) {
	m := loadDeps(t, newVersionCacheTestModel(t), testLib())
	checks := 0
	fakeDepsExecutor{execute: func(coredeps.Intent) (coredeps.Event, error) {
		checks++
		return coredeps.CheckUpdatesDoneEvent{Dependencies: testLib()}, nil
	}}.bind(&m)
	m = m.BindVersionOperations(VersionOperations{
		Install: func(_ context.Context, r install.Request) (install.Result, error) {
			return install.Result{Version: r.Version, Path: "/go/" + r.Version}, nil
		},
	})
	m = press(t, m, shiftTab(), shiftTab())
	m = applyFilter(t, m, "1.25.0")
	updated, installCmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = press(t, updated.(Model), tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab})
	updated, blocked := m.Update(tea.KeyPressMsg{Code: 'u'})
	m = updated.(Model)
	if blocked != nil || checks != 0 || m.inputContext() == inputDepsDialog {
		t.Fatal("dependency update started while a catalog mutation was in flight")
	}
	m = runCatalogTestCmd(t, m, installCmd)
	updated, allowed := m.Update(tea.KeyPressMsg{Code: 'u'})
	m = runCatalogTestCmd(t, updated.(Model), allowed)
	if checks != 1 || m.inputContext() != inputDepsDialog {
		t.Fatalf("dependency update did not resume: checks=%d, context=%v", checks, m.inputContext())
	}
}
