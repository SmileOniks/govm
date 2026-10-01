// Package deps implements the Dependency Update Cycle: a pure state
// machine (UpdateCycle) driving check -> confirm -> apply -> checks ->
// rollback, plus an Executor that runs the operational intents the
// cycle emits.
//
// UpdateCycle is immutable and free of IO/UI dependencies. Invalid
// events return InvalidTransitionError and leave state unchanged. All
// slices and snapshots stored in or returned from the cycle, including
// nested ModuleDependency.Versions slices, are defensively cloned so
// callers cannot mutate internal state.
package deps

import (
	"errors"
	"fmt"
)

// Phases and Outcomes.

type Phase int

const (
	PhaseIdle Phase = iota
	PhaseChecking
	PhaseConfirmApply
	PhaseApplying
	PhaseCompensating
	PhaseConfirmChecks
	PhaseRunningChecks
	PhaseConfirmRollback
	PhaseRollingBack
	PhaseTerminal
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseChecking:
		return "checking"
	case PhaseConfirmApply:
		return "confirm-apply"
	case PhaseApplying:
		return "applying"
	case PhaseCompensating:
		return "compensating"
	case PhaseConfirmChecks:
		return "confirm-checks"
	case PhaseRunningChecks:
		return "running-checks"
	case PhaseConfirmRollback:
		return "confirm-rollback"
	case PhaseRollingBack:
		return "rolling-back"
	case PhaseTerminal:
		return "terminal"
	default:
		return fmt.Sprintf("phase(%d)", int(p))
	}
}

type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeNoUpdates
	OutcomeApplyCanceled
	OutcomeUpdatedUnchecked
	OutcomeUpdatedVerified
	OutcomeUpdatesKeptWithFailedChecks
	OutcomeRolledBack
	OutcomeUpdateFailedRestored
	OutcomeRecoveryRequired
	OutcomeFailed
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNone:
		return "none"
	case OutcomeNoUpdates:
		return "no-updates"
	case OutcomeApplyCanceled:
		return "apply-canceled"
	case OutcomeUpdatedUnchecked:
		return "updated-unchecked"
	case OutcomeUpdatedVerified:
		return "updated-verified"
	case OutcomeUpdatesKeptWithFailedChecks:
		return "updates-kept-with-failed-checks"
	case OutcomeRolledBack:
		return "rolled-back"
	case OutcomeUpdateFailedRestored:
		return "update-failed-restored"
	case OutcomeRecoveryRequired:
		return "recovery-required"
	case OutcomeFailed:
		return "failed"
	default:
		return fmt.Sprintf("outcome(%d)", int(o))
	}
}

// Sentinel errors for apply/rollback contract violations.

// ErrMissingApplySnapshot: apply reported success but returned no
// rollback snapshot, voiding the rollback guarantee.
var ErrMissingApplySnapshot = errors.New("deps: apply succeeded without a rollback snapshot")

// ErrMissingApplyBackup: apply reported success but saved no persistent
// backup, violating the always-retain policy.
var ErrMissingApplyBackup = errors.New("deps: apply succeeded without a persistent backup")

// ErrMissingRollbackSnapshot: rollback requested but no in-memory
// snapshot is available.
var ErrMissingRollbackSnapshot = errors.New("deps: rollback requested but snapshot is missing")

// Events (sealed interface). Each event represents one step outcome or
// a user decision fed back into the cycle.

type Event interface {
	isEvent()
	eventName() string
}

// StartEvent begins a cycle. Selection decides which modules the plan
// covers and at which level; the zero value means "all direct
// dependencies to Latest".
type StartEvent struct {
	ModuleDir string
	Selection UpdateSelection
}

func (StartEvent) isEvent()          {}
func (StartEvent) eventName() string { return "start" }

type CheckUpdatesDoneEvent struct {
	Dependencies []ModuleDependency
	Err          error
}

func (CheckUpdatesDoneEvent) isEvent()          {}
func (CheckUpdatesDoneEvent) eventName() string { return "check-updates-done" }

type ConfirmApplyEvent struct {
	Yes bool
}

func (ConfirmApplyEvent) isEvent()          {}
func (ConfirmApplyEvent) eventName() string { return "confirm-apply" }

// ChangeLevelEvent switches the update level while the apply
// confirmation is pending. The plan is rebuilt from the dependencies
// already checked and IntentConfirmApply is emitted again, possibly
// with no entries.
type ChangeLevelEvent struct {
	Level UpdateLevel
}

func (ChangeLevelEvent) isEvent()          {}
func (ChangeLevelEvent) eventName() string { return "change-level" }

