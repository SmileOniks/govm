package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/deps"
)

// This file is the Bubbletea adapter for the standalone (non-cycle)
// dependency commands: manual refresh, lazy load, backups listing, and
// restore. It is the only place in the model that knows about tea.Cmd
// for these operations; the underlying work is the dependency
// executor, which is tea-free. The update workflow (check -> apply -> checks ->
// rollback) is driven by deps.UpdateCycle through cycle_adapter.go and
// has no entry here.

// dependenciesMsg carries the list of module dependencies. It is the
// result of both listDependenciesCmd (lazy load) and
// checkDependencyUpdatesCmd (manual refresh).
type dependenciesMsg []deps.ModuleDependency

// dependencyBackupsMsg carries the saved dependency backups for the
// current module.
type dependencyBackupsMsg []deps.DependencyBackupInfo

// dependenciesRestoredMsg is sent after restoring a dependency backup.
type dependenciesRestoredMsg deps.DependencyRestoreResult

// dependencyErrMsg carries a dependency-related error without affecting
// the main error state. It is intentionally a plain struct (not an
// error) so that it does not satisfy the error interface and therefore
// does not collide with ErrMsg in type switches.
type dependencyErrMsg struct {
	Err error
}

// listDependenciesCmd lists current module dependencies without
// checking for updates online. Used for the lazy load on first visit
// to the Deps tab.
func listDependenciesCmd(executor deps.API) tea.Cmd {
	return dependencyCmd(func() (dependenciesMsg, error) {
		dependencies, err := executor.List()
		return dependenciesMsg(dependencies), err
	})
}

// checkDependencyUpdatesCmd lists module dependencies and checks
// for available updates online. Used for the manual refresh action.
func checkDependencyUpdatesCmd(executor deps.API) tea.Cmd {
	return dependencyCmd(func() (dependenciesMsg, error) {
		dependencies, err := executor.CheckUpdates()
		return dependenciesMsg(dependencies), err
	})
}

// listDependencyBackupsCmd lists saved dependency backups for the
// current module, newest first.
func listDependencyBackupsCmd(executor deps.API) tea.Cmd {
	return dependencyCmd(func() (dependencyBackupsMsg, error) {
		backups, err := executor.Backups()
		return dependencyBackupsMsg(backups), err
	})
}

// restoreDependencyBackupCmd restores a saved dependency backup by
// filename, saving the current files first as a pre-restore backup.
func restoreDependencyBackupCmd(executor deps.API, backupName string) tea.Cmd {
	return dependencyCmd(func() (dependenciesRestoredMsg, error) {
		result, err := executor.Restore(backupName)
		return dependenciesRestoredMsg(result), err
	})
}

// dependencyCmd adapts a synchronous internal/deps call into a tea.Cmd.
// The returned tea.Msg is the typed result on success, or
// dependencyErrMsg wrapping the error on failure.
func dependencyCmd[T any](run func() (T, error)) tea.Cmd {
	return func() tea.Msg {
		result, err := run()
		if err != nil {
			return dependencyErrMsg{Err: err}
		}
		return result
	}
}
