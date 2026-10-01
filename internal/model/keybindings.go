package model

import tea "charm.land/bubbletea/v2"

type mouseControl struct {
	label string
	key   tea.KeyPressMsg
}

func mouseControls(label string, key tea.KeyPressMsg) [2]mouseControl {
	return [2]mouseControl{{label: label, key: key}}
}

func directionalControls(previous, next rune, vertical bool) [2]mouseControl {
	left, right := "← previous", "→ next"
	if vertical {
		left, right = "↑ previous", "↓ next"
	}
	return [2]mouseControl{
		{label: left, key: tea.KeyPressMsg{Code: previous}},
		{label: right, key: tea.KeyPressMsg{Code: next}},
	}
}

// This registry drives the wrapped action bar and the Help overlay.
// Mouse controls carry canonical keys rather than parsing displayed hints.
//
// The registry lists only bindings the key-dispatch code actually
// accepts: handleKey, handleDialogKey, handleSettingsKey, or
// depsDialog.Handle must recognise every documented key.

// keyBinding documents a keyboard command and its optional visible controls.
type keyBinding struct {
	keys          string
	desc          string
	mouseControls [2]mouseControl
}

// helpSection is a titled group of bindings. Tab sections, dialog
// sections, and confirmation sections carry the bindings of one
// input context; the global sections list bindings that work across
// a whole context family.
type helpSection struct {
	title    string
	bindings []keyBinding
}

// globalKeyBindings lists commands available on every tab. Tab switching is
// documented in Help; the tab labels themselves provide mouse navigation.
func globalKeyBindings() helpSection {
	return helpSection{
		title: "Global",
		bindings: []keyBinding{
			{keys: "tab", desc: "next tab"},
			{keys: "shift+tab", desc: "previous tab"},
			{keys: "?", desc: "help", mouseControls: mouseControls("? help", tea.KeyPressMsg{Code: '?'})},
			{keys: "q / ctrl+c", desc: "quit", mouseControls: mouseControls("q / ctrl+c quit", tea.KeyPressMsg{Code: 'q'})},
		},
	}
}

// dialogGlobalKeyBindings lists the keys that work while any modal
// dialog or confirmation is open. Tab and Shift+Tab are deliberately
// absent: inside dialogs they toggle the highlighted choice instead
// of switching tabs.
func dialogGlobalKeyBindings() helpSection {
	return helpSection{
		title: "Global",
		bindings: []keyBinding{
			{keys: "?", desc: "help", mouseControls: mouseControls("? help", tea.KeyPressMsg{Code: '?'})},
			{keys: "q / ctrl+c", desc: "quit", mouseControls: mouseControls("q / ctrl+c quit", tea.KeyPressMsg{Code: 'q'})},
		},
	}
}

// tabKeyBindings returns the section describing the bindings of one
// tab. Cursor movement is overlay-only on the list/table tabs (the
// hint bar keeps it on Settings, which has few other actions).
func tabKeyBindings(tab int) helpSection {
	move := keyBinding{keys: "↑/↓ k/j", desc: "move cursor"}
	switch tab {
	case AvailableTab:
		return helpSection{
			title: "Available",
			bindings: []keyBinding{
				{keys: "i", desc: "install", mouseControls: mouseControls("i install", tea.KeyPressMsg{Code: 'i'})},
				{keys: "u", desc: "use", mouseControls: mouseControls("u use", tea.KeyPressMsg{Code: 'u'})},
				{keys: "d", desc: "delete", mouseControls: mouseControls("d delete", tea.KeyPressMsg{Code: 'd'})},
				{keys: "r", desc: "refresh", mouseControls: mouseControls("r refresh", tea.KeyPressMsg{Code: 'r'})},
				{keys: "f", desc: "find", mouseControls: mouseControls("f find", tea.KeyPressMsg{Code: 'f'})},
				move,
			},
		}
	case InstalledTab:
		return helpSection{
			title: "Installed",
			bindings: []keyBinding{
				{keys: "u", desc: "use", mouseControls: mouseControls("u use", tea.KeyPressMsg{Code: 'u'})},
				{keys: "d", desc: "delete", mouseControls: mouseControls("d delete", tea.KeyPressMsg{Code: 'd'})},
				{keys: "p", desc: "prune", mouseControls: mouseControls("p prune", tea.KeyPressMsg{Code: 'p'})},
				{keys: "r", desc: "refresh", mouseControls: mouseControls("r refresh", tea.KeyPressMsg{Code: 'r'})},
				move,
			},
		}
	case DepsTab:
		return helpSection{
			title: "Deps",
			bindings: []keyBinding{
				{keys: "r", desc: "check updates", mouseControls: mouseControls("r check updates", tea.KeyPressMsg{Code: 'r'})},
				{keys: "space", desc: "mark", mouseControls: mouseControls("space mark", tea.KeyPressMsg{Code: tea.KeySpace})},
				{keys: "a", desc: "mark all / none", mouseControls: mouseControls("a mark all / none", tea.KeyPressMsg{Code: 'a'})},
				{keys: "u", desc: "update", mouseControls: mouseControls("u update", tea.KeyPressMsg{Code: 'u'})},
				{keys: "b", desc: "backups", mouseControls: mouseControls("b backups", tea.KeyPressMsg{Code: 'b'})},
				move,
			},
		}
	case SettingsTab:
		return helpSection{
			title: "Settings",
			bindings: []keyBinding{
				{keys: "↑/↓ k/j", desc: "move", mouseControls: directionalControls(tea.KeyUp, tea.KeyDown, true)},
				{keys: "enter / space", desc: "toggle or edit", mouseControls: mouseControls("enter / space toggle or edit", tea.KeyPressMsg{Code: tea.KeyEnter})},
				{keys: "←/→ h/l", desc: "toggle or adjust", mouseControls: directionalControls(tea.KeyLeft, tea.KeyRight, false)},
			},
		}
	}
	return helpSection{title: "Tabs", bindings: nil}
}

