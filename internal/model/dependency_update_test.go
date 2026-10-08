package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
)

func TestResetUpdateConfirmationClearsDialogAndEntries(t *testing.T) {
	m := modelAtConfirmApply(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected update dialog to close on cancel")
	}
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if len(got.deps.cycle.Entries()) != 0 {
		t.Fatalf("cycle entries = %d, want 0", len(got.deps.cycle.Entries()))
	}
}

func TestResetChecksConfirmationClearsDialog(t *testing.T) {
	m := modelAtConfirmChecks(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected checks dialog to close on cancel")
	}
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
}

func TestQuitKeysWorkWhileDialogOpen(t *testing.T) {
	keys := []tea.KeyPressMsg{
		{Code: 'q'},
		{Code: 'c', Mod: tea.ModCtrl},
	}
	for _, key := range keys {
		m := modelAtConfirmApply(t)
		_, cmd := m.Update(key)
		if cmd == nil || cmd() == nil {
			t.Fatalf("key %q did not return a quit command", key.String())
		}
	}
}

func TestRestoreBackupDialogEnterTriggersRestoreCmd(t *testing.T) {
	m := openRestoreDialog(t, newTestModel(t), []deps.DependencyBackupInfo{
		{Name: "2026-07-09_12-00-00.json"},
	})

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected restore dialog to close")
	}
	if got.deps.phase != depsRestoringBackup {
		t.Fatal("expected RestoringBackup to be true")
	}
	if cmd == nil {
		t.Fatal("expected restore command")
	}
}

func TestEscClosesConfirmDialog(t *testing.T) {
	m := modelAtConfirmApply(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.deps.dialog.active() {
		t.Fatal("expected dialog to close on esc")
	}
	if len(m.deps.cycle.Entries()) != 0 {
		t.Fatalf("expected cycle entries to clear on cancel, got %d", len(m.deps.cycle.Entries()))
	}
}

func TestRightArrowTogglesDialogChoice(t *testing.T) {
	m := modelAtConfirmApply(t)

	if !m.deps.dialog.choiceYes {
		t.Fatal("expected default to be Yes")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)

	if m.deps.dialog.choiceYes {
		t.Fatal("expected right arrow to toggle choice to No")
	}
}

func TestConfirmOnNoClosesDialogWithoutUpdate(t *testing.T) {
	m := modelAtConfirmApply(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.deps.dialog.active() {
		t.Fatal("expected dialog to close after confirm on No")
	}
	if m.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", m.deps.cycle.Phase())
	}
}

func TestConfirmOnYesTriggersUpdateCmd(t *testing.T) {
	m := modelAtConfirmApply(t)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.deps.dialog.active() {
		t.Fatal("expected dialog to close after confirm on Yes")
	}
	if m.deps.cycle.Phase() != deps.PhaseApplying {
		t.Fatalf("cycle phase = %s, want applying", m.deps.cycle.Phase())
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned after confirming Yes")
	}
}

func TestRollbackCmdTriggeredByRollbackYes(t *testing.T) {
	m := modelAtConfirmRollback(t)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected rollback dialog to close")
	}
	if got.deps.cycle.Phase() != deps.PhaseRollingBack {
		t.Fatalf("cycle phase = %s, want rolling-back", got.deps.cycle.Phase())
	}
	if cmd == nil {
		t.Fatal("expected a command to be returned for rollback")
	}
}

func TestKeepCmdClearsRollbackDialog(t *testing.T) {
	m := modelAtConfirmRollback(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected rollback dialog to close when keeping updates")
	}
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if got.Status.Kind() != "warning" {
		t.Fatalf("expected warning status, got %q", got.Status.Kind())
	}
}

func TestEscOnChecksDialogSkipsChecks(t *testing.T) {
	m := modelAtConfirmChecks(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected dialog to close on esc")
	}
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
}

func TestEscOnRollbackDialogKeepsUpdates(t *testing.T) {
	m := modelAtConfirmRollback(t)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(Model)

	if got.deps.dialog.active() {
		t.Fatal("expected dialog to close on esc")
	}
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
}
