// Package lifecycle provides transactional activation and deletion of installed
// Go toolchains without depending on a user-interface framework.
package lifecycle

import (
	"errors"
	"fmt"

	"github.com/SmileOniks/govm/internal/state"
)

// Phase identifies the durable step at which an operation failed.
type Phase string

const (
	PhaseValidate               Phase = "validate"
	PhasePrepare                Phase = "prepare"
	PhasePrepared               Phase = "prepared"
	PhaseLiveBackedUp           Phase = "live-backed-up"
	PhaseLiveInstalled          Phase = "live-installed"
	PhaseActiveRecordCommitting Phase = "active-record-committing"
	PhaseActiveRecordCommitted  Phase = "active-record-committed"
	PhaseQuarantined            Phase = "quarantined"
)

// Error reports a lifecycle failure before its operation committed.
type Error struct {
	Operation state.Operation
	Phase     Phase
	Err       error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s Go version during %s: %v", e.Operation, e.Phase, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// NotInstalledError reports that the canonical installed-version directory is
// absent.
type NotInstalledError struct {
	Version string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("Go version %s is not installed", e.Version)
}

// ActiveVersionError reports an attempt to delete the active toolchain.
type ActiveVersionError struct {
	Version string
}

func (e *ActiveVersionError) Error() string {
	return fmt.Sprintf("cannot delete active Go version %s", e.Version)
}

// Warning is a non-fatal post-commit or recovery condition.
type Warning interface {
	error
	lifecycleWarning()
}

// CleanupWarning reports that an operation committed but an obsolete artifact
// could not be removed. The durable marker is preserved for later recovery.
type CleanupWarning struct {
	Operation state.Operation
	Path      string
	Err       error
}

func (w *CleanupWarning) Error() string {
	return fmt.Sprintf("%s committed but cleanup of %q failed: %v", w.Operation, w.Path, w.Err)
}

func (w *CleanupWarning) Unwrap() error     { return w.Err }
func (w *CleanupWarning) lifecycleWarning() {}

// RecoveryWarning wraps a non-fatal warning produced while the shared
// coordinator recovered a transaction left by an earlier process.
type RecoveryWarning struct {
	Warning state.Warning
}

func (w *RecoveryWarning) Error() string     { return "lifecycle recovery warning: " + w.Warning.Error() }
func (w *RecoveryWarning) Unwrap() error     { return w.Warning.Err }
func (w *RecoveryWarning) lifecycleWarning() {}

// ActivationResult describes the newly active toolchain and complete shim set.
type ActivationResult struct {
	Version  string
	ShimDir  string
	Shims    []string
	Warnings []Warning
}

// DeletionResult describes a committed deletion.
type DeletionResult struct {
	Version  string
	Warnings []Warning
}

func operationError(operation state.Operation, phase Phase, err error) error {
	if err == nil {
		return nil
	}
	var lifecycleErr *Error
	if errors.As(err, &lifecycleErr) {
		return err
	}
	return &Error{Operation: operation, Phase: phase, Err: err}
}
