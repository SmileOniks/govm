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
	"github.com/smileoniks-ctrl/govm/internal/prune"
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
				for mouseGeometryCursor(m) > 18 {
					m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
				}
				for mouseGeometryCursor(m) < 18 {
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
				globals := []string{"? help", "q / ctrl+c quit"}
				for _, surface := range []struct {
					name   string
					build  func(*testing.T, Model) Model
					labels []string
				}{
					{
						name: "Available", build: func(_ *testing.T, m Model) Model { return m },
						labels: []string{"i install", "u use", "d delete", "r refresh", "f find"},
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
						labels: []string{"u use", "d delete", "p prune", "r refresh"},
					},
					{
						name: "Deps", build: func(t *testing.T, m Model) Model {
							return loadDeps(t, m, mouseGeometryDependencies(45))
						},
						labels: []string{"r check updates", "space mark", "a mark all / none", "u update", "b backups"},
					},
					{
						name: "Settings", build: func(t *testing.T, m Model) Model {
							m, _ = mouseClick(t, m, "Settings")
							return m
						},
						labels: []string{"↑ previous", "↓ next", "enter / space toggle or edit", "← previous", "→ next"},
					},
					{
						name: "Applied long filter", build: func(t *testing.T, m Model) Model {
							return applyFilter(t, m, strings.Repeat("界e\u0301", 35))
						},
						labels: []string{"i install", "u use", "d delete", "r refresh", "f find", "esc clear"},
					},
					{
						name: "Available inline delete", build: func(t *testing.T, m Model) Model {
							m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
							return feed(t, m, tea.KeyPressMsg{Code: 'd'})
						},
						labels: []string{"y confirm", "n cancel"},
					},
					{
						name: "Installed inline delete", build: func(t *testing.T, m Model) Model {
							m, _ = mouseClick(t, m, "Installed")
							m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
							return feed(t, m, tea.KeyPressMsg{Code: 'd'})
						},
						labels: []string{"y confirm", "n cancel"},
					},
				} {
					t.Run(surface.name, func(t *testing.T) {
						m := surface.build(t, base(t))
						labels := append(append([]string{}, surface.labels...), globals...)
						mouseGeometryControls(t, m, labels)
						for _, label := range []string{"Available", "Installed", "Deps", "Settings"} {
							mouseText(t, m.View(), label)
						}
						mouseText(t, m.View(), "GoVM is not in your PATH.")
						mouseText(t, m.View(), "界 e\u0301")
						before := m.inputContext()
						m, _ = mouseClick(t, m, "? help")
						mouseGeometryControls(t, m, []string{"esc close help", "ctrl+c quit"})
						m, _ = mouseClick(t, m, "esc close help")
						if m.HelpVisible || m.inputContext() != before {
							t.Fatal("Close did not restore the underlying surface")
						}
					})
				}

				t.Run("Filter editor", func(t *testing.T) {
					m := typeIntoFilter(t, openFilter(t, base(t)), strings.Repeat("界e\u0301", 35))
					mouseGeometryControls(t, m, []string{"enter apply", "esc clear", "tab next tab", "ctrl+c quit"})
					m, _ = mouseClick(t, m, "enter apply")
					if m.filterInputActive() {
						t.Fatal("visible Apply did not commit the filter")
					}
					m, _ = mouseClick(t, m, "esc clear")
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
						mouseGeometryControls(t, m, append(append([]string{}, modal.labels...), "? help", "q quit"))
						before := m.inputContext()
						m, _ = mouseClick(t, m, "? help")
						mouseGeometryControls(t, m, []string{"esc close help", "ctrl+c quit"})
						m, _ = mouseClick(t, m, "esc close help")
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
					{name: "Backup limit editor", row: settingRowDepsBackups, labels: []string{"enter save", "esc cancel"}},
					{name: "Source editor", row: settingRowDistributionSource, labels: []string{"enter check and save", "r reset to official", "esc cancel"}},
				} {
					t.Run(editor.name, func(t *testing.T) {
						m := focusSetting(t, base(t), editor.row)
						m = feed(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
						mouseGeometryControls(t, m, editor.labels)
						m, _ = mouseClick(t, m, "esc cancel")
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
			m, _ = mouseClick(t, m, "? help")
			m, _ = mouseClick(t, m, "esc close help")
			if m.HelpVisible {
				t.Fatal("resizing to a supported viewport did not restore Close")
			}
		})
	}
}
