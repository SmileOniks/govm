package model

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/application"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/loader"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestSettingsDepsBackupLimitDialogOpensWithCurrentValue(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter},
		{Code: ' '},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := newTestModel(t)
			settingsStore(m).values.DepsBackupLimit = 25
			m.settings.values.DepsBackupLimit = 25
			m = focusSetting(t, m, settingRowDepsBackups)

			updated, _ := m.Update(key)
			m = updated.(Model)

			view := stripANSI(m.View().Content)
			if !strings.Contains(view, "Set dependency backup limit") {
				t.Fatalf("expected backup-limit dialog, got:\n%s", view)
			}
			if !strings.Contains(view, "25") {
				t.Fatalf("expected dialog to contain current value, got:\n%s", view)
			}
			if got := m.settings.values.DepsBackupLimit; got != 25 {
				t.Fatalf("backup limit = %d, want unchanged 25", got)
			}
		})
	}
}

func TestSettingsDepsBackupLimitDialogValidatesAndSaves(t *testing.T) {
	m := newTestModel(t)
	m.settings.values.DepsBackupLimit = 10
	m = focusSetting(t, m, settingRowDepsBackups)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	for _, invalid := range []string{"", "abc", "0", "101"} {
		m.settings.depsBackupLimitInput.SetValue(invalid)
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		if !m.settings.editingDepsBackupLimit {
			t.Fatalf("expected dialog to remain open after invalid value %q", invalid)
		}
		if got := m.settings.values.DepsBackupLimit; got != 10 {
			t.Fatalf("backup limit after %q = %d, want unchanged 10", invalid, got)
		}
	}

	m.settings.depsBackupLimitInput.SetValue("25")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.settings.editingDepsBackupLimit {
		t.Fatal("expected dialog to close after valid value")
	}
	if got := m.settings.values.DepsBackupLimit; got != 25 {
		t.Fatalf("backup limit = %d, want 25", got)
	}

	if saved := settingsStore(m).values.DepsBackupLimit; saved != 25 {
		t.Fatalf("saved backup limit = %d, want 25", saved)
	}
}

func TestSettingsDepsBackupLimitDialogCancelsAndBlocksGlobalKeys(t *testing.T) {
	m := newTestModel(t)
	m.settings.values.DepsBackupLimit = 10
	m = focusSetting(t, m, settingRowDepsBackups)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.settings.depsBackupLimitInput.SetValue("25")

	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyTab},
		{Code: tea.KeyUp},
		{Code: 'h'},
		{Code: 'q'},
	} {
		updated, cmd := m.Update(key)
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("key %q returned a global command while dialog was open", key.String())
		}
	}
	if m.CurrentTab != SettingsTab || m.settings.cursor != settingRowDepsBackups {
		t.Fatalf("global navigation changed while dialog was open: tab=%d cursor=%d", m.CurrentTab, m.settings.cursor)
	}
	if got := m.settings.values.DepsBackupLimit; got != 10 {
		t.Fatalf("backup limit = %d, want unchanged 10", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.settings.editingDepsBackupLimit {
		t.Fatal("expected dialog to close after escape")
	}
	if got := m.settings.values.DepsBackupLimit; got != 10 {
		t.Fatalf("backup limit = %d, want unchanged 10", got)
	}
}

func TestSettingsDepsBackupLimitDialogKeepsValueAfterSaveFailure(t *testing.T) {
	m := newTestModel(t)
	m.settings.values.DepsBackupLimit = 10
	settingsStore(m).err = errors.New("disk full")
	m = focusSetting(t, m, settingRowDepsBackups)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.settings.depsBackupLimitInput.SetValue("25")

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.settings.editingDepsBackupLimit {
		t.Fatal("expected dialog to remain open after save failure")
	}
	if got := m.settings.values.DepsBackupLimit; got != 10 {
		t.Fatalf("backup limit = %d, want unchanged 10", got)
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, "Failed to save settings") {
		t.Fatalf("expected save error in dialog, got:\n%s", view)
	}
}

func TestSettingsDistributionSourceDialogValidatesAndCancels(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDistributionSource)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "Set distribution source") {
		t.Fatalf("expected distribution source dialog, got:\n%s", view)
	}
	if !strings.Contains(view, config.DefaultDistributionSource) {
		t.Fatalf("expected dialog to contain current source, got:\n%s", view)
	}

	m.settings.distributionSourceInput.SetValue("")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("invalid source returned a command")
	}
	if !m.settings.editingDistributionSource {
		t.Fatal("expected dialog to remain open after invalid source")
	}
	if m.settings.values.DistributionSource != config.DefaultDistributionSource {
		t.Fatalf("source = %q, want unchanged default", m.settings.values.DistributionSource)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.settings.editingDistributionSource {
		t.Fatal("expected dialog to close after escape")
	}
}

func TestSettingsDistributionSourceUsesOperationResult(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDistributionSource)
	m = m.BindVersionOperations(VersionOperations{
		DistributionSource: func(context.Context, string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{
				Source: "https://mirror.example/dl/",
				Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{{
					Version:  "1.26.0",
					Filename: "go1.26.0.darwin-arm64.tar.gz",
				}}},
			}, nil
		},
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.settings.distributionSourceInput.SetValue("https://mirror.example/dl")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.settings.editingDistributionSource || !m.settings.checkingDistributionSource {
		t.Fatal("dialog did not remain open while source operation was running")
	}

	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.settings.editingDistributionSource {
		t.Fatal("dialog remained open after successful source change")
	}
	if m.settings.values.DistributionSource != "https://mirror.example/dl/" {
		t.Fatalf("source = %q", m.settings.values.DistributionSource)
	}
	if _, ok := m.projection.lookup("1.26.0"); !ok {
		t.Fatal("operation catalog was not applied")
	}
}

func TestSettingsDistributionSourceKeepsDialogOnOperationFailure(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDistributionSource)
	m = m.BindVersionOperations(VersionOperations{
		DistributionSource: func(context.Context, string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{}, errors.New("catalog unavailable")
		},
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.settings.distributionSourceInput.SetValue("https://mirror.example/dl")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	if !m.settings.editingDistributionSource {
		t.Fatal("dialog closed after failed source change")
	}
	if m.settings.checkingDistributionSource {
		t.Fatal("source check remained active after failure")
	}
	if m.settings.values.DistributionSource != config.DefaultDistributionSource {
		t.Fatalf("source = %q, want previous source", m.settings.values.DistributionSource)
	}
	if !strings.Contains(m.settings.distributionSourceInputErr, "catalog unavailable") {
		t.Fatalf("error = %q", m.settings.distributionSourceInputErr)
	}
}
