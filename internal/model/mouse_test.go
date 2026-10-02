package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func mouseSized(t testing.TB, m Model, w, h int) Model {
	t.Helper()
	return feed(t, m, tea.WindowSizeMsg{Width: w, Height: h})
}

// Coordinates come from visible text, independently of the hit-target geometry.
func mouseText(t testing.TB, v tea.View, text string) (int, int) {
	t.Helper()
	x, y := -1, -1
	for row, line := range strings.Split(ansi.Strip(v.Content), "\n") {
		if at := strings.LastIndex(line, text); at >= 0 {
			x, y = ansi.StringWidth(line[:at]), row
		}
	}
	if y < 0 {
		t.Fatalf("visible text %q missing:\n%s", text, ansi.Strip(v.Content))
	}
	return x, y
}

func mouseAt(t testing.TB, m Model, v tea.View, msg tea.MouseMsg) (Model, tea.Cmd) {
	t.Helper()
	if v.OnMouse == nil {
		return m, nil
	}
	cmd := v.OnMouse(msg)
	if cmd == nil {
		return m, nil
	}
	updated, next := m.Update(cmd())
	return updated.(Model), next
}

func mouseClick(t testing.TB, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	v := m.View()
	x, y := mouseText(t, v, text)
	return mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func mouseClickRun(t testing.TB, m Model, text string) Model {
	t.Helper()
	m, cmd := mouseClick(t, m, text)
	return mouseRunCmd(t, m, cmd)
}

// Settle finite application work, not recurring cursor/spinner timers.
func mouseRunCmd(t testing.TB, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case nil, cursor.BlinkMsg, spinner.TickMsg:
		return m
	case tea.BatchMsg:
		for _, child := range msg {
			m = mouseRunCmd(t, m, child)
		}
		return m
	default:
		next, follow := m.Update(msg)
		return mouseRunCmd(t, next.(Model), follow)
	}
}

func mouseWheel(t testing.TB, m Model, text string, delta int) Model {
	t.Helper()
	v := m.View()
	x, y := mouseText(t, v, text)
	button := tea.MouseWheelDown
	if delta < 0 {
		button = tea.MouseWheelUp
	}
	m, _ = mouseAt(t, m, v, tea.MouseWheelMsg{X: x, Y: y, Button: button})
	return m
}

func TestMouseTabsAndActions(t *testing.T) {
	t.Run("tabs and refresh", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 80, 24)
		loads := 0
		m = m.BindVersionOperations(VersionOperations{LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
			loads++
			return []utils.GoVersion{{Version: "1.29.0", Installed: true, Path: "/go/1.29.0"}}, nil
		}})
		for _, tc := range []struct {
			label string
			tab   int
		}{{"Installed", InstalledTab}, {"Deps", DepsTab}, {"Settings", SettingsTab}, {"Available", AvailableTab}, {"Installed", InstalledTab}} {
			m = mouseClickRun(t, m, tc.label)
			if m.CurrentTab != tc.tab {
				t.Fatalf("%s selected tab %d", tc.label, m.CurrentTab)
			}
		}
		m = mouseClickRun(t, m, "Refresh r")
		if loads != 1 {
			t.Fatalf("refresh loads=%d", loads)
		}
		if _, ok := m.projection.lookup("1.29.0"); !ok {
			t.Fatal("refresh did not publish catalog")
		}
	})
	for _, tab := range []string{"Available", "Installed"} {
		t.Run(tab+" selection and operations", func(t *testing.T) {
			m := newTestModel(t)
			seedVersions(t, &m, []utils.GoVersion{{Version: "1.26.1", Installed: true, Path: "/go/1.26.1"}, {Version: "1.25.1", Installed: true, Path: "/go/1.25.1"}, {Version: "1.24.1", Installed: true, Active: true, Path: "/go/1.24.1"}})
			m = mouseSized(t, m, 80, 30)
			activated, deleted := "", ""
			m = m.BindVersionOperations(VersionOperations{
				Activate: func(_ context.Context, v string) (lifecycle.ActivationResult, error) {
					activated = v
					return lifecycle.ActivationResult{Version: v}, nil
				},
				Delete: func(_ context.Context, v string) (lifecycle.DeletionResult, error) {
					deleted = v
					return lifecycle.DeletionResult{Version: v}, nil
				},
				ShimInPath: func() bool { return true },
			})
			m = mouseClickRun(t, m, tab)
			m = mouseClickRun(t, m, "1.25.1")
			if activated != "" || deleted != "" {
				t.Fatal("selection performed an operation")
			}
			m = mouseClickRun(t, m, "Use u")
			if activated != "1.25.1" {
				t.Fatalf("activated %q", activated)
			}
			active, ok := m.projection.lookup("1.25.1")
			if !ok || !active.Active {
				t.Fatal("activation not reflected in catalog")
			}
			m = mouseClickRun(t, m, "1.26.1")
			m = mouseClickRun(t, m, "Delete d")
			m = mouseClickRun(t, m, "Cancel n")
			if deleted != "" {
				t.Fatal("cancel deleted version")
			}
			m = mouseClickRun(t, m, "Delete d")
			// Inline confirmation must not allow changing its captured version.
			m = mouseClickRun(t, m, "1.24.1")
			m = mouseClickRun(t, m, "Confirm y")
			if deleted != "1.26.1" {
				t.Fatalf("deleted %q", deleted)
			}
			removed, _ := m.projection.lookup("1.26.1")
			kept, _ := m.projection.lookup("1.24.1")
			if removed.Installed || !kept.Installed {
				t.Fatal("deletion changed wrong catalog row")
			}
		})
	}
}

