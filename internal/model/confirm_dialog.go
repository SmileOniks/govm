package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/SmileOniks/govm/internal/styles"
)

// depsDialogKind identifies which Yes/No dependency dialog is currently
// active. The zero value dialogIdle means "no dialog open", which
// makes a freshly constructed depsDialog inactive by default.
type depsDialogKind int

const (
	dialogIdle depsDialogKind = iota
	dialogUpdate
	dialogChecks
	dialogRollback
	dialogRestore
)

// dialogAction is the side-effect-free signal returned by
// depsDialog.Handle. Model.Update interprets it to decide which
// per-kind side-effect runner to invoke (apply*Choice / cancel*).
type dialogAction int

const (
	dialogNoop        dialogAction = iota // ←/→, ↑/↓ — state already updated inside Handle
	dialogConfirm                         // enter / y — caller runs the per-kind confirm path
	dialogCancel                          // n / esc — caller runs the per-kind cancel path
	dialogChangeLevel                     // ↑/↓ on the update dialog — Level already updated; caller rebuilds the plan
	dialogChangeScope                     // space on the update dialog — Explicit already flipped; caller rebuilds the plan
)

// depsDialog is the single module that owns the active Yes/No
// dialog for the Deps tab. Four dialogs (update, checks, rollback,
// restore) collapse into one struct parameterised by Kind. Only
// restore uses the Cursor / MaxCursor pair, which controls navigation
// over the Backups slice stored on depsTab. UpdateEntries and
// CheckResult retain the prompt payload so rendering stays independent
// from the cycle's defensively copied accessors. Inconclusive selects
// the distinct rollback copy used when checks could not run.
//
// The struct is intentionally small and side-effect free. Key handling
// that mutates only dialog-internal state (choice toggle, list
// navigation) lives in Handle; commands and depsTab mutations stay
// in Model.Update, which interprets the returned dialogAction.
type depsDialog struct {
	kind          depsDialogKind
	choiceYes     bool
	cursor        int
	maxCursor     int
	inconclusive  bool
	updateEntries []deps.DependencyUpdateEntry
	checkResult   *deps.DependencyCheckResult
	// Level is the Update level the update dialog's entries were built
	// for; Explicit is true when the plan covers ExplicitModules (the
	// Update scope "marked"/"current") rather than every direct
	// dependency. ExplicitModules is captured when the dialog opens so
	// the scope can be toggled back and forth without re-reading marks.
	level           deps.UpdateLevel
	explicit        bool
	explicitModules []string
}

// CanToggleScope reports whether the update dialog has an explicit
// module set to switch to.
func (d depsDialog) canToggleScope() bool {
	return d.kind == dialogUpdate && len(d.explicitModules) > 0
}

// Active reports whether any dialog is currently open. The zero value
// of depsDialog (Kind == dialogIdle) is inactive.
func (d depsDialog) active() bool { return d.kind != dialogIdle }

// Handle translates a key press into a new dialog state plus an
// action the caller interprets. It mutates only fields on the dialog
// itself (ChoiceYes, Cursor). Per-kind commands, status messages, and
// in-flight flag mutations are performed by the caller based on the
// returned action.
func (d depsDialog) handle(msg tea.KeyPressMsg) (depsDialog, dialogAction) {
	// The update dialog uses the vertical keys to cycle the level and
	// space to toggle the scope between all direct dependencies and
	// the explicit set.
	if d.kind == dialogUpdate {
		switch msg.String() {
		case "up", "k":
			d.level = adjacentLevel(d.level, -1)
			return d, dialogChangeLevel
		case "down", "j":
			d.level = adjacentLevel(d.level, +1)
			return d, dialogChangeLevel
		case "space":
			if !d.canToggleScope() {
				return d, dialogNoop
			}
			d.explicit = !d.explicit
			return d, dialogChangeScope
		}
	}
	// Restore is the only kind that navigates a list inside the dialog.
	if d.kind == dialogRestore {
		switch msg.String() {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
			}
			return d, dialogNoop
		case "down", "j":
			if d.cursor < d.maxCursor {
				d.cursor++
			}
			return d, dialogNoop
		}
	}

	var action dialogAction
	d.choiceYes, action = yesNoKeyAction(msg.String(), d.choiceYes)
	return d, action
}

