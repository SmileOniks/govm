package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func mouseGeometryVersions(count int) []utils.GoVersion {
	versions := make([]utils.GoVersion, count)
	for i := range versions {
		versions[i] = utils.GoVersion{
			Version: fmt.Sprintf("1.25.%d", i), Installed: true, Active: i == 0,
			Path:     fmt.Sprintf("/versions/界/e\u0301/go1.25.%d", i),
			Filename: fmt.Sprintf("go1.25.%d.darwin-arm64.tar.gz", i),
		}
	}
	return versions
}

func mouseGeometryDependencies(count int) []deps.ModuleDependency {
	modules := make([]deps.ModuleDependency, 0, count+1)
	for i := range count {
		path := fmt.Sprintf("example.com/module-%02d", i)
		if i == 17 || i == 18 {
			// These differ only beyond the displayed column. Selection must
			// retain the full identity rather than resolve the truncated text.
			path = "example.com/" + strings.Repeat("same-visible-prefix-", 8) + fmt.Sprintf("/%d", i)
		}
		modules = append(modules, deps.ModuleDependency{
			Path: path, Version: fmt.Sprintf("v0.0.%d", i), Latest: fmt.Sprintf("v1.0.%d", i),
		})
		if i == 2 {
			modules = append(modules, deps.ModuleDependency{
				Path: "example.com/hidden-indirect", Version: "v9.9.9", Indirect: true,
			})
		}
	}
	return modules
}

// Screen rows are identified by visible cell text, never by mouse targets or
// table scroll offsets. The dependency version is unique even when paths are
// indistinguishable after clipping.
type mouseGeometryVisibleRow struct {
	identity, marker string
	x, y             int
}

func mouseGeometryVisibleRows(t testing.TB, m Model) []mouseGeometryVisibleRow {
	t.Helper()
	markers := make(map[string]string)
	if m.CurrentTab == InstalledTab {
		for _, row := range m.projection.installedModel().Rows() {
			markers[row[0]] = row[0]
		}
	} else {
		for i, row := range m.deps.table.Rows() {
			markers[row[1]] = m.deps.rowPaths[i]
		}
	}
	var visible []mouseGeometryVisibleRow
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		for _, field := range strings.Fields(line) {
			identity, ok := markers[field]
			if !ok {
				continue
			}
			byteX := strings.Index(line, field)
			visible = append(visible, mouseGeometryVisibleRow{
				identity: identity, marker: field, x: ansi.StringWidth(line[:byteX]), y: y,
			})
			break
		}
	}
	return visible
}

func mouseGeometrySelected(m Model) string {
	if m.CurrentTab == InstalledTab {
		if row := m.projection.installedModel().SelectedRow(); len(row) > 0 {
			return row[0]
		}
		return ""
	}
	cursor := m.deps.table.Cursor()
	if cursor < 0 || cursor >= len(m.deps.rowPaths) {
		return ""
	}
	return m.deps.rowPaths[cursor]
}

func mouseGeometryCursor(m Model) int {
	if m.CurrentTab == InstalledTab {
		return m.projection.installedModel().Cursor()
	}
	return m.deps.table.Cursor()
}

func mouseGeometryAssertSelectedVisible(t testing.TB, m Model) {
	t.Helper()
	selected := mouseGeometrySelected(m)
	for _, row := range mouseGeometryVisibleRows(t, m) {
		if row.identity == selected {
			return
		}
	}
	t.Fatalf("selected identity %q is not visible:\n%s", selected, ansi.Strip(m.View().Content))
}

func mouseGeometryClickEdges(t testing.TB, m Model) Model {
	t.Helper()
	for _, edge := range []string{"first", "last"} {
		rows := mouseGeometryVisibleRows(t, m)
		if len(rows) == 0 {
			t.Fatalf("no visible table rows:\n%s", ansi.Strip(m.View().Content))
		}
		row := rows[0]
		if edge == "last" {
			row = rows[len(rows)-1]
		}
		var cmd tea.Cmd
		m, cmd = mouseAt(t, m, m.View(), tea.MouseClickMsg{X: row.x, Y: row.y, Button: tea.MouseLeft})
		if cmd != nil {
			t.Fatalf("selecting %s row %q started an operation", edge, row.identity)
		}
		if got := mouseGeometrySelected(m); got != row.identity {
			t.Fatalf("click on %s visible row selected %q, want %q", edge, got, row.identity)
		}
	}
	return m
}

