package setup

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type failingShimResolver struct {
	err error
}

func (r failingShimResolver) ShimDir() (string, error) {
	return "", r.err
}

func TestNewWithResolverReturnsShimDirError(t *testing.T) {
	wantErr := errors.New("home directory unavailable")

	_, err := newWithResolver(failingShimResolver{err: wantErr})

	if !errors.Is(err, wantErr) {
		t.Fatalf("newWithResolver error = %v, want %v", err, wantErr)
	}
}

type fixedShimResolver struct {
	path string
}

func (r fixedShimResolver) ShimDir() (string, error) {
	return r.path, nil
}

func newSetupMouseModel(t *testing.T, width, height int) Model {
	t.Helper()
	m, err := newWithResolver(fixedShimResolver{path: "/tmp/govm-mouse-test/shim"})
	if err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if cmd != nil {
		t.Fatal("resize unexpectedly dispatched a command")
	}
	return updated.(Model)
}

func setupTextPosition(t *testing.T, view tea.View, text string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(view.Content), "\n") {
		if index := strings.Index(line, text); index >= 0 {
			return ansi.StringWidth(line[:index]), y
		}
	}
	t.Fatalf("visible text %q not found in:\n%s", text, ansi.Strip(view.Content))
	return 0, 0
}

func requireSetupQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("continue did not dispatch quit")
	}
	if msg := cmd(); msg != nil {
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		t.Fatalf("continue returned %T, want tea.QuitMsg", msg)
	}
	t.Fatal("continue command returned nil")
}

func TestSetupMouseContinue(t *testing.T) {
	t.Run("only the footer left click continues", func(t *testing.T) {
		m := newSetupMouseModel(t, 80, 24)
		view := m.View()
		if !view.AltScreen || view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
			t.Fatal("setup did not enable frame-relative terminal mouse input")
		}
		x, y := setupTextPosition(t, view, "[Enter continue]")
		titleX, titleY := setupTextPosition(t, view, "GoVM First-Time Setup")
		for _, tt := range []struct {
			name string
			msg  tea.MouseMsg
		}{
			{name: "instructions", msg: tea.MouseClickMsg{X: titleX, Y: titleY, Button: tea.MouseLeft}},
			{name: "footer gap", msg: tea.MouseClickMsg{X: x - 1, Y: y, Button: tea.MouseLeft}},
			{name: "past footer", msg: tea.MouseClickMsg{X: x + ansi.StringWidth("[Enter continue]"), Y: y, Button: tea.MouseLeft}},
			{name: "right", msg: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}},
			{name: "middle", msg: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseMiddle}},
			{name: "shift", msg: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: tea.ModShift}},
			{name: "ctrl", msg: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: tea.ModCtrl}},
			{name: "alt", msg: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: tea.ModAlt}},
			{name: "release", msg: tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}},
			{name: "motion", msg: tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}},
			{name: "wheel", msg: tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				if cmd := view.OnMouse(tt.msg); cmd != nil {
					t.Fatal("ignored mouse event dispatched a semantic action")
				}
				if _, cmd := m.Update(tt.msg); cmd != nil {
					t.Fatal("raw mouse event dispatched an action")
				}
			})
		}

		click := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
		if _, cmd := m.Update(click); cmd != nil {
			t.Fatal("raw click continued setup before frame hit testing")
		}
		action := view.OnMouse(click)
		if action == nil {
			t.Fatal("visible Continue button was not clickable")
		}
		updated, quit := m.Update(action())
		requireSetupQuit(t, quit)
		m = updated.(Model)
		release := tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
		if cmd := view.OnMouse(release); cmd != nil {
			t.Fatal("release repeated the Continue action")
		}
		if _, cmd := m.Update(release); cmd != nil {
			t.Fatal("raw release repeated the Continue action")
		}
	})

	t.Run("keyboard remains available", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			key  tea.KeyPressMsg
		}{
			{name: "enter", key: tea.KeyPressMsg{Code: tea.KeyEnter}},
			{name: "space", key: tea.KeyPressMsg{Code: tea.KeySpace}},
			{name: "q", key: tea.KeyPressMsg{Code: 'q'}},
			{name: "ctrl+c", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				m := newSetupMouseModel(t, 0, 0)
				_, quit := m.Update(tt.key)
				requireSetupQuit(t, quit)
			})
		}
	})

	t.Run("unknown dimensions have no targets", func(t *testing.T) {
		for _, size := range []struct {
			name          string
			width, height int
		}{
			{name: "zero", width: 0, height: 0},
			{name: "zero width", width: 0, height: 24},
			{name: "zero height", width: 80, height: 0},
		} {
			t.Run(size.name, func(t *testing.T) {
				m := newSetupMouseModel(t, size.width, size.height)
				if view := m.View(); view.OnMouse != nil {
					t.Fatal("unknown terminal dimensions exposed mouse targets")
				}
				updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				view := updated.(Model).View()
				x, y := setupTextPosition(t, view, "[Enter continue]")
				if view.OnMouse == nil || view.OnMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}) == nil {
					t.Fatal("resize did not restore the Continue target")
				}
			})
		}
	})

	t.Run("footer survives a short terminal", func(t *testing.T) {
		for _, size := range []struct {
			name          string
			width, height int
		}{
			{name: "one row", width: 16, height: 1},
			{name: "two rows", width: 16, height: 2},
			{name: "four rows", width: 32, height: 4},
		} {
			t.Run(size.name, func(t *testing.T) {
				m := newSetupMouseModel(t, size.width, size.height)
				view := m.View()
				x, y := setupTextPosition(t, view, "[Enter continue]")
				if y != size.height-1 {
					t.Fatalf("footer row = %d, want %d", y, size.height-1)
				}
				if lines := strings.Split(view.Content, "\n"); len(lines) != size.height {
					t.Fatalf("view has %d rows, want %d", len(lines), size.height)
				}
				click := view.OnMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				if click == nil {
					t.Fatal("short terminal hid the Continue target")
				}
				_, quit := m.Update(click())
				requireSetupQuit(t, quit)
			})
		}
	})
}

