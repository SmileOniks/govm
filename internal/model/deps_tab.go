package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

// This file is the single entry of the Deps tab module. The Model
// routes keys to update when the Input context is the Deps tab or a
// Deps dialog, routes every dependency message to it, and calls enter
// on arrival at the tab. The module answers with the tea.Cmd to run
// and a depsStatus effect the Model applies to its StatusLine; it
// never touches the Model.

// depsStatusScope says what the Model should do with a depsStatus.
type depsStatusScope int

const (
	// depsStatusUntouched (the zero value) leaves the status line as
	// it is.
	depsStatusUntouched depsStatusScope = iota
	// depsStatusTab sets a tab-scoped message.
	depsStatusTab
	// depsStatusGlobal sets a global message.
	depsStatusGlobal
	// depsStatusClear clears the status line entirely.
	depsStatusClear
	// depsStatusClearTab clears the tab-scoped message only.
	depsStatusClearTab
)

// depsStatus is the status-line effect a Deps tab operation asks for.
// It is a value so the module can be tested without a StatusLine.
type depsStatus struct {
	scope depsStatusScope
	text  string
	kind  string
}

func depsTabStatus(text, kind string) depsStatus {
	return depsStatus{scope: depsStatusTab, text: text, kind: kind}
}

func depsGlobalStatus(text, kind string) depsStatus {
	return depsStatus{scope: depsStatusGlobal, text: text, kind: kind}
}

var (
	depsClearStatus    = depsStatus{scope: depsStatusClear}
	depsClearTabStatus = depsStatus{scope: depsStatusClearTab}
)

// executor returns the dependency executor bound to the current
// backup limit, or the unavailable executor while none is bound.
func (s depsTab) executor() deps.API {
	if s.newExecutor == nil {
		return unavailableDepsExecutor{}
	}
	return s.newExecutor(s.backupLimit)
}

// applySettings pushes the Settings values the tab depends on: which
// rows the table shows and the backup limit handed to the executor.
// It is the only way Settings reach the tab.
func (s *depsTab) applySettings(values config.Settings) {
	values = config.Normalize(values)
	s.display = values.DepsDisplay
	s.backupLimit = values.DepsBackupLimit
	s.updateDependencyTable()
}

// enter runs the arrival side effect of the tab: the dependency list
// is loaded lazily on the first visit.
func (s *depsTab) enter() (tea.Cmd, depsStatus) {
	if s.loaded {
		return nil, depsStatus{}
	}
	s.phase = depsChecking
	return listDependenciesCmd(s.executor()), depsStatus{}
}

// update is the single entry for keys and messages. Keys arrive only
// while the tab or one of its dialogs owns the keyboard (the Model
// keeps ctrl+c, q and tab navigation for itself); every other message
// is either one of the tab's own results or forwarded to the table.
func (s *depsTab) update(msg tea.Msg) (tea.Cmd, depsStatus) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return s.handleKey(msg)
	case dependenciesMsg:
		s.loaded = true
		s.phase = depsIdle
		s.replaceDependencies(msg)
		return nil, depsClearTabStatus
	case dependencyBackupsMsg:
		s.phase = depsIdle
		s.backups = msg
		if len(msg) == 0 {
			return nil, depsTabStatus("No dependency backups found.", "warning")
		}
		s.dialog = depsDialog{
			kind:      dialogRestore,
			choiceYes: true,
			maxCursor: len(msg) - 1,
		}
		return nil, depsTabStatus("Select a dependency backup to restore.", "info")
	case dependenciesRestoredMsg:
		s.setUpdatedDependencies(msg.Dependencies)
		return nil, depsGlobalStatus(fmt.Sprintf("Restored dependencies from %s.", msg.BackupName), "success")
	case dependencyExecutionErrMsg:
		s.cycle = deps.NewUpdateCycle()
		s.resetDialog()
		return nil, depsGlobalStatus(msg.Err.Error(), "error")
	case dependencyErrMsg:
		s.reset()
		if msg.Err != nil {
			return nil, depsGlobalStatus(msg.Err.Error(), "error")
		}
		return nil, depsGlobalStatus("", "error")
	case deps.CheckUpdatesDoneEvent,
		deps.ApplyUpdatesDoneEvent,
		deps.CompensateDoneEvent,
		deps.ChecksDoneEvent,
		deps.RollbackDoneEvent:
		return s.handleCycleEvent(msg.(deps.Event))
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return cmd, depsStatus{}
}

// replaceDependencies installs a fresh dependency list and drops the
// Marks of modules that are no longer in it (see CONTEXT.md "Mark"):
// a refresh, a restore or a finished cycle step is the only moment the
// set of modules changes, so it is the only moment a mark can go
// stale. How the list is displayed never touches marks.
func (s *depsTab) replaceDependencies(list []deps.ModuleDependency) {
	s.dependencies = list
	listed := make(map[string]bool, len(list))
	for _, d := range list {
		listed[d.Path] = true
	}
	for path := range s.marks {
		if !listed[path] {
			delete(s.marks, path)
		}
	}
	s.updateDependencyTable()
}

