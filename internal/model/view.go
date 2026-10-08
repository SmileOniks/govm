package model

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/charmbracelet/x/ansi"
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
	if m.inMinimumViewport() {
		v := tea.NewView(renderMinimumViewport(t, m.TermWidth, m.TermHeight))
		v.BackgroundColor = t.MinimumViewportBackground
		v.AltScreen = true
		return v
	}

	chrome := m.chrome(width)
	var content renderedSurface
	switch m.CurrentTab {
	case AvailableTab:
		content = m.projection.availableView()
		if filterLine := m.renderAppliedFilterLine(t, width); filterLine != "" {
			content = joinSurfaces(
				renderedSurface{content: filterLine},
				contentCanvas(content, width, max(0, height-lipgloss.Height(filterLine))),
			)
		} else {
			content = contentCanvas(content, width, height)
		}
	case InstalledTab:
		content = contentCanvas(m.projection.installedView(), width, height)
	case DepsTab:
		content = contentCanvas(m.deps.view(), width, height)
	case SettingsTab:
		content = contentCanvas(m.settings.view(width), width, height)
	}
	if m.inputContext() != inputTab {
		content.targets = nil
	}
	ctx := m.inputContext()
	if ctx != inputTab && ctx != inputFilter && ctx != inputDeleteConfirm {
		chrome.tabs.targets = nil
	}
	surface := joinSurfaces(chrome.header, chrome.tabs, chrome.warning, content, chrome.summary, chrome.status, chrome.controls)
	frameH, frameV := styles.FrameOverhead(m.Layout)
	surface = surface.translated(frameH/2, frameV/2, cellRect{width: viewport.Width, height: viewport.Height})
	surface.content = appStyle.Render(surface.content)

	// The modal surface of the context beneath the Help overlay is
	// drawn first; the overlay, when open, sits on top of it.
	switch m.inputContextBeneathHelp() {
	case inputSettingsInput:
		if m.settings.editingSource() {
			surface = overlayDialog(surface, renderDistributionSourceDialog(t, m.settings, viewport), viewport)
		} else {
			surface = overlayDialog(surface, renderDepsBackupLimitDialog(t, m.settings, viewport), viewport)
		}
	case inputDepsDialog:
		surface = overlayDialog(surface, m.deps.dialogView(t, viewport), viewport)
	case inputPruneConfirm:
		surface = overlayDialog(surface, m.installed.dialogView(t, viewport), viewport)
	}
	if m.HelpVisible {
		surface = overlayDialog(surface, renderHelpOverlay(t, m, viewport), viewport)
	}

	v := tea.NewView(mouseFrameContent(surface.content, m.mouseRevision))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.OnMouse = mouseCallback(surface, mouseActionMsg{
		revision: m.mouseRevision, tab: m.CurrentTab,
		context: m.inputContext(), dialogKind: m.deps.dialog.kind,
	})
	return v
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