func TestMouseTableSelectionAfterScroll(t *testing.T) {
	for _, tab := range []struct {
		name  string
		index int
	}{
		{name: "Installed", index: InstalledTab},
		{name: "Deps", index: DepsTab},
	} {
		t.Run(tab.name, func(t *testing.T) {
			m := newTestModel(t)
			refill := func(m Model, count int) Model {
				if tab.index == InstalledTab {
					seedVersions(t, &m, mouseGeometryVersions(count))
					return mouseSized(t, m, m.TermWidth, m.TermHeight)
				}
				return loadDeps(t, m, mouseGeometryDependencies(count))
			}
			m = mouseSized(t, m, 80, 24)
			m = refill(m, 45)
			if tab.index == InstalledTab {
				m, _ = mouseClick(t, m, "Installed")
			}
			if len(mouseGeometryVisibleRows(t, m)) >= 45 {
				t.Fatal("fixture does not exceed the visible table body")
			}
			if tab.index == DepsTab && strings.Contains(ansi.Strip(m.View().Content), "hidden-indirect") {
				t.Fatal("indirect dependency is visible in direct-only mode")
			}

			for range 22 {
				m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			}
			mouseGeometryAssertSelectedVisible(t, m)
			m = mouseGeometryClickEdges(t, m)
			for range 9 {
				rows := mouseGeometryVisibleRows(t, m)
				before := mouseGeometryCursor(m)
				m = mouseWheel(t, m, rows[len(rows)-1].marker, 1)
				if got, want := mouseGeometryCursor(m), min(before+1, 44); got != want {
					t.Fatalf("wheel down cursor = %d, want %d", got, want)
				}
			}
			m = mouseGeometryClickEdges(t, m)
			for range 4 {
				m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
			}
			for range 3 {
				rows := mouseGeometryVisibleRows(t, m)
				before := mouseGeometryCursor(m)
				m = mouseWheel(t, m, rows[0].marker, -1)
				if got, want := mouseGeometryCursor(m), max(before-1, 0); got != want {
					t.Fatalf("wheel up cursor = %d, want %d", got, want)
				}
			}
			m = mouseGeometryClickEdges(t, m)

			if tab.index == DepsTab {
				// Bring the ambiguous pair into view using ordinary navigation.
				for mouseGeometryCursor(m) > 17 {
					m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
				}
				for mouseGeometryCursor(m) < 17 {
					m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				}
				for _, index := range []int{17, 18} {
					want := m.deps.rowPaths[index]
					m, _ = mouseClick(t, m, fmt.Sprintf("v0.0.%d", index))
					if got := mouseGeometrySelected(m); got != want {
						t.Fatalf("ambiguous path click selected %q, want %q", got, want)
					}
				}
			}

			for range 50 {
				m = feed(t, m, tea.KeyPressMsg{Code: 'j'})
			}
			if got := mouseGeometryCursor(m); got != 44 {
				t.Fatalf("end cursor = %d, want 44", got)
			}
			m = mouseWheel(t, m, mouseGeometryVisibleRows(t, m)[0].marker, 1)
			if got := mouseGeometryCursor(m); got != 44 {
				t.Fatalf("wheel at end wrapped to %d", got)
			}
			m = mouseGeometryClickEdges(t, m)
			for _, size := range [][2]int{{64, 20}, {130, 30}, {80, 24}} {
				selected := mouseGeometrySelected(m)
				m = mouseSized(t, m, size[0], size[1])
				if got := mouseGeometrySelected(m); got != selected {
					t.Fatalf("resize changed selection from %q to %q", selected, got)
				}
				mouseGeometryAssertSelectedVisible(t, m)
				m = mouseGeometryClickEdges(t, m)
			}

			m = refill(m, 3)
			if cursor := mouseGeometryCursor(m); cursor < 0 || cursor >= 3 {
				t.Fatalf("shrunken table cursor = %d, want in [0,3)", cursor)
			}
			mouseGeometryAssertSelectedVisible(t, m)
			m = mouseGeometryClickEdges(t, m)
			m = refill(m, 0)
			if mouseGeometryCursor(m) != -1 || mouseGeometrySelected(m) != "" || len(mouseGeometryVisibleRows(t, m)) != 0 {
				t.Fatalf("empty table retains a selection: cursor=%d selected=%q", mouseGeometryCursor(m), mouseGeometrySelected(m))
			}
			m = refill(m, 45)
			if mouseGeometryCursor(m) != 0 {
				t.Fatalf("refilled table cursor = %d, want first row", mouseGeometryCursor(m))
			}
			mouseGeometryAssertSelectedVisible(t, m)
			m = mouseGeometryClickEdges(t, m)
		})
	}
}

