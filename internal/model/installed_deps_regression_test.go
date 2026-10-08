package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/utils"
)

// TestInstalledTab_UKeyTriggersSwitchVersion regression-tests a bug
// where pressing `u` on the Installed tab was a no-op: handleUseKey
// only had branches for AvailableTab and DepsTab, so the key fell
// through and returned a nil command. The Installed tab must read its
// own selected row (InstalledTable.SelectedRow) and dispatch
// SwitchVersion for the matching version.
func TestInstalledTab_UKeyTriggersSwitchVersion(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = InstalledTab
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Active: true, Path: "/p/1.24.4"},
		{Version: "1.26.0", Installed: true, Active: false, Path: "/p/1.26.0"},
	})
	called := ""
	m = m.BindVersionOperations(VersionOperations{
		Activate: func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
			called = version
			return lifecycle.ActivationResult{Version: version}, nil
		},
		ShimInPath: func() bool { return true },
	})
	// Move the cursor to the non-active row (1.26.0).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if got := m.projection.installedModel().Cursor(); got != 1 {
		t.Fatalf("cursor = %d, want 1 before pressing u", got)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'u'})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("expected SwitchVersion command when pressing 'u' on Installed tab, got nil")
	}
	if activity := got.projection.activityState(); activity.kind != catalogActivityActivating || activity.version != "1.26.0" {
		t.Fatalf("activity = %+v, want activating 1.26.0", activity)
	}
	got = runCatalogTestCmd(t, got, cmd)
	if v, _ := got.projection.lookup("1.26.0"); called != "1.26.0" || !v.Active {
		t.Fatalf("activation target=%q, installed version=%+v", called, v)
	}
	assertVersionViewsConsistent(t, got)
}

// TestDepsTab_CheckStatusClearsAfterDependenciesMsg regression-tests
// a bug where "Checking for dependency updates..." leaked across all
// tabs after pressing `r` on the Deps tab. handleRefreshKey used
// Status.SetGlobal (global scope) but the dependenciesMsg handler
// called Status.ClearTab (tab scope only), so the message was never
// torn down and survived tab switches.
func TestDepsTab_CheckStatusClearsAfterDependenciesMsg(t *testing.T) {
	m := newTestModel(t)
	// Switch to DepsTab (also lazy-loads).
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(dependenciesMsg{})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	if m.deps.phase != depsChecking {
		t.Fatalf("phase = %v, want depsChecking", m.deps.phase)
	}

	// Simulate the check completing.
	updated, _ = m.Update(dependenciesMsg{})
	m = updated.(Model)

	if m.Status.Text() != "" {
		t.Fatalf("expected status to be cleared after dependenciesMsg, got %q", m.Status.Text())
	}
	if m.deps.phase != depsIdle {
		t.Fatalf("phase = %v, want depsIdle after dependenciesMsg", m.deps.phase)
	}
}

// TestDepsTab_CheckPhaseShowsSpinner regression-tests a bug where the
// Deps tab `r` (check updates) flow showed a static status line with
// no spinner animation, unlike the Available tab refresh. The root
// cause was that SpinnerText returned "" for depsChecking/OpUpdating
// while an imperative Status.SetGlobal filled the status text with a
// non-empty string; composeStatus therefore fell through its spinner
// branches and never prefixed Spinner.View().
func TestDepsTab_CheckPhaseShowsSpinner(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(dependenciesMsg{})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)

	status, _ := m.composeStatus()
	plain := stripANSI(status)

	if !strings.Contains(plain, stripANSI(m.Spinner.View())) {
		t.Fatalf("expected status to contain spinner frame %q, got %q", stripANSI(m.Spinner.View()), plain)
	}
	if !strings.Contains(plain, "Checking") {
		t.Fatalf("expected status to mention 'Checking', got %q", plain)
	}
}

// TestDepsTab_BackupsPhaseStartsEmpty regression-tests a bug where
// pressing `b` on the Deps tab leaked a global-scope "Loading
// dependency backups..." status across tab switches. handleBackupsKey
// used Status.SetGlobal (global scope) but the load is in-flight and
// its progress text already comes from depsTab.SpinnerText(); the
// global scope meant the message survived tab switches in the window
// between handleBackupsKey and dependencyBackupsMsg. The fix mirrors
// handleRefreshKey: Status.Clear() keeps the scope tab-local.
func TestDepsTab_BackupsPhaseStartsEmpty(t *testing.T) {
	m := newTestModel(t)
	// Switch to DepsTab (also lazy-loads).
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(dependenciesMsg{})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'b'})
	m = updated.(Model)
	if m.deps.phase != depsLoadingBackups {
		t.Fatalf("phase = %v, want depsLoadingBackups", m.deps.phase)
	}

	// While the load is in-flight, the status must be empty: progress
	// text is rendered by composeStatus via depsTab.SpinnerText(), so
	// a non-empty Status.Text() here means a second writer leaked a
	// global-scope message that survives tab switches.
	if got := m.Status.Text(); got != "" {
		t.Fatalf("expected status to be empty during depsLoadingBackups (spinner renders progress), got %q", got)
	}
	if m.Status.Scope() != statusScopeTab {
		t.Fatalf("expected status scope to be tab-local, got %v", m.Status.Scope())
	}
}