// ChangeScopeEvent switches the update scope while the apply
// confirmation is pending: Modules names the explicit set to cover,
// nil means every direct dependency. The level is kept and the plan
// is rebuilt from the dependencies already checked.
type ChangeScopeEvent struct {
	Modules []string
}

func (ChangeScopeEvent) isEvent()          {}
func (ChangeScopeEvent) eventName() string { return "change-scope" }

type ApplyUpdatesDoneEvent struct {
	Snapshot     *DependencySnapshot
	Backup       *DependencyBackupInfo
	Dependencies []ModuleDependency
	Err          error
}

func (ApplyUpdatesDoneEvent) isEvent()          {}
func (ApplyUpdatesDoneEvent) eventName() string { return "apply-updates-done" }

type CompensateDoneEvent struct {
	Dependencies []ModuleDependency
	Err          error
}

func (CompensateDoneEvent) isEvent()          {}
func (CompensateDoneEvent) eventName() string { return "compensate-done" }

type ConfirmChecksEvent struct {
	Yes bool
}

func (ConfirmChecksEvent) isEvent()          {}
func (ConfirmChecksEvent) eventName() string { return "confirm-checks" }

// ChecksDoneEvent carries the result of the check commands. When Err
// is non-nil the checks were inconclusive (could not run). When
// Result.OK is false a check command failed. Both lead to ConfirmRollback.
type ChecksDoneEvent struct {
	Result DependencyCheckResult
	Err    error
}

func (ChecksDoneEvent) isEvent()          {}
func (ChecksDoneEvent) eventName() string { return "checks-done" }

type ConfirmRollbackEvent struct {
	Yes bool
}

func (ConfirmRollbackEvent) isEvent()          {}
func (ConfirmRollbackEvent) eventName() string { return "confirm-rollback" }

type RollbackDoneEvent struct {
	Dependencies []ModuleDependency
	Err          error
}

func (RollbackDoneEvent) isEvent()          {}
func (RollbackDoneEvent) eventName() string { return "rollback-done" }

// Intents (sealed interface). Operational intents are executed by the
// Executor; confirmation intents are handled by the consumer/UI. All
// confirmation intents default to Yes.

type Intent interface {
	isIntent()
	intentName() string
}

type NoIntent struct{}

func (NoIntent) isIntent()          {}
func (NoIntent) intentName() string { return "none" }

type IntentCheckUpdates struct {
}

func (IntentCheckUpdates) isIntent()          {}
func (IntentCheckUpdates) intentName() string { return "check-updates" }

// IntentConfirmApply asks the consumer to confirm applying updates.
// DefaultYes is always true. Entries is a defensive copy. Level is
// the update level the entries were built for; Explicit reports
// whether the selection named specific modules.
type IntentConfirmApply struct {
	Entries    []DependencyUpdateEntry
	DefaultYes bool
	Level      UpdateLevel
	Explicit   bool
}

func (IntentConfirmApply) isIntent()          {}
func (IntentConfirmApply) intentName() string { return "confirm-apply" }

type IntentApplyUpdates struct {
	Entries []DependencyUpdateEntry
}

func (IntentApplyUpdates) isIntent()          {}
func (IntentApplyUpdates) intentName() string { return "apply-updates" }

type IntentCompensate struct {
	Snapshot *DependencySnapshot
}

func (IntentCompensate) isIntent()          {}
func (IntentCompensate) intentName() string { return "compensate" }

// IntentConfirmChecks asks the consumer whether to run checks after a
// successful apply and reports how many direct dependencies changed.
// DefaultYes is always true.
type IntentConfirmChecks struct {
	UpdatedCount int
	DefaultYes   bool
}

func (IntentConfirmChecks) isIntent()          {}
func (IntentConfirmChecks) intentName() string { return "confirm-checks" }

type IntentRunChecks struct {
}

func (IntentRunChecks) isIntent()          {}
func (IntentRunChecks) intentName() string { return "run-checks" }

// IntentConfirmRollback asks the consumer whether to roll back after
// failed or inconclusive checks. DefaultYes is always true.
// CheckErr distinguishes inconclusive checks (non-nil) from check
// command failures (nil, CheckResult.OK false).
type IntentConfirmRollback struct {
	CheckResult *DependencyCheckResult
	CheckErr    error
	DefaultYes  bool
}

func (IntentConfirmRollback) isIntent()          {}
func (IntentConfirmRollback) intentName() string { return "confirm-rollback" }

type IntentRollback struct {
	Snapshot *DependencySnapshot
}

