package model

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

func TestRowTableVisibleSelection(t *testing.T) {
	columns := []table.Column{{Title: "Version", Width: 12}}
	rows := make([]table.Row, 12)
	for index := range rows {
		rows[index] = table.Row{fmt.Sprintf("version-%02d", index)}
	}
	newTable := func() rowTable { return newRowTable(columns, 20, 4, tableStyles(testTheme())) }
	assertVisible := func(t *testing.T, tbl rowTable, cursor, first int) {
		t.Helper()
		if tbl.Cursor() != cursor || tbl.firstVisible != first {
			t.Fatalf("selection/window = %d/%d, want %d/%d", tbl.Cursor(), tbl.firstVisible, cursor, first)
		}
		if cursor < 0 {
			return
		}
		rendered := ansi.Strip(tbl.render(mouseInstalledRow).content)
		selected := tbl.SelectedRow()[0]
		if !strings.Contains(rendered, selected) {
			t.Fatalf("selected row %q is not visible:\n%s", selected, rendered)
		}
		if first > 0 && strings.Contains(rendered, rows[first-1][0]) {
			t.Fatalf("row before visible window leaked into rendering:\n%s", rendered)
		}
	}
	t.Run("empty refill and clear", func(t *testing.T) {
		tbl := newTable()
		tbl.SetRows(nil)
		if tbl.SelectedRow() != nil || tbl.Cursor() != -1 || tbl.firstVisible != 0 {
			t.Fatal("empty table retained a selection")
		}
		for _, target := range tbl.render(mouseInstalledRow).targets {
			if target.action.kind == mouseInstalledRow {
				t.Fatal("empty table rendered a selectable row")
			}
		}
		tbl.SetRows(rows)
		assertVisible(t, tbl, 0, 0)
		tbl.Move(8)
		assertVisible(t, tbl, 8, 6)
		tbl.SetRows(nil)
		if tbl.SelectedRow() != nil || tbl.Cursor() != -1 {
			t.Fatal("clearing rows retained selected data")
		}
		tbl.SetRows(rows[:2])
		assertVisible(t, tbl, 0, 0)
	})
	t.Run("direct selection movement and bounds", func(t *testing.T) {
		tbl := newTable()
		tbl.SetRows(rows)
		tbl.SetCursor(7)
		assertVisible(t, tbl, 7, 5)
		tbl.Move(-1)
		assertVisible(t, tbl, 6, 5)
		tbl.SetCursor(1)
		assertVisible(t, tbl, 1, 1)
		tbl.Move(-100)
		assertVisible(t, tbl, 0, 0)
		tbl.Move(100)
		assertVisible(t, tbl, 11, 9)
	})
	t.Run("resize shrink and zero body", func(t *testing.T) {
		tbl := newTable()
		tbl.SetRows(rows)
		tbl.SetCursor(10)
		tbl.Resize(20, 6, columns)
		assertVisible(t, tbl, 10, 7)
		tbl.Resize(20, 3, columns)
		assertVisible(t, tbl, 10, 9)
		tbl.SetRows(rows[:4])
		assertVisible(t, tbl, 3, 2)
		tbl.Resize(20, 1, columns)
		if tbl.Height() != 0 {
			t.Fatalf("body height = %d, want zero", tbl.Height())
		}
		surface := tbl.render(mouseInstalledRow)
		if strings.Contains(ansi.Strip(surface.content), tbl.SelectedRow()[0]) {
			t.Fatal("zero-height body rendered a row")
		}
		for _, target := range surface.targets {
			if target.action.kind == mouseInstalledRow {
				t.Fatal("zero-height body has row targets")
			}
		}
		tbl.Resize(20, 4, columns)
		assertVisible(t, tbl, 3, 1)
	})
}

func TestRowTableSelectionPreservesGeometry(t *testing.T) {
	for _, theme := range []config.ThemeName{config.ThemeCurrent, config.ThemeLight} {
		for _, width := range []int{64, 80, 129, 130} {
			t.Run(fmt.Sprintf("%s/%d", theme, width), func(t *testing.T) {
				tbl := newRowTable(
					dependencyTableColumns(width), width, 4,
					tableStyles(styles.NewTheme(theme)),
				)
				tbl.SetRows([]table.Row{
					{"[○] example.com/" + strings.Repeat("界e\u0301", 40), "v1.0.0", "v1.1.0", "update avail"},
					{"[●] example.com/other", "v2.0.0", "v2.1.0", "update avail"},
				})
				before := tbl.render(mouseDependencyRow).content
				tbl.SetCursor(1)
				after := tbl.render(mouseDependencyRow).content
				if before == after {
					t.Fatal("moving the cursor did not change the visible highlight")
				}
				if ansi.Strip(before) != ansi.Strip(after) {
					t.Fatalf("selection moved or clipped table cells:\nbefore:\n%s\nafter:\n%s",
						ansi.Strip(before), ansi.Strip(after))
				}
				for _, line := range strings.Split(after, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatal("highlighted row exceeded the table width")
					}
				}
			})
		}
	}
}
