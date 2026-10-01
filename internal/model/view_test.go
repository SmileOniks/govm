package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestViewUsesModernZones(t *testing.T) {
	m := newTestModel(t)

	prev := utils.Version
	utils.Version = "v9.9.9-test"
	defer func() { utils.Version = prev }()

	view := stripANSI(m.View().Content)

	for _, want := range []string{"GoVM", "Go Version Manager", "v9.9.9-test", "● Available", "○ Installed", "✓ Successfully installed Go 1.24.4", "i install", "u use", "d delete", "r refresh", "f find", "? help", "q / ctrl+c quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, view)
		}
	}

	if strings.Contains(view, "Press 'i'") || strings.Contains(view, "[ Available Versions ]") {
		t.Fatalf("expected modern tabs and help text, got:\n%s", view)
	}
}

func TestGoDevErrorKeepsTUIClosable(t *testing.T) {
	m := newTestModel(t)

	load := m.projection.startLoad(catalogLoadPurposeRefresh)
	updated, _ := m.Update(catalogLoadFailedMsg{
		RequestID: load.loadRequest.ID,
		Err:       errors.New("failed to connect to go.dev: context deadline exceeded"),
	})
	m = updated.(Model)

	view := stripANSI(m.View().Content)

	for _, want := range []string{"GoVM", "Available", "failed to connect to go.dev", "r refresh", "q / ctrl+c quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestViewDoesNotRenderShimPathWarningWhenEmpty(t *testing.T) {
	m := newTestModel(t)
	m.ShimPathWarning = ""

	view := stripANSI(m.View().Content)
	if strings.Contains(view, "GoVM is not in your PATH.") {
		t.Fatalf("did not expect PATH warning, got:\n%s", view)
	}
}

func TestViewRendersConfiguredShimPathWarning(t *testing.T) {
	m := newTestModel(t)
	const warning = "Use the configured shim directory."
	m.ShimPathWarning = warning

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, warning) {
		t.Fatalf("expected PATH warning %q, got:\n%s", warning, view)
	}
}

func TestRenderContentCanvasClearsEveryRowToCanvasWidth(t *testing.T) {
	const width = 10
	got := strings.Split(renderContentCanvas("row", width, 3), "\n")

	if len(got) != 3 {
		t.Fatalf("canvas line count = %d, want 3", len(got))
	}
	for i, line := range got {
		if visibleWidth := ansi.StringWidth(line); visibleWidth != width {
			t.Errorf("canvas line %d visible width = %d, want %d; line = %q", i, visibleWidth, width, line)
		}
	}
}

func TestRenderContentCanvasPreservesANSIAndDisplayWidth(t *testing.T) {
	content := "\x1b[31m界e\u0301😀\x1b[0m\nplain"
	const width = 10

	got := strings.Split(renderContentCanvas(content, width, 3), "\n")
	if len(got) != 3 {
		t.Fatalf("canvas line count = %d, want 3", len(got))
	}
	if !strings.Contains(got[0], "\x1b[31m") || !strings.Contains(got[0], "\x1b[0m") {
		t.Fatalf("expected ANSI styling to be preserved, got %q", got[0])
	}
	if plain := stripANSI(got[0]); !strings.HasPrefix(plain, "界e\u0301😀") {
		t.Fatalf("first row = %q, want prefix %q", plain, "界e\u0301😀")
	}
	for i, line := range got {
		if visibleWidth := ansi.StringWidth(line); visibleWidth != width {
			t.Errorf("canvas line %d visible width = %d, want %d; line = %q", i, visibleWidth, width, line)
		}
	}
}

func TestSettingsTabRendersRowsAndHelp(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = SettingsTab

	view := stripANSI(m.View().Content)

	for _, want := range []string{
		"Settings",
		"Deps display: Direct only",
		"Theme: Current",
		"Deps backups: 10",
		"Distribution source: https://go.dev/dl/",
		"Upgrade notice: On",
		"↑/↓",
		"enter",
		"q",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected settings view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestDepsTabRenders(t *testing.T) {
	m := newTestModel(t)

	// Switch to deps tab
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	m = updated.(Model)

	view := stripANSI(m.View().Content)

	if !strings.Contains(view, "Deps") {
		t.Fatalf("expected deps tab label in view, got:\n%s", view)
	}

	if !strings.Contains(view, "check updates") {
		t.Fatalf("expected 'check updates' help hint, got:\n%s", view)
	}
}

func TestMaxInt(t *testing.T) {
	if maxInt(3, 7) != 7 {
		t.Fatal("expected 7")
	}
	if maxInt(7, 3) != 7 {
		t.Fatal("expected 7")
	}
	if maxInt(4, 4) != 4 {
		t.Fatal("expected 4")
	}
}

func TestRenderHelp_ConfirmsDeleteVariant(t *testing.T) {
	m := newVersionCacheTestModel(t)
	m = applyFilter(t, m, "1.26.0")
	m = press(t, m, tea.KeyPressMsg{Code: 'd'})
	got := renderHelpBar(testTheme(), m, 80)
	if !strings.Contains(stripANSI(got), "confirm") {
		t.Fatalf("expected confirm hint, got: %s", got)
	}
	if !strings.Contains(stripANSI(got), "cancel") {
		t.Fatalf("expected cancel hint, got: %s", got)
	}
}

func TestRenderHelp_InstalledIncludesPrune(t *testing.T) {
	got := stripANSI(renderHelpBar(testTheme(), Model{CurrentTab: InstalledTab}, 80))
	if !strings.Contains(got, "p prune") {
		t.Fatalf("expected prune hint, got: %s", got)
	}
}

func TestRenderHelp_RestoreUsesSelectedAction(t *testing.T) {
	tests := []struct {
		name             string
		restoreChoiceYes bool
		want             string
	}{
		{name: "restore selected", restoreChoiceYes: true, want: "enter restore"},
		{name: "cancel selected", restoreChoiceYes: false, want: "enter cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(renderHelpBar(testTheme(), Model{CurrentTab: DepsTab, deps: depsTab{dialog: depsDialog{kind: dialogRestore, choiceYes: tt.restoreChoiceYes}}}, 80))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("expected help to contain %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRenderHelp_DepsTruncatesToWidth(t *testing.T) {
	got := renderHelpBar(testTheme(), Model{CurrentTab: DepsTab}, 20)
	if got == "" {
		t.Fatal("expected non-empty help for deps")
	}
}

func TestRenderStatus_EmptyMessage(t *testing.T) {
	if renderStatus(testTheme(), "info", "", 80) != "" {
		t.Fatal("expected empty result for empty message")
	}
}

func TestRenderStatus_AllTypes(t *testing.T) {
	types := []string{"success", "error", "warning", "info", "unknown"}
	for _, ty := range types {
		got := renderStatus(testTheme(), ty, "msg", 80)
		if !strings.Contains(stripANSI(got), "msg") {
			t.Fatalf("status type %q should include message, got: %s", ty, got)
		}
	}
}

func TestView_NoPanicWhenListEmpty(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, nil)
	view := m.View()
	if view.Content == "" {
		t.Fatal("expected non-empty view")
	}
}
