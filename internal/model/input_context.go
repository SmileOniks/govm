package model

// inputContext names the Input context (see CONTEXT.md): the one mode
// that owns the keyboard at a given moment. Exactly one context is
// active at any time; the Model's mode flags are mutually exclusive
// and inputContext resolves them in a single canonical order, so the
// key dispatcher, the hint bar, the Help overlay, the overlay renderer
// and the "may this mode open?" guards all agree on who is in charge.
type inputContext int

const (
	// inputTab is the plain state: nothing owns the keyboard and keys
	// are commands of the current tab.
	inputTab inputContext = iota
	// inputFilter: the Available tab's filter input has focus.
	inputFilter
	// inputDeleteConfirm: the inline delete confirmation awaits y/n.
	inputDeleteConfirm
	// inputPruneConfirm: the prune plan dialog awaits its answer.
	inputPruneConfirm
	// inputDepsDialog: a Deps confirmation dialog is open.
	inputDepsDialog
	// inputHelpOverlay: the Help overlay is open above the context it
	// describes.
	inputHelpOverlay
	// inputSettingsInput: a Settings text input has focus.
	inputSettingsInput
)

// String names the context for test failures and logs.
func (c inputContext) String() string {
	switch c {
	case inputTab:
		return "tab"
	case inputFilter:
		return "filter input"
	case inputDeleteConfirm:
		return "delete confirmation"
	case inputPruneConfirm:
		return "prune confirmation"
	case inputDepsDialog:
		return "deps dialog"
	case inputHelpOverlay:
		return "help overlay"
	case inputSettingsInput:
		return "settings input"
	}
	return "unknown"
}

// textEntry reports whether the context consumes ordinary characters
// as text. In a text-entry context q and ? are input, not commands,
// so neither quitting nor the Help overlay is reachable from them.
func (c inputContext) textEntry() bool {
	return c == inputSettingsInput || c == inputFilter
}

// inputContext resolves the active Input context. The order is the
// canonical priority: a Settings input is the innermost mode (it is
// opened from the Settings tab and nothing opens above it), the Help
// overlay sits above every choice mode, the choice modes exclude each
// other by tab, and the filter input is the outermost mode because a
// pending confirmation keeps it from opening.
//
// The pointer receiver is deliberate: this runs on every key press
// and every render, and a value receiver would copy the Model.
func (m *Model) inputContext() inputContext {
	return m.resolveInputContext(m.HelpVisible)
}

// inputContextBeneathHelp resolves the context the Help overlay
// describes: the same resolution with the overlay itself ignored.
func (m *Model) inputContextBeneathHelp() inputContext {
	return m.resolveInputContext(false)
}

func (m *Model) resolveInputContext(helpVisible bool) inputContext {
	switch {
	case m.settings.textInputActive():
		return inputSettingsInput
	case helpVisible:
		return inputHelpOverlay
	case m.deps.dialogActive():
		return inputDepsDialog
	case m.Prune.Confirming():
		return inputPruneConfirm
	case m.ConfirmingDelete:
		return inputDeleteConfirm
	case m.filterInputActive():
		return inputFilter
	default:
		return inputTab
	}
}

// canOpenMode reports whether a new keyboard-owning mode (the filter
// input, an inline confirmation) may open now: nothing else owns the
// keyboard and the viewport is large enough to render it.
func (m *Model) canOpenMode() bool {
	return m.inputContext() == inputTab && !m.inMinimumViewport()
}

// canOpenHelp reports whether ? opens the Help overlay now. The
// overlay opens above every choice mode (dialogs, confirmations) but
// not above text entry, where ? is an ordinary character, and not in
// the minimum viewport, where it would not render.
func (m *Model) canOpenHelp() bool {
	ctx := m.inputContext()
	return ctx != inputHelpOverlay && !ctx.textEntry() && !m.inMinimumViewport()
}

// contextKeyBindings resolves the registry sections (ADR-0001) that
// describe the given context: the mode's own section followed by the
// matching global section. The hint bar renders the sections of the
// active context; the Help overlay renders the sections of the context
// beneath it.
func contextKeyBindings(m Model, ctx inputContext) []helpSection {
	switch ctx {
	case inputSettingsInput:
		return []helpSection{editingKeyBindings(m.settings.editingSource())}
	case inputHelpOverlay:
		return []helpSection{helpOverlayBarBindings()}
	case inputDepsDialog:
		return []helpSection{m.deps.dialogKeyBindings(), dialogGlobalKeyBindings()}
	case inputPruneConfirm:
		return []helpSection{confirmPruneKeyBindings(), dialogGlobalKeyBindings()}
	case inputDeleteConfirm:
		return []helpSection{confirmDeleteKeyBindings(), dialogGlobalKeyBindings()}
	case inputFilter:
		return []helpSection{filterInputKeyBindings()}
	default:
		return []helpSection{tabKeyBindings(m.CurrentTab), globalKeyBindings()}
	}
}
