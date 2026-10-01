package model

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// rowTable owns both selection and the visible window; rendering and hit testing
// therefore never depend on a widget's inaccessible scrolling history.
type rowTable struct {
	rows         []table.Row
	columns      []table.Column
	styles       table.Styles
	cursor       int
	firstVisible int
	width        int
	totalHeight  int
}

func newRowTable(columns []table.Column, width, totalHeight int, styles table.Styles) rowTable {
	return rowTable{columns: columns, width: width, totalHeight: totalHeight, styles: styles, cursor: -1}
}

func (t *rowTable) SetRows(rows []table.Row) {
	t.rows = rows
	t.SetCursor(t.cursor)
}

func (t *rowTable) SetCursor(cursor int) {
	if len(t.rows) == 0 {
		t.cursor, t.firstVisible = -1, 0
		return
	}
	t.cursor = max(0, min(cursor, len(t.rows)-1))
	t.ensureVisible()
}

func (t *rowTable) SetStyles(styles table.Styles) {
	t.styles = styles
	t.ensureVisible()
}

func (t *rowTable) Resize(width, totalHeight int, columns []table.Column) {
	t.width, t.totalHeight, t.columns = max(0, width), max(0, totalHeight), columns
	t.ensureVisible()
}

func (t *rowTable) Move(delta int)   { t.SetCursor(t.cursor + delta) }
func (t rowTable) Rows() []table.Row { return t.rows }
func (t rowTable) SelectedRow() table.Row {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return nil
	}
	return t.rows[t.cursor]
}
func (t rowTable) Cursor() int       { return t.cursor }
func (t rowTable) Width() int        { return t.width }
func (t rowTable) Height() int       { return max(0, t.totalHeight-t.headerHeight()) }
func (t rowTable) headerHeight() int { return 1 + t.styles.Header.GetVerticalFrameSize() }

func (t *rowTable) ensureVisible() {
	bodyHeight := t.Height()
	if len(t.rows) == 0 {
		t.firstVisible = 0
		return
	}
	if t.cursor < t.firstVisible {
		t.firstVisible = t.cursor
	}
	if bodyHeight > 0 && t.cursor >= t.firstVisible+bodyHeight {
		t.firstVisible = t.cursor - bodyHeight + 1
	}
	t.firstVisible = max(0, min(t.firstVisible, max(0, len(t.rows)-bodyHeight)))
}

func tableCell(value string, width int, style lipgloss.Style) string {
	value = ansi.Truncate(value, width, "…")
	// Match the inline cell's normalization without a second Style.Render.
	if strings.ContainsAny(value, "\t\r\n") {
		value = strings.ReplaceAll(value, "\t", "    ")
		value = strings.ReplaceAll(value, "\r\n", "")
		value = strings.ReplaceAll(value, "\n", "")
		value = ansi.Truncate(value, width, "")
	}
	if padding := width - ansi.StringWidth(value); padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return style.Render(value)
}

func (t rowTable) render(rowKind mouseActionKind) renderedSurface {
	if t.width <= 0 || t.totalHeight <= 0 {
		return renderedSurface{}
	}
	cells := make([]string, 0, len(t.columns))
	for _, column := range t.columns {
		if column.Width <= 0 {
			continue
		}
		cells = append(cells, tableCell(column.Title, column.Width, t.styles.Header))
	}
	header := ansi.Cut(lipgloss.JoinHorizontal(lipgloss.Top, cells...), 0, t.width)
	var content strings.Builder
	content.WriteString(header)
	headerHeight := t.headerHeight()
	bodyHeight := t.Height()
	targetCount := min(bodyHeight, len(t.rows))
	if rowKind == mouseDependencyRow {
		targetCount *= 2
	}
	targets := make([]mouseTarget, 0, targetCount+1)
	if bodyHeight > 0 {
		targets = append(targets, mouseTarget{
			rect:   cellRect{y: headerHeight, width: t.width, height: bodyHeight},
			action: mouseAction{kind: mouseScroll},
		})
	}
	end := min(len(t.rows), t.firstVisible+bodyHeight)
	for index := t.firstVisible; index < end; index++ {
		cells = cells[:0]
		for col, column := range t.columns {
			if column.Width <= 0 {
				continue
			}
			value := ""
			if col < len(t.rows[index]) {
				value = t.rows[index][col]
			}
			cells = append(cells, tableCell(value, column.Width, t.styles.Cell))
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, cells...)
		glyphX := t.styles.Cell.GetPaddingLeft() + t.styles.Cell.GetBorderLeftSize()
		if index == t.cursor {
			row = t.styles.Selected.Render(row)
			glyphX += t.styles.Selected.GetPaddingLeft() + t.styles.Selected.GetBorderLeftSize()
		}
		row = ansi.Cut(row, 0, t.width)
		content.WriteByte('\n')
		content.WriteString(row)
		y := headerHeight + index - t.firstVisible
		targets = append(targets, mouseTarget{
			rect:   cellRect{y: y, width: min(t.width, ansi.StringWidth(row)), height: 1},
			action: mouseAction{kind: rowKind, index: index},
		})
		if rowKind == mouseDependencyRow && glyphX < t.width {
			targets = append(targets, mouseTarget{
				rect:   cellRect{x: glyphX, y: y, width: 1, height: 1},
				action: mouseAction{kind: mouseDependencyMark, index: index},
			})
		}
	}
	// The owner places this fragment into the content canvas once. Rows and
	// targets already share the bounded visible range; do not pad it twice.
	surface := renderedSurface{content: content.String(), targets: targets}
	if headerHeight > t.totalHeight {
		return contentCanvas(surface, t.width, t.totalHeight)
	}
	return surface
}
