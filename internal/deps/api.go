package deps

// API is the seam through which the adapters (the TUI's Deps tab and
// the CLI's deps commands) perform every side-effecting dependency
// operation. *Executor satisfies it in production; the adapters'
// tests substitute a fake. One interface lives here, next to the
// implementation, so the two adapters cannot drift apart again.
type API interface {
	// Execute runs an operational intent of the UpdateCycle and
	// returns the corresponding event.
	Execute(intent Intent) (Event, error)
	// List returns the module dependencies without going online.
	List() ([]ModuleDependency, error)
	// CheckUpdates returns the module dependencies with their
	// available updates and known versions.
	CheckUpdates() ([]ModuleDependency, error)
	// Backups lists the saved backups of the module, newest first.
	Backups() ([]DependencyBackupInfo, error)
	// Restore replaces go.mod and go.sum with the named backup,
	// saving a pre-restore backup first.
	Restore(backupName string) (DependencyRestoreResult, error)
}