func mouseGeometryBounds(t testing.TB, view tea.View, width, height int) {
	t.Helper()
	lines := strings.Split(view.Content, "\n")
	if len(lines) > height {
		t.Fatalf("view height = %d exceeds %d:\n%s", len(lines), height, ansi.Strip(view.Content))
	}
	for y, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("row %d width = %d exceeds %d: %q", y, got, width, line)
		}
	}
}

func mouseGeometryControls(t testing.TB, m Model, labels []string) {
	t.Helper()
	view := m.View()
	mouseGeometryBounds(t, view, m.TermWidth, m.TermHeight)
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
		t.Fatal("supported viewport has no cell-motion mouse controls")
	}
	for _, label := range labels {
		x, y := mouseText(t, view, label)
		if x < 0 || y < 0 || x+ansi.StringWidth(label) > m.TermWidth || y >= m.TermHeight {
			t.Fatalf("control %q is outside the viewport at (%d,%d)", label, x, y)
		}
		if cmd := view.OnMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); cmd == nil {
			t.Fatalf("visible control %q cannot be clicked at (%d,%d)", label, x, y)
		}
	}
}

// Locate the painted brackets independently of the renderer's hit targets.
func mouseActionButtonRect(t testing.TB, view tea.View, label string) cellRect {
	t.Helper()
	_, y := mouseText(t, view, label)
	line := strings.Split(ansi.Strip(view.Content), "\n")[y]
	labelStart := strings.Index(line, label)
	left := strings.LastIndex(line[:labelStart], "[")
	labelEnd := labelStart + len(label)
	rightOffset := strings.Index(line[labelEnd:], "]")
	if left < 0 || rightOffset < 0 {
		t.Fatalf("button %q has no visible brackets", label)
	}
	right := labelEnd + rightOffset
	return cellRect{
		x: ansi.StringWidth(line[:left]), y: y,
		width: ansi.StringWidth(line[left : right+1]), height: 1,
	}
}

func TestMouseActionButtonCells(t *testing.T) {
	for _, theme := range []config.ThemeName{config.ThemeCurrent, config.ThemeLight} {
		for _, control := range []struct {
			label string
			close bool
		}{
			{label: "Help ?"},
			{label: "Close help esc", close: true},
		} {
			for cell := range ansi.StringWidth(control.label) + 4 {
				t.Run(fmt.Sprintf("%s/%s/cell-%d", theme, control.label, cell), func(t *testing.T) {
					m := newTestModel(t)
					m.settings.values.Theme = theme
					m.applyRuntimeTheme()
					m = mouseSized(t, m, 130, 30)
					if control.close {
						m = feed(t, m, tea.KeyPressMsg{Code: '?'})
					}
					v := m.View()
					r := mouseActionButtonRect(t, v, control.label)
					m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: r.x + cell, Y: r.y, Button: tea.MouseLeft})
					if m.HelpVisible == control.close {
						t.Fatal("click on button text, brackets, or padding did not toggle Help")
					}
				})
			}
		}
		t.Run(fmt.Sprintf("%s/gap", theme), func(t *testing.T) {
			m := newTestModel(t)
			m.settings.values.Theme = theme
			m.applyRuntimeTheme()
			m = mouseSized(t, m, 130, 30)
			v := m.View()
			r := mouseActionButtonRect(t, v, "Help ?")
			x, y := r.x+r.width, r.y
			if got := ansi.Cut(strings.Split(v.Content, "\n")[y], x, x+1); ansi.Strip(got) != " " {
				t.Fatal("fixture does not point into the gap")
			}
			m, cmd := mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if cmd != nil || m.inputContext() != inputTab || m.HelpVisible {
				t.Fatal("button gap dispatched an action")
			}
		})
	}
}

