package model

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

func TestMinimumViewportShowsWarningWithinTerminalBounds(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{name: "narrow", width: 63, height: 20},
		{name: "short", width: 64, height: 19},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.TermWidth = tt.width
			m.TermHeight = tt.height
			beforeWidth, beforeHeight := m.TermWidth, m.TermHeight

			view := m.View()
			content := stripANSI(view.Content)

			if !view.AltScreen {
				t.Fatal("AltScreen = false, want true")
			}
			if view.BackgroundColor != testTheme().MinimumViewportBackground {
				t.Fatalf("BackgroundColor = %v, want %v", view.BackgroundColor, testTheme().MinimumViewportBackground)
			}
			wants := []string{
				"Minimum terminal size is 64x20.",
				"Current size: " + strconv.Itoa(tt.width) + "x" + strconv.Itoa(tt.height) + ".",
			}
			for _, want := range wants {
				found := false
				for _, line := range strings.Split(content, "\n") {
					if strings.TrimSpace(line) == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("view is missing centered line %q:\n%s", want, content)
				}
			}
			assertMinimumViewportCentered(t, content, tt.width, tt.height, wants)
			assertViewportBounds(t, view, tt.width, tt.height)
			if m.TermWidth != beforeWidth || m.TermHeight != beforeHeight {
				t.Fatal("View mutated the model")
			}
		})
	}
}

func TestMinimumViewportIsNotShownAtMinimumSize(t *testing.T) {
	m := newTestModel(t)
	m.TermWidth = styles.MinTermWidth
	m.TermHeight = styles.MinTermHeight

	content := stripANSI(m.View().Content)
	if strings.Contains(content, "Minimum terminal size is") {
		t.Fatalf("minimum-size viewport unexpectedly shows warning:\n%s", content)
	}
}

func TestMinimumViewportOneByOneStaysWithinBounds(t *testing.T) {
	m := newTestModel(t)
	m.TermWidth = 1
	m.TermHeight = 1

	assertViewportBounds(t, m.View(), 1, 1)
}

func TestMinimumViewportUpdateZeroUsesSafeView(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	m = updated.(Model)

	view := m.View()
	if !view.AltScreen {
		t.Fatal("AltScreen = false, want true")
	}
	if view.BackgroundColor != testTheme().MinimumViewportBackground {
		t.Fatalf("BackgroundColor = %v, want %v", view.BackgroundColor, testTheme().MinimumViewportBackground)
	}
	assertViewportBounds(t, view, 1, 1)
}

func TestMinimumViewportRendererNormalizesExtremeDimensions(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{name: "zero", width: 0, height: 0},
		{name: "negative", width: -1, height: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := renderMinimumViewport(testTheme(), tt.width, tt.height)
			lines := strings.Split(content, "\n")

			if len(lines) != 1 {
				t.Fatalf("rendered height = %d, want 1: %q", len(lines), content)
			}
			if got := ansi.StringWidth(lines[0]); got != 1 {
				t.Fatalf("rendered width = %d, want 1: %q", got, lines[0])
			}
		})
	}
}

func assertMinimumViewportCentered(t *testing.T, content string, width, height int, wants []string) {
	t.Helper()

	lines := strings.Split(content, "\n")
	firstRow := -1
	messageWidth := 0
	for _, want := range wants {
		messageWidth = max(messageWidth, ansi.StringWidth(want))
	}
	for _, want := range wants {
		row := -1
		column := -1
		for i, line := range lines {
			if index := strings.Index(line, want); index >= 0 {
				row = i
				column = ansi.StringWidth(line[:index])
				break
			}
		}
		if row < 0 {
			t.Fatalf("view is missing line %q:\n%s", want, content)
		}
		if firstRow < 0 {
			firstRow = row
		}

		expectedColumn := (width - messageWidth) / 2
		if delta := column - expectedColumn; delta < -1 || delta > 1 {
			t.Errorf("line %q column = %d, want centered at %d (±1)", want, column, expectedColumn)
		}
	}

	expectedFirstRow := (height - len(wants)) / 2
	if delta := firstRow - expectedFirstRow; delta < -1 || delta > 1 {
		t.Errorf("first message row = %d, want centered at %d (±1)", firstRow, expectedFirstRow)
	}
}

func assertViewportBounds(t *testing.T, view tea.View, width, height int) {
	t.Helper()

	lines := strings.Split(view.Content, "\n")
	if len(lines) != height {
		t.Fatalf("view height = %d, want %d:\n%s", len(lines), height, view.Content)
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("line width = %d, want %d: %q", got, width, line)
		}
	}
}

func TestWindowSizeMsgRespectsMinimumViewportHeight(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 20})
	m = updated.(Model)

	if m.Height != 12 {
		t.Fatalf("content height = %d, want 12", m.Height)
	}

	lineCount := len(strings.Split(stripANSI(m.View().Content), "\n"))
	if lineCount > 20 {
		t.Fatalf("normal view line count = %d, want at most 20", lineCount)
	}
}

func TestViewReportsMinimumViewportSize(t *testing.T) {
	for _, tt := range []struct {
		name     string
		width    int
		height   int
		wantHint bool
	}{
		{name: "one column too narrow", width: 63, height: 20, wantHint: true},
		{name: "one row too short", width: 64, height: 19, wantHint: true},
		{name: "minimum supported viewport", width: 64, height: 20, wantHint: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m = updated.(Model)

			view := stripANSI(m.View().Content)
			const minimumSizeHint = "Minimum terminal size is 64x20."
			if got := strings.Contains(view, minimumSizeHint); got != tt.wantHint {
				t.Fatalf("minimum-size hint present = %t, want %t for %dx%d viewport:\n%s", got, tt.wantHint, tt.width, tt.height, view)
			}
		})
	}
}

func TestSettingsBackupLimitDialogFitsMinimumViewport(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDepsBackups)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 20})
	m = updated.(Model)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.settings.depsBackupLimitInput.SetValue("0")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	view := stripANSI(m.View().Content)
	for _, want := range []string{
		"Set dependency backup limit",
		"must be between 1 and 100",
		"enter: save  esc: cancel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected minimum-viewport dialog to contain %q, got:\n%s", want, view)
		}
	}
}