// yesNoKeyAction is the key handling every Yes/No dialog shares: the
// horizontal keys toggle the highlighted button, enter commits it, y
// and n answer directly, esc declines. It returns the new choice and
// the action the caller enacts. dialogConfirm is returned only when
// the committed choice is Yes; enter on No cancels.
func yesNoKeyAction(key string, choiceYes bool) (bool, dialogAction) {
	switch key {
	case "left", "right", "tab", "shift+tab", "h", "l":
		return !choiceYes, dialogNoop
	case "enter":
		if choiceYes {
			return choiceYes, dialogConfirm
		}
		return choiceYes, dialogCancel
	case "y", "Y":
		return true, dialogConfirm
	case "n", "N", "esc":
		return false, dialogCancel
	}
	return choiceYes, dialogNoop
}

func (d depsDialog) render(t styles.Theme, tab depsTab, viewport viewportSize) renderedSurface {
	yes, no := buttonLabels(d.kind)
	footer := joinSurfaces(
		renderedSurface{content: " "},
		renderYesNoButtons(t, d.choiceYes, yes, no),
		renderDialogControls(t, dialogWidth(viewport)-6),
	)
	budget := dialogBodyHeight(t, viewport, footer)
	var body renderedSurface
	switch d.kind {
	case dialogUpdate:
		body = d.updateSurface(t, tab, budget)
	case dialogChecks:
		body.content = strings.Join(checksDialogLines(t), "\n")
	case dialogRollback:
		body.content = strings.Join(rollbackDialogLines(t, d.checkResult, d.inconclusive, max(0, budget-6)), "\n")
	case dialogRestore:
		body = restoreDialogSurface(t, tab.backups, d.cursor, budget)
	}
	return renderDialog(t, joinSurfaces(body, footer), d.errorStyle(), viewport)
}

// renderYesNoButtons draws the shared button row of a Yes/No dialog
// with the chosen button highlighted.
func renderYesNoButtons(t styles.Theme, choiceYes bool, yesLabel, noLabel string) renderedSurface {
	yesStyle, noStyle := t.DialogInactiveStyle, t.DialogInactiveStyle
	if choiceYes {
		yesStyle = t.DialogActiveStyle
	} else {
		noStyle = t.DialogActiveStyle
	}
	yes, no := yesStyle.Render(yesLabel), noStyle.Render(noLabel)
	return renderedSurface{
		content: lipgloss.JoinHorizontal(lipgloss.Center, yes, "  ", no),
		targets: []mouseTarget{
			{rect: cellRect{width: lipgloss.Width(yes), height: lipgloss.Height(yes)},
				action: mouseAction{kind: mouseKey, key: tea.KeyPressMsg{Code: 'y'}}},
			{rect: cellRect{x: lipgloss.Width(yes) + 2, width: lipgloss.Width(no), height: lipgloss.Height(no)},
				action: mouseAction{kind: mouseKey, key: tea.KeyPressMsg{Code: 'n'}}},
		},
	}
}

func (d depsDialog) errorStyle() bool {
	return d.kind == dialogRollback
}

func buttonLabels(kind depsDialogKind) (yes, no string) {
	switch kind {
	case dialogRollback:
		return "Roll back", "Keep"
	case dialogRestore:
		return "Restore", "Cancel"
	default:
		return "Yes", "No"
	}
}

// adjacentLevel returns the level step positions away from level in
// deps.Levels order, wrapping at both ends.
func adjacentLevel(level deps.UpdateLevel, step int) deps.UpdateLevel {
	n := len(deps.Levels)
	for i, l := range deps.Levels {
		if l == level {
			return deps.Levels[((i+step)%n+n)%n]
		}
	}
	return deps.Levels[0]
}

