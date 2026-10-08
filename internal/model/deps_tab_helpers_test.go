package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
)

// The helpers in this file bring the Deps tab into a given state the
// way the user would: through the tab's own result messages and key
// presses, never by writing its fields. A state that cannot be reached
// here cannot be reached by the user either.

// testLib is the one-module dependency list most cycle tests start
// from: an update is available, so u opens the update dialog.
func testLib() []deps.ModuleDependency {
	return []deps.ModuleDependency{
		{Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0"},
	}
}

// feed sends msg through Update and returns the resulting Model,
// discarding the command.
func feed(tb testing.TB, m Model, msg tea.Msg) Model {
	tb.Helper()
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// loadDeps makes the Deps tab current and lists the given dependencies
// through the tab's own result message, exactly as the lazy load or a
// refresh would.
func loadDeps(tb testing.TB, m Model, list []deps.ModuleDependency) Model {
	tb.Helper()
	m.CurrentTab = DepsTab
	return feed(tb, m, dependenciesMsg(list))
}

// openUpdateDialog presses u on the Deps tab and completes the check
// with the listed dependencies, so the update dialog is open.
func openUpdateDialog(tb testing.TB, m Model) Model {
	tb.Helper()
	m = feed(tb, m, tea.KeyPressMsg{Code: 'u'})
	m = feed(tb, m, deps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	if m.deps.dialog.kind != dialogUpdate {
		tb.Fatalf("dialog = %#v, want update dialog", m.deps.dialog)
	}
	return m
}

// confirmApplyFrom lists testLib on the Deps tab of m and opens the
// update dialog.
func confirmApplyFrom(tb testing.TB, m Model) Model {
	tb.Helper()
	return openUpdateDialog(tb, loadDeps(tb, m, testLib()))
}

// confirmChecksFrom confirms the update plan and completes the apply,
// so the checks dialog is open.
func confirmChecksFrom(tb testing.TB, m Model) Model {
	tb.Helper()
	m = confirmApplyFrom(tb, m)
	m = feed(tb, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = feed(tb, m, deps.ApplyUpdatesDoneEvent{
		Snapshot: &deps.DependencySnapshot{
			ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n"},
		},
		Backup:       &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"},
		Dependencies: m.deps.cycle.Dependencies(),
	})
	if m.deps.dialog.kind != dialogChecks {
		tb.Fatalf("dialog = %#v, want checks dialog", m.deps.dialog)
	}
	return m
}

// confirmRollbackFrom confirms the checks and completes them with the
// given result, so the rollback dialog is open.
func confirmRollbackFrom(tb testing.TB, m Model, result deps.DependencyCheckResult) Model {
	tb.Helper()
	m = confirmChecksFrom(tb, m)
	m = feed(tb, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = feed(tb, m, deps.ChecksDoneEvent{Result: result})
	if m.deps.dialog.kind != dialogRollback {
		tb.Fatalf("dialog = %#v, want rollback dialog", m.deps.dialog)
	}
	return m
}

func failedChecks() deps.DependencyCheckResult {
	return deps.DependencyCheckResult{Command: "go test ./...", Output: "FAIL"}
}

func modelAtConfirmApply(tb testing.TB) Model {
	tb.Helper()
	return confirmApplyFrom(tb, newTestModel(tb))
}

func modelAtConfirmChecks(tb testing.TB) Model {
	tb.Helper()
	return confirmChecksFrom(tb, newTestModel(tb))
}

func modelAtConfirmRollback(tb testing.TB) Model {
	tb.Helper()
	return confirmRollbackFrom(tb, newTestModel(tb), failedChecks())
}

// openRestoreDialog delivers the backups list, which opens the restore
// dialog.
func openRestoreDialog(tb testing.TB, m Model, backups []deps.DependencyBackupInfo) Model {
	tb.Helper()
	m = feed(tb, m, dependencyBackupsMsg(backups))
	if m.deps.dialog.kind != dialogRestore {
		tb.Fatalf("dialog = %#v, want restore dialog", m.deps.dialog)
	}
	return m
}

// modelAtStandalonePhase drives a loaded Deps tab into the given
// standalone phase: r for checking, b for loading backups, and a
// confirmed restore dialog for restoring.
func modelAtStandalonePhase(tb testing.TB, phase depsPhase) Model {
	tb.Helper()
	m := loadDeps(tb, newTestModel(tb), nil)
	switch phase {
	case depsChecking:
		m = feed(tb, m, tea.KeyPressMsg{Code: 'r'})
	case depsLoadingBackups:
		m = feed(tb, m, tea.KeyPressMsg{Code: 'b'})
	case depsRestoringBackup:
		m = openRestoreDialog(tb, m, []deps.DependencyBackupInfo{{Name: "2026-07-09_12-00-00.json"}})
		m = feed(tb, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if m.deps.phase != phase {
		tb.Fatalf("phase = %v, want %v", m.deps.phase, phase)
	}
	return m
}

// withDialog runs build on *m and restores the tab that was current
// before, so a dialog can be opened for a test that renders another
// tab underneath it.
func withDialog(m *Model, build func(Model) Model) {
	tab := m.CurrentTab
	*m = build(*m)
	m.CurrentTab = tab
}
