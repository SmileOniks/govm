package model

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

var (
	benchColumnsSink []table.Column
	benchModelSink   tea.Model
	benchStringSink  string
	benchViewSink    tea.View
)

// benchModel builds a Model that mimics a realistic TUI state for
// benchmarking the View/Update hot paths without network or filesystem
// side effects.
func benchModel(b *testing.B) Model {
	b.Helper()

	versions := make([]utils.GoVersion, 0, 30)
	for i := 0; i < 30; i++ {
		version := strconv.Itoa(i/10+1) + ".2" + strconv.Itoa(i%10)
		v := utils.GoVersion{
			Version:   version,
			Filename:  "go1.2.darwin-arm64.tar.gz",
			Installed: i%3 == 0,
			Active:    i == 0,
		}
		if v.Installed {
			v.Path = "/Users/example/.govm/versions/go" + v.Version
		}
		versions = append(versions, v)
	}

	depItems := make([]deps.ModuleDependency, 0, 10)
	for i := 0; i < 10; i++ {
		depItems = append(depItems, deps.ModuleDependency{
			Path:    "github.com/example/dep" + string(rune('a'+i)),
			Version: "v1.0.0",
			Latest:  "v1.1.0",
		})
	}

	m := New("", config.DefaultSettings(), newMemorySettingsStore(config.DefaultSettings()), "", styles.NewTheme(config.ThemeCurrent))
	if cmd, err := replaceVersions(&m, versions); err != nil {
		b.Fatalf("replaceVersions: %v", err)
	} else {
		_ = cmd
	}
	m.projection.resize(80, 24)
	m = loadDeps(b, m, depItems)
	m.CurrentTab = AvailableTab
	m.Width = 80
	m.Height = 24
	return m
}

func benchModelAtViewport(b *testing.B, width int) Model {
	b.Helper()
	m := benchModel(b)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	return updated.(Model)
}

func BenchmarkView_NormalLayout(b *testing.B) {
	m := benchModel(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View()
	}
}

func BenchmarkView_InstalledTab(b *testing.B) {
	m := benchModel(b)
	m.CurrentTab = 1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View()
	}
}

func BenchmarkVersionCatalog_MutationAndSync(b *testing.B) {
	m := benchModel(b)
	const (
		version = "1.21"
		path    = "/Users/example/.govm/versions/go1.21"
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var outcome catalogProjectionOutcome
		if i%2 == 0 {
			op := m.projection.startMutation(catalogMutationInstall, version)
			outcome = m.projection.completeInstall(op.id, version, path, nil)
		} else {
			op := m.projection.startMutation(catalogMutationDeletion, version)
			outcome = m.projection.completeDeletion(op.id, version, nil)
		}
		if outcome.err != nil {
			b.Fatal(outcome.err)
		}
	}
}

func BenchmarkView_DepsTab(b *testing.B) {
	m := benchModel(b)
	m.CurrentTab = 2
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View()
	}
}

func BenchmarkView_DepsTabWithDialog(b *testing.B) {
	m := openUpdateDialog(b, benchModel(b))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View()
	}
}

func BenchmarkView_DepsTabWithPhysicalViewport(b *testing.B) {
	for _, width := range []int{64, 80, 120} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			m := openUpdateDialog(b, benchModel(b))
			m.TermWidth = width
			m.TermHeight = 30

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchViewSink = m.View()
			}
		})
	}
}

func BenchmarkView_DepsTabWithPhysicalViewportWithoutDialog(b *testing.B) {
	for _, width := range []int{64, 80, 120} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			m := benchModelAtViewport(b, width)
			m.CurrentTab = DepsTab

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchViewSink = m.View()
			}
		})
	}
}

func BenchmarkView_DepsTabWithPhysicalViewportWithDialog(b *testing.B) {
	for _, width := range []int{64, 80, 120} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			m := openUpdateDialog(b, benchModelAtViewport(b, width))

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchViewSink = m.View()
			}
		})
	}
}

func BenchmarkUpdate_WindowSizeMsg(b *testing.B) {
	m := benchModel(b)
	msg := tea.WindowSizeMsg{Width: 100, Height: 30}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updated, _ := m.Update(msg)
		benchModelSink = updated
	}
}

func BenchmarkUpdate_KeyPressMsg(b *testing.B) {
	m := benchModel(b)
	msg := tea.KeyPressMsg{Code: 'q'}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updated, _ := m.Update(msg)
		benchModelSink = updated
	}
}