func (IntentRollback) isIntent()          {}
func (IntentRollback) intentName() string { return "rollback" }

func IsOperational(intent Intent) bool {
	switch intent.(type) {
	case IntentCheckUpdates, IntentApplyUpdates, IntentCompensate,
		IntentRunChecks, IntentRollback:
		return true
	default:
		return false
	}
}

// Errors.

type InvalidTransitionError struct {
	Phase Phase
	Event string
}

func (e InvalidTransitionError) Error() string {
	return fmt.Sprintf("deps: invalid transition: event %q is not valid in phase %s", e.Event, e.Phase)
}

// UpdateCycle is the immutable pure value driving the Dependency Update
// Cycle. Handle returns a new value; the receiver is never mutated.
type UpdateCycle struct {
	phase        Phase
	outcome      Outcome
	failure      error
	selection    UpdateSelection
	dependencies []ModuleDependency
	entries      []DependencyUpdateEntry
	snapshot     *DependencySnapshot
	backup       *DependencyBackupInfo
	checkResult  *DependencyCheckResult
}

func NewUpdateCycle() UpdateCycle {
	return UpdateCycle{phase: PhaseIdle}
}

func (c UpdateCycle) Phase() Phase { return c.phase }

func (c UpdateCycle) Outcome() Outcome { return c.outcome }

func (c UpdateCycle) IsTerminal() bool { return c.phase == PhaseTerminal }

// Failure returns the operational error preserved for terminal failure
// outcomes (OutcomeFailed, OutcomeUpdateFailedRestored,
// OutcomeRecoveryRequired), or nil otherwise.
func (c UpdateCycle) Failure() error { return c.failure }

// Selection returns a defensive copy of the selection the cycle was
// started with (level updated by ChangeLevelEvent, modules by
// ChangeScopeEvent).
func (c UpdateCycle) Selection() UpdateSelection {
	return cloneSelection(c.selection)
}

func (c UpdateCycle) Dependencies() []ModuleDependency {
	return cloneDeps(c.dependencies)
}

func (c UpdateCycle) Entries() []DependencyUpdateEntry {
	return cloneEntries(c.entries)
}

// Snapshot returns a defensive copy of the in-memory pre-update
// snapshot, or nil when not set. Cleared on terminal transitions.
func (c UpdateCycle) Snapshot() *DependencySnapshot {
	return cloneSnapshot(c.snapshot)
}

// Backup returns a defensive copy of the persistent backup metadata,
// or nil when not set. Retained across terminal transitions.
func (c UpdateCycle) Backup() *DependencyBackupInfo {
	return cloneBackupPtr(c.backup)
}

// CheckResult returns a defensive copy of the last check result, or nil
// when not set. Cleared on terminal transitions.
func (c UpdateCycle) CheckResult() *DependencyCheckResult {
	if c.checkResult == nil {
		return nil
	}
	cr := *c.checkResult
	return &cr
}

// Handle advances the cycle by one event, returning the next state,
// the intent for the consumer/executor, and an error. On an invalid
// event the returned cycle equals the receiver and the error is an
// InvalidTransitionError.
func (c UpdateCycle) Handle(event Event) (UpdateCycle, Intent, error) {
	switch e := event.(type) {
	case StartEvent:
		return c.handleStart(e)
	case CheckUpdatesDoneEvent:
		return c.handleCheckUpdatesDone(e)
	case ConfirmApplyEvent:
		return c.handleConfirmApply(e)
	case ChangeLevelEvent:
		return c.handleChangeLevel(e)
	case ChangeScopeEvent:
		return c.handleChangeScope(e)
	case ApplyUpdatesDoneEvent:
		return c.handleApplyUpdatesDone(e)
	case CompensateDoneEvent:
		return c.handleCompensateDone(e)
	case ConfirmChecksEvent:
		return c.handleConfirmChecks(e)
	case ChecksDoneEvent:
		return c.handleChecksDone(e)
	case ConfirmRollbackEvent:
		return c.handleConfirmRollback(e)
	case RollbackDoneEvent:
		return c.handleRollbackDone(e)
	default:
		name := "<nil>"
		if event != nil {
			name = event.eventName()
		}
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: name}
	}
}

func (c UpdateCycle) handleStart(e StartEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseIdle {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "start"}
	}
	next := c
	next.phase = PhaseChecking
	next.selection = cloneSelection(e.Selection)
	return next, IntentCheckUpdates{}, nil
}

