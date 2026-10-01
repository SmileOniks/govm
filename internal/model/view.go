package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func (m Model) View() tea.View {
	t := m.theme
	appStyle := t.AppStyleFor(m.Layout)
	width := m.viewWidth()
	height := m.viewHeight()
	viewport := viewportSize{Width: width, Height: height}
	if m.TermWidth > 0 {
		viewport.Width = m.TermWidth
	}
	if m.TermHeight > 0 {
		viewport.Height = m.TermHeight
	}
	if (m.TermWidth > 0 || m.TermHeight > 0 || (m.Width == 1 && m.Height == 1)) &&
		(m.TermWidth < styles.MinTermWidth || m.TermHeight < styles.MinTermHeight) {
		v := tea.NewView(renderMinimumViewport(t, m.TermWidth, m.TermHeight))
		v.BackgroundColor = t.MinimumViewportBackground
		v.AltScreen = true
		return v
	}

	components := make([]string, 0, 6)
	components = append(components, renderHeader(t, width, utils.GetVersion(), m.upgradeNotice))
	components = append(components, renderTabs(t, m.CurrentTab))

	if m.ShimPathWarning != "" {
		components = append(components, renderStatus(t, "warning", m.ShimPathWarning, width))
	}

	switch m.CurrentTab {
	case AvailableTab:
		content := m.projection.availableView()
		if filterLine := m.renderAppliedFilterLine(t, width); filterLine != "" {
			// The indicator borrows one row from the content canvas
			// so the overall layout height is unchanged.
			components = append(components, filterLine)
			content = renderContentCanvas(content, width, maxInt(0, height-1))
		} else {
			content = renderContentCanvas(content, width, height)
		}
		components = append(components, content)
	case InstalledTab:
		components = append(components, renderContentCanvas(m.projection.installedView(), width, height))
		if m.diskUsage != nil {
			components = append(components, renderInstalledSummary(m.DiskUsage))
		}
	case DepsTab:
		components = append(components, renderContentCanvas(m.deps.view(), width, height))
	case SettingsTab:
		components = append(components, renderContentCanvas(m.settings.view(), width, height))
	}

	if status, statusType := m.composeStatus(); status != "" {
		components = append(components, renderStatus(t, statusType, status, width))
	}

	components = append(components, renderHelpBar(t, m, width))
	rendered := appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, components...))

	// The modal surface of the context beneath the Help overlay is
	// drawn first; the overlay, when open, sits on top of it.
	switch m.inputContextBeneathHelp() {
	case inputSettingsInput:
		if m.settings.editingSource() {
			rendered = overlayDialog(rendered, renderDistributionSourceDialog(t, m.settings, viewport), viewport)
		} else {
			rendered = overlayDialog(rendered, renderDepsBackupLimitDialog(t, m.settings, viewport), viewport)
		}
	case inputDepsDialog:
		rendered = overlayDialog(rendered, m.deps.dialogView(t, viewport), viewport)
	case inputPruneConfirm:
		rendered = overlayDialog(rendered, renderPruneDialog(t, m.Prune, viewport), viewport)
	}
	if m.HelpVisible {
		rendered = overlayDialog(rendered, renderHelpOverlay(t, m, viewport), viewport)
	}

	v := tea.NewView(rendered)
	v.AltScreen = true
	return v
}

// renderInstalledSummary reports the disk footprint of the managed
// toolchains.
//
// Interrupted downloads are reported only when some exist. A finished
// install leaves none behind — the archive is streamed to a .part file
// that is removed on success, on failure, and again by the next
// install's orphan sweep — so a permanent field would read "0 B" in
// every situation a user can observe, and say nothing about the one
// situation that matters: a crash left debris on disk.
func renderInstalledSummary(summary prune.Summary) string {
	line := fmt.Sprintf(
		"Installed: %s  Reclaimable: %s",
		prune.FormatBytes(summary.InstalledBytes),
		prune.FormatBytes(summary.ReclaimableBytes),
	)
	if summary.DownloadBytes > 0 {
		line += fmt.Sprintf("  Interrupted: %s", prune.FormatBytes(summary.DownloadBytes))
	}
	return line
}