func TestSetupMouseScroll(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	m := newSetupMouseModel(t, 80, 12)
	view := m.View()
	x, y := setupTextPosition(t, view, "GoVM First-Time Setup")
	_, footerY := setupTextPosition(t, view, "[Enter continue]")
	if footerY != 11 {
		t.Fatalf("footer row = %d, want 11", footerY)
	}
	if strings.Contains(ansi.Strip(view.Content), "After adding to PATH") {
		t.Fatal("fixture does not have hidden instructions")
	}
	down := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
	updated, cmd := m.Update(down)
	if cmd != nil || updated.(Model).View().Content != view.Content {
		t.Fatal("raw wheel bypassed frame hit testing")
	}
	step := view.OnMouse(down)
	if step == nil {
		t.Fatal("wheel over instructions did not dispatch scrolling")
	}
	updated, cmd = m.Update(step())
	if cmd != nil {
		t.Fatal("scroll unexpectedly dispatched a command")
	}
	m = updated.(Model)
	scrolled := m.View()
	beforeLines := strings.Split(ansi.Strip(view.Content), "\n")
	afterLines := strings.Split(ansi.Strip(scrolled.Content), "\n")
	if afterLines[0] != beforeLines[1] {
		t.Fatal("wheel did not scroll instructions by exactly one rendered line")
	}
	if _, got := setupTextPosition(t, scrolled, "[Enter continue]"); got != footerY {
		t.Fatal("scroll moved the fixed footer")
	}
	up := view.OnMouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	updated, _ = m.Update(up())
	m = updated.(Model)
	if m.View().Content != view.Content {
		t.Fatal("wheel up did not restore the initial instruction frame")
	}

	for _, tt := range []struct {
		name string
		msg  tea.MouseMsg
	}{
		{name: "horizontal", msg: tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelRight}},
		{name: "modified", msg: tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown, Mod: tea.ModCtrl}},
		{name: "outside", msg: tea.MouseWheelMsg{X: -1, Y: y, Button: tea.MouseWheelDown}},
		{name: "footer", msg: tea.MouseWheelMsg{X: x, Y: footerY, Button: tea.MouseWheelDown}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if cmd := view.OnMouse(tt.msg); cmd != nil {
				t.Fatal("wheel outside the instruction area dispatched scrolling")
			}
		})
	}

	// Events from the same displayed frame must not lose fast wheel steps.
	for range 80 {
		updated, cmd = m.Update(step())
		if cmd != nil {
			t.Fatal("scroll dispatched a command instead of updating the viewport")
		}
		m = updated.(Model)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "After adding to PATH") {
		t.Fatal("wheel did not reveal the hidden final instructions")
	}
	if runtime.GOOS != "windows" {
		setupTextPosition(t, m.View(), "source ~/.bashrc")
	}
	atBottom := m.View().Content
	updated, _ = m.Update(step())
	m = updated.(Model)
	if m.View().Content != atBottom {
		t.Fatal("wheel down at the end wrapped the instruction viewport")
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 100})
	m = updated.(Model)
	expanded := m.View()
	setupTextPosition(t, expanded, "GoVM First-Time Setup")
	setupTextPosition(t, expanded, "After adding to PATH")
	if _, got := setupTextPosition(t, expanded, "[Enter continue]"); got != 99 {
		t.Fatalf("expanded footer row = %d, want 99", got)
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 64, Height: 8})
	m = updated.(Model)
	shrunk := m.View()
	footerX, footerY := setupTextPosition(t, shrunk, "[Enter continue]")
	if footerY != 7 {
		t.Fatalf("shrunk footer row = %d, want 7", footerY)
	}
	if lines := strings.Split(shrunk.Content, "\n"); len(lines) != 8 {
		t.Fatalf("shrunk view has %d rows, want 8", len(lines))
	} else {
		for _, line := range lines {
			if width := ansi.StringWidth(line); width > 64 {
				t.Fatalf("shrunk view exceeds the terminal width: %d", width)
			}
		}
	}
	continueCmd := shrunk.OnMouse(tea.MouseClickMsg{X: footerX, Y: footerY, Button: tea.MouseLeft})
	if continueCmd == nil {
		t.Fatal("footer stopped accepting clicks after resize")
	}
	_, quit := m.Update(continueCmd())
	requireSetupQuit(t, quit)
}
