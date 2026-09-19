package model

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := (&m).update(msg)
	if next, ok := updated.(*Model); ok {
		return *next, cmd
	}
	return updated, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// A focused Settings input receives non-key messages (cursor
	// blink) here; key presses are dispatched once, by the Input
	// context switch below.
	if m.inputContext() == inputSettingsInput {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			return m.delegateSettings(key)
		}
		cmd, status := m.settings.update(msg)
		effCmd := m.applySettingsStatus(status)
		cmds = append(cmds, cmd, effCmd)
	}

	// The Deps tab's own results reach it whatever the current tab.
	if isDepsMsg(msg) {
		return m.delegateDeps(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// ? opens the Help overlay above every choice mode, so it is
		// claimed before the context's own handler sees the key;
		// canOpenHelp keeps it ordinary input in text-entry contexts.
		if msg.String() == "?" && m.canOpenHelp() {
			m.HelpVisible = true
			return m, nil
		}
		// The Input context decides who handles the key; see
		// input_context.go for the priority order.
		switch m.inputContext() {
		case inputSettingsInput:
			return m.delegateSettings(msg)
		case inputHelpOverlay:
			return m.handleHelpOverlayKey(msg)
		case inputDepsDialog:
			// Quitting stays with the Model; every other key,
			// including tab, belongs to the dialog.
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			}
			return m.delegateDeps(msg)
		case inputPruneConfirm:
			return m.handlePruneDialogKey(msg)
		}
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.TermWidth = msg.Width
		m.TermHeight = msg.Height
		m.Layout = styles.GetLayoutMode(msg.Width)

		frameH, frameV := styles.FrameOverhead(m.Layout)
		contentWidth := msg.Width - frameH
		if contentWidth < 1 {
			contentWidth = 1
		}

		const fixedUIElements = 6
		contentHeight := msg.Height - frameV - fixedUIElements
		if contentHeight < 1 {
			contentHeight = 1
		}

		m.Width = contentWidth
		m.Height = contentHeight
		m.projection.resize(contentWidth, contentHeight)
		m.deps.resize(contentWidth, contentHeight)
		return m, nil

	case catalogLoadedMsg:
		updated, cmd := m.handleCatalogOutcome(m.projection.acceptLoad(msg.RequestID, msg.Versions))
		usage := m.projection.setDiskUsage(m.DiskUsage.VersionBytes)
		return updated, tea.Batch(cmd, usage.cmd)

	case catalogLoadFailedMsg:
		return m.handleCatalogOutcome(m.projection.failLoad(msg.RequestID, msg.Err))

	case distributionSourceValidatedMsg:
		return m.delegateSettings(msg)

	case upgradeCheckStartMsg:
		return m, m.startUpgradeCheck()

	case upgradeCheckedMsg:
		m.handleUpgradeChecked(msg)
		return m, nil

	case diskUsageMsg:
		m.DiskUsage = msg.Summary
		outcome := m.projection.setDiskUsage(msg.Summary.VersionBytes)
		if msg.Err != nil {
			m.Status.SetTab(fmt.Sprintf("Disk usage unavailable: %v", msg.Err), "warning")
		} else if len(msg.Summary.Warnings) > 0 {
			m.Status.SetTab("Disk usage is approximate; some files could not be inspected.", "warning")
		}
		return m, outcome.cmd

	case prunePreviewMsg:
		if !m.Prune.AcceptPreview(msg.Result) {
			if msg.Err != nil {
				m.Status.SetTab(fmt.Sprintf("Prune unavailable: %v", msg.Err), "error")
			} else {
				m.Status.SetTab("Nothing to prune.", "info")
			}
			return m, nil
		}
		if msg.Err != nil {
			m.Status.SetTab(fmt.Sprintf("Prune has warnings: %v", msg.Err), "warning")
		} else {
			m.Status.SetTab("Review the prune plan and press Y to confirm.", "warning")
		}
		return m, nil

	case pruneDoneMsg:
		m.Prune.Finish()
		if msg.Err != nil {
			m.Status.SetGlobal(fmt.Sprintf("Prune completed with warnings: %v", msg.Err), "warning")
		} else {
			m.Status.SetGlobal(
				fmt.Sprintf("Pruned %d object(s), freed %s.", len(msg.Result.Removed), formatDiskUsage(pruneResultBytes(msg.Result))),
				"success",
			)
		}
		outcome := m.projection.startLoad(catalogLoadPurposeRefresh)
		if outcome.kind != catalogProjectionOutcomeLoadStarted {
			return m, m.diskUsageCmd()
		}
		return m, tea.Batch(LoadVersionsCmd(m.loadCatalog, outcome.loadRequest), m.diskUsageCmd())

	case list.FilterMatchesMsg:
		return m, m.projection.updateAvailable(msg)

	case catalogProjectionRefilterMsg:
		return m, m.projection.settleRefilter(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case installProgressMsg:
		return m, m.handleInstallProgress(msg)

	case installProgressPollMsg:
		return m, m.handleInstallProgressPoll(msg)

	case installSuccessMsg:
		return m.handleInstallSuccess(msg)

	case installFailureMsg:
		return m.handleInstallFailure(msg)

	case activationSuccessMsg:
		return m.handleActivationSuccess(msg)

	case deletionSuccessMsg:
		return m.handleDeletionSuccess(msg)

	case lifecycleFailureMsg:
		outcome := m.projection.failMutation(msg.OperationID, msg.Err)
		if outcome.kind == catalogProjectionOutcomeStale {
			return m, nil
		}
		m.Status.SetGlobal(fmt.Sprintf("Failed to %s Go %s: %v", msg.Operation, msg.Version, msg.Err), "error")
		return m, nil
	}

	cmds = append(cmds, m.projection.update(msg))
	depsCmd, depsStatus := m.deps.update(msg)
	m.applyDepsStatus(depsStatus)
	cmds = append(cmds, depsCmd)
	return m, tea.Batch(cmds...)
}

func pruneResultBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Removed {
		total += candidate.Bytes
	}
	return total
}

// pruneCandidateBytes sums the plan awaiting confirmation. The plan
// carries Candidates only; Removed is filled in by the run.
func pruneCandidateBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Candidates {
		total += candidate.Bytes
	}
	return total
}

func (m *Model) handleCatalogOutcome(outcome catalogProjectionOutcome) (tea.Model, tea.Cmd) {
	switch outcome.kind {
	case catalogProjectionOutcomeStale, catalogProjectionOutcomeSuppressed:
		return m, nil
	case catalogProjectionOutcomeLoadStarted:
		m.Status.SetGlobal(m.verifyingStatus(outcome.receipt.operation), "warning")
		return m, LoadVersionsCmd(m.loadCatalog, outcome.loadRequest)
	case catalogProjectionOutcomeReconciled:
		m.applyCompletion(outcome.receipt.operation)
		return m, outcome.cmd
	case catalogProjectionOutcomeRejected:
		if outcome.receipt.operation.id != 0 {
			m.Status.SetGlobal(fmt.Sprintf("Could not verify the operation: %v.", outcome.err), "error")
		} else {
			m.Status.SetGlobal(fmt.Sprintf("Failed to load Go versions: %v.", outcome.err), "error")
		}
		return m, outcome.cmd
	case catalogProjectionOutcomeFailed:
		if outcome.receipt.operation.id != 0 {
			m.Status.SetGlobal("The operation could not be confirmed against the installed catalog.", "error")
		} else {
			text := "catalog load failed"
			if outcome.err != nil {
				text = outcome.err.Error()
			}
			m.Status.SetGlobal(text, "error")
		}
		return m, outcome.cmd
	case catalogProjectionOutcomeCommittedWarning:
		m.applyCommittedProjectionWarning(outcome.receipt.operation, outcome.err)
		return m, outcome.cmd
	default:
		return m, outcome.cmd
	}
}

func (m *Model) handleInstallSuccess(msg installSuccessMsg) (tea.Model, tea.Cmd) {
	outcome := m.projection.completeInstall(
		msg.OperationID,
		msg.Version,
		msg.Path,
		msg.Warnings,
	)
	updated, cmd := m.handleMutationCompletion(outcome)
	return updated, tea.Batch(cmd, m.diskUsageCmd())
}