func (c UpdateCycle) handleCheckUpdatesDone(e CheckUpdatesDoneEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseChecking {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "check-updates-done"}
	}
	if e.Err != nil {
		next := c.terminal(OutcomeFailed)
		next.failure = e.Err
		return next, NoIntent{}, nil
	}
	next := c
	next.dependencies = cloneDeps(e.Dependencies)
	entries, err := BuildUpdatePlan(e.Dependencies, c.selection)
	if err != nil {
		next = next.terminal(OutcomeFailed)
		next.failure = err
		return next, NoIntent{}, nil
	}
	next.entries = cloneEntries(entries)
	if len(entries) == 0 {
		return next.terminal(OutcomeNoUpdates), NoIntent{}, nil
	}
	next.phase = PhaseConfirmApply
	return next, next.confirmApplyIntent(), nil
}

func (c UpdateCycle) confirmApplyIntent() IntentConfirmApply {
	return IntentConfirmApply{
		Entries:    cloneEntries(c.entries),
		DefaultYes: true,
		Level:      c.selection.Level,
		Explicit:   c.selection.Explicit(),
	}
}

// handleChangeLevel rebuilds the plan at a new level while the apply
// confirmation is pending. The dependency list was already checked,
// so no IO is needed. The plan may become empty; confirming an empty
// plan ends the cycle with OutcomeNoUpdates.
func (c UpdateCycle) handleChangeLevel(e ChangeLevelEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseConfirmApply {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "change-level"}
	}
	sel := cloneSelection(c.selection)
	sel.Level = e.Level
	return c.rebuildPlan(sel)
}

// handleChangeScope rebuilds the plan for a different module set while
// the apply confirmation is pending, keeping the level.
func (c UpdateCycle) handleChangeScope(e ChangeScopeEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseConfirmApply {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "change-scope"}
	}
	sel := cloneSelection(c.selection)
	sel.Modules = nil
	if len(e.Modules) > 0 {
		sel.Modules = make([]string, len(e.Modules))
		copy(sel.Modules, e.Modules)
	}
	return c.rebuildPlan(sel)
}

// rebuildPlan replaces the selection and rebuilds the plan from the
// checked dependencies, re-emitting IntentConfirmApply.
func (c UpdateCycle) rebuildPlan(sel UpdateSelection) (UpdateCycle, Intent, error) {
	next := c
	next.selection = sel
	entries, err := BuildUpdatePlan(c.dependencies, sel)
	if err != nil {
		next = next.terminal(OutcomeFailed)
		next.failure = err
		return next, NoIntent{}, nil
	}
	next.entries = cloneEntries(entries)
	return next, next.confirmApplyIntent(), nil
}

func (c UpdateCycle) handleConfirmApply(e ConfirmApplyEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseConfirmApply {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "confirm-apply"}
	}
	if !e.Yes {
		return c.terminal(OutcomeApplyCanceled), NoIntent{}, nil
	}
	if len(c.entries) == 0 {
		return c.terminal(OutcomeNoUpdates), NoIntent{}, nil
	}
	next := c
	next.phase = PhaseApplying
	return next, IntentApplyUpdates{
		Entries: cloneEntries(c.entries),
	}, nil
}

func (c UpdateCycle) handleApplyUpdatesDone(e ApplyUpdatesDoneEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseApplying {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "apply-updates-done"}
	}
	if e.Err != nil {
		// Err + nil snapshot => failure before mutation; no compensation.
		if e.Snapshot == nil {
			next := c.terminal(OutcomeFailed)
			next.failure = e.Err
			return next, NoIntent{}, nil
		}
		// Err + non-nil snapshot => mutation may have started; compensate.
		next := c
		next.phase = PhaseCompensating
		next.failure = e.Err
		next.snapshot = cloneSnapshot(e.Snapshot)
		next.backup = cloneBackupPtr(e.Backup)
		return next, IntentCompensate{
			Snapshot: cloneSnapshot(e.Snapshot),
		}, nil
	}
	// Success without snapshot: rollback guarantee absent.
	if e.Snapshot == nil {
		next := c.terminal(OutcomeFailed)
		next.failure = ErrMissingApplySnapshot
		return next, NoIntent{}, nil
	}
	// Success without persistent backup: always-retain policy violated.
	if e.Backup == nil {
		next := c.terminal(OutcomeFailed)
		next.failure = ErrMissingApplyBackup
		return next, NoIntent{}, nil
	}
	next := c
	next.phase = PhaseConfirmChecks
	next.dependencies = cloneDeps(e.Dependencies)
	next.snapshot = cloneSnapshot(e.Snapshot)
	next.backup = cloneBackupPtr(e.Backup)
	return next, IntentConfirmChecks{
		UpdatedCount: len(next.entries),
		DefaultYes:   true,
	}, nil
}

