package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	coredeps "github.com/smileoniks-ctrl/govm/internal/deps"
)

func TestRenderDependencyUpdateDialogContainsWarning(t *testing.T) {
	dialog := stripANSI(depsDialog{
		kind:          dialogUpdate,
		choiceYes:     true,
		updateEntries: []coredeps.DependencyUpdateEntry{{Path: "example.com/lib", OldVersion: "v1.0.0", NewVersion: "v1.1.0"}},
	}.render(testTheme(), depsTab{}, viewportSize{Width: 64, Height: 20}))

	for _, want := range []string{"Warning", "Level:", "Patch", "Minor", "Latest", "Scope:", "All", "will be updated", "Yes", "No"} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("expected dialog to contain %q, got:\n%s", want, dialog)
		}
	}
}

func TestRenderDependencyUpdateDialogListsModules(t *testing.T) {
	entries := []coredeps.DependencyUpdateEntry{
		{Path: "github.com/example/lib", OldVersion: "v1.0.0", NewVersion: "v1.1.0"},
		{Path: "github.com/example/other", OldVersion: "v2.0.0", NewVersion: "v2.1.0"},
	}
	dialog := stripANSI(depsDialog{
		kind:          dialogUpdate,
		choiceYes:     true,
		updateEntries: entries,
	}.render(testTheme(), depsTab{}, viewportSize{Width: 64, Height: 20}))

	for _, want := range []string{"github.com/example/lib", "v1.0.0", "v1.1.0", "github.com/example/other"} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("expected dialog to contain %q, got:\n%s", want, dialog)
		}
	}
}

func TestRenderDependencyUpdateDialogTruncatesLongLists(t *testing.T) {
	entries := make([]coredeps.DependencyUpdateEntry, 0, 12)
	for i := 0; i < 12; i++ {
		entries = append(entries, coredeps.DependencyUpdateEntry{
			Path:       fmt.Sprintf("github.com/example/dep%d", i),
			OldVersion: "v1.0.0",
			NewVersion: "v1.1.0",
		})
	}
	dialog := stripANSI(depsDialog{
		kind:          dialogUpdate,
		choiceYes:     true,
		updateEntries: entries,
	}.render(testTheme(), depsTab{}, viewportSize{Width: 64, Height: 20}))

	if !strings.Contains(dialog, "and") || !strings.Contains(dialog, "more") {
		t.Fatalf("expected truncation hint in dialog, got:\n%s", dialog)
	}
}

func TestRenderDependencyRestoreDialogKeepsCursorVisible(t *testing.T) {
	backups := make([]coredeps.DependencyBackupInfo, 0, 7)
	for i := 0; i < 7; i++ {
		backups = append(backups, coredeps.DependencyBackupInfo{
			Name:    fmt.Sprintf("2026-07-09_12-00-0%d.json", i),
			Kind:    coredeps.DependencyBackupKindPreUpdate,
			Updated: i,
		})
	}

	dialog := stripANSI(depsDialog{kind: dialogRestore, choiceYes: true, cursor: 6, maxCursor: 6}.
		render(testTheme(), depsTab{backups: backups}, viewportSize{Width: 64, Height: 20}))

	if !strings.Contains(dialog, "> 2026-07-09_12-00-06.json") {
		t.Fatalf("expected selected backup to be visible, got:\n%s", dialog)
	}
}

func TestRenderDependencyChecksDialogContainsCommands(t *testing.T) {
	dialog := stripANSI(depsDialog{kind: dialogChecks, choiceYes: true}.
		render(testTheme(), depsTab{}, viewportSize{Width: 64, Height: 20}))

	for _, want := range []string{"Run checks?", "go test", "go vet", "Yes", "No"} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("expected dialog to contain %q, got:\n%s", want, dialog)
		}
	}
}

func TestRenderDependencyRollbackDialogContainsCommand(t *testing.T) {
	result := coredeps.DependencyCheckResult{
		OK:      false,
		Command: "go test ./...",
		Output:  "FAIL: example_test.go:10: expected 1, got 2",
	}
	dialog := stripANSI(depsDialog{
		kind:        dialogRollback,
		choiceYes:   true,
		checkResult: &result,
	}.render(testTheme(), depsTab{}, viewportSize{Width: 64, Height: 20}))

	for _, want := range []string{"Checks failed", "go test ./...", "FAIL: example_test", "Roll back", "Keep"} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("expected dialog to contain %q, got:\n%s", want, dialog)
		}
	}
}