// renderPruneDialog draws the prune confirmation as a Dialog: the
// shared warning title, the plan, and the Yes/No buttons, in the same
// box as the Deps dialogs.
func renderPruneDialog(t styles.Theme, state PruneState, viewport viewportSize) string {
	result := state.Plan()
	lines := []string{
		t.DialogTitleStyle.Render(t.DialogWarningStyle.Render("⚠ Prune inactive Go versions?")),
		"",
		t.DialogBodyStyle.Render(fmt.Sprintf("Candidates: %d", len(result.Candidates))),
		t.DialogBodyStyle.Render(fmt.Sprintf("Reclaimable: %s", prune.FormatBytes(pruneCandidateBytes(result)))),
		"",
	}
	visible := result.Candidates
	extra := 0
	if len(visible) > maxDependencyListLines {
		extra = len(visible) - maxDependencyListLines
		visible = visible[:maxDependencyListLines]
	}
	for _, candidate := range visible {
		label := candidate.Version
		if label == "" {
			label = candidate.Path
		}
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("  %s  %s", label, prune.FormatBytes(candidate.Bytes))))
	}
	if extra > 0 {
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("  …and %d more", extra)))
	}
	lines = append(lines, "", renderYesNoButtons(t, state.ChoiceYes(), "Yes", "No"))
	return renderDialog(t, lipgloss.JoinVertical(lipgloss.Left, lines...), false, viewport)
}

func renderMinimumViewport(t styles.Theme, width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)

	lines := []string{
		fmt.Sprintf("Minimum terminal size is %dx%d.", styles.MinTermWidth, styles.MinTermHeight),
		fmt.Sprintf("Current size: %dx%d.", width, height),
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = ansi.Cut(line, 0, width)
	}

	message := lipgloss.NewStyle().
		Foreground(t.MinimumViewportText).
		Render(strings.Join(lines, "\n"))
	background := lipgloss.NewStyle().Background(t.MinimumViewportBackground)
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		message,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceStyle(background),
	)
}

// composeStatus returns the current status message and type, taking
// loading/spinner state into account so the caller doesn't have to.
func (m Model) composeStatus() (string, string) {
	status := m.Status.Text()
	statusType := m.Status.Kind()
	activity := m.projection.activityState()
	if activity.kind != catalogActivityIdle || m.deps.busy() {
		statusType = "info"
		switch activity.kind {
		case catalogActivityInstalling:
			if p, ok := activity.installProgress(); ok {
				status = m.installStageStatus(p, m.viewWidth())
			} else {
				status = fmt.Sprintf("%s Preparing Go %s", m.Spinner.View(), activity.version)
			}
		case catalogActivityActivating:
			if status == "" {
				status = fmt.Sprintf("%s Switching to Go %s", m.Spinner.View(), activity.version)
			}
		case catalogActivityDeleting:
			if status == "" {
				status = fmt.Sprintf("%s Deleting Go %s", m.Spinner.View(), activity.version)
			}
		case catalogActivityReconciling:
			if status == "" {
				status = fmt.Sprintf("%s Verifying catalog", m.Spinner.View())
			}
		}
		if text := m.deps.spinnerText(); status == "" && text != "" {
			status = fmt.Sprintf("%s %s", m.Spinner.View(), text)
		}
		if status == "" {
			status = fmt.Sprintf("%s Loading", m.Spinner.View())
		}
	}
	return status, statusType
}

// renderAppliedFilterLine renders the indicator shown while a
// committed filter narrows the Available list: the query, the visible
// share of the catalog, and the key that clears it. The widget's own
// status bar (its default indicator) is hidden, so without this line
// a shortened list would be indistinguishable from the full catalog.
func (m Model) renderAppliedFilterLine(t styles.Theme, width int) string {
	if !m.projection.availableFilterApplied() {
		return ""
	}
	query, visible, total := m.projection.availableFilterSummary()
	if query == "" {
		return ""
	}
	text := fmt.Sprintf("find: %q · %d/%d · esc clear", query, visible, total)
	return t.HelpTextStyle.Width(width).Render(text)
}

// renderHeader draws the title on the left and the version metadata on
// the right. When notice is non-empty the Upgrade notice follows the
// version. If the right side does not fit, the "Go Version Manager"
// prefix is dropped first and the notice second; the version itself is
// never truncated because it is what users paste into bug reports.
func renderHeader(t styles.Theme, width int, version, notice string) string {
	title := t.TitleStyle.Render("GoVM")
	budget := width - lipgloss.Width(title) - 1

	prefixed := "Go Version Manager " + version
	var candidates []string
	if notice != "" {
		tail := t.HeaderMetaStyle.Render(" · ") + t.HeaderNoticeStyle.Render("↑ "+notice+" available")
		candidates = append(candidates,
			t.HeaderMetaStyle.Render(prefixed)+tail,
			t.HeaderMetaStyle.Render(version)+tail,
		)
	}
	candidates = append(candidates,
		t.HeaderMetaStyle.Render(prefixed),
		t.HeaderMetaStyle.Render(version),
	)
	meta := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= budget {
			meta = candidate
			break
		}
	}

	spacerWidth := maxInt(1, width-lipgloss.Width(title)-lipgloss.Width(meta))
	return lipgloss.JoinHorizontal(lipgloss.Top, title, strings.Repeat(" ", spacerWidth), meta)
}

