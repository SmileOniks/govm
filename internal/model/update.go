package model

import (
	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
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
	switch msg.(type) {
	case tea.MouseMsg:
		return m, nil
	case mouseActionMsg:
		defer m.relayout()
		return m.handleMouse(msg.(mouseActionMsg))
	case spinner.TickMsg, cursor.BlinkMsg:
	default:
		m.mouseRevision++
		defer m.relayout()
	}
	var cmds []tea.Cmd
	// A focused Settings input receives cursor ticks here. Keys and
	// source results take one route, so a source result's refilter
	// command cannot be lost to a second, already-settled delivery.
	if m.inputContext() == inputSettingsInput {
		switch msg.(type) {
		case tea.KeyPressMsg:
			return m.dispatchKey(msg.(tea.KeyPressMsg))
		case distributionSourceValidatedMsg:
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
	if isInstalledMsg(msg) {
		return m.delegateInstalled(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.dispatchKey(msg)

	case tea.WindowSizeMsg:
		m.mouseWindowKnown = true
		m.TermWidth = msg.Width
		m.TermHeight = msg.Height
		m.Layout = styles.GetLayoutMode(msg.Width)

		return m, nil

	case catalogLoadedMsg:
		cmd := m.applyCatalog(msg)
		_, usage := m.delegateInstalled(msg)
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

func (m *Model) dispatchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "?" && m.canOpenHelp() {
		m.HelpVisible = true
		return m, nil
	}
	switch m.inputContext() {
	case inputSettingsInput:
		return m.delegateSettings(msg)
	case inputHelpOverlay:
		return m.handleHelpOverlayKey(msg)
	case inputDepsDialog:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		return m.delegateDeps(msg)
	case inputPruneConfirm:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		return m.delegateInstalled(msg)
	}
	return m.handleKey(msg)
}
