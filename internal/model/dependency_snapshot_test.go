package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
)

func TestCycleAdapterKeepsRollbackContextUntilDecision(t *testing.T) {
	snapshot := &deps.DependencySnapshot{
		ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n"},
	}
	backup := &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"}
	dependencies := []deps.ModuleDependency{{
		Path: "example.com/dependency", Version: "v1.0.0", Latest: "v1.1.0",
	}}

	m := openUpdateDialog(t, loadDeps(t, newTestModel(t), dependencies))
	m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(deps.ApplyUpdatesDoneEvent{
		Snapshot:     snapshot,
		Backup:       backup,
		Dependencies: dependencies,
	})
	m = updated.(Model)
	if m.deps.cycle.Snapshot() == nil {
		t.Fatal("cycle should retain the snapshot while checks are pending")
	}
	if m.deps.dialog.kind != dialogChecks {
		t.Fatalf("dialog kind = %v, want dialogChecks", m.deps.dialog.kind)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.deps.cycle.Phase() != deps.PhaseRunningChecks {
		t.Fatalf("cycle phase = %s, want running-checks", m.deps.cycle.Phase())
	}

	updated, _ = m.Update(deps.ChecksDoneEvent{
		Result: deps.DependencyCheckResult{Command: "go test ./...", Output: "FAIL"},
	})
	m = updated.(Model)
	if m.deps.cycle.Snapshot() == nil || m.deps.cycle.CheckResult() == nil {
		t.Fatal("cycle should retain rollback context while rollback is pending")
	}
	if m.deps.dialog.kind != dialogRollback {
		t.Fatalf("dialog kind = %v, want dialogRollback", m.deps.dialog.kind)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n'})
	m = updated.(Model)
	if m.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle after terminal rendering", m.deps.cycle.Phase())
	}
	if m.deps.cycle.Snapshot() != nil || m.deps.cycle.CheckResult() != nil {
		t.Fatal("terminal adapter state should not retain rollback context")
	}
}
