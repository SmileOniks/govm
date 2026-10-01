package model

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

// openFilter presses "f" through the production Update path and fails
// the test unless the filter input takes focus.
func openFilter(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f'})
	got := updated.(Model)
	if !got.filterInputActive() {
		t.Fatal("expected 'f' to focus the filter input")
	}
	return got
}

// settleCmd executes a command returned by Update and feeds every
// produced message back through Update, expanding tea.BatchMsg so the
// list's asynchronous FilterMatchesMsg (batched in with the input's
// cursor blink) actually lands.
func settleCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, inner := range msg {
			m = settleCmd(t, m, inner)
		}
		return m
	default:
		updated, _ := m.Update(msg)
		return updated.(Model)
	}
}

// typeIntoFilter sends runes through the production Update path and
// settles the asynchronous filter pipeline so the visible items
// reflect everything typed so far. Intermediate per-keystroke
// commands are superseded by the last one and can be skipped safely.
func typeIntoFilter(t *testing.T, m Model, text string) Model {
	t.Helper()
	var cmd tea.Cmd
	for _, r := range text {
		// bubbletea v2 delivers printable runes in Text (Code alone is
		// not enough for the text input to insert a character).
		updated, c := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(Model)
		cmd = c
	}
	return settleCmd(t, m, cmd)
}

// applyFilter opens the input, types the query, and commits it with
// enter, leaving the list in the FilterApplied state.
func applyFilter(t *testing.T, m Model, query string) Model {
	t.Helper()
	m = openFilter(t, m)
	m = typeIntoFilter(t, m, query)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.projection.availableFilterApplied() {
		t.Fatalf("expected enter to commit filter %q", query)
	}
	return m
}

func TestFindKeyOpensFilterInput(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})

	m = openFilter(t, m)

	if got := m.projection.availableModel().FilterInput.Value(); got != "" {
		t.Fatalf("filter input = %q, want empty on open", got)
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, "Find: ") {
		t.Fatalf("expected the open input to show the Find prompt, got:\n%s", view)
	}
}

func TestFindKeyIsInertOffAvailableTab(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = InstalledTab

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f'})
	m = updated.(Model)

	if m.projection.availableSettingFilter() {
		t.Fatal("expected 'f' to be inert on the Installed tab")
	}
}

func TestFilterInputCapturesCommandKeys(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
		{Version: "1.25.0"},
	})
	m = openFilter(t, m)

	// Every command key must land in the input instead: q (quit), ?
	// (help), y/n (delete confirm), i/u/d/r (actions), f (find itself).
	for _, key := range []rune{'q', '?', 'y', 'n', 'i', 'u', 'd', 'r', 'f'} {
		updated, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		m = updated.(Model)
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				t.Fatalf("key %q must not quit while the filter input has focus", key)
			}
		}
	}

	if got, want := m.projection.availableModel().FilterInput.Value(), "q?yniudrf"; got != want {
		t.Fatalf("filter input = %q, want %q", got, want)
	}
	if m.HelpVisible {
		t.Fatal("? must not open the Help overlay while the filter input has focus")
	}
	if m.inputContext() != inputFilter {
		t.Fatal("command keys must leave the filter input in control")
	}
}

func TestFilterTypingNarrowsAndEnterCommits(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
		{Version: "1.25.0"},
	})

	m = openFilter(t, m)
	m = typeIntoFilter(t, m, "1.25")

	if got := m.projection.availableModel().FilterInput.Value(); got != "1.25" {
		t.Fatalf("filter input = %q, want %q", got, "1.25")
	}
	if got := len(m.projection.availableModel().VisibleItems()); got != 1 {
		t.Fatalf("visible items while typing = %d, want 1", got)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if !m.projection.availableFilterApplied() {
		t.Fatal("expected enter to commit the filter")
	}
	if m.filterInputActive() {
		t.Fatal("expected the input to lose focus after enter")
	}
	if got := selectedListVersion(m); got != "1.25.0" {
		t.Fatalf("selected version after commit = %q, want 1.25.0", got)
	}
}

func TestFilterCommandsWorkAfterCommit(t *testing.T) {
	m := newTestModel(t)
	var deleted string
	m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
		deleted = version
		return lifecycle.DeletionResult{Version: version}, nil
	}})
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Active: true, Path: "/p/1.24.4"},
		{Version: "1.25.0", Installed: true, Path: "/p/1.25.0"},
	})
	m = applyFilter(t, m, "1.25")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	m = updated.(Model)

	if m.inputContext() != inputDeleteConfirm || !strings.Contains(m.Status.Text(), "delete Go 1.25.0?") {
		t.Fatalf("filtered delete prompt = %q, context=%v", m.Status.Text(), m.inputContext())
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if deleted != "1.25.0" || m.inputContext() == inputDeleteConfirm {
		t.Fatalf("filtered deletion target=%q, context=%v", deleted, m.inputContext())
	}
}

func TestEscClearsCommittedFilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})
	m = applyFilter(t, m, "1.25")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.projection.availableFilterApplied() || m.projection.availableSettingFilter() {
		t.Fatal("expected esc to clear the committed filter")
	}
	if got := m.projection.availableModel().FilterInput.Value(); got != "" {
		t.Fatalf("filter input after esc = %q, want empty", got)
	}
	if got := len(m.projection.availableModel().VisibleItems()); got != 2 {
		t.Fatalf("visible items after esc = %d, want 2", got)
	}
}