func (m *Model) handleInstallFailure(msg installFailureMsg) (tea.Model, tea.Cmd) {
	outcome := m.projection.failMutation(msg.OperationID, msg.Err)
	if outcome.kind == catalogProjectionOutcomeStale {
		return m, nil
	}
	m.Status.SetGlobal(fmt.Sprintf("Failed to install Go %s: %v", msg.Version, msg.Err), "error")
	return m, nil
}

func (m *Model) handleActivationSuccess(msg activationSuccessMsg) (tea.Model, tea.Cmd) {
	outcome := m.projection.completeActivation(
		msg.OperationID,
		msg.Result.Version,
		msg.Result.Warnings,
		msg.ShimInPath,
	)
	return m.handleMutationCompletion(outcome)
}

func (m *Model) handleDeletionSuccess(msg deletionSuccessMsg) (tea.Model, tea.Cmd) {
	outcome := m.projection.completeDeletion(
		msg.OperationID,
		msg.Result.Version,
		msg.Result.Warnings,
	)
	return m.handleMutationCompletion(outcome)
}

func (m *Model) handleMutationCompletion(outcome catalogProjectionOutcome) (tea.Model, tea.Cmd) {
	if outcome.kind == catalogProjectionOutcomeStale {
		return m, nil
	}
	if outcome.kind == catalogProjectionOutcomeLoadStarted {
		return m.handleCatalogOutcome(outcome)
	}
	if outcome.kind == catalogProjectionOutcomeCommittedWarning {
		return m.handleCatalogOutcome(outcome)
	}
	if outcome.kind == catalogProjectionOutcomePublished || outcome.kind == catalogProjectionOutcomeNoop {
		m.applyCompletion(outcome.receipt.operation)
	}
	return m, outcome.cmd
}

func (m *Model) applyCompletion(operation catalogOperation) {
	switch operation.kind {
	case catalogMutationInstall:
		text, kind := installSuccessStatus(operation.version, operation.installWarnings)
		m.Status.SetGlobal(text, kind)
	case catalogMutationActivation:
		if len(operation.lifecycleWarnings) > 0 {
			m.Status.SetGlobal(
				fmt.Sprintf(
					"Switched to Go %s with warnings: %s",
					operation.version,
					joinLifecycleWarnings(operation.lifecycleWarnings),
				),
				"warning",
			)
			return
		}
		if operation.shimInPath {
			m.Status.SetTab(
				fmt.Sprintf("Switched to Go %s! Run 'go version' to verify.", operation.version),
				"success",
			)
			return
		}
		m.Status.SetTab(
			fmt.Sprintf("Switched to Go %s!\n\n%s", operation.version, utils.GetShimPathInstructions()),
			"success",
		)
	case catalogMutationDeletion:
		if len(operation.lifecycleWarnings) > 0 {
			m.Status.SetGlobal(
				fmt.Sprintf(
					"Deleted Go %s with warnings: %s",
					operation.version,
					joinLifecycleWarnings(operation.lifecycleWarnings),
				),
				"warning",
			)
			return
		}
		m.Status.SetGlobal(fmt.Sprintf("Successfully deleted Go %s", operation.version), "success")
	}
}

func (m *Model) applyCommittedProjectionWarning(operation catalogOperation, err error) {
	action := "Version operation"
	switch operation.kind {
	case catalogMutationInstall:
		action = fmt.Sprintf("Installed Go %s", operation.version)
	case catalogMutationActivation:
		action = fmt.Sprintf("Switched to Go %s", operation.version)
	case catalogMutationDeletion:
		action = fmt.Sprintf("Deleted Go %s", operation.version)
	}
	m.Status.SetGlobal(
		fmt.Sprintf("%s, but the catalog view could not be updated: %v. Refresh to synchronize.", action, err),
		"warning",
	)
}

func (m Model) verifyingStatus(operation catalogOperation) string {
	if operation.id == 0 {
		return "Verifying catalog..."
	}
	switch operation.kind {
	case catalogMutationInstall:
		return fmt.Sprintf("Installed Go %s; verifying catalog...", operation.version)
	case catalogMutationActivation:
		return fmt.Sprintf("Switched to Go %s; verifying catalog...", operation.version)
	case catalogMutationDeletion:
		return fmt.Sprintf("Deleted Go %s; verifying catalog...", operation.version)
	default:
		return "Verifying catalog..."
	}
}