func (c UpdateCycle) handleCompensateDone(e CompensateDoneEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseCompensating {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "compensate-done"}
	}
	if e.Err != nil {
		// Compensation failed: store compensation error (overrides the
		// original apply error since it is the actionable failure now).
		next := c.terminal(OutcomeRecoveryRequired)
		next.failure = e.Err
		return next, NoIntent{}, nil
	}
	// Compensation succeeded: failure retains the original apply error.
	next := c.terminal(OutcomeUpdateFailedRestored)
	next.dependencies = cloneDeps(e.Dependencies)
	return next, NoIntent{}, nil
}

func (c UpdateCycle) handleConfirmChecks(e ConfirmChecksEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseConfirmChecks {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "confirm-checks"}
	}
	if !e.Yes {
		return c.terminal(OutcomeUpdatedUnchecked), NoIntent{}, nil
	}
	next := c
	next.phase = PhaseRunningChecks
	return next, IntentRunChecks{}, nil
}

func (c UpdateCycle) handleChecksDone(e ChecksDoneEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseRunningChecks {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "checks-done"}
	}
	if e.Err == nil && e.Result.OK {
		return c.terminal(OutcomeUpdatedVerified), NoIntent{}, nil
	}
	// Failed or inconclusive checks both lead to ConfirmRollback.
	result := e.Result
	next := c
	next.phase = PhaseConfirmRollback
	next.checkResult = &result
	crCopy := result
	return next, IntentConfirmRollback{
		CheckResult: &crCopy,
		CheckErr:    e.Err,
		DefaultYes:  true,
	}, nil
}

func (c UpdateCycle) handleConfirmRollback(e ConfirmRollbackEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseConfirmRollback {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "confirm-rollback"}
	}
	if !e.Yes {
		return c.terminal(OutcomeUpdatesKeptWithFailedChecks), NoIntent{}, nil
	}
	if c.snapshot == nil {
		next := c.terminal(OutcomeRecoveryRequired)
		next.failure = ErrMissingRollbackSnapshot
		return next, NoIntent{}, nil
	}
	next := c
	next.phase = PhaseRollingBack
	return next, IntentRollback{
		Snapshot: cloneSnapshot(c.snapshot),
	}, nil
}

func (c UpdateCycle) handleRollbackDone(e RollbackDoneEvent) (UpdateCycle, Intent, error) {
	if c.phase != PhaseRollingBack {
		return c, NoIntent{}, InvalidTransitionError{Phase: c.phase, Event: "rollback-done"}
	}
	if e.Err != nil {
		next := c.terminal(OutcomeRecoveryRequired)
		next.failure = e.Err
		return next, NoIntent{}, nil
	}
	next := c.terminal(OutcomeRolledBack)
	next.dependencies = cloneDeps(e.Dependencies)
	return next, NoIntent{}, nil
}

// terminal returns a copy in PhaseTerminal with the given outcome.
// In-memory snapshot and check context are cleared; failure, backup
// metadata, dependencies, and outcome are preserved.
func (c UpdateCycle) terminal(o Outcome) UpdateCycle {
	next := c
	next.phase = PhaseTerminal
	next.outcome = o
	next.snapshot = nil
	next.checkResult = nil
	return next
}

// Clone helpers (defensive copies for pure-value isolation).

func cloneDeps(deps []ModuleDependency) []ModuleDependency {
	if deps == nil {
		return nil
	}
	out := make([]ModuleDependency, len(deps))
	copy(out, deps)
	for i := range out {
		if deps[i].Versions != nil {
			out[i].Versions = make([]string, len(deps[i].Versions))
			copy(out[i].Versions, deps[i].Versions)
		}
	}
	return out
}

func cloneEntries(entries []DependencyUpdateEntry) []DependencyUpdateEntry {
	if entries == nil {
		return nil
	}
	out := make([]DependencyUpdateEntry, len(entries))
	copy(out, entries)
	return out
}

func cloneSnapshot(snap *DependencySnapshot) *DependencySnapshot {
	if snap == nil {
		return nil
	}
	out := &DependencySnapshot{
		ModFile: snap.ModFile,
		SumFile: snap.SumFile,
	}
	if snap.Updatable != nil {
		out.Updatable = make([]DependencyUpdateEntry, len(snap.Updatable))
		copy(out.Updatable, snap.Updatable)
	}
	return out
}

func cloneBackupPtr(b *DependencyBackupInfo) *DependencyBackupInfo {
	if b == nil {
		return nil
	}
	copy := *b
	return &copy
}