func TestMouseAvailableSelection(t *testing.T) {
	t.Run("title description and gaps", func(t *testing.T) {
		m := newTestModel(t)
		seedVersions(t, &m, []utils.GoVersion{{Version: "1.26.1", Installed: true, Path: "/go/first"}, {Version: "1.25.1", Installed: true, Path: "/go/second"}})
		m = mouseSized(t, m, 80, 24)
		m = mouseClickRun(t, m, "1.25.1")
		if selectedListVersion(m) != "1.25.1" {
			t.Fatal("title did not select second row")
		}
		v := m.View()
		x, y := mouseText(t, v, "1.26.1  installed")
		m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y + 1, Button: tea.MouseLeft})
		if selectedListVersion(m) != "1.26.1" {
			t.Fatalf("description at (%d,%d) selected %q:\n%s", x, y+1, selectedListVersion(m), ansi.Strip(v.Content))
		}
		for _, offset := range []int{-1, 2, 8} {
			v = m.View()
			m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y + offset, Button: tea.MouseLeft})
			if selectedListVersion(m) != "1.26.1" {
				t.Fatalf("gap offset %d changed selection", offset)
			}
		}
	})
	t.Run("pagination and frame wheel burst", func(t *testing.T) {
		m := newTestModel(t)
		versions := make([]utils.GoVersion, 25)
		for i := range versions {
			versions[i].Version = fmt.Sprintf("1.30.%02d", 24-i)
		}
		seedVersions(t, &m, versions)
		m = mouseSized(t, m, 80, 24)
		v := m.View()
		x, y := mouseText(t, v, versions[0].Version)
		for range 12 {
			m, _ = mouseAt(t, m, v, tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		}
		if selectedListVersion(m) != versions[12].Version {
			t.Fatalf("wheel burst selected %s", selectedListVersion(m))
		}
		// The current visible page uses filtered indices, not page-local indices.
		list := m.projection.availableModel()
		start, end := list.Paginator.GetSliceBounds(len(list.VisibleItems()))
		if start == 0 || end <= start {
			t.Fatal("wheel did not advance page")
		}
		name := list.VisibleItems()[start].(interface{ FilterValue() string }).FilterValue()
		m = mouseClickRun(t, m, name)
		if selectedListVersion(m) != name {
			t.Fatalf("page click selected %s, want %s", selectedListVersion(m), name)
		}
		for range 40 {
			m = mouseWheel(t, m, selectedListVersion(m), 1)
		}
		if selectedListVersion(m) != versions[24].Version {
			t.Fatal("wheel wrapped past last version")
		}
	})
	t.Run("filter apply clear and no matches", func(t *testing.T) {
		m := newTestModel(t)
		seedVersions(t, &m, []utils.GoVersion{{Version: "1.26.1"}, {Version: "1.25.2"}, {Version: "1.25.1"}})
		m = mouseSized(t, m, 80, 24)
		m = mouseClickRun(t, m, "Find f")
		m = typeIntoFilter(t, m, "1.25")
		before := selectedListVersion(m)
		m = mouseClickRun(t, m, "1.25.1")
		m = mouseWheel(t, m, "1.25.1", 1)
		if selectedListVersion(m) != before || !m.filterInputActive() {
			t.Fatal("filter editing allowed row navigation")
		}
		m = mouseClickRun(t, m, "Apply enter")
		m = mouseClickRun(t, m, "1.25.1")
		if selectedListVersion(m) != "1.25.1" {
			t.Fatal("filtered row identity mismatch")
		}
		m = mouseClickRun(t, m, "Clear esc")
		if m.projection.availableFilterApplied() {
			t.Fatal("clear kept filter")
		}
		m = mouseClickRun(t, m, "Find f")
		m = typeIntoFilter(t, m, "nomatch")
		if len(m.projection.availableModel().VisibleItems()) != 0 {
			t.Fatal("expected no matches")
		}
		m = mouseClickRun(t, m, "Clear esc")
		if len(m.projection.availableModel().VisibleItems()) != 3 {
			t.Fatal("clear did not restore catalog")
		}
	})
	t.Run("explicit selection survives pending refilter", func(t *testing.T) {
		m := newTestModel(t)
		versions := []utils.GoVersion{{Version: "1.25.3"}, {Version: "1.25.2"}, {Version: "1.25.1"}}
		seedVersions(t, &m, versions)
		m = mouseSized(t, m, 80, 30)
		m = applyFilter(t, m, "1.25")
		cmd, err := replaceVersions(&m, versions)
		if err != nil {
			t.Fatal(err)
		}
		m = mouseClickRun(t, m, "1.25.2")
		m = settleCmd(t, m, cmd)
		if selectedListVersion(m) != "1.25.2" {
			t.Fatalf("refilter restored old selection: %s", selectedListVersion(m))
		}
	})
}