func renderTabs(t styles.Theme, currentTab int) renderedSurface {
	labels := [...]string{"Available", "Installed", "Deps", "Settings"}
	parts := make([]string, 0, len(labels))
	targets := make([]mouseTarget, 0, len(labels))
	x := 0
	for index, label := range labels {
		part := renderTab(t, label, currentTab == index)
		partWidth, partHeight := lipgloss.Width(part), lipgloss.Height(part)
		parts = append(parts, part)
		targets = append(targets, mouseTarget{
			rect:   cellRect{x: x, width: partWidth, height: partHeight},
			action: mouseAction{kind: mouseTab, index: index},
		})
		x += partWidth
	}
	return renderedSurface{content: lipgloss.JoinHorizontal(lipgloss.Left, parts...), targets: targets}
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
func renderSettingsView(settings settingsTab, width int) renderedSurface {
	values := settings.values
	labels := [...]string{"Deps display", "Theme", "Deps backups", "Distribution source", "Upgrade notice"}
	valueLabels := [...]string{
		depsDisplayLabel(values.DepsDisplay), themeLabel(values.Theme),
		fmt.Sprint(values.DepsBackupLimit), values.DistributionSource,
		upgradeNoticeLabel(values.UpgradeNotice),
	}
	lines := make([]string, 0, len(settingRows))
	targets := make([]mouseTarget, 0, len(settingRows)*2+3)
	targets = append(targets, mouseTarget{
		rect:   cellRect{width: width, height: len(settingRows)},
		action: mouseAction{kind: mouseScroll},
	})
	for index, kind := range settingRows {
		prefix := "  "
		if kind == settings.cursor {
			prefix = "> "
		}
		prefix += labels[index] + ": "
		x := ansi.StringWidth(prefix)
		extraWidth := 0
		if kind == settingRowDepsBackups {
			extraWidth = 12 // Space plus two padded step buttons.
		}
		value := "[ " + truncateSettingValue(valueLabels[index], max(0, width-x-extraWidth-4)) + " ]"
		line := prefix + value
		targets = append(targets,
			mouseTarget{rect: cellRect{y: index, width: width, height: 1},
				action: mouseAction{kind: mouseSettingRow, index: index}},
			mouseTarget{rect: cellRect{x: x, y: index, width: ansi.StringWidth(value), height: 1},
				action: mouseAction{kind: mouseSettingActivate, index: index}},
		)
		if kind == settingRowDepsBackups {
			x += ansi.StringWidth(value) + 1
			line += " [ − ] [ + ]"
			for step := range 2 {
				targets = append(targets, mouseTarget{
					rect:   cellRect{x: x + step*6, y: index, width: 5, height: 1},
					action: mouseAction{kind: mouseSettingStep, index: index, delta: step*2 - 1},
				})
			}
		}
		lines = append(lines, line)
	}
	return renderedSurface{content: strings.Join(lines, "\n"), targets: targets}
}

func truncateSettingValue(value string, width int) string {
	return ansi.Truncate(value, max(0, width), "…")
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

// renderHelpBar renders whole registry controls, wrapping without hiding actions.
func renderHelpBar(t styles.Theme, m Model, width int) renderedSurface {
	sections := contextKeyBindings(m, m.inputContext())
	if m.inputContext() == inputTab && m.CurrentTab == AvailableTab && m.projection.availableFilterApplied() {
		sections = append(sections, helpSection{bindings: []keyBinding{{
			mouseControls: mouseControls("esc clear", tea.KeyPressMsg{Code: tea.KeyEscape}),
		}}})
	}
	return renderControls(t, sections, width, m.settings.checkingDistributionSource)
}

func renderControls(t styles.Theme, sections []helpSection, width int, checking bool) renderedSurface {
	if width < 5 {
		return renderedSurface{}
	}
	labelStyle := lipgloss.NewStyle().Foreground(t.Text)
	open, close := t.HelpTextStyle.Render("[ "), t.HelpTextStyle.Render(" ]")
	var content strings.Builder
	targets := make([]mouseTarget, 0)
	x, y := 0, 0
	for _, section := range sections {
		// Keep short action groups, such as Help and Quit, on the same row.
		if x > 0 {
			groupWidth := 0
			for _, binding := range section.bindings {
				for _, control := range binding.mouseControls {
					if control.label == "" {
						continue
					}
					if groupWidth > 0 {
						groupWidth++
					}
					groupWidth += lipgloss.Width(control.label) + 4
				}
			}
			if groupWidth > 0 && groupWidth <= width && x+1+groupWidth > width {
				content.WriteByte('\n')
				x = 0
				y++
			}
		}
		for _, binding := range section.bindings {
			for _, control := range binding.mouseControls {
				if control.label == "" {
					continue
				}
				keyLabel, description, _ := strings.Cut(control.label, " ")
				if binding.keys != "" && strings.HasPrefix(control.label, binding.keys+" ") {
					keyLabel, description = binding.keys, strings.TrimPrefix(control.label, binding.keys+" ")
				}
				if description != "" {
					first, size := utf8.DecodeRuneInString(description)
					description = string(unicode.ToUpper(first)) + description[size:]
				}
				disabled := checking && (control.key.Code == tea.KeyEnter || control.key.Code == 'r')
				var inner string
				if disabled {
					inner = keyLabel
					if description != "" {
						inner = description + " " + inner
					}
					inner = t.HelpTextStyle.Render(inner)
				} else {
					inner = t.HelpKeyStyle.Render(keyLabel)
					if description != "" {
						inner = labelStyle.Render(description) + " " + inner
					}
				}
				if lipgloss.Width(inner) > width-4 {
					inner = ansi.Cut(inner, 0, width-4)
				}
				button := open + inner + close
				buttonWidth := lipgloss.Width(button)
				if x > 0 && x+1+buttonWidth > width {
					content.WriteByte('\n')
					x = 0
					y++
				}
				if x > 0 {
					content.WriteByte(' ')
					x++
				}
				content.WriteString(button)
				if !disabled {
					targets = append(targets, mouseTarget{
						rect:   cellRect{x: x, y: y, width: buttonWidth, height: 1},
						action: mouseAction{kind: mouseKey, key: control.key},
					})
				}
				x += buttonWidth
			}
		}
	}
	return renderedSurface{content: content.String(), targets: targets}
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
		line = ansi.Cut(line, 0, max(0, width))
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