func (d depsDialog) updateSurface(t styles.Theme, tab depsTab, budget int) renderedSurface {
	top := joinSurfaces(
		renderedSurface{content: t.DialogTitleStyle.Render(t.DialogWarningStyle.Render("⚠ Warning")) + "\n "},
		levelSelectorLine(t, d.level),
		scopeSelectorLine(t, d.explicit, explicitScopeLabel(tab, d.explicitModules)),
		renderedSurface{content: " "},
	)
	if len(d.updateEntries) == 0 {
		hint := "↑/↓ change level · Yes ends without changes"
		if d.canToggleScope() {
			hint = "↑/↓ change level · space change scope · Yes ends without changes"
		}
		return joinSurfaces(top, renderedSurface{content: t.DialogBodyStyle.Render(fmt.Sprintf("No updates available at the %s level.", d.level)) +
			"\n \n" + t.DialogMutedStyle.Render(hint),
		})
	}
	kind := "direct "
	if d.explicit {
		kind = ""
	}
	summary := t.DialogBodyStyle.Render(fmt.Sprintf(
		"%d %s%s will be updated:", len(d.updateEntries), kind,
		deps.Pluralize(len(d.updateEntries), "dependency", "dependencies"),
	))
	warnings := renderedSurface{content: " \n" +
		t.DialogBodyStyle.Render("go.mod and go.sum will be modified.") + "\n" +
		t.DialogBodyStyle.Render("A snapshot is taken before the update so changes can be rolled back."),
	}
	previewBudget := max(0, budget-surfaceHeight(top)-1-surfaceHeight(warnings))
	limit := min(maxDependencyListLines, previewBudget)
	if len(d.updateEntries) > limit {
		limit = min(maxDependencyListLines, max(0, previewBudget-1))
	}
	lines := []string{summary}
	for _, entry := range d.updateEntries[:min(limit, len(d.updateEntries))] {
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf(
			"  %s: %s -> %s", entry.Path, entry.OldVersion, entry.NewVersion,
		)))
	}
	if extra := len(d.updateEntries) - limit; extra > 0 {
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("  …and %d more", extra)))
	}
	return joinSurfaces(top, renderedSurface{content: strings.Join(lines, "\n")}, warnings)
}

// levelSelectorLine renders "Level: Patch  Minor  [Latest]" with the
// active level highlighted.
func levelSelectorLine(t styles.Theme, level deps.UpdateLevel) renderedSurface {
	content := t.DialogBodyStyle.Render("Level:")
	targets := make([]mouseTarget, 0, len(deps.Levels))
	x := lipgloss.Width(content)
	for _, value := range deps.Levels {
		style := t.DialogMutedStyle
		if value == level {
			style = t.DialogActiveStyle
		}
		part := style.Render(value.Label())
		targets = append(targets, mouseTarget{
			rect:   cellRect{x: x, width: lipgloss.Width(part), height: lipgloss.Height(part)},
			action: mouseAction{kind: mouseDialogLevel, level: value},
		})
		content = lipgloss.JoinHorizontal(lipgloss.Center, content, part)
		x += lipgloss.Width(part)
	}
	return renderedSurface{content: content, targets: targets}
}

// scopeSelectorLine renders "Scope: All  [Marked (2)]" with the active
// Update scope highlighted. Without an explicit set (no marks and no
// cursor module) only "All" is shown.
func scopeSelectorLine(t styles.Theme, explicit bool, explicitLabel string) renderedSurface {
	content := t.DialogBodyStyle.Render("Scope:")
	targets := make([]mouseTarget, 0, 2)
	x := lipgloss.Width(content)
	labels := []string{"All"}
	if explicitLabel != "" {
		labels = append(labels, explicitLabel)
	}
	for index, label := range labels {
		value := index == 1
		style := t.DialogMutedStyle
		if value == explicit {
			style = t.DialogActiveStyle
		}
		part := style.Render(label)
		if explicitLabel != "" {
			targets = append(targets, mouseTarget{
				rect:   cellRect{x: x, width: lipgloss.Width(part), height: lipgloss.Height(part)},
				action: mouseAction{kind: mouseDialogScope, explicit: value},
			})
		}
		content = lipgloss.JoinHorizontal(lipgloss.Center, content, part)
		x += lipgloss.Width(part)
	}
	return renderedSurface{content: content, targets: targets}
}