func TestMouseDependencyMarks(t *testing.T) {
	m := mouseSized(t, marksFixture(t), 80, 24)
	m = mouseClickRun(t, m, "example.com/c")
	if len(m.deps.markedPaths()) != 0 || m.deps.rowPaths[m.deps.table.Cursor()] != "example.com/c" {
		t.Fatal("path click must only select")
	}
	for _, path := range []string{"example.com/a", "example.com/c"} {
		// The space between the checkbox and path belongs to row selection.
		v := m.View()
		x, y := mouseText(t, v, path)
		m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: x - 1, Y: y, Button: tea.MouseLeft})
		if m.deps.marked(path) || m.deps.rowPaths[m.deps.table.Cursor()] != path {
			t.Fatal("checkbox gap must select the row without marking it")
		}
		for cell, marked := range []bool{true, false, true} {
			if cell != 1 {
				m = mouseClickRun(t, m, "example.com/b")
			}
			v := m.View()
			x, y := mouseText(t, v, path)
			line := ansi.Strip(strings.Split(v.Content, "\n")[y])
			prefix := ansi.Cut(line, 0, x)
			at := strings.LastIndex(prefix, "[")
			if at < 0 {
				t.Fatalf("checkbox missing in %q", line)
			}
			gx := ansi.StringWidth(prefix[:at]) + cell
			m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: gx, Y: y, Button: tea.MouseLeft})
			if m.deps.marked(path) != marked {
				t.Fatalf("checkbox for %s marked=%v want %v", path, m.deps.marked(path), marked)
			}
			checkbox := "[○] "
			if marked {
				checkbox = "[●] "
			}
			mouseText(t, m.View(), checkbox+path)
			m, _ = mouseAt(t, m, m.View(), tea.MouseReleaseMsg{X: gx, Y: y, Button: tea.MouseLeft})
			if m.deps.marked(path) != marked {
				t.Fatal("release toggled mark twice")
			}
		}
	}
	m, _ = mouseClick(t, m, "Update u")
	selection := startedSelection(t, m)
	if len(selection.Modules) != 2 || selection.Modules[0] != "example.com/a" || selection.Modules[1] != "example.com/c" {
		t.Fatalf("update selection=%+v", selection)
	}
	m = mouseClickRun(t, m, "example.com/b")
	m = mouseClickRun(t, m, "Mark space")
	if m.deps.marked("example.com/b") {
		t.Fatal("busy action changed marks")
	}
	if m.deps.rowPaths[m.deps.table.Cursor()] != "example.com/b" {
		t.Fatal("busy blocked selection")
	}
}

