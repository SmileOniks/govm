package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
)

func TestSettingsDepsBackupLimitShortcutControlsAndSaves(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		start int
		want  int
	}{
		{name: "left wraps minimum to maximum", key: tea.KeyPressMsg{Code: tea.KeyLeft}, start: config.MinDepsBackupLimit, want: config.MaxDepsBackupLimit},
		{name: "right wraps maximum to minimum", key: tea.KeyPressMsg{Code: tea.KeyRight}, start: config.MaxDepsBackupLimit, want: config.MinDepsBackupLimit},
		{name: "h wraps minimum to maximum", key: tea.KeyPressMsg{Code: 'h'}, start: config.MinDepsBackupLimit, want: config.MaxDepsBackupLimit},
		{name: "l wraps maximum to minimum", key: tea.KeyPressMsg{Code: 'l'}, start: config.MaxDepsBackupLimit, want: config.MinDepsBackupLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.settings.values.DepsBackupLimit = tt.start
			settingsStore(m).values.DepsBackupLimit = tt.start
			m = focusSetting(t, m, settingRowDepsBackups)

			updated, _ := m.Update(tt.key)
			m = updated.(Model)
			if got := m.settings.values.DepsBackupLimit; got != tt.want {
				t.Fatalf("backup limit after %q = %d, want %d", tt.key.String(), got, tt.want)
			}

			if saved := settingsStore(m).values.DepsBackupLimit; saved != tt.want {
				t.Fatalf("saved backup limit after %q = %d, want %d", tt.key.String(), saved, tt.want)
			}
		})
	}
}

func TestSettingsToggleDepsDisplayUpdatesDependencyRows(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDepsDisplay)
	updated, _ := m.Update(dependenciesMsg{
		{Path: "github.com/example/direct", Version: "v1.0.0"},
		{Path: "github.com/example/indirect", Version: "v1.0.0", Indirect: true},
	})
	m = updated.(Model)

	if rows := m.deps.table.Rows(); len(rows) != 1 {
		t.Fatalf("expected default direct-only view to show 1 row, got %d", len(rows))
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.settings.values.DepsDisplay != config.DepsDisplayAll {
		t.Fatalf("expected deps display all, got %q", m.settings.values.DepsDisplay)
	}
	if rows := m.deps.table.Rows(); len(rows) != 2 {
		t.Fatalf("expected all deps view to show 2 rows, got %d", len(rows))
	}
	if m.Status.Kind() == "error" {
		t.Fatalf("expected non-error message after save, got %q: %s", m.Status.Kind(), m.Status.Text())
	}
}

func TestSettingsSaveErrorShowsErrorMessage(t *testing.T) {
	m := newTestModel(t)
	settingsStore(m).err = errors.New("disk full")
	m = focusSetting(t, m, settingRowDepsDisplay)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.Status.Kind() != "error" {
		t.Fatalf("expected error message type, got %q", m.Status.Kind())
	}
	if !strings.Contains(m.Status.Text(), "settings") {
		t.Fatalf("expected settings save error message, got %q", m.Status.Text())
	}
}
