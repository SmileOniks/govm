package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestGoDevErrorKeepsTUIClosable(t *testing.T) {
	m := newTestModel(t)

	load := m.projection.startLoad(catalogLoadPurposeRefresh)
	updated, _ := m.Update(catalogLoadFailedMsg{
		RequestID: load.loadRequest.ID,
		Err:       errors.New("failed to connect to go.dev: context deadline exceeded"),
	})
	m = updated.(Model)

	view := stripANSI(m.View().Content)

	for _, want := range []string{"GoVM", "Available", "failed to connect to go.dev"} {
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
