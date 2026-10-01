package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

type catalogStatusScope uint8

const (
	catalogStatusUntouched catalogStatusScope = iota
	catalogStatusTab
	catalogStatusGlobal
)

// catalogStatus describes effects, not the transition that produced them.
// settingsMsg completes the synchronous handshake with the Settings tab.
type catalogStatus struct {
	scope                   catalogStatusScope
	text                    string
	kind                    string
	settingsMsg             tea.Msg
	confirmDeleteVersion    string
	clearDeleteConfirmation bool
	refreshDiskUsage        bool
}

type catalogActionKind uint8

const (
	catalogActionInstall catalogActionKind = iota + 1
	catalogActionActivate
	catalogActionRequestDelete
	catalogActionConfirmDelete
)

type catalogActionMsg struct {
	kind    catalogActionKind
	version string
	tab     int
}

type catalogRefreshMsg struct {
	manual bool
}

type catalogThemeMsg struct{ theme styles.Theme }
type catalogDiskUsageMsg struct{ sizes map[string]int64 }
type catalogSourceCheckMsg struct{ source string }
type catalogSourceAcceptedMsg struct {
	requestID uint64
	versions  []utils.GoVersion
}

func catalogGlobalStatus(text, kind string) catalogStatus {
	return catalogStatus{scope: catalogStatusGlobal, text: text, kind: kind}
}

func catalogTabStatus(text, kind string) catalogStatus {
	return catalogStatus{scope: catalogStatusTab, text: text, kind: kind}
}

func (a *catalogProjectionAdapter) handleAction(msg catalogActionMsg) (tea.Cmd, catalogStatus) {
	if a.state.phase != catalogOperationPhaseIdle {
		return nil, catalogStatus{}
	}
	if msg.tab != AvailableTab && msg.tab != InstalledTab {
		return nil, catalogStatus{}
	}
	v, ok := a.lookup(msg.version)
	switch msg.kind {
	case catalogActionInstall:
		if msg.tab != AvailableTab || !ok || v.Installed {
			return nil, catalogStatus{}
		}
		op := a.startMutation(catalogMutationInstall, v.Version)
		return a.installProgressVersionCmd(op.id, buildInstallRequest(v)), catalogGlobalStatus("", "")
	case catalogActionActivate:
		if !ok || !v.Installed {
			if msg.tab == AvailableTab {
				return nil, catalogTabStatus("You need to install this version first. Press 'i' to install.", "error")
			}
			return nil, catalogStatus{}
		}
		if msg.tab == InstalledTab && v.Active {
			return nil, catalogTabStatus(fmt.Sprintf("Go %s is already active.", v.Version), "info")
		}
		op := a.startMutation(catalogMutationActivation, v.Version)
		return a.activateVersionCmd(op.id, v.Version),
			catalogGlobalStatus(fmt.Sprintf("Switching to Go %s...", v.Version), "info")
	case catalogActionRequestDelete, catalogActionConfirmDelete:
		confirm := msg.kind == catalogActionConfirmDelete
		if !confirm && msg.version == "" {
			return nil, catalogStatus{}
		}
		var cmd tea.Cmd
		var status catalogStatus
		switch {
		case !ok:
			if confirm {
				status = catalogTabStatus(fmt.Sprintf("Go %s is no longer available to delete.", msg.version), "error")
			} else if msg.tab == AvailableTab {
				status = catalogTabStatus("This version is not installed.", "error")
			}
		case !v.Installed:
			if confirm {
				status = catalogTabStatus(fmt.Sprintf("Go %s is no longer installed.", v.Version), "info")
			} else if msg.tab == AvailableTab {
				status = catalogTabStatus("This version is not installed.", "error")
			}
		case v.Active:
			status = catalogTabStatus("Cannot delete active version. Switch to another version first.", "error")
		case !confirm:
			status = catalogTabStatus(
				fmt.Sprintf("Are you sure you want to delete Go %s? Press Y to confirm, N to cancel.", v.Version),
				"warning",
			)
			status.confirmDeleteVersion = v.Version
		default:
			op := a.startMutation(catalogMutationDeletion, v.Version)
			cmd = a.deleteVersionCmd(op.id, v.Version)
			status = catalogGlobalStatus(fmt.Sprintf("Deleting Go %s...", v.Version), "info")
		}
		status.clearDeleteConfirmation = confirm
		return cmd, status
	}
	return nil, catalogStatus{}
}

func (a *catalogProjectionAdapter) mutationInFlight() bool {
	return a.state.phase == catalogOperationPhaseMutating
}

func (a *catalogProjectionAdapter) canStartPrune() bool {
	return a.state.phase == catalogOperationPhaseIdle
}

func (a *catalogProjectionAdapter) refreshInFlight() bool {
	return a.state.phase == catalogOperationPhaseLoading ||
		a.state.phase == catalogOperationPhaseReconciling ||
		a.refilterPending
}

