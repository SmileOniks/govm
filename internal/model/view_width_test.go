package model

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/charmbracelet/x/ansi"
)

func TestViewRespectsTerminalWidth(t *testing.T) {
	dialogs := []struct {
		name  string
		setup func(*testing.T, *Model)
	}{
		{
			name:  "without dialog",
			setup: func(*testing.T, *Model) {},
		},
		{
			name: "update dialog",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model { return confirmApplyFrom(t, m) })
			},
		},
		{
			name: "checks dialog",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model { return confirmChecksFrom(t, m) })
			},
		},
		{
			name: "rollback dialog",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return confirmRollbackFrom(t, m, deps.DependencyCheckResult{
						Command: "go test ./...",
						Output:  strings.Repeat("failure output ", 12),
					})
				})
			},
		},
		{
			name: "restore dialog",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return openRestoreDialog(t, m, []deps.DependencyBackupInfo{{
						Name:    "2026-07-09_12-00-00-a-very-long-backup-filename.json",
						Kind:    deps.DependencyBackupKindPreUpdate,
						Updated: 1,
					}})
				})
			},
		},
		{
			name: "backup limit dialog",
			setup: func(t *testing.T, m *Model) {
				*m = focusSetting(t, *m, settingRowDepsBackups)
				*m = press(t, *m, tea.KeyPressMsg{Code: tea.KeyEnter})
			},
		},
	}

	for _, width := range []int{64, 80, 120} {
		for tab := AvailableTab; tab <= SettingsTab; tab++ {
			for _, dialog := range dialogs {
				t.Run(strconv.Itoa(width)+"/tab-"+strconv.Itoa(tab)+"/"+dialog.name, func(t *testing.T) {
					m := newTestModel(t)
					updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
					m = updated.(Model)
					m.CurrentTab = tab
					dialog.setup(t, &m)

					for _, line := range strings.Split(m.View().Content, "\n") {
						if got := ansi.StringWidth(line); got > width {
							t.Fatalf("line width = %d, want <= %d: %q", got, width, line)
						}
					}
				})
			}
		}
	}
}

func TestOverlayModalsRespectPhysicalViewport(t *testing.T) {
	modals := []struct {
		name  string
		setup func(*testing.T, *Model)
	}{
		{
			name: "dependency update",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model { return confirmApplyFrom(t, m) })
			},
		},
		{
			name: "dependency checks",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model { return confirmChecksFrom(t, m) })
			},
		},
		{
			name: "dependency rollback",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return confirmRollbackFrom(t, m, deps.DependencyCheckResult{
						Command: "go test ./...",
						Output: strings.Join([]string{
							"FAIL: example.com/module/package",
							"expected: successful update",
							"actual: failed dependency check",
						}, "\n"),
					})
				})
			},
		},
		{
			name: "dependency restore",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return openRestoreDialog(t, m, []deps.DependencyBackupInfo{{
						Name:    "2026-07-09_12-00-00-a-very-long-backup-filename.json",
						Kind:    deps.DependencyBackupKindPreUpdate,
						Updated: 1,
					}})
				})
			},
		},
		{
			name: "dependency backup limit",
			setup: func(t *testing.T, m *Model) {
				*m = focusSetting(t, *m, settingRowDepsBackups)
				*m = press(t, *m, tea.KeyPressMsg{Code: tea.KeyEnter})
			},
		},
	}
	viewports := []struct {
		name          string
		width, height int
	}{
		{name: "64x20", width: 64, height: 20},
		{name: "80x30", width: 80, height: 30},
		{name: "wide", width: 140, height: 40},
	}

	for _, viewport := range viewports {
		for _, modal := range modals {
			t.Run(viewport.name+"/"+modal.name, func(t *testing.T) {
				m := newTestModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{
					Width:  viewport.width,
					Height: viewport.height,
				})
				m = updated.(Model)
				modal.setup(t, &m)

				content := m.View().Content
				lines := strings.Split(content, "\n")
				if len(lines) > viewport.height {
					t.Fatalf("view height = %d, want <= %d:\n%s", len(lines), viewport.height, content)
				}
				for _, line := range lines {
					if got := ansi.StringWidth(line); got > viewport.width {
						t.Fatalf("line width = %d, want <= %d: %q", got, viewport.width, line)
					}
				}

			})
		}
	}
}

func TestRollbackDialogLimitsLongOutput(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	output := make([]string, maxDependencyListLines+4)
	for i := range output {
		output[i] = "failure output line " + strconv.Itoa(i+1)
	}
	m = confirmRollbackFrom(t, m, deps.DependencyCheckResult{
		Command: "go test ./...",
		Output:  strings.Join(output, "\n"),
	})

	content := m.View().Content
	want := "…and 4 more"
	if !strings.Contains(content, want) {
		t.Fatalf("rollback dialog is missing truncated-output indicator %q:\n%s", want, content)
	}
}

func TestViewHasEqualHeightAcrossTabsAtStandardViewport(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	var lineCount int
	for tab := AvailableTab; tab < tabCount; tab++ {
		if m.CurrentTab != tab {
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		}
		got := len(strings.Split(m.View().Content, "\n"))
		if tab == AvailableTab {
			lineCount = got
			continue
		}
		if got != lineCount {
			t.Fatalf("tab %d view line count = %d, want %d", tab, got, lineCount)
		}
	}
}

func TestViewDependencyDialogsRespectTerminalWidth(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Model)
	}{
		{
			name: "update",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return openUpdateDialog(t, loadDeps(t, m, []deps.ModuleDependency{{
						Path:    "github.com/acme/very-long-module-name-that-must-not-overflow-the-terminal",
						Version: "v1.0.0",
						Latest:  "v1.1.0",
					}}))
				})
			},
		},
		{
			name: "checks",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model { return confirmChecksFrom(t, m) })
			},
		},
		{
			name: "rollback",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return confirmRollbackFrom(t, m, deps.DependencyCheckResult{
						Command: "go test ./...",
						Output:  strings.Repeat("failure output ", 12),
					})
				})
			},
		},
		{
			name: "restore",
			setup: func(t *testing.T, m *Model) {
				withDialog(m, func(m Model) Model {
					return openRestoreDialog(t, m, []deps.DependencyBackupInfo{{
						Name:    "2026-07-09_12-00-00-a-very-long-backup-filename.json",
						Kind:    deps.DependencyBackupKindPreUpdate,
						Updated: 1,
					}})
				})
			},
		},
	}

	for _, width := range []int{64, 80, 120} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s-%d", tt.name, width), func(t *testing.T) {
				m := newTestModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				m = updated.(Model)
				tt.setup(t, &m)

				for _, line := range strings.Split(m.View().Content, "\n") {
					if got := ansi.StringWidth(line); got > width {
						t.Fatalf("view line width = %d, want <= %d: %q", got, width, line)
					}
				}
			})
		}
	}
}
