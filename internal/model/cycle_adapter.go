package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
)

type dependencyExecutionErrMsg struct {
	Err error
}

// handleCycleEvent feeds a deps.Event (received as a tea.Msg from an
// operational tea.Cmd) into the Cycle and dispatches the resulting
// intent. Operational intents become a tea.Cmd that runs the executor;
// confirmation intents open the matching dialog; a terminal outcome
// (NoIntent) is rendered as a status message.
func (s *depsTab) handleCycleEvent(event deps.Event) (tea.Cmd, depsStatus) {
	next, intent, err := s.cycle.Handle(event)
	if err != nil {
		s.cycle = deps.NewUpdateCycle()
		return nil, depsGlobalStatus(err.Error(), "error")
	}
	s.cycle = next
	return s.applyCycleIntent(intent)
}

// applyCycleIntent maps a Cycle intent to its TUI side effect.
func (s *depsTab) applyCycleIntent(intent deps.Intent) (tea.Cmd, depsStatus) {
	switch i := intent.(type) {
	case deps.NoIntent:
		return s.applyCycleTerminal()
	case deps.IntentCheckUpdates, deps.IntentApplyUpdates,
		deps.IntentCompensate, deps.IntentRunChecks, deps.IntentRollback:
		return s.applyCycleOperational(i)
	case deps.IntentConfirmApply:
		choice := i.DefaultYes
		// The explicit set (marks, else the cursor module) is frozen
		// for the whole cycle: keys that change marks are inert while
		// the cycle runs, so reading it here is stable.
		explicitModules := s.explicitModules()
		if s.dialog.kind == dialogUpdate {
			// A level or scope change re-emits the intent: keep the
			// button the user had highlighted and the captured set.
			choice = s.dialog.choiceYes
			explicitModules = s.dialog.explicitModules
		}
		s.dialog = depsDialog{
			kind:            dialogUpdate,
			choiceYes:       choice,
			updateEntries:   i.Entries,
			level:           i.Level,
			explicit:        i.Explicit,
			explicitModules: explicitModules,
		}
		return nil, depsClearStatus
	case deps.IntentConfirmChecks:
		s.syncDepsFromCycle()
		n := i.UpdatedCount
		s.dialog = depsDialog{kind: dialogChecks, choiceYes: i.DefaultYes}
		return nil, depsGlobalStatus(fmt.Sprintf(
			"Updated %d direct %s. Run checks?",
			n, deps.Pluralize(n, "dependency", "dependencies")), "success")
	case deps.IntentConfirmRollback:
		s.dialog = depsDialog{
			kind:         dialogRollback,
			choiceYes:    i.DefaultYes,
			inconclusive: i.CheckErr != nil,
			checkResult:  i.CheckResult,
		}
		if i.CheckErr != nil {
			return nil, depsGlobalStatus(fmt.Sprintf("Checks could not run: %s", i.CheckErr), "error")
		}
		if i.CheckResult != nil {
			return nil, depsGlobalStatus(fmt.Sprintf("Checks failed: %s", i.CheckResult.Command), "error")
		}
		return nil, depsStatus{}
	}
	s.cycle = deps.NewUpdateCycle()
	return nil, depsGlobalStatus(fmt.Sprintf("Unhandled dependency cycle intent %T", intent), "error")
}

// applyCycleOperational sets the progress status and returns the tea.Cmd
// that runs the operational intent through the execution seam.
func (s *depsTab) applyCycleOperational(intent deps.Intent) (tea.Cmd, depsStatus) {
	status := depsStatus{}
	switch intent.(type) {
	case deps.IntentApplyUpdates:
		status = depsGlobalStatus("Updating dependencies...", "info")
	case deps.IntentCompensate:
		status = depsGlobalStatus("Reverting partial update...", "info")
	case deps.IntentRunChecks:
		status = depsGlobalStatus("Running checks...", "info")
	case deps.IntentRollback:
		status = depsGlobalStatus("Rolling back dependencies...", "info")
	}
	return s.cycleExecuteCmd(intent), status
}

