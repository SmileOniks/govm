package model

import (
	"strings"

	"github.com/SmileOniks/govm/internal/styles"
	"github.com/charmbracelet/x/ansi"
)

const maxDependencyListLines = 6

type viewportSize struct {
	Width  int
	Height int
}

func dialogWidth(viewport viewportSize) int {
	width := viewport.Width
	if width < 1 {
		width = 64
	}
	return min(64, width)
}

func dialogBodyHeight(t styles.Theme, viewport viewportSize, footer renderedSurface) int {
	height := viewport.Height
	if height <= 0 {
		height = 24
	}
	return max(1, height-t.DialogBoxStyle.GetVerticalFrameSize()-surfaceHeight(footer))
}

func surfaceHeight(surface renderedSurface) int {
	if surface.content == "" {
		return 0
	}
	return len(strings.Split(surface.content, "\n"))
}

func renderDialogControls(t styles.Theme, width int) renderedSurface {
	section := dialogGlobalKeyBindings()
	// The modal footer uses a compact quit label; its canonical key still
	// comes from the registry shared with the ordinary context controls.
	section.bindings[1].mouseControls[0].label = "q quit"
	return renderControls(t, []helpSection{section}, width, false)
}

// renderDialog wraps content in the themed dialog border box. It takes
// the theme as a parameter rather than reading package-level style
// state; the previous init()/rebuildDialogStyles machinery is gone
// along with the style vars it maintained.
func renderDialog(t styles.Theme, surface renderedSurface, errorStyle bool, viewport viewportSize) renderedSurface {
	width := dialogWidth(viewport)
	style := t.DialogBoxStyle
	if errorStyle {
		style = t.DialogErrorBoxStyle
	}
	contentWidth := max(1, width-style.GetHorizontalFrameSize())
	contentHeight := max(1, viewport.Height-style.GetVerticalFrameSize())
	lines := strings.Split(surface.content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	targets := make([]mouseTarget, 0, len(surface.targets))
	left := style.GetPaddingLeft() + style.GetBorderLeftSize()
	top := style.GetPaddingTop() + style.GetBorderTopSize()
	for row, line := range lines {
		lines[row] = ansi.Cut(line, 0, contentWidth)
		for _, target := range surface.targets {
			part := target.rect.intersect(cellRect{y: row, width: contentWidth, height: 1})
			if part.width <= 0 || part.height <= 0 {
				continue
			}
			part.x += left
			part.y += top
			target.rect = part
			targets = append(targets, target)
		}
	}
	return renderedSurface{content: style.Width(width).Render(strings.Join(lines, "\n")), targets: targets}
}

func overlayDialog(background, dialog renderedSurface, viewport viewportSize) renderedSurface {
	width, height := viewport.Width, viewport.Height
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}

	dialogLines := strings.Split(strings.TrimRight(dialog.content, "\n"), "\n")
	bgLines := strings.Split(background.content, "\n")
	if len(bgLines) > height {
		bgLines = bgLines[:height]
	}
	blankRow := strings.Repeat(" ", width)
	for len(bgLines) < height {
		bgLines = append(bgLines, blankRow)
	}

	startRow := 0
	if height > len(dialogLines) {
		startRow = (height - len(dialogLines)) / 2
	}
	if startRow+len(dialogLines) > height {
		startRow = height - len(dialogLines)
		if startRow < 0 {
			startRow = 0
		}
	}
	endRow := startRow + len(dialogLines)
	if endRow > height {
		endRow = height
	}
	dialogLines = dialogLines[:endRow-startRow]

	targets := make([]mouseTarget, 0, len(dialog.targets))
	for i, dline := range dialogLines {
		row := startRow + i
		bgLine := bgLines[row]
		bgW := ansi.StringWidth(bgLine)
		dW := ansi.StringWidth(dline)
		col := 0
		if bgW > dW {
			col = (bgW - dW) / 2
		}
		bgLines[row] = spliceCentered(bgLine, dline, col, bgW, dW)
		for _, target := range dialog.targets {
			part := target.rect.intersect(cellRect{y: i, width: dW, height: 1})
			if part.width <= 0 || part.height <= 0 {
				continue
			}
			part.x += col
			part.y = row
			target.rect = part.intersect(cellRect{y: row, width: min(width, bgW), height: 1})
			if target.rect.width > 0 {
				targets = append(targets, target)
			}
		}
	}

	return renderedSurface{content: strings.Join(bgLines, "\n"), targets: targets}
}

func spliceCentered(bg, overlay string, col, bgW, overlayW int) string {
	if col < 0 {
		col = 0
	}
	// col is a column index measured in visible cells (the value the caller
	// got from ansi.StringWidth / lipgloss.Width). Slicing by []rune would
	// (a) drop a wide rune that straddles the cut point and (b) chop ANSI
	// escape sequences in half, which corrupts the surrounding styled
	// table output. Use ANSI-aware cuts instead.
	if col > bgW {
		col = bgW
	}

	prefix := ansi.Cut(bg, 0, col)
	suffix := ""
	if col+overlayW < bgW {
		suffix = ansi.Cut(bg, col+overlayW, bgW)
	}
	return prefix + overlay + suffix
}