func TestMouseActionButtonWrappedRows(t *testing.T) {
	labels := []string{"Install i", "Use u", "Delete d", "Refresh r", "Find f", "Help ?", "Quit q / ctrl+c"}
	for _, size := range [][2]int{{64, 20}, {80, 24}, {129, 30}, {130, 30}} {
		for _, theme := range []config.ThemeName{config.ThemeCurrent, config.ThemeLight} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], theme), func(t *testing.T) {
				for _, tab := range []int{AvailableTab, DepsTab} {
					for _, label := range []string{"Help ?", "Find f"} {
						if tab == DepsTab && label == "Find f" {
							continue
						}
						for _, offset := range []int{0, 1, 2, -2, -1} {
							m := newTestModel(t)
							m.settings.values.Theme = theme
							m.applyRuntimeTheme()
							m.CurrentTab = tab
							m = mouseSized(t, m, size[0], size[1])
							v := m.View()
							mouseGeometryBounds(t, v, size[0], size[1])
							help := mouseActionButtonRect(t, v, "Help ?")
							quit := mouseActionButtonRect(t, v, "Quit q / ctrl+c")
							if help.y != quit.y {
								t.Fatal("Help and Quit should remain together on one row")
							}
							if tab == AvailableTab {
								mouseGeometryControls(t, m, labels)
								first := mouseActionButtonRect(t, v, labels[0])
								last := mouseActionButtonRect(t, v, labels[len(labels)-1])
								if size[0] == 130 && first.y != last.y {
									t.Fatal("wide Available footer should fit in one row")
								}
								if size[0] == 64 && last.y-first.y != 1 {
									t.Fatal("64-column Available footer should fit in two rows")
								}
								if size[0] == 80 && first.y == last.y {
									t.Fatal("80-column Available footer should wrap")
								}
							}
							r := mouseActionButtonRect(t, v, label)
							if offset < 0 {
								offset += r.width
							}
							m, _ = mouseAt(t, m, v, tea.MouseClickMsg{X: r.x + offset, Y: r.y, Button: tea.MouseLeft})
							if label == "Help ?" && !m.HelpVisible {
								t.Fatal("wrapped Help button did not open Help")
							}
							if label == "Find f" && !m.filterInputActive() {
								t.Fatal("wrapped Find button did not open filter")
							}
						}
					}
				}
			})
		}
	}
}

func TestMouseActionButtonContextTransitions(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.25.0", Installed: true, Path: "/go/1.25.0"}})
	deletes := 0
	m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
		deletes++
		return lifecycle.DeletionResult{Version: version}, nil
	}})
	m = mouseSized(t, m, 64, 20)
	clickButton := func(label string, offset int) {
		t.Helper()
		v := m.View()
		r := mouseActionButtonRect(t, v, label)
		if offset < 0 {
			offset += r.width
		}
		var cmd tea.Cmd
		m, cmd = mouseAt(t, m, v, tea.MouseClickMsg{X: r.x + offset, Y: r.y, Button: tea.MouseLeft})
		m = mouseRunCmd(t, m, cmd)
	}
	clickButton("Find f", 0)
	if !m.filterInputActive() {
		t.Fatal("Find bracket did not open the filter")
	}
	for _, key := range "q?" {
		updated, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		m = updated.(Model)
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatal("filter text dispatched Quit")
			}
		}
	}
	if m.HelpVisible || m.projection.availableModel().FilterInput.Value() != "q?" {
		t.Fatal("filter did not capture command keys")
	}
	clickButton("Clear esc", 1)
	if m.inputContext() != inputTab || m.projection.availableFilterApplied() {
		t.Fatal("Clear padding did not restore normal context")
	}
	clickButton("Delete d", -1)
	if m.inputContext() != inputDeleteConfirm || deletes != 0 {
		t.Fatal("Delete must only open confirmation")
	}
	clickButton("Cancel n", -2)
	version, ok := m.projection.lookup("1.25.0")
	if m.inputContext() != inputTab || deletes != 0 || !ok || !version.Installed {
		t.Fatal("Cancel deleted the version or retained confirmation")
	}
}

