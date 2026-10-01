package model

import (
	"errors"

	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

// depsPhase tracks standalone (non-cycle) dependency operations:
// manual refresh, lazy load, backups listing, and restore. The update
// workflow (check -> apply -> checks -> rollback) is driven by the
// deps.UpdateCycle and has no entry here.
type depsPhase int

const (
	depsIdle depsPhase = iota
	depsChecking
	depsLoadingBackups
	depsRestoringBackup
)

// depsTab is the Deps tab module: everything the tab owns, behind the
// entry points in deps_tab.go. The deps.UpdateCycle owns the
// update-workflow phase, pending decision, entries, snapshot, and
// check context; depsTab holds the presentation and standalone state
// around it: the dependency table projection, the standalone
// operation phase, saved backups for the restore flow, the Marks and
// the active confirmation dialog. Every field is private to the
// module; the Model and the tests reach it only through its methods.
type depsTab struct {
	moduleDir    string
	table        rowTable
	dependencies []deps.ModuleDependency
	loaded       bool
	phase        depsPhase
	backups      []deps.DependencyBackupInfo
	dialog       depsDialog
	cycle        deps.UpdateCycle
	// marks holds the module paths the user marked for the next
	// update (see CONTEXT.md "Mark"). Keyed by path, never by row,
	// because the table hides indirect rows and is rebuilt often.
	marks map[string]bool
	// rowPaths maps table row index to module path, refreshed by
	// updateDependencyTable, so the cursor can be resolved to a module.
	rowPaths []string
	// newExecutor returns the dependency executor bound to the given
	// backup limit. It is read before every operation so a mid-session
	// limit change in Settings is honoured. It is bound through
	// Model.BindDeps: main.go binds a single deps.Executor for
	// moduleDir; tests bind a fake to drive the cycle and the
	// standalone operations without IO. Unbound, every operation
	// reports errDepsUnavailable through the ordinary error path.
	newExecutor func(backupLimit int) deps.API
	// display and backupLimit are the Settings values the tab depends
	// on, pushed through applySettings.
	display     config.DepsDisplayMode
	backupLimit int
}

// errDepsUnavailable is what every dependency operation returns while
// no deps.API is bound. It surfaces as a status message instead of
// a nil dereference, so a Model built without BindDeps
// degrades quietly rather than crashing the TUI.
var errDepsUnavailable = errors.New("dependency operations are unavailable")

// unavailableDepsExecutor is the deps.API in force before BindDeps:
// every method fails with errDepsUnavailable.
type unavailableDepsExecutor struct{}

func (unavailableDepsExecutor) Execute(deps.Intent) (deps.Event, error) {
	return nil, errDepsUnavailable
}
func (unavailableDepsExecutor) List() ([]deps.ModuleDependency, error) {
	return nil, errDepsUnavailable
}
func (unavailableDepsExecutor) CheckUpdates() ([]deps.ModuleDependency, error) {
	return nil, errDepsUnavailable
}
func (unavailableDepsExecutor) Backups() ([]deps.DependencyBackupInfo, error) {
	return nil, errDepsUnavailable
}
func (unavailableDepsExecutor) Restore(string) (deps.DependencyRestoreResult, error) {
	return deps.DependencyRestoreResult{}, errDepsUnavailable
}

// newDepsTab builds an empty Deps tab for the module in moduleDir,
// with its table styled by theme. The Cycle is a fresh idle value. No
// executor is bound yet: constructing the tab never touches the go
// toolchain.
func newDepsTab(moduleDir string, theme styles.Theme) depsTab {
	tbl := newRowTable(
		dependencyTableColumns(defaultConstructionWidth),
		defaultConstructionWidth,
		15,
		tableStyles(theme),
	)
	return depsTab{
		moduleDir: moduleDir,
		table:     tbl,
		cycle:     deps.NewUpdateCycle(),
	}
}

// resize fits the table to the content area the Model has laid out.
func (s *depsTab) resize(width, height int) {
	s.table.Resize(width, height, dependencyTableColumns(width))
}

// applyTheme restyles the table after a runtime theme change.
func (s *depsTab) applyTheme(theme styles.Theme) {
	s.table.SetStyles(tableStyles(theme))
}

// busy reports whether any dependency operation —
// standalone or update-cycle — is in flight.
func (s depsTab) busy() bool {
	if s.phase != depsIdle {
		return true
	}
	p := s.cycle.Phase()
	return p != deps.PhaseIdle && p != deps.PhaseTerminal
}

// SpinnerText returns the noun phrase to render next to the spinner
// while an operation is in-flight, or "" if the caller should fall
// back to its own status text.
func (s depsTab) spinnerText() string {
	switch s.phase {
	case depsChecking:
		return "Checking for dependency updates"
	case depsRestoringBackup:
		return "Restoring dependency backup"
	case depsLoadingBackups:
		return "Loading dependency backups"
	}
	switch s.cycle.Phase() {
	case deps.PhaseChecking:
		return "Checking for dependency updates"
	case deps.PhaseApplying:
		return "Updating dependencies"
	case deps.PhaseCompensating:
		return "Reverting partial update"
	case deps.PhaseRunningChecks:
		return "Running checks"
	case deps.PhaseRollingBack:
		return "Rolling back dependencies"
	}
	return ""
}

// Reset clears the standalone operation phase. It does not touch the
// Cycle; cycle errors are handled by the cycle adapter.
func (s *depsTab) reset() {
	s.phase = depsIdle
}

// Marked reports whether the module at path carries a Mark.
func (s depsTab) marked(path string) bool { return s.marks[path] }

// ToggleMark flips the Mark on path.
func (s *depsTab) flipMark(path string) {
	if s.marks == nil {
		s.marks = map[string]bool{}
	}
	if s.marks[path] {
		delete(s.marks, path)
		return
	}
	s.marks[path] = true
}

// ClearMarks removes every Mark.
func (s *depsTab) clearMarks() { s.marks = nil }

// MarkedPaths returns the marked module paths in dependency-list
// order. Marks on modules that are no longer listed are ignored.
func (s depsTab) markedPaths() []string {
	if len(s.marks) == 0 {
		return nil
	}
	paths := make([]string, 0, len(s.marks))
	for _, d := range s.dependencies {
		if s.marks[d.Path] {
			paths = append(paths, d.Path)
		}
	}
	return paths
}

// ToggleMarkAll clears every mark when any exists, otherwise marks
// every listed row (the rows the current display mode shows). It
// reports whether marks were added. Whether a marked module actually
// moves is decided by the update plan.
func (s *depsTab) flipAllMarks() bool {
	if len(s.markedPaths()) > 0 || len(s.rowPaths) == 0 {
		s.clearMarks()
		return false
	}
	s.marks = make(map[string]bool, len(s.rowPaths))
	for _, p := range s.rowPaths {
		s.marks[p] = true
	}
	return true
}

// cursorDependency resolves the table cursor to its module, mapping
// through RowPaths because hidden indirect rows shift indices.
func (s depsTab) cursorDependency() (deps.ModuleDependency, bool) {
	i := s.table.Cursor()
	if i < 0 || i >= len(s.rowPaths) {
		return deps.ModuleDependency{}, false
	}
	path := s.rowPaths[i]
	for _, d := range s.dependencies {
		if d.Path == path {
			return d, true
		}
	}
	return deps.ModuleDependency{}, false
}

// explicitModules returns the module set behind the explicit Update
// scope: the marked modules, or the module under the cursor when
// nothing is marked. nil when neither exists.
func (s depsTab) explicitModules() []string {
	if paths := s.markedPaths(); len(paths) > 0 {
		return paths
	}
	if d, found := s.cursorDependency(); found {
		return []string{d.Path}
	}
	return nil
}

// updateSelection builds the selection `u` starts the cycle with. The
// initial Update scope is "marked" when marks exist and "all direct
// dependencies" otherwise; the dialog lets the user switch between
// the two. ok is false when the list is empty.
func (s depsTab) updateSelection() (sel deps.UpdateSelection, ok bool) {
	if paths := s.markedPaths(); len(paths) > 0 {
		return deps.UpdateSelection{Modules: paths}, true
	}
	if len(s.rowPaths) == 0 {
		return deps.UpdateSelection{}, false
	}
	return deps.UpdateSelection{}, true
}