// explicitScopeLabel names the explicit Update scope offered by the
// dialog: the marked modules when marks exist, otherwise the module
// under the cursor. Empty when there is no explicit set.
func explicitScopeLabel(state depsTab, modules []string) string {
	if len(modules) == 0 {
		return ""
	}
	if n := len(state.markedPaths()); n > 0 {
		return fmt.Sprintf("Marked (%d)", n)
	}
	return "Current"
}

func checksDialogLines(t styles.Theme) []string {
	return []string{
		t.DialogTitleStyle.Render(t.StatusInfoStyle.Render("✓ Run checks?")),
		"",
		t.DialogBodyStyle.Render("After the update the following will be executed:"),
		t.DialogBodyStyle.Render("  • go test ./..."),
		t.DialogBodyStyle.Render("  • go vet ./..."),
		"",
		t.DialogMutedStyle.Render("If a check fails you will be offered to roll back the dependencies."),
	}
}

func rollbackDialogLines(t styles.Theme, result *deps.DependencyCheckResult, inconclusive bool, limit int) []string {
	title := "⚠ Checks failed"
	if inconclusive {
		title = "⚠ Checks inconclusive"
	}
	lines := []string{
		t.DialogTitleStyle.Render(t.DialogWarningStyle.Render(title)),
		"",
	}
	if result != nil && result.Command != "" {
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("Command: %s", result.Command)))
		if result.Output != "" {
			output := strings.Split(result.Output, "\n")
			visible := output
			limit = min(maxDependencyListLines, max(0, limit))
			if len(visible) > limit {
				visible = visible[:limit]
			}
			for _, l := range visible {
				lines = append(lines, t.DialogMutedStyle.Render(l))
			}
			if extra := len(output) - len(visible); extra > 0 {
				lines = append(lines, t.DialogMutedStyle.Render(fmt.Sprintf("…and %d more", extra)))
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, t.DialogBodyStyle.Render("Roll back the dependencies to their pre-update state?"))
	return lines
}

func restoreWindow(count, cursor, limit int) (start, end int) {
	limit = max(1, limit)
	start = max(0, cursor-limit+1)
	start = min(start, max(0, count-limit))
	return start, min(count, start+limit)
}

func restoreDialogSurface(t styles.Theme, backups []deps.DependencyBackupInfo, cursor, budget int) renderedSurface {
	top := renderedSurface{content: strings.Join([]string{
		t.DialogTitleStyle.Render(t.DialogWarningStyle.Render("Dependency backups")),
		" ",
		t.DialogBodyStyle.Render("Choose a saved dependency backup:"),
	}, "\n")}
	limit := min(maxDependencyListLines, max(1, budget-surfaceHeight(top)-3))
	start, end := restoreWindow(len(backups), cursor, limit)
	lines := make([]string, 0, end-start+1)
	targets := make([]mouseTarget, 0, end-start+1)
	targets = append(targets, mouseTarget{
		rect:   cellRect{width: 58, height: end - start},
		action: mouseAction{kind: mouseScroll},
	})
	for index := start; index < end; index++ {
		backup := backups[index]
		prefix := "  "
		if index == cursor {
			prefix = "> "
		}
		line := t.DialogBodyStyle.Render(fmt.Sprintf(
			"%s%s  %s  %d update(s)", prefix, backup.Name, backup.Kind, backup.Updated,
		))
		lines = append(lines, line)
		targets = append(targets, mouseTarget{
			rect:   cellRect{y: index - start, width: lipgloss.Width(line), height: lipgloss.Height(line)},
			action: mouseAction{kind: mouseBackupRow, index: index, identity: backup.Name, path: backup.Path},
		})
	}
	if end < len(backups) {
		lines = append(lines, t.DialogMutedStyle.Render(fmt.Sprintf("  …and %d more", len(backups)-end)))
	}
	return joinSurfaces(
		top,
		renderedSurface{content: strings.Join(lines, "\n"), targets: targets},
		renderedSurface{content: " \n" + t.DialogMutedStyle.Render("Current go.mod and go.sum will be saved before restore.")},
	)
}