func (a *catalogProjectionAdapter) prepareInitialLoad() {
	a.initialLoad = a.startLoad(catalogLoadPurposeInitial).loadRequest
}

// init builds the command only after the process has bound the loader.
func (a *catalogProjectionAdapter) init() tea.Cmd {
	if a.initialLoad.ID == 0 {
		return nil
	}
	return LoadVersionsCmd(a.loadCatalog, a.initialLoad)
}

// apply is the catalog's message boundary. Only this module interprets
// outcomes; callers forward its command and apply its status effect.
func (a *catalogProjectionAdapter) apply(msg tea.Msg) (tea.Cmd, catalogStatus) {
	var outcome catalogProjectionOutcome
	switch msg := msg.(type) {
	case catalogActionMsg:
		return a.handleAction(msg)
	case installProgressMsg:
		return a.handleInstallProgress(msg), catalogStatus{}
	case installProgressPollMsg:
		return a.handleInstallProgressPoll(msg), catalogStatus{}
	case catalogLoadedMsg:
		outcome = a.acceptLoad(msg.RequestID, msg.Versions)
	case catalogLoadFailedMsg:
		outcome = a.failLoad(msg.RequestID, msg.Err)
	case catalogRefreshMsg:
		if msg.manual && a.refreshInFlight() {
			return nil, catalogStatus{}
		}
		outcome = a.startLoad(catalogLoadPurposeRefresh)
	case catalogSourceCheckMsg:
		outcome = a.startLoad(catalogLoadPurposeRefresh)
	case catalogSourceAcceptedMsg:
		outcome = a.acceptLoad(msg.requestID, msg.versions)
	case installSuccessMsg:
		outcome = a.completeInstall(
			msg.OperationID,
			msg.Version,
			msg.Path,
			msg.Warnings,
		)
	case activationSuccessMsg:
		outcome = a.completeActivation(
			msg.OperationID,
			msg.Result.Version,
			msg.Result.Warnings,
			msg.ShimInPath,
		)
	case deletionSuccessMsg:
		outcome = a.completeDeletion(msg.OperationID, msg.Result.Version, msg.Result.Warnings)
	case installFailureMsg:
		outcome = a.failMutation(msg.OperationID, msg.Err)
	case lifecycleFailureMsg:
		outcome = a.failMutation(msg.OperationID, msg.Err)
	case catalogThemeMsg:
		outcome = a.setTheme(msg.theme)
	case catalogDiskUsageMsg:
		// Disk diagnostics have their own status; changing cached sizes
		// only republishes the widgets.
		return a.setDiskUsage(msg.sizes).cmd, catalogStatus{}
	default:
		return nil, catalogStatus{}
	}
	cmd, status := a.effect(outcome, msg)
	if _, installed := msg.(installSuccessMsg); installed && outcome.receipt.operation.id != 0 {
		status.refreshDiskUsage = true
	}
	return cmd, status
}

func (a *catalogProjectionAdapter) effect(
	outcome catalogProjectionOutcome,
	msg tea.Msg,
) (tea.Cmd, catalogStatus) {
	// Source checks share load correlation with refresh, but their editor
	// owns validation errors and their successful status is Settings-specific.
	switch msg := msg.(type) {
	case catalogSourceCheckMsg:
		if outcome.kind != catalogProjectionOutcomeLoadStarted {
			return outcome.cmd, catalogStatus{settingsMsg: sourceCheckRejectedMsg{
				reason: "cannot check distribution source while another operation is active",
			}}
		}
		status := catalogGlobalStatus("Checking distribution source...", "warning")
		status.settingsMsg = sourceCheckStartedMsg{requestID: outcome.loadRequest.ID}
		cmd := ChangeDistributionSourceCmd(a.distributionSource, outcome.loadRequest, msg.source)
		return tea.Batch(outcome.cmd, cmd), status
	case catalogSourceAcceptedMsg:
		if outcome.kind == catalogProjectionOutcomeRejected {
			return outcome.cmd, catalogStatus{settingsMsg: sourceCheckRejectedMsg{
				reason: fmt.Sprintf("Failed to apply catalog: %v", outcome.err),
			}}
		}
		return outcome.cmd, catalogTabStatus("Settings saved.", "info")
	}

	switch outcome.kind {
	case catalogProjectionOutcomeLoadStarted:
		cmd := tea.Batch(outcome.cmd, LoadVersionsCmd(a.loadCatalog, outcome.loadRequest))
		if outcome.receipt.operation.id != 0 {
			return cmd, catalogGlobalStatus(verifyingStatus(outcome.receipt.operation), "warning")
		}
		if refresh, ok := msg.(catalogRefreshMsg); ok && refresh.manual {
			return cmd, catalogGlobalStatus("", "")
		}
		return cmd, catalogStatus{}
	case catalogProjectionOutcomeReconciled:
		return outcome.cmd, completionStatus(outcome.receipt.operation)
	case catalogProjectionOutcomePublished, catalogProjectionOutcomeNoop:
		if outcome.receipt.operation.id != 0 {
			return outcome.cmd, completionStatus(outcome.receipt.operation)
		}
	case catalogProjectionOutcomeRejected:
		if outcome.receipt.operation.id != 0 {
			text := fmt.Sprintf("Could not verify the operation: %v.", outcome.err)
			return outcome.cmd, catalogGlobalStatus(text, "error")
		}
		text := fmt.Sprintf("Failed to load Go versions: %v.", outcome.err)
		return outcome.cmd, catalogGlobalStatus(text, "error")
	case catalogProjectionOutcomeFailed:
		switch msg := msg.(type) {
		case installFailureMsg:
			text := fmt.Sprintf("Failed to install Go %s: %v", msg.Version, msg.Err)
			return outcome.cmd, catalogGlobalStatus(text, "error")
		case lifecycleFailureMsg:
			return outcome.cmd, catalogGlobalStatus(
				fmt.Sprintf("Failed to %s Go %s: %v", msg.Operation, msg.Version, msg.Err), "error",
			)
		}
		if outcome.receipt.operation.id != 0 {
			text := "The operation could not be confirmed against the installed catalog."
			return outcome.cmd, catalogGlobalStatus(text, "error")
		}
		text := "catalog load failed"
		if outcome.err != nil {
			text = outcome.err.Error()
		}
		return outcome.cmd, catalogGlobalStatus(text, "error")
	case catalogProjectionOutcomeCommittedWarning:
		return outcome.cmd, committedProjectionWarning(outcome.receipt.operation, outcome.err)
	}
	// Stale and suppressed results leave the status untouched. Forward
	// commands uniformly, including any future refilter effects.
	return outcome.cmd, catalogStatus{}
}