func BenchmarkProgramModelUpdate_KeyPressMsg(b *testing.B) {
	m := newProgramModel(benchModel(b))
	msg := tea.KeyPressMsg{Code: 'r'}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchModelSink, _ = m.Update(msg)
	}
}

func BenchmarkDependencyTableColumns(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchColumnsSink = dependencyTableColumns(120)
	}
}

func BenchmarkInstalledTableColumns(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchColumnsSink = installedTableColumns(120)
	}
}

func BenchmarkOverlayDialog(b *testing.B) {
	for _, size := range []viewportSize{
		{Width: 64, Height: 20},
		{Width: 80, Height: 30},
		{Width: 140, Height: 40},
	} {
		b.Run(strconv.Itoa(size.Width)+"x"+strconv.Itoa(size.Height), func(b *testing.B) {
			row := strings.Repeat("x", size.Width)
			bg := strings.TrimSuffix(strings.Repeat(row+"\n", size.Height), "\n")
			dlg := strings.Repeat("overlay dialog content\n", 4)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchStringSink = overlayDialog(bg, dlg, size)
			}
		})
	}
}

func BenchmarkSpliceCentered(b *testing.B) {
	bg := "this is a moderately long background line for benchmarking splice behavior"
	overlay := "OVERLAY"
	bgW := ansi.StringWidth(bg)
	overlayW := ansi.StringWidth(overlay)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = spliceCentered(bg, overlay, 12, bgW, overlayW)
	}
}

func BenchmarkRenderContentCanvas(b *testing.B) {
	cases := []struct {
		name    string
		content string
	}{
		{name: "plain", content: "first row\nsecond row\nthird row"},
		{name: "ANSI", content: "\x1b[31mfirst row\x1b[0m\n\x1b[32msecond row\x1b[0m\n\x1b[34mthird row\x1b[0m"},
		{name: "wide", content: "界e\u0301😀 first row\n界e\u0301😀 second row\n界e\u0301😀 third row"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchStringSink = renderContentCanvas(tc.content, 80, 24)
			}
		})
	}
}

func BenchmarkRenderDependencyUpdateDialog(b *testing.B) {
	entries := []deps.DependencyUpdateEntry{
		{Path: "github.com/example/dep1", OldVersion: "v1.0.0", NewVersion: "v1.1.0"},
		{Path: "github.com/example/dep2", OldVersion: "v2.0.0", NewVersion: "v2.1.0"},
		{Path: "github.com/example/dep3", OldVersion: "v3.0.0", NewVersion: "v3.1.0"},
	}
	updateYes := depsDialog{kind: dialogUpdate, choiceYes: true, updateEntries: entries}
	updateNo := depsDialog{kind: dialogUpdate, choiceYes: false, updateEntries: entries}
	theme := styles.NewTheme(config.ThemeCurrent)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStringSink = updateYes.render(theme, depsTab{}, viewportSize{Width: 64, Height: 20})
		benchStringSink = updateNo.render(theme, depsTab{}, viewportSize{Width: 64, Height: 20})
	}
}

func BenchmarkRenderDependencyDialogsMinimumViewport(b *testing.B) {
	result := deps.DependencyCheckResult{
		Command: "go test ./...",
		Output:  "FAIL: dependency test output that is deliberately longer than the compact dialog content area",
	}
	backups := []deps.DependencyBackupInfo{{
		Name:    "2026-07-09_12-00-00-a-long-backup-filename.json",
		Kind:    deps.DependencyBackupKindPreUpdate,
		Updated: 1,
	}}
	checksDialog := depsDialog{kind: dialogChecks, choiceYes: true}
	rollbackDialog := depsDialog{kind: dialogRollback, choiceYes: true, checkResult: &result}
	restoreDialog := depsDialog{kind: dialogRestore, choiceYes: true}
	restoreDeps := depsTab{backups: backups}
	theme := styles.NewTheme(config.ThemeCurrent)

	for _, width := range []int{64, 80} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				viewport := viewportSize{Width: width, Height: 20}
				benchStringSink = checksDialog.render(theme, depsTab{}, viewport)
				benchStringSink = rollbackDialog.render(theme, depsTab{}, viewport)
				benchStringSink = restoreDialog.render(theme, restoreDeps, viewport)
			}
		})
	}
}
