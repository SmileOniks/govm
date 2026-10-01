package model

// clearDeleteContext resets the Available tab's inline confirmation.
func (m *Model) clearDeleteContext() {
	m.availableConfirmingDelete = false
	m.availableDeleteVersion = ""
}

// clearTabContext tears down everything the current tab accumulated:
// the tab-scoped status line and the delete-confirmation context.
// Global-scoped status messages survive (a successful install should
// still be visible after switching tabs).
func (m *Model) clearTabContext() {
	m.Status.ClearTab()
	m.clearDeleteContext()
	if m.CurrentTab == InstalledTab {
		m.installed.update(installedLeaveMsg{})
	}
}