// applyCycleTerminal renders the terminal outcome as a status message,
// syncs the dependency table, and resets the Cycle to idle.
func (s *depsTab) applyCycleTerminal() (tea.Cmd, depsStatus) {
	c := s.cycle
	// Marks describe the next update; once a cycle has run its course
	// they are spent. Cancelling in the apply dialog keeps them so the
	// user can adjust and retry.
	if c.Outcome() != deps.OutcomeApplyCanceled {
		s.clearMarks()
		s.updateDependencyTable()
	}
	if c.Outcome() != deps.OutcomeRecoveryRequired && c.Outcome() != deps.OutcomeFailed {
		s.syncDepsFromCycle()
	}
	var status depsStatus
	switch c.Outcome() {
	case deps.OutcomeNoUpdates:
		status = depsTabStatus(noUpdatesMessage(c.Selection()), "warning")
	case deps.OutcomeApplyCanceled:
		status = depsTabStatus("Update canceled.", "info")
	case deps.OutcomeUpdatedUnchecked:
		status = depsGlobalStatus("Update complete. Checks skipped.", "info")
	case deps.OutcomeUpdatedVerified:
		status = depsGlobalStatus("Checks passed.", "success")
	case deps.OutcomeUpdatesKeptWithFailedChecks:
		status = depsGlobalStatus("Update kept. Failed checks were not rolled back.", "warning")
	case deps.OutcomeRolledBack:
		status = depsGlobalStatus("Rolled back to pre-update state.", "success")
	case deps.OutcomeUpdateFailedRestored:
		status = depsGlobalStatus(cycleUpdateFailedRestoredMessage(c), "warning")
	case deps.OutcomeRecoveryRequired:
		status = depsGlobalStatus(cycleRecoveryRequiredMessage(c), "error")
	case deps.OutcomeFailed:
		if c.Failure() != nil {
			status = depsGlobalStatus(c.Failure().Error(), "error")
		} else {
			status = depsGlobalStatus("Update failed.", "error")
		}
	}
	s.cycle = deps.NewUpdateCycle()
	return nil, status
}

// noUpdatesMessage renders the no-updates outcome: bulk selections
// report on direct dependencies, explicit ones name the modules.
func noUpdatesMessage(sel deps.UpdateSelection) string {
	suffix := ""
	if sel.Level != deps.LevelLatest {
		suffix = fmt.Sprintf(" at the %s level", sel.Level)
	}
	if !sel.Explicit() {
		return fmt.Sprintf("No direct dependency updates available%s.", suffix)
	}
	const maxNamed = 3
	names := sel.Modules
	extra := ""
	if len(names) > maxNamed {
		extra = fmt.Sprintf(" …and %d more", len(names)-maxNamed)
		names = names[:maxNamed]
	}
	return fmt.Sprintf("Already up to date%s: %s%s", suffix, strings.Join(names, ", "), extra)
}

func cycleUpdateFailedRestoredMessage(c deps.UpdateCycle) string {
	if c.Failure() != nil {
		return fmt.Sprintf("Update failed and was reverted: %s", c.Failure())
	}
	return "Update failed and was reverted."
}

func cycleRecoveryRequiredMessage(c deps.UpdateCycle) string {
	msg := "Recovery required."
	if c.Failure() != nil {
		msg = fmt.Sprintf("Recovery required: %s", c.Failure())
	}
	if b := c.Backup(); b != nil && b.Name != "" {
		if b.Path != "" {
			msg = fmt.Sprintf("%s A backup was saved: %s (%s)", msg, b.Name, b.Path)
		} else {
			msg = fmt.Sprintf("%s A backup was saved: %s", msg, b.Name)
		}
	}
	return msg
}

// cycleExecuteCmd builds the tea.Cmd that runs an operational intent
// through the dependency executor.
func (s depsTab) cycleExecuteCmd(intent deps.Intent) tea.Cmd {
	executor := s.executor()
	return func() tea.Msg {
		event, err := executor.Execute(intent)
		if err != nil {
			return dependencyExecutionErrMsg{Err: err}
		}
		return event
	}
}

// syncDepsFromCycle copies the Cycle's current dependency list into the
// presentation table when the Cycle has one (post-check, post-apply,
// post-rollback). It is a no-op when the Cycle has no dependencies.
func (s *depsTab) syncDepsFromCycle() {
	if d := s.cycle.Dependencies(); d != nil {
		s.replaceDependencies(d)
	}
}