func TestRenderDependencyDialogsRespectViewportWidth(t *testing.T) {
	longPath := "github.com/acme/very-long-module-name-that-must-not-overflow-the-terminal"
	longOutput := "FAIL: " + strings.Repeat("a", 100)
	backups := []coredeps.DependencyBackupInfo{{
		Name:    "2026-07-09_12-00-00-a-very-long-backup-filename.json",
		Kind:    coredeps.DependencyBackupKindPreUpdate,
		Updated: 1,
	}}

	tests := []struct {
		name   string
		render func(width int) string
	}{
		{
			name: "update",
			render: func(width int) string {
				return depsDialog{
					kind:      dialogUpdate,
					choiceYes: true,
					updateEntries: []coredeps.DependencyUpdateEntry{{
						Path: longPath, OldVersion: "v1.0.0", NewVersion: "v1.1.0",
					}},
				}.render(
					testTheme(),
					depsTab{},
					viewportSize{Width: width, Height: 20})
			},
		},
		{
			name: "checks",
			render: func(width int) string {
				return depsDialog{kind: dialogChecks, choiceYes: true}.
					render(testTheme(), depsTab{}, viewportSize{Width: width, Height: 20})
			},
		},
		{
			name: "rollback",
			render: func(width int) string {
				return depsDialog{
					kind:      dialogRollback,
					choiceYes: true,
					checkResult: &coredeps.DependencyCheckResult{
						Command: longPath,
						Output:  longOutput,
					},
				}.render(
					testTheme(),
					depsTab{},
					viewportSize{Width: width, Height: 20})
			},
		},
		{
			name: "restore",
			render: func(width int) string {
				return depsDialog{kind: dialogRestore, choiceYes: true}.
					render(testTheme(), depsTab{backups: backups}, viewportSize{Width: width, Height: 20})
			},
		},
	}

	for _, width := range []int{64, 80, 120} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s-%d", tt.name, width), func(t *testing.T) {
				for _, line := range strings.Split(tt.render(width), "\n") {
					if got := ansi.StringWidth(line); got > width {
						t.Fatalf("line width = %d, want <= %d: %q", got, width, line)
					}
				}
			})
		}
	}
}

func TestDialogRendersOverDepsView(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: '\t'})
	updated, _ = updated.Update(dependenciesMsg{
		{Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0"},
	})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: 'u'})
	updated, _ = updated.Update(coredeps.CheckUpdatesDoneEvent{Dependencies: []coredeps.ModuleDependency{{
		Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0",
	}}})
	m = updated.(Model)

	view := stripANSI(m.View().Content)

	if !strings.Contains(view, "Warning") {
		t.Fatal("expected warning text in view when dialog is open")
	}
	if !strings.Contains(view, "Deps") {
		t.Fatal("expected deps tab content to still be visible behind dialog")
	}
	// Regression guard: the actual dependency row must still be rendered
	// somewhere on screen above or below the modal. Previously the dialog
	// erased the whole deps table.
	if !strings.Contains(view, "github.com/example/lib") {
		t.Fatalf("expected dependency row to remain visible when confirm dialog is open, got:\n%s", view)
	}
}

func TestSpliceCentered_Basic(t *testing.T) {
	bg, overlay := "hello world", "ABC"
	got := spliceCentered(bg, overlay, 3, ansi.StringWidth(bg), ansi.StringWidth(overlay))
	want := "helABCworld"
	if got != want {
		t.Fatalf("spliceCentered: got %q, want %q", got, want)
	}
}

