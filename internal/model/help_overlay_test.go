package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/utils"
)

// pressKey dispatches a key press through the full Update path and
// returns the resulting model.
func pressKey(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, _ := m.Update(key)
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return next
}

// resizeModel delivers a WindowSizeMsg so the rendered app height
// matches the viewport. Without it the fallback viewport is shorter
// than the app and overlayDialog slices the hint bar off the bottom.
func resizeModel(t *testing.T, m Model, width, height int) Model {
	t.Helper()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return next
}

func TestHelpOverlayOpensWithQuestionMark(t *testing.T) {
	m := newTestModel(t)
	m = resizeModel(t, m, 100, 30)

	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	if !m.HelpVisible {
		t.Fatal("expected ? to open the Help overlay")
	}

	view := stripANSI(m.View().Content)
	for _, want := range []string{"Keyboard Shortcuts", "AVAILABLE", "install", "GLOBAL"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected overlay to contain %q, got:\n%s", want, view)
		}
	}
}

func TestHelpOverlaySwallowsKeysAndCloses(t *testing.T) {
	m := newTestModel(t)
	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})

	// While the overlay is open, tab must not switch tabs and q must
	// not quit (nil command, overlay still open).
	for _, key := range []tea.KeyPressMsg{
		{Code: '\t'},
		{Code: 'q'},
		{Code: 'i'},
	} {
		before := m.CurrentTab
		updated, cmd := m.Update(key)
		next := updated.(Model)
		if !next.HelpVisible {
			t.Fatalf("key %q must not close the overlay", key.String())
		}
		if next.CurrentTab != before {
			t.Fatalf("key %q must be swallowed, but the tab changed to %d", key.String(), next.CurrentTab)
		}
		if cmd != nil {
			t.Fatalf("key %q must be swallowed, but got command %T", key.String(), cmd)
		}
		m = next
	}

	// esc closes without side effects.
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.HelpVisible {
		t.Fatal("expected esc to close the Help overlay")
	}

	// ? toggles as well.
	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	if m.HelpVisible {
		t.Fatal("expected ? to toggle the Help overlay closed")
	}
}

func TestHelpOverlayCtrlCStillQuits(t *testing.T) {
	m := newTestModel(t)
	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected ctrl+c to quit while the overlay is open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected ctrl+c to produce tea.QuitMsg while the overlay is open")
	}
}

func TestHelpOverlayShowsDialogContext(t *testing.T) {
	m := confirmApplyFrom(t, resizeModel(t, newTestModel(t), 100, 30))

	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	if !m.HelpVisible {
		t.Fatal("expected ? to open the overlay above an open dialog")
	}

	view := stripANSI(m.View().Content)
	for _, want := range []string{"Keyboard Shortcuts", "UPDATE DEPENDENCIES", "accept"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected overlay over dialog to contain %q, got:\n%s", want, view)
		}
	}
	// The dialog's section replaces the tab section: neither the tab
	// section header nor tab-only binding descriptions may appear.
	// ("install" alone would false-positive on the list's
	// "installed" labels underneath the overlay.)
	for _, forbidden := range []string{"AVAILABLE", "move cursor"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("overlay over a dialog must not show tab bindings (%q present), got:\n%s", forbidden, view)
		}
	}
}

func TestHelpOverlayNotAvailableInMinimumViewport(t *testing.T) {
	m := newTestModel(t)
	m.TermWidth = 60
	m.TermHeight = 24

	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	if m.HelpVisible {
		t.Fatal("the overlay must not open in the minimum viewport")
	}
}

func TestHelpOverlayNotAvailableInTextInput(t *testing.T) {
	m := newTestModel(t)
	// Open the limit editor the way the user does: navigate to the
	// Deps backups row and press enter.
	m = focusSetting(t, m, settingRowDepsBackups)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// "?" is ordinary input while a text field has focus: the overlay
	// must not open. The numeric validator rejects the character, but
	// the key is still consumed by the input, not by the overlay.
	m = pressKey(t, m, tea.KeyPressMsg{Code: '?'})
	if m.HelpVisible {
		t.Fatal("the overlay must not open while a text input has focus")
	}
	if view := stripANSI(m.View().Content); strings.Contains(view, "Keyboard Shortcuts") {
		t.Fatalf("the overlay must not render while a text input has focus, got:\n%s", view)
	}
}

func TestRenderHelpOverlayTruncatesToViewport(t *testing.T) {
	m := newTestModel(t)

	// The full content (title + Available section + Global section)
	// is taller than six rows; the overlay must truncate instead of
	// overflowing the viewport.
	got := renderHelpOverlay(testTheme(), m, viewportSize{Width: 64, Height: 6}).content
	lines := strings.Split(stripANSI(got), "\n")
	if len(lines) > 6 {
		t.Fatalf("overlay height = %d lines, want <= 6:\n%s", len(lines), got)
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("expected non-empty overlay, got:\n%s", got)
	}
}

func TestHelpOverlaySectionsResolveContext(t *testing.T) {
	m := newTestModel(t)

	if sections := helpOverlaySections(m); sections[0].title != "Available" {
		t.Fatalf("plain model context = %q, want Available", sections[0].title)
	}

	seedVersions(t, &m, []utils.GoVersion{{Version: "1.23.0", Installed: true, Path: "/p/1.23.0"}})
	m = press(t, m, tea.KeyPressMsg{Code: 'd'})
	if sections := helpOverlaySections(m); sections[0].title != "Confirm delete" {
		t.Fatalf("delete confirmation context = %q, want Confirm delete", sections[0].title)
	}

	m = press(t, m, tea.KeyPressMsg{Code: 'n'}, tea.KeyPressMsg{Code: tea.KeyTab})
	confirmPrune(t, &m)
	if sections := helpOverlaySections(m); sections[0].title != "Confirm prune" {
		t.Fatalf("prune confirmation context = %q, want Confirm prune", sections[0].title)
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	withDialog(&m, func(m Model) Model { return confirmChecksFrom(t, m) })
	if sections := helpOverlaySections(m); sections[0].title != "Run checks" {
		t.Fatalf("dialog context = %q, want Run checks", sections[0].title)
	}
}
