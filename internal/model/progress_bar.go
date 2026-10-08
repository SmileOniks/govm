package model

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/styles"
)

// installBarPercentWidth is the trailing " 100%" field reserved inside the
// progress block, so callers size the whole block and not just the track.
const installBarPercentWidth = 5

// installBarMinWidth and installBarMaxWidth bound the progress block so it
// stays readable on a narrow terminal without swallowing a wide one.
const (
	installBarMinWidth = 8
	installBarMaxWidth = 28
)

// installBarWidth clamps the space a caller has left over into the block
// width the bar is willing to render at.
func installBarWidth(available int) int {
	return maxInt(installBarMinWidth, minInt(installBarMaxWidth, available))
}

// installBarEighths indexes the partial-cell head by eighths of a cell.
// Index 0 is unused: a zero remainder draws no head at all.
var installBarEighths = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// installBarFill splits ratio into whole filled cells and the eighth-of-a-
// cell head that follows them. head is 0 when the fill lands on a cell
// boundary or when the track is already full, so the caller never draws a
// head past the end of the track.
//
// Separating the arithmetic from the styling is what makes the sub-cell
// behaviour testable without parsing ANSI.
func installBarFill(width int, ratio float64) (full, head int) {
	if width <= 0 {
		return 0, 0
	}
	ratio = math.Max(0, math.Min(1, ratio))

	exact := ratio * float64(width)
	full = int(exact)
	if full >= width {
		return width, 0
	}
	return full, int((exact - float64(full)) * 8)
}

// renderInstallProgressBar renders the download block: a solid fill, a
// sub-cell head, a muted track and the percentage. width covers the whole
// block including the percentage field.
//
// The fill is drawn as background-coloured spaces rather than block glyphs
// so the head can advance an eighth of a cell at a time; a glyph-based bar
// can only move a whole character per step, which reads as stuttering at
// the widths this status line gets.
func renderInstallProgressBar(theme styles.Theme, width int, ratio float64) string {
	track := maxInt(1, width-installBarPercentWidth)
	full, head := installBarFill(track, ratio)

	filled := lipgloss.NewStyle().Background(theme.Primary)
	empty := lipgloss.NewStyle().Background(theme.Muted)
	edge := lipgloss.NewStyle().Foreground(theme.Primary).Background(theme.Muted)

	var b strings.Builder
	b.WriteString(filled.Render(strings.Repeat(" ", full)))
	used := full
	if head > 0 {
		b.WriteString(edge.Render(installBarEighths[head]))
		used++
	}
	if used < track {
		b.WriteString(empty.Render(strings.Repeat(" ", track-used)))
	}

	percent := math.Max(0, math.Min(1, ratio)) * 100
	b.WriteString(lipgloss.NewStyle().
		Foreground(theme.Info).
		Render(fmt.Sprintf("%4.0f%%", percent)))
	return b.String()
}
