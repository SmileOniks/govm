package model

import (
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/charmbracelet/x/ansi"
)

func TestInstallBarFill(t *testing.T) {
	cases := []struct {
		name     string
		width    int
		ratio    float64
		wantFull int
		wantHead int
	}{
		{"empty", 20, 0, 0, 0},
		{"full", 20, 1, 20, 0},
		{"half on a cell boundary", 20, 0.5, 10, 0},
		{"sub-cell head only", 20, 0.02, 0, 3},
		{"whole cells plus a head", 20, 0.38, 7, 4},
		{"almost full keeps a head inside the track", 20, 0.999, 19, 7},
		{"negative ratio clamps to empty", 20, -1, 0, 0},
		{"ratio above one clamps to full", 20, 2, 20, 0},
		{"zero width", 0, 0.5, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full, head := installBarFill(tc.width, tc.ratio)
			if full != tc.wantFull || head != tc.wantHead {
				t.Fatalf("installBarFill(%d, %v) = (%d, %d), want (%d, %d)",
					tc.width, tc.ratio, full, head, tc.wantFull, tc.wantHead)
			}
			if full+min1(head) > tc.width {
				t.Fatalf("fill overflows the track: full=%d head=%d width=%d", full, head, tc.width)
			}
		})
	}
}

// TestInstallBarFillAdvancesWithinACell is the reason this bar exists: a
// ratio change too small to move a whole cell must still move the head.
func TestInstallBarFillAdvancesWithinACell(t *testing.T) {
	const width = 20
	fullA, headA := installBarFill(width, 0.300)
	fullB, headB := installBarFill(width, 0.320)

	if fullA != fullB {
		t.Fatalf("expected both ratios inside the same cell, got %d and %d", fullA, fullB)
	}
	if headB <= headA {
		t.Fatalf("head did not advance within the cell: %d -> %d", headA, headB)
	}
}

func TestRenderInstallProgressBarKeepsRequestedWidth(t *testing.T) {
	theme := styles.NewTheme(config.ThemeCurrent)
	for _, width := range []int{installBarMinWidth, 14, installBarMaxWidth} {
		for _, ratio := range []float64{0, 0.07, 0.5, 0.999, 1} {
			got := ansi.StringWidth(renderInstallProgressBar(theme, width, ratio))
			if got != width {
				t.Fatalf("width(bar(%d, %v)) = %d, want %d", width, ratio, got, width)
			}
		}
	}
}

func TestRenderInstallProgressBarShowsPercentage(t *testing.T) {
	theme := styles.NewTheme(config.ThemeCurrent)
	bar := stripANSI(renderInstallProgressBar(theme, installBarMaxWidth, 0.5))
	if !strings.Contains(bar, "50%") {
		t.Fatalf("bar = %q, want it to contain %q", bar, "50%")
	}
}

func TestInstallBarWidthClampsToBounds(t *testing.T) {
	if got := installBarWidth(2); got != installBarMinWidth {
		t.Fatalf("installBarWidth(2) = %d, want %d", got, installBarMinWidth)
	}
	if got := installBarWidth(200); got != installBarMaxWidth {
		t.Fatalf("installBarWidth(200) = %d, want %d", got, installBarMaxWidth)
	}
	if got := installBarWidth(17); got != 17 {
		t.Fatalf("installBarWidth(17) = %d, want 17", got)
	}
}

// min1 counts the head as the single cell it occupies when it is drawn.
func min1(head int) int {
	if head > 0 {
		return 1
	}
	return 0
}