func TestMouseContextIsolation(t *testing.T) {
	t.Run("raw wheel is not delivered twice", func(t *testing.T) {
		m := newTestModel(t)
		seedVersions(t, &m, []utils.GoVersion{{Version: "1.26.1"}, {Version: "1.25.1"}, {Version: "1.24.1"}})
		m = mouseSized(t, m, 80, 24)
		v := m.View()
		x, y := mouseText(t, v, "1.26.1")
		wheel := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
		m, _ = mouseAt(t, m, v, wheel)
		m = feed(t, m, wheel)
		if selectedListVersion(m) != "1.25.1" {
			t.Fatalf("raw event duplicated wheel: %s", selectedListVersion(m))
		}
	})
	t.Run("unsupported events and stale frames", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 80, 24)
		v := m.View()
		x, y := mouseText(t, v, "Installed")
		for _, msg := range []tea.MouseMsg{
			tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseMiddle},
			tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}, tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft},
			tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft, Mod: tea.ModShift}, tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelLeft},
		} {
			m, _ = mouseAt(t, m, v, msg)
			if m.CurrentTab != AvailableTab {
				t.Fatalf("%T switched tab", msg)
			}
		}
		for _, change := range []struct {
			name  string
			apply func(Model) Model
		}{
			{"resize", func(m Model) Model { return mouseSized(t, m, 130, 30) }},
			{"refresh", func(m Model) Model { return feed(t, m, catalogLoadFailedMsg{Err: fmt.Errorf("offline")}) }},
			{"tab", func(m Model) Model {
				return press(t, m, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab})
			}},
			{"help", func(m Model) Model { return press(t, m, tea.KeyPressMsg{Code: '?'}) }},
		} {
			t.Run(change.name, func(t *testing.T) {
				original := m
				v := original.View()
				x, y := mouseText(t, v, "Installed")
				original = change.apply(original)
				tab := original.CurrentTab
				original, _ = mouseAt(t, original, v, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				if original.CurrentTab != tab {
					t.Fatal("stale frame performed action")
				}
			})
		}
	})
	t.Run("help blocks underlying dialog", func(t *testing.T) {
		m := mouseSized(t, modelAtConfirmApply(t), 80, 24)
		old := m.View()
		x, y := mouseText(t, old, "Yes")
		m = mouseClickRun(t, m, "Help ?")
		if !m.HelpVisible {
			t.Fatal("help did not open")
		}
		m, _ = mouseAt(t, m, old, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		m, _ = mouseAt(t, m, m.View(), tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		if m.deps.cycle.Phase() != deps.PhaseConfirmApply {
			t.Fatalf("help activated underlying dialog: %s", m.deps.cycle.Phase())
		}
		m = mouseClickRun(t, m, "Close help esc")
		if m.HelpVisible || m.inputContext() != inputDepsDialog {
			t.Fatal("close did not restore dialog context")
		}
		m, _ = mouseAt(t, m, m.View(), tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		if m.inputContext() != inputDepsDialog {
			t.Fatal("outside click dismissed modal")
		}
		// A confirmation captured before the transition must not answer the next dialog.
		old = m.View()
		x, y = mouseText(t, old, "Yes")
		m = press(t, m, tea.KeyPressMsg{Code: 'y'})
		m = feed(t, m, deps.ApplyUpdatesDoneEvent{Snapshot: &deps.DependencySnapshot{ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n"}}, Backup: &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"}, Dependencies: m.deps.cycle.Dependencies()})
		if m.deps.dialog.kind != dialogChecks {
			t.Fatal("apply did not reach checks")
		}
		m, _ = mouseAt(t, m, old, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if m.deps.dialog.kind != dialogChecks {
			t.Fatal("stale Yes answered new dialog")
		}
	})
}