func TestSpliceCentered_EdgeCases(t *testing.T) {
	cases := []struct {
		name        string
		bg, overlay string
		col         int
		want        string
	}{
		{"overlay-shorter-than-bg", "abcdef", "XY", 1, "aXYdef"},
		{"col-zero", "abcdef", "XY", 0, "XYcdef"},
		{"col-negative-clamped", "abcdef", "XY", -5, "XYcdef"},
		{"col-beyond-bg-clamped", "abc", "XYZ", 100, "abcXYZ"},
		{"col-at-end", "abc", "XY", 3, "abcXY"},
		{"middle-replacement", "abcdefgh", "OK", 3, "abcOKfgh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spliceCentered(tc.bg, tc.overlay, tc.col, ansi.StringWidth(tc.bg), ansi.StringWidth(tc.overlay))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOverlayDialog_ReplacesCenterRegion(t *testing.T) {
	bg := strings.Repeat("line\n", 9) + "line"
	dlg := "AAA\nBBB\nCCC"
	out := overlayDialog(bg, dlg, viewportSize{Width: 20, Height: 10})
	stripped := stripANSI(out)
	for _, want := range []string{"AAA", "BBB", "CCC"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines preserved, got %d", len(lines))
	}
}

func TestOverlayDialog_ClampsToSize(t *testing.T) {
	bg := strings.Repeat("bg\n", 15) + "bg"
	dlg := "VISIBLE"
	out := overlayDialog(bg, dlg, viewportSize{})
	stripped := stripANSI(out)
	if !strings.Contains(stripped, "VISIBLE") {
		t.Fatalf("expected dialog content in output, got:\n%s", out)
	}
}

// TestOverlayDialog_PreservesRowsOutsideDialog guards against the regression
// captured in CleanShot 2026-06-27 at 17.54.47@2x.png, where the dependency
// update confirmation dialog erased most of the deps table because
// overlayDialog built a full-height canvas and overwrote every row, even the
// ones outside the actual modal box.
func TestOverlayDialog_PreservesRowsOutsideDialog(t *testing.T) {
	// 20 background rows, each tagged with a unique marker. The dialog is
	// only 3 rows tall, so rows 0..7 and 11..19 must survive untouched.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("BG_ROW_%02d", i))
	}
	bg := strings.Join(lines, "\n")
	dlg := "AAA\nBBB\nCCC"

	out := overlayDialog(bg, dlg, viewportSize{Width: 30, Height: 20})
	stripped := stripANSI(out)
	strippedLines := strings.Split(stripped, "\n")

	overwritten := 0
	for _, l := range strippedLines {
		if strings.Contains(l, "AAA") || strings.Contains(l, "BBB") || strings.Contains(l, "CCC") {
			overwritten++
		}
	}
	if overwritten > 3 {
		t.Fatalf("overlayDialog overwrote %d background rows with dialog content; expected at most 3:\n%s", overwritten, stripped)
	}

	survivors := 0
	for _, marker := range []string{
		"BG_ROW_00", "BG_ROW_01", "BG_ROW_02", "BG_ROW_03", "BG_ROW_04",
		"BG_ROW_15", "BG_ROW_16", "BG_ROW_17", "BG_ROW_18", "BG_ROW_19",
	} {
		if strings.Contains(stripped, marker) {
			survivors++
		}
	}
	if survivors < 8 {
		t.Fatalf("expected at least 8 background rows preserved outside the dialog, got %d:\n%s", survivors, stripped)
	}
}

// TestSpliceCentered_UsesVisibleColumnsWithANSI guards against spliceCentered
// slicing by rune count when col is measured in visible cells, which used to
// break ANSI escape sequences in styled table output.
func TestSpliceCentered_UsesVisibleColumnsWithANSI(t *testing.T) {
	// A styled background line where the visible content is 9 cells but the
	// raw string contains ANSI escape sequences that pad it to many more
	// bytes/runes.
	styled := "\x1b[31mhello    \x1b[0m" // 9 visible cells: h e l l o _ _ _ _
	overlay := "X"
	// col is measured in visible cells; placing at col=4 must REPLACE the
	// "o" at cell 4 with "X" and keep the trailing 4 spaces plus the
	// surrounding ANSI codes intact.
	got := spliceCentered(styled, overlay, 4, ansi.StringWidth(styled), ansi.StringWidth(overlay))

	if w := ansi.StringWidth(got); w != 9 {
		t.Fatalf("expected result width 9, got %d (raw: %q)", w, got)
	}
	plain := stripANSI(got)
	if !strings.HasPrefix(plain, "hellX") {
		t.Fatalf("expected plain output to start with %q, got %q", "hellX", plain)
	}
	if !strings.HasSuffix(plain, "    ") {
		t.Fatalf("expected plain output to end with 4 spaces, got %q", plain)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("expected ANSI opening sequence to be preserved, got %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("expected ANSI reset sequence to be preserved, got %q", got)
	}
}

func TestOverlayDialogPreservesANSIWideAndCombiningContent(t *testing.T) {
	background := strings.Join([]string{
		"\x1b[32mROW_00  界e\u0301😀      \x1b[0m",
		"\x1b[32mROW_01  界e\u0301😀      \x1b[0m",
		"\x1b[32mROW_02  界e\u0301😀      \x1b[0m",
		"\x1b[32mROW_03  界e\u0301😀      \x1b[0m",
		"\x1b[32mROW_04  界e\u0301😀      \x1b[0m",
	}, "\n")
	dialog := "\x1b[35m界e\u0301😀\x1b[0m"

	got := overlayDialog(background, dialog, viewportSize{Width: 20, Height: 5})
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("line count = %d, want 5", len(lines))
	}
	if !strings.Contains(lines[2], "\x1b[35m") || !strings.Contains(lines[2], "\x1b[0m") {
		t.Fatalf("expected ANSI dialog styling to be preserved, got %q", lines[2])
	}
	if plain := stripANSI(lines[2]); !strings.Contains(plain, "界e\u0301😀") {
		t.Fatalf("center row = %q, want dialog content", plain)
	}
	for i, line := range lines {
		wantWidth := 19
		if i == 2 {
			// ansi.Cut preserves a grapheme that straddles the replacement
			// boundary rather than splitting the emoji cell.
			wantWidth = 20
		}
		if width := ansi.StringWidth(line); width != wantWidth {
			t.Errorf("line %d width = %d, want %d", i, width, wantWidth)
		}
	}
	if !strings.Contains(stripANSI(lines[0]), "ROW_00") || !strings.Contains(stripANSI(lines[4]), "ROW_04") {
		t.Fatalf("expected rows outside dialog to remain intact, got:\n%s", stripANSI(got))
	}
}

func TestViewShowsRollbackDialog(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = confirmRollbackFrom(t, updated.(Model), coredeps.DependencyCheckResult{
		Command: "go test ./...",
		Output:  "FAIL: foo_test.go:42",
	})

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "Checks failed") {
		t.Fatalf("expected rollback dialog in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Roll back") {
		t.Fatal("expected rollback button in view")
	}
}

func TestViewShowsChecksDialog(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = confirmChecksFrom(t, updated.(Model))

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "Run checks?") {
		t.Fatalf("expected checks dialog in view, got:\n%s", view)
	}
}