// dialogKeyBindings returns the section describing the open
// dependency dialog. The restore dialog adds backup navigation and
// re-labels enter to the highlighted button, mirroring the hint bar.
func dialogKeyBindings(dialog depsDialog) helpSection {
	var bindings []keyBinding
	title := "Dialog"
	escDesc := "cancel"
	enterDesc := "confirm"

	switch dialog.kind {
	case dialogUpdate:
		title = "Update dependencies"
		bindings = append(bindings, keyBinding{keys: "↑/↓ k/j", desc: "level"})
		if dialog.canToggleScope() {
			bindings = append(bindings, keyBinding{keys: "space", desc: "scope"})
		}
	case dialogChecks:
		title = "Run checks"
		escDesc = "skip"
	case dialogRollback:
		title = "Roll back"
	case dialogRestore:
		title = "Restore backup"
		escDesc = "cancel"
		enterDesc = "cancel"
		if dialog.choiceYes {
			enterDesc = "restore"
		}
		bindings = append(bindings, keyBinding{keys: "↑/↓ k/j", desc: "select backup"})
	}

	bindings = append(bindings,
		keyBinding{keys: "←/→ h/l", desc: "choose"},
		keyBinding{keys: "enter", desc: enterDesc},
		keyBinding{keys: "y", desc: "accept"},
		keyBinding{keys: "n / esc", desc: escDesc},
	)
	return helpSection{title: title, bindings: bindings}
}

// confirmDeleteKeyBindings is the section shown while a deletion
// awaits its y/n confirmation.
func confirmDeleteKeyBindings() helpSection {
	return helpSection{
		title: "Confirm delete",
		bindings: []keyBinding{
			{keys: "y", desc: "confirm", mouseControls: mouseControls("y confirm", tea.KeyPressMsg{Code: 'y'})},
			{keys: "n", desc: "cancel", mouseControls: mouseControls("n cancel", tea.KeyPressMsg{Code: 'n'})},
		},
	}
}

// confirmPruneKeyBindings is the section shown while the prune dialog
// awaits its answer. It lists the same keys as the Deps dialogs.
func confirmPruneKeyBindings() helpSection {
	return helpSection{
		title: "Confirm prune",
		bindings: []keyBinding{
			{keys: "←/→ h/l", desc: "choose"},
			{keys: "enter", desc: "confirm"},
			{keys: "y", desc: "accept"},
			{keys: "n / esc", desc: "cancel"},
		},
	}
}

// editingKeyBindings returns the bar-only section for the Settings
// text inputs. The Help overlay cannot open while an input has focus
// ("?" is ordinary input there), so these bindings never appear in
// the overlay.
func editingKeyBindings(editingSource bool) helpSection {
	if editingSource {
		return helpSection{
			title: "Edit distribution source",
			bindings: []keyBinding{
				{keys: "enter", desc: "check and save", mouseControls: mouseControls("enter check and save", tea.KeyPressMsg{Code: tea.KeyEnter})},
				{keys: "r", desc: "reset", mouseControls: mouseControls("r reset to official", tea.KeyPressMsg{Code: 'r'})},
				{keys: "esc", desc: "cancel", mouseControls: mouseControls("esc cancel", tea.KeyPressMsg{Code: tea.KeyEscape})},
			},
		}
	}
	return helpSection{
		title: "Edit backup limit",
		bindings: []keyBinding{
			{keys: "enter", desc: "save", mouseControls: mouseControls("enter save", tea.KeyPressMsg{Code: tea.KeyEnter})},
			{keys: "esc", desc: "cancel", mouseControls: mouseControls("esc cancel", tea.KeyPressMsg{Code: tea.KeyEscape})},
		},
	}
}

// filterInputKeyBindings is the bar-only section shown while the
// Available list's filter input has focus. q and ? are deliberately
// absent: they are ordinary input characters while typing. The Help
// overlay cannot open in this state (? is ordinary input there), so these
// bindings never appear in the overlay.
func filterInputKeyBindings() helpSection {
	return helpSection{
		title: "Find input",
		bindings: []keyBinding{
			{keys: "enter", desc: "apply", mouseControls: mouseControls("enter apply", tea.KeyPressMsg{Code: tea.KeyEnter})},
			{keys: "esc", desc: "clear", mouseControls: mouseControls("esc clear", tea.KeyPressMsg{Code: tea.KeyEscape})},
			{keys: "tab", desc: "next tab", mouseControls: mouseControls("tab next tab", tea.KeyPressMsg{Code: tea.KeyTab})},
			{keys: "ctrl+c", desc: "quit", mouseControls: mouseControls("ctrl+c quit", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})},
		},
	}
}

// helpOverlayBarBindings is the hint bar shown while the Help overlay
// itself is open. q is deliberately absent: while the overlay is open
// q is swallowed and only ctrl+c quits.
func helpOverlayBarBindings() helpSection {
	return helpSection{
		title: "Help overlay",
		bindings: []keyBinding{
			{keys: "? / esc", desc: "close help", mouseControls: mouseControls("esc close help", tea.KeyPressMsg{Code: tea.KeyEscape})},
			{keys: "ctrl+c", desc: "quit", mouseControls: mouseControls("ctrl+c quit", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})},
		},
	}
}