func renderTabs(t styles.Theme, currentTab int) string {
	tabs := []string{
		renderTab(t, "Available", currentTab == AvailableTab),
		renderTab(t, "Installed", currentTab == InstalledTab),
		renderTab(t, "Deps", currentTab == DepsTab),
		renderTab(t, "Settings", currentTab == SettingsTab),
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, tabs...)
}

func renderTab(t styles.Theme, label string, active bool) string {
	if active {
		return t.ActiveTabStyle.Render("● " + label)
	}
	return t.InactiveTabStyle.Render("○ " + label)
}

func renderStatus(t styles.Theme, messageType, message string, width int) string {
	if message == "" {
		return ""
	}

	icon := "•"
	style := t.StatusInfoStyle
	switch messageType {
	case "success":
		icon = "✓"
		style = t.StatusSuccessStyle
	case "error":
		icon = "✕"
		style = t.StatusErrorStyle
	case "warning":
		icon = "!"
		style = t.StatusWarningStyle
	case "info":
		icon = "•"
		style = t.StatusInfoStyle
	}

	return style.Width(width).Render(fmt.Sprintf("%s %s", icon, message))
}

// renderSettingsView renders the rows of the Settings tab; the tab
// owns it and the Model reaches it through view().
func renderSettingsView(settings settingsTab) string {
	values := settings.values
	rows := []string{
		fmt.Sprintf("Deps display: %s", depsDisplayLabel(values.DepsDisplay)),
		fmt.Sprintf("Theme: %s", themeLabel(values.Theme)),
		fmt.Sprintf("Deps backups: %d", values.DepsBackupLimit),
		fmt.Sprintf("Distribution source: %s", truncateSettingValue(values.DistributionSource, 48)),
		fmt.Sprintf("Upgrade notice: %s", upgradeNoticeLabel(values.UpgradeNotice)),
	}
	for i, kind := range settingRows {
		prefix := "  "
		if kind == settings.cursor {
			prefix = "> "
		}
		rows[i] = prefix + rows[i]
	}
	return strings.Join(rows, "\n")
}

func truncateSettingValue(value string, max int) string {
	if max < 1 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func depsDisplayLabel(mode config.DepsDisplayMode) string {
	if mode == config.DepsDisplayAll {
		return "All"
	}
	return "Direct only"
}

func upgradeNoticeLabel(mode config.UpgradeNoticeMode) string {
	if mode == config.UpgradeNoticeOff {
		return "Off"
	}
	return "On"
}

func themeLabel(name config.ThemeName) string {
	if name == config.ThemeLight {
		return "Light"
	}
	return "Current"
}

// renderHelpBar renders the one-line hint bar from the keybinding
// registry (ADR-0001): the short-flagged bindings of the active input
// context (dependency dialog, delete confirmation, or tab) followed
// by the short global bindings. The overlay and every other hint
// variant render from the same registry, so the two can never drift
// apart.
func renderHelpBar(t styles.Theme, m Model, width int) string {
	return renderKeyHints(t, shortHints(contextKeyBindings(m, m.inputContext())), width)
}

func renderKeyHints(t styles.Theme, hints [][2]string, width int) string {
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, fmt.Sprintf("%s %s", t.HelpKeyStyle.Render(hint[0]), t.HelpTextStyle.Render(hint[1])))
	}

	helpText := strings.Join(parts, "  ")

	if lipgloss.Width(helpText) > width {
		helpText = styles.TruncateText(helpText, width)
	}

	return helpText
}

func renderContentCanvas(content string, width, height int) string {
	if height < 1 {
		return ""
	}

	content = strings.TrimRight(content, "\n")
	var canvas strings.Builder
	canvas.Grow(height * (maxInt(0, width) + 1))

	lineStart := 0
	for row := 0; row < height; row++ {
		var line string
		if lineStart < len(content) {
			lineEnd := strings.IndexByte(content[lineStart:], '\n')
			if lineEnd < 0 {
				line = content[lineStart:]
				lineStart = len(content)
			} else {
				lineEnd += lineStart
				line = content[lineStart:lineEnd]
				lineStart = lineEnd + 1
			}
		}
		canvas.WriteString(line)
		if padding := width - ansi.StringWidth(line); padding > 0 {
			for range padding {
				canvas.WriteByte(' ')
			}
		}
		if row+1 < height {
			canvas.WriteByte('\n')
		}
	}
	return canvas.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