func TestEscCancelsWhileTyping(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})
	m = openFilter(t, m)
	m = typeIntoFilter(t, m, "1.25")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.projection.availableSettingFilter() || m.projection.availableFilterApplied() {
		t.Fatal("expected esc to cancel the filter while typing")
	}
	if got := len(m.projection.availableModel().VisibleItems()); got != 2 {
		t.Fatalf("visible items after esc = %d, want 2", got)
	}
}

func TestFilterSurvivesTabSwitch(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})
	m = applyFilter(t, m, "1.25")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.CurrentTab != InstalledTab {
		t.Fatalf("current tab = %d, want InstalledTab", m.CurrentTab)
	}
	if m.filterInputActive() {
		t.Fatal("filter input must not claim the keyboard on another tab")
	}
	if !m.projection.availableFilterApplied() {
		t.Fatal("expected the committed filter to survive leaving the tab")
	}

	updated, _ = m.Update(shiftTab())
	m = updated.(Model)
	if m.CurrentTab != AvailableTab {
		t.Fatalf("current tab = %d, want AvailableTab", m.CurrentTab)
	}
	if !m.projection.availableFilterApplied() {
		t.Fatal("expected the committed filter to survive returning to the tab")
	}
	if got := m.projection.availableModel().FilterInput.Value(); got != "1.25" {
		t.Fatalf("filter text after round trip = %q, want 1.25", got)
	}
}

func TestFindKeyBlockedDuringDeleteConfirmation(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
	})
	m = press(t, m, tea.KeyPressMsg{Code: 'd'})
	if m.inputContext() != inputDeleteConfirm {
		t.Fatal("expected delete confirmation before pressing f")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f'})
	m = updated.(Model)

	if m.projection.availableSettingFilter() {
		t.Fatal("expected 'f' to be inert while a delete confirmation is pending")
	}
}

func TestFindKeyBlockedDuringPruneConfirmation(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	confirmPrune(t, &m)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f'})
	m = updated.(Model)

	if m.projection.availableSettingFilter() {
		t.Fatal("expected 'f' to be inert while a prune confirmation is pending")
	}
	if m.inputContext() != inputPruneConfirm || m.CurrentTab != InstalledTab {
		t.Fatal("f must leave the Installed prune dialog in control")
	}
}

func TestFindKeyBlockedInMinimumViewport(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
	})
	m.TermWidth = 40
	m.TermHeight = 10

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'f'})
	m = updated.(Model)

	if m.projection.availableSettingFilter() {
		t.Fatal("expected 'f' to be inert below the minimum terminal size")
	}
}

func TestAppliedFilterIndicatorLine(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})

	if view := stripANSI(m.View().Content); strings.Contains(view, "find:") {
		t.Fatalf("did not expect a filter indicator without a filter, got:\n%s", view)
	}

	m = applyFilter(t, m, "1.25")
	view := stripANSI(m.View().Content)

	for _, want := range []string{`find: "1.25"`, "1/2", "esc clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected applied-filter indicator to contain %q, got:\n%s", want, view)
		}
	}
}

func TestFilterHintBar(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
		{Version: "1.25.0"},
	})

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "f find") {
		t.Fatalf("expected the Available hint bar to advertise the filter key, got:\n%s", view)
	}

	m = openFilter(t, m)
	view = stripANSI(m.View().Content)

	for _, want := range []string{"enter apply", "esc clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected the filter-input hint bar to contain %q, got:\n%s", want, view)
		}
	}
	for _, stale := range []string{"i install", "? help", "q / ctrl+c quit"} {
		if strings.Contains(view, stale) {
			t.Fatalf("filter-input hint bar must not contain %q (the key is ordinary input), got:\n%s", stale, view)
		}
	}
}

func TestFilterProgramMessageKeepsRepeatedRWhileFiltering(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4"},
	})
	m = openFilter(t, m)
	program := newProgramModel(m)

	repeat := tea.KeyPressMsg{Code: 'r', IsRepeat: true}
	if got := FilterProgramMessage(program, repeat); got == nil {
		t.Fatal("expected a repeated r to reach Update while the filter input has focus")
	}
}

// TestFilterHighlightKeepsTitleANSIIntact reproduces filter-match
// highlighting corrupting the pre-rendered title: with the filter
// narrowing the list, no row may expose a leaked SGR fragment such as
// "[1;38;2;229;231;235m" once the real escape sequences are stripped.
func TestFilterHighlightKeepsTitleANSIIntact(t *testing.T) {
	m := newTestModel(t)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = resized.(Model)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
		{Version: "1.25.0"},
	})

	m = openFilter(t, m)
	m = typeIntoFilter(t, m, "1")

	raw := m.View().Content
	if !regexp.MustCompile(`\x1b\[(?:[0-9]+;)*4(?:;[0-9]+)*m1`).MatchString(raw) {
		t.Fatalf("expected the matched rune to be underlined, got:\n%q", raw)
	}
	plain := stripANSI(raw)
	if leaked := regexp.MustCompile(`\[[0-9;]*m`).FindString(plain); leaked != "" {
		t.Fatalf("filtered view leaks SGR fragment %q:\n%s", leaked, plain)
	}
	for _, want := range []string{"1.24.4", "1.25.0"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("filtered view missing %q:\n%s", want, plain)
		}
	}
}

// TestSlashNoLongerOpensFilterInput guards the rebinding from "/" to
// "f": the widget's default key must stay inert on the Available tab.
func TestSlashNoLongerOpensFilterInput(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4"}, {Version: "1.25.0"}})

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	got := updated.(Model)
	if got.filterInputActive() {
		t.Fatal("expected '/' to stay inert now that find is bound to 'f'")
	}
}