// isDepsMsg reports whether msg is one of the tab's own results, which
// the Model routes to update regardless of the current tab.
func isDepsMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case dependenciesMsg, dependencyBackupsMsg, dependenciesRestoredMsg,
		dependencyExecutionErrMsg, dependencyErrMsg,
		deps.CheckUpdatesDoneEvent, deps.ApplyUpdatesDoneEvent,
		deps.CompensateDoneEvent, deps.ChecksDoneEvent, deps.RollbackDoneEvent:
		return true
	}
	return false
}

// handleKey maps a key to the tab's own action. While an operation is
// in flight the action keys are inert; an unknown key has no effect.
func (s *depsTab) handleKey(msg tea.KeyPressMsg) (tea.Cmd, depsStatus) {
	if s.dialog.active() {
		return s.handleDialogKey(msg)
	}
	key := msg.String()
	if s.busy() {
		switch key {
		case "u", "r", "b", "space", "a":
			return nil, depsStatus{}
		}
	}
	switch key {
	case "u":
		return s.startUpdate()
	case "r":
		return s.refresh()
	case "b":
		return s.loadBackups()
	case "space":
		return s.toggleMark()
	case "a":
		return s.toggleMarkAll()
	case "up", "down", "k", "j":
		var cmd tea.Cmd
		s.table, cmd = s.table.Update(msg)
		return cmd, depsStatus{}
	}
	return nil, depsStatus{}
}

// startUpdate begins an update cycle for the current Update scope:
// the marked modules, else every direct dependency.
func (s *depsTab) startUpdate() (tea.Cmd, depsStatus) {
	if !s.loaded {
		return nil, depsStatus{}
	}
	selection, ok := s.updateSelection()
	if !ok {
		return nil, depsTabStatus("Nothing to update.", "warning")
	}
	return s.startUpdateCycle(selection)
}

// startUpdateCycle creates a fresh Cycle, feeds StartEvent, and
// returns the tea.Cmd that runs the initial check-updates intent. The
// update confirmation dialog only opens after the check completes.
func (s *depsTab) startUpdateCycle(selection deps.UpdateSelection) (tea.Cmd, depsStatus) {
	s.cycle = deps.NewUpdateCycle()
	next, intent, err := s.cycle.Handle(deps.StartEvent{ModuleDir: s.moduleDir, Selection: selection})
	if err != nil {
		return nil, depsTabStatus("Could not start update.", "error")
	}
	s.cycle = next
	return s.cycleExecuteCmd(intent), depsClearStatus
}

// refresh re-lists the dependencies and checks for updates online.
// Progress text comes from SpinnerText while the phase is in flight,
// so only a stale status needs clearing; the tab-local scope lets the
// dependenciesMsg handler tear it down cleanly.
func (s *depsTab) refresh() (tea.Cmd, depsStatus) {
	s.phase = depsChecking
	return checkDependencyUpdatesCmd(s.executor()), depsClearStatus
}

// loadBackups lists the saved backups; the restore dialog opens when
// the list arrives.
func (s *depsTab) loadBackups() (tea.Cmd, depsStatus) {
	s.phase = depsLoadingBackups
	return listDependencyBackupsCmd(s.executor()), depsClearStatus
}

// toggleMark flips the Mark on the module under the cursor.
func (s *depsTab) toggleMark() (tea.Cmd, depsStatus) {
	if !s.loaded {
		return nil, depsStatus{}
	}
	d, ok := s.cursorDependency()
	if !ok {
		return nil, depsStatus{}
	}
	s.flipMark(d.Path)
	s.updateDependencyTable()
	return nil, depsStatus{}
}

// toggleMarkAll marks every listed dependency, or clears all marks
// when any exist.
func (s *depsTab) toggleMarkAll() (tea.Cmd, depsStatus) {
	if !s.loaded {
		return nil, depsStatus{}
	}
	added := s.flipAllMarks()
	s.updateDependencyTable()
	if n := len(s.markedPaths()); added {
		return nil, depsTabStatus(fmt.Sprintf("Marked %d %s.", n, deps.Pluralize(n, "dependency", "dependencies")), "info")
	}
	return nil, depsTabStatus("Marks cleared.", "info")
}

// view renders the tab's content canvas.
func (s depsTab) view() string { return s.table.View() }

// dialogActive reports whether one of the tab's dialogs owns the
// keyboard; the Model's Input context resolver reads it.
func (s depsTab) dialogActive() bool { return s.dialog.active() }

// dialogView renders the open dialog for the overlay.
func (s depsTab) dialogView(t styles.Theme, viewport viewportSize) string {
	return s.dialog.render(t, s, viewport)
}

// dialogKeyBindings describes the open dialog for the hint bar and the
// Help overlay.
func (s depsTab) dialogKeyBindings() helpSection {
	return dialogKeyBindings(s.dialog)
}

// Model side: the adapter between the tab and the Model's StatusLine.

// applyDepsStatus enacts a depsStatus on the Model's status line.
func (m *Model) applyDepsStatus(s depsStatus) {
	switch s.scope {
	case depsStatusTab:
		m.Status.SetTab(s.text, s.kind)
	case depsStatusGlobal:
		m.Status.SetGlobal(s.text, s.kind)
	case depsStatusClear:
		m.Status.Clear()
	case depsStatusClearTab:
		m.Status.ClearTab()
	}
}

// delegateDeps routes msg to the Deps tab and applies its status.
func (m *Model) delegateDeps(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd, status := m.deps.update(msg)
	m.applyDepsStatus(status)
	return m, cmd
}
