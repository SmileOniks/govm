package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/deps"
)

// resetDialog closes any open dependency confirmation dialog.
func (s *depsTab) resetDialog() {
	s.dialog = depsDialog{}
}

// setUpdatedDependencies applies the shared bookkeeping for standalone
// deps-result handlers (dependenciesMsg, dependenciesRestoredMsg):
// reset the standalone phase, store the dependency list, and rebuild
// the dependency table.
func (s *depsTab) setUpdatedDependencies(modules []deps.ModuleDependency) {
	s.phase = depsIdle
	s.replaceDependencies(modules)
}

// handleDialogKey is the single entry point for key presses while a
// dependency confirmation dialog is open. Pure key handling (choice
// toggle, list navigation for restore) lives in depsDialog.Handle;
// this method enacts the returned dialogAction by delegating to the
// per-kind apply* helpers. Quitting is the Model's business and never
// reaches here.
func (s *depsTab) handleDialogKey(msg tea.KeyPressMsg) (tea.Cmd, depsStatus) {
	newDialog, action := s.dialog.handle(msg)
	s.dialog = newDialog
	switch action {
	case dialogConfirm:
		return s.applyDialogConfirm()
	case dialogCancel:
		return s.applyDialogCancel()
	case dialogChangeLevel:
		return s.applyDialogLevelChange()
	case dialogChangeScope:
		return s.applyDialogScopeChange()
	}
	return nil, depsStatus{}
}

func (s *depsTab) selectDialogLevel(level deps.UpdateLevel) (tea.Cmd, depsStatus) {
	if s.dialog.kind != dialogUpdate || s.dialog.level == level {
		return nil, depsStatus{}
	}
	valid := false
	for _, value := range deps.Levels {
		if value == level {
			valid = true
			break
		}
	}
	if !valid {
		return nil, depsStatus{}
	}
	s.dialog.level = level
	return s.applyDialogLevelChange()
}

func (s *depsTab) selectDialogScope(explicit bool) (tea.Cmd, depsStatus) {
	if !s.dialog.canToggleScope() || s.dialog.explicit == explicit {
		return nil, depsStatus{}
	}
	s.dialog.explicit = explicit
	return s.applyDialogScopeChange()
}

// applyDialogLevelChange asks the Cycle to rebuild the plan at the
// level the dialog now shows. The re-emitted IntentConfirmApply
// replaces the dialog contents; the dialog stays open.
func (s *depsTab) applyDialogLevelChange() (tea.Cmd, depsStatus) {
	if s.dialog.kind != dialogUpdate {
		return nil, depsStatus{}
	}
	return s.rebuildDialogPlan(deps.ChangeLevelEvent{Level: s.dialog.level})
}

// applyDialogScopeChange asks the Cycle to rebuild the plan for the
// Update scope the dialog now shows: the explicit module set, or
// every direct dependency.
func (s *depsTab) applyDialogScopeChange() (tea.Cmd, depsStatus) {
	if s.dialog.kind != dialogUpdate {
		return nil, depsStatus{}
	}
	var modules []string
	if s.dialog.explicit {
		modules = s.dialog.explicitModules
	}
	return s.rebuildDialogPlan(deps.ChangeScopeEvent{Modules: modules})
}

// rebuildDialogPlan feeds a plan-changing event into the Cycle while
// the update dialog is open. The re-emitted IntentConfirmApply
// replaces the dialog contents; the dialog stays open.
func (s *depsTab) rebuildDialogPlan(event deps.Event) (tea.Cmd, depsStatus) {
	next, intent, err := s.cycle.Handle(event)
	if err != nil {
		s.cycle = deps.NewUpdateCycle()
		s.resetDialog()
		return nil, depsGlobalStatus(err.Error(), "error")
	}
	s.cycle = next
	return s.applyCycleIntent(intent)
}

// applyDialogConfirm dispatches the confirm action. Update/checks/
// rollback confirmations feed the user's choice to the Cycle as a
// decision event; restore runs its own standalone command.
func (s *depsTab) applyDialogConfirm() (tea.Cmd, depsStatus) {
	switch s.dialog.kind {
	case dialogUpdate:
		return s.feedCycleDecision(deps.ConfirmApplyEvent{Yes: s.dialog.choiceYes})
	case dialogChecks:
		return s.feedCycleDecision(deps.ConfirmChecksEvent{Yes: s.dialog.choiceYes})
	case dialogRollback:
		return s.feedCycleDecision(deps.ConfirmRollbackEvent{Yes: s.dialog.choiceYes})
	case dialogRestore:
		return s.applyRestoreBackupChoice()
	}
	s.resetDialog()
	return nil, depsStatus{}
}

// applyDialogCancel dispatches the cancel action. Update/checks/
// rollback cancel feed a No decision to the Cycle; restore is torn
// down locally.
func (s *depsTab) applyDialogCancel() (tea.Cmd, depsStatus) {
	switch s.dialog.kind {
	case dialogUpdate:
		return s.feedCycleDecision(deps.ConfirmApplyEvent{Yes: false})
	case dialogChecks:
		return s.feedCycleDecision(deps.ConfirmChecksEvent{Yes: false})
	case dialogRollback:
		return s.feedCycleDecision(deps.ConfirmRollbackEvent{Yes: false})
	case dialogRestore:
		s.resetDialog()
		return nil, depsTabStatus("Restore canceled.", "info")
	}
	s.resetDialog()
	return nil, depsStatus{}
}

// feedCycleDecision closes the dialog and feeds a decision event into
// the Cycle through the central adapter.
func (s *depsTab) feedCycleDecision(event deps.Event) (tea.Cmd, depsStatus) {
	s.resetDialog()
	return s.handleCycleEvent(event)
}

func (s *depsTab) applyRestoreBackupChoice() (tea.Cmd, depsStatus) {
	if !s.dialog.choiceYes {
		s.resetDialog()
		return nil, depsTabStatus("Restore canceled.", "info")
	}
	if len(s.backups) == 0 || s.dialog.cursor < 0 || s.dialog.cursor >= len(s.backups) {
		s.resetDialog()
		return nil, depsTabStatus("Restore unavailable: no backup selected.", "error")
	}

	backup := s.backups[s.dialog.cursor]
	s.resetDialog()
	s.phase = depsRestoringBackup
	return restoreDependencyBackupCmd(s.executor(), backup.Name),
		depsGlobalStatus("Restoring dependency backup...", "info")
}