func TestMouseActionButtonNarrowAndDisabled(t *testing.T) {
	for _, binding := range []keyBinding{
		{keys: "r", mouseControls: mouseControls("r 界refresh", tea.KeyPressMsg{Code: 'r'})},
		{keys: "enter", mouseControls: mouseControls("enter check", tea.KeyPressMsg{Code: tea.KeyEnter})},
	} {
		t.Run(binding.keys, func(t *testing.T) {
			sections := []helpSection{{bindings: []keyBinding{binding}}}
			for _, width := range []int{-1, 0, 4, 5, 6, 10} {
				t.Run(fmt.Sprint(width), func(t *testing.T) {
					for _, checking := range []bool{false, true} {
						surface := renderControls(styles.NewTheme(config.ThemeCurrent), sections, width, checking)
						if width < 5 {
							if surface.content != "" || len(surface.targets) != 0 {
								t.Fatal("unsupported button width should be empty")
							}
							continue
						}
						mouseGeometryBounds(t, tea.NewView(surface.content), width, 1)
						if checking && len(surface.targets) != 0 {
							t.Fatal("checking button should have no mouse targets")
						}
						callback := mouseCallback(surface, mouseActionMsg{})
						for x := range ansi.StringWidth(surface.content) {
							cmd := callback(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
							if (cmd == nil) != checking {
								t.Fatalf("checking=%v at (%d,0): clickable=%v", checking, x, cmd != nil)
							}
						}
					}
				})
			}
		})
	}
}

func TestMouseViewportGeometry(t *testing.T) {
	for _, size := range [][2]int{{64, 20}, {80, 24}, {129, 30}, {130, 30}} {
		for _, theme := range []config.ThemeName{config.ThemeCurrent, config.ThemeLight} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], theme), func(t *testing.T) {
				base := func(t *testing.T) Model {
					m := newTestModel(t)
					m.settings.values.Theme = theme
					m.applyRuntimeTheme()
					seedVersions(t, &m, mouseGeometryVersions(45))
					m.ShimPathWarning = "GoVM is not in your PATH.\n\x1b[33m界 e\u0301: add the shim directory to PATH\x1b[0m"
					return mouseSized(t, m, size[0], size[1])
				}
				globals := []string{"Help ?", "Quit q / ctrl+c"}
				for _, surface := range []struct {
					name   string
					build  func(*testing.T, Model) Model
					labels []string
				}{
					{
						name: "Available", build: func(_ *testing.T, m Model) Model { return m },
						labels: []string{"Install i", "Use u", "Delete d", "Refresh r", "Find f"},
					},
					{
						name: "Installed summary", build: func(t *testing.T, m Model) Model {
							m = m.BindVersionOperations(VersionOperations{DiskUsage: func(context.Context) (prune.Summary, error) {
								return prune.Summary{}, nil
							}})
							m, _ = mouseClick(t, m, "Installed")
							return feed(t, m, diskUsageMsg{Summary: prune.Summary{
								InstalledBytes: 4096, ReclaimableBytes: 2048, DownloadBytes: 1024,
							}})
						},
						labels: []string{"Use u", "Delete d", "Prune p", "Refresh r"},
					},
					{
						name: "Deps", build: func(t *testing.T, m Model) Model {
							return loadDeps(t, m, mouseGeometryDependencies(45))
						},
						labels: []string{"Check updates r", "Mark space", "Mark all / none a", "Update u", "Backups b"},
					},
					{
						name: "Settings", build: func(t *testing.T, m Model) Model {
							m, _ = mouseClick(t, m, "Settings")
							return m
						},
						labels: []string{"Previous ↑", "Next ↓", "Toggle or edit enter / space", "Previous ←", "Next →"},
					},
					{
						name: "Applied long filter", build: func(t *testing.T, m Model) Model {
							return applyFilter(t, m, strings.Repeat("界e\u0301", 35))
						},
						labels: []string{"Install i", "Use u", "Delete d", "Refresh r", "Find f", "Clear esc"},
					},
					{
						name: "Available inline delete", build: func(t *testing.T, m Model) Model {
							m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
							return feed(t, m, tea.KeyPressMsg{Code: 'd'})
						},
						labels: []string{"Confirm y", "Cancel n"},
					},
					{
						name: "Installed inline delete", build: func(t *testing.T, m Model) Model {
							m, _ = mouseClick(t, m, "Installed")
							m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
							return feed(t, m, tea.KeyPressMsg{Code: 'd'})
						},
						labels: []string{"Confirm y", "Cancel n"},
					},
				} {
					t.Run(surface.name, func(t *testing.T) {
						m := surface.build(t, base(t))
						labels := append(append([]string{}, surface.labels...), globals...)
						mouseGeometryControls(t, m, labels)
						help := mouseActionButtonRect(t, m.View(), globals[0])
						quit := mouseActionButtonRect(t, m.View(), globals[1])
						if help.y != quit.y {
							t.Fatal("Help and Quit should remain together on one row")
						}
						if size[0] == 64 && (surface.name == "Available" || surface.name == "Deps" || surface.name == "Settings") {
							first := mouseActionButtonRect(t, m.View(), surface.labels[0])
							if quit.y-first.y > 1 {
								t.Fatal("compact footer should fit in at most two rows")
							}
						}
						for _, label := range []string{"Available", "Installed", "Deps", "Settings"} {
							mouseText(t, m.View(), label)
						}
						mouseText(t, m.View(), "GoVM is not in your PATH.")
						mouseText(t, m.View(), "界 e\u0301")
						before := m.inputContext()
						m, _ = mouseClick(t, m, "Help ?")
						mouseGeometryControls(t, m, []string{"Close help esc", "Quit ctrl+c"})
						m, _ = mouseClick(t, m, "Close help esc")
						if m.HelpVisible || m.inputContext() != before {
							t.Fatal("Close did not restore the underlying surface")
						}
					})
				}

				t.Run("Filter editor", func(t *testing.T) {
					m := typeIntoFilter(t, openFilter(t, base(t)), strings.Repeat("界e\u0301", 35))
					mouseGeometryControls(t, m, []string{"Apply enter", "Clear esc", "Next tab tab", "Quit ctrl+c"})
					m, _ = mouseClick(t, m, "Apply enter")
					if m.filterInputActive() {
						t.Fatal("visible Apply did not commit the filter")
					}
					m, _ = mouseClick(t, m, "Clear esc")
					if m.projection.availableFilterApplied() {
						t.Fatal("visible Clear did not remove the filter")
					}
				})

				for _, modal := range []struct {
					name   string
					build  func(*testing.T, Model) Model
					labels []string
					cancel string
				}{
					{name: "Update", build: func(t *testing.T, m Model) Model {
						return openUpdateDialog(t, loadDeps(t, m, mouseGeometryDependencies(45)))
					}, labels: []string{"Patch", "Minor", "Latest", "All", "Current", "Yes", "No"}, cancel: "No"},
					{name: "Checks", build: func(t *testing.T, m Model) Model {
						return confirmChecksFrom(t, m)
					}, labels: []string{"Yes", "No"}, cancel: "No"},
					{name: "Update marked", build: func(t *testing.T, m Model) Model {
						m = loadDeps(t, m, mouseGeometryDependencies(45))
						m = feed(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
						return openUpdateDialog(t, m)
					}, labels: []string{"Patch", "Minor", "Latest", "All", "Marked (1)", "Yes", "No"}, cancel: "No"},
					{name: "Rollback", build: func(t *testing.T, m Model) Model {
						return confirmRollbackFrom(t, m, deps.DependencyCheckResult{
							Command: "go test ./...", Output: strings.Repeat("界 e\u0301 failed output\n", 100),
						})
					}, labels: []string{"Roll back", "Keep"}, cancel: "Keep"},
					{name: "Restore", build: func(t *testing.T, m Model) Model {
						var backups []deps.DependencyBackupInfo
						for i := range 20 {
							backups = append(backups, deps.DependencyBackupInfo{
								Name: fmt.Sprintf("2026-07-%02d-界-e\u0301-long-backup-name.json", i+1),
								Path: fmt.Sprintf("/backups/%d.json", i), Kind: deps.DependencyBackupKindPreUpdate, Updated: 1,
							})
						}
						return openRestoreDialog(t, loadDeps(t, m, testLib()), backups)
					}, labels: []string{"Restore", "Cancel"}, cancel: "Cancel"},
					{name: "Prune", build: func(t *testing.T, _ Model) Model {
						m := pruneConfirmingModel(t)
						m.settings.values.Theme = theme
						m.applyRuntimeTheme()
						return mouseSized(t, m, size[0], size[1])
					}, labels: []string{"Yes", "No"}, cancel: "No"},
				} {
					t.Run(modal.name, func(t *testing.T) {
						m := modal.build(t, base(t))
						mouseGeometryControls(t, m, append(append([]string{}, modal.labels...), "Help ?", "Quit q"))
						before := m.inputContext()
						m, _ = mouseClick(t, m, "Help ?")
						mouseGeometryControls(t, m, []string{"Close help esc", "Quit ctrl+c"})
						m, _ = mouseClick(t, m, "Close help esc")
						if m.HelpVisible || m.inputContext() != before {
							t.Fatal("Close did not restore the modal context")
						}
						m, _ = mouseClick(t, m, modal.cancel)
						if m.inputContext() == before {
							t.Fatalf("visible %q did not dismiss the modal", modal.cancel)
						}
					})
				}

				for _, editor := range []struct {
					name   string
					row    settingsRowKind
					labels []string
				}{
					{name: "Backup limit editor", row: settingRowDepsBackups, labels: []string{"Save enter", "Cancel esc"}},
					{name: "Source editor", row: settingRowDistributionSource, labels: []string{"Check and save enter", "Reset to official r", "Cancel esc"}},
				} {
					t.Run(editor.name, func(t *testing.T) {
						m := focusSetting(t, base(t), editor.row)
						m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
						mouseGeometryControls(t, m, editor.labels)
						m, _ = mouseClick(t, m, "Cancel esc")
						if m.inputContext() == inputSettingsInput {
							t.Fatal("visible Cancel left the editor open")
						}
					})
				}
			})
		}
	}

	for _, size := range [][2]int{{63, 20}, {64, 19}, {0, 0}} {
		t.Run(fmt.Sprintf("Unsupported/%dx%d", size[0], size[1]), func(t *testing.T) {
			m := mouseSized(t, newTestModel(t), 80, 24)
			m = mouseSized(t, m, size[0], size[1])
			view := m.View()
			if view.MouseMode != tea.MouseModeNone {
				t.Fatal("undersized viewport enables mouse tracking")
			}
			if view.OnMouse != nil {
				for y := range max(1, size[1]) {
					for x := range max(1, size[0]) {
						if view.OnMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}) != nil {
							t.Fatalf("undersized viewport has an active click at (%d,%d)", x, y)
						}
						if view.OnMouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}) != nil {
							t.Fatalf("undersized viewport has an active wheel at (%d,%d)", x, y)
						}
					}
				}
			}
			m = mouseSized(t, m, 64, 20)
			m, _ = mouseClick(t, m, "Settings")
			if m.CurrentTab != SettingsTab {
				t.Fatal("resizing to a supported viewport did not restore tab clicks")
			}
			m, _ = mouseClick(t, m, "Help ?")
			m, _ = mouseClick(t, m, "Close help esc")
			if m.HelpVisible {
				t.Fatal("resizing to a supported viewport did not restore Close")
			}
		})
	}
}