func completionStatus(operation catalogOperation) catalogStatus {
	switch operation.kind {
	case catalogMutationInstall:
		text, kind := installSuccessStatus(operation.version, operation.installWarnings)
		return catalogGlobalStatus(text, kind)
	case catalogMutationActivation:
		if len(operation.lifecycleWarnings) > 0 {
			return catalogGlobalStatus(
				fmt.Sprintf(
					"Switched to Go %s with warnings: %s",
					operation.version,
					joinLifecycleWarnings(operation.lifecycleWarnings),
				),
				"warning",
			)
		}
		if operation.shimInPath {
			return catalogTabStatus(
				fmt.Sprintf("Switched to Go %s! Run 'go version' to verify.", operation.version),
				"success",
			)
		}
		return catalogTabStatus(
			fmt.Sprintf("Switched to Go %s!\n\n%s", operation.version, utils.GetShimPathInstructions()),
			"success",
		)
	case catalogMutationDeletion:
		if len(operation.lifecycleWarnings) > 0 {
			return catalogGlobalStatus(
				fmt.Sprintf(
					"Deleted Go %s with warnings: %s",
					operation.version,
					joinLifecycleWarnings(operation.lifecycleWarnings),
				),
				"warning",
			)
		}
		return catalogGlobalStatus(fmt.Sprintf("Successfully deleted Go %s", operation.version), "success")
	}
	return catalogStatus{}
}

func committedProjectionWarning(operation catalogOperation, err error) catalogStatus {
	action := "Version operation"
	switch operation.kind {
	case catalogMutationInstall:
		action = fmt.Sprintf("Installed Go %s", operation.version)
	case catalogMutationActivation:
		action = fmt.Sprintf("Switched to Go %s", operation.version)
	case catalogMutationDeletion:
		action = fmt.Sprintf("Deleted Go %s", operation.version)
	}
	return catalogGlobalStatus(
		fmt.Sprintf("%s, but the catalog view could not be updated: %v. Refresh to synchronize.", action, err),
		"warning",
	)
}

func verifyingStatus(operation catalogOperation) string {
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

func (m *Model) applyCatalogStatus(status catalogStatus) {
	if status.clearDeleteConfirmation {
		m.ConfirmingDelete = false
		m.DeleteVersion = ""
	}
	if status.confirmDeleteVersion != "" {
		m.ConfirmingDelete = true
		m.DeleteVersion = status.confirmDeleteVersion
	}
	switch status.scope {
	case catalogStatusTab:
		m.Status.SetTab(status.text, status.kind)
	case catalogStatusGlobal:
		m.Status.SetGlobal(status.text, status.kind)
	}
	if status.settingsMsg != nil {
		m.settings.update(status.settingsMsg)
	}
}

func (m *Model) applyCatalog(msg tea.Msg) tea.Cmd {
	cmd, status := m.projection.apply(msg)
	m.applyCatalogStatus(status)
	if status.refreshDiskUsage {
		return tea.Batch(cmd, m.diskUsageCmd())
	}
	return cmd
}
