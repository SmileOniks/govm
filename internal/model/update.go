package model

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
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
	// A focused Settings input receives cursor ticks here. Keys and
	// source results take one route, so a source result's refilter
	// command cannot be lost to a second, already-settled delivery.
	if m.inputContext() == inputSettingsInput {
		switch msg.(type) {
		case tea.KeyPressMsg, distributionSourceValidatedMsg:
			return m.delegateSettings(msg)
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
		cmd := m.applyCatalog(msg)
		usage := m.applyCatalog(catalogDiskUsageMsg{sizes: m.DiskUsage.VersionBytes})
		return m, tea.Batch(cmd, usage)

	case catalogLoadFailedMsg:
		return m, m.applyCatalog(msg)

	case distributionSourceValidatedMsg:
		return m.delegateSettings(msg)

	case upgradeCheckStartMsg:
		return m, m.startUpgradeCheck()

	case upgradeCheckedMsg:
		m.handleUpgradeChecked(msg)
		return m, nil

	case diskUsageMsg:
		m.DiskUsage = msg.Summary
		cmd := m.applyCatalog(catalogDiskUsageMsg{sizes: msg.Summary.VersionBytes})
		if msg.Err != nil {
			m.Status.SetTab(fmt.Sprintf("Disk usage unavailable: %v", msg.Err), "warning")
		} else if len(msg.Summary.Warnings) > 0 {
			m.Status.SetTab("Disk usage is approximate; some files could not be inspected.", "warning")
		}
		return m, cmd

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
		return m, tea.Batch(m.applyCatalog(catalogRefreshMsg{}), m.diskUsageCmd())

	case list.FilterMatchesMsg:
		return m, m.projection.updateAvailable(msg)

	case catalogProjectionRefilterMsg:
		return m, m.projection.settleRefilter(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case installProgressMsg, installProgressPollMsg,
		installSuccessMsg, installFailureMsg,
		activationSuccessMsg, deletionSuccessMsg, lifecycleFailureMsg:
		return m, m.applyCatalog(msg)
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
