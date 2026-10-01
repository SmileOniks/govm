package model

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func confirmPrune(t *testing.T, m *Model) {
	t.Helper()
	*m = m.BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) {
			return prune.Result{Candidates: []prune.Candidate{{Version: "1.23.0", Bytes: 1024}}}, nil
		},
	})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'p'})
	if cmd == nil {
		t.Fatal("expected prune preview command")
	}
	*m = runCatalogTestCmd(t, updated.(Model), cmd)
	if m.inputContext() != inputPruneConfirm {
		t.Fatalf("preview context = %v, want prune confirmation", m.inputContext())
	}
}

func TestInputContextResolvesEachState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, m Model) Model
		want  inputContext
	}{
		{
			name:  "plain tab",
			setup: func(t *testing.T, m Model) Model { return m },
			want:  inputTab,
		},
		{
			name: "filter input",
			setup: func(t *testing.T, m Model) Model {
				return openFilter(t, resizeModel(t, m, 100, 30))
			},
			want: inputFilter,
		},
		{
			name: "delete confirmation",
			setup: func(t *testing.T, m Model) Model {
				m.CurrentTab = InstalledTab
				seedVersions(t, &m, []utils.GoVersion{
					{Version: "1.24.4", Installed: true, Path: "/p/1.24.4"},
					{Version: "1.25.0", Installed: true, Path: "/p/1.25.0"},
				})
				setInstalledCursor(&m, 1)
				return pressKey(t, m, tea.KeyPressMsg{Code: 'd'})
			},
			want: inputDeleteConfirm,
		},
		{
			name: "prune confirmation",
			setup: func(t *testing.T, m Model) Model {
				m.CurrentTab = InstalledTab
				confirmPrune(t, &m)
				return m
			},
			want: inputPruneConfirm,
		},
		{
			name: "deps dialog",
			setup: func(t *testing.T, m Model) Model {
				return confirmApplyFrom(t, m)
			},
			want: inputDepsDialog,
		},
		{
			name: "help overlay",
			setup: func(t *testing.T, m Model) Model {
				return pressKey(t, resizeModel(t, m, 100, 30), tea.KeyPressMsg{Code: '?'})
			},
			want: inputHelpOverlay,
		},
		{
			name: "settings backup limit input",
			setup: func(t *testing.T, m Model) Model {
				m = focusSetting(t, m, settingRowDepsBackups)
				return pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			want: inputSettingsInput,
		},
		{
			name: "settings distribution source input",
			setup: func(t *testing.T, m Model) Model {
				m = focusSetting(t, m, settingRowDistributionSource)
				return pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			want: inputSettingsInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.setup(t, newTestModel(t))
			if got := m.inputContext(); got != tt.want {
				t.Fatalf("inputContext() = %s, want %s", got, tt.want)
			}
			wantCanOpen := tt.want == inputTab
			if got := m.canOpenMode(); got != wantCanOpen {
				t.Fatalf("canOpenMode() in %s = %v, want %v", tt.want, got, wantCanOpen)
			}
			wantCanOpenHelp := tt.want != inputHelpOverlay && !tt.want.textEntry()
			if got := m.canOpenHelp(); got != wantCanOpenHelp {
				t.Fatalf("canOpenHelp() in %s = %v, want %v", tt.want, got, wantCanOpenHelp)
			}
		})
	}
}

func TestInputContextPriorityWhenFlagsOverlap(t *testing.T) {
	m := modelAtConfirmApply(t)
	m.HelpVisible = true
	if got := m.inputContext(); got != inputHelpOverlay {
		t.Fatalf("help above dialog: inputContext() = %s, want help overlay", got)
	}
	if got := m.inputContextBeneathHelp(); got != inputDepsDialog {
		t.Fatalf("inputContextBeneathHelp() = %s, want deps dialog", got)
	}

	m.settings.editingDepsBackupLimit = true
	if got := m.inputContext(); got != inputSettingsInput {
		t.Fatalf("settings input above help: inputContext() = %s, want settings input", got)
	}
}

func TestInputContextTextEntry(t *testing.T) {
	for _, ctx := range []inputContext{inputSettingsInput, inputFilter} {
		if !ctx.textEntry() {
			t.Fatalf("%s must be a text-entry context", ctx)
		}
	}
	for _, ctx := range []inputContext{inputTab, inputDeleteConfirm, inputPruneConfirm, inputDepsDialog, inputHelpOverlay} {
		if ctx.textEntry() {
			t.Fatalf("%s must not be a text-entry context", ctx)
		}
	}
}
