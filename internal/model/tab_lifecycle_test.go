package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/utils"
)

func TestTabSwitchClearsTabLocalStatus(t *testing.T) {
	m := newTestModel(t)
	m.CurrentTab = DepsTab
	m.Status.SetTab("No direct dependency updates available.", "warning")

	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	got := updated.(Model)

	if got.CurrentTab != SettingsTab {
		t.Fatalf("current tab = %d, want %d", got.CurrentTab, SettingsTab)
	}
	if got.Status.Text() != "" || got.Status.Kind() != "" {
		t.Fatalf("tab-local status = (%q, %q), want empty", got.Status.Text(), got.Status.Kind())
	}
}

func TestTabSwitchPreservesGlobalStatus(t *testing.T) {
	m := newTestModel(t)
	m.Status.SetGlobal("Successfully installed Go 1.24.4", "success")

	updated, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	got := updated.(Model)

	if got.CurrentTab != InstalledTab {
		t.Fatalf("current tab = %d, want %d", got.CurrentTab, InstalledTab)
	}
	if got.Status.Text() != "Successfully installed Go 1.24.4" {
		t.Fatalf("message = %q, want preserved global status", got.Status.Text())
	}
	if got.Status.Kind() != "success" {
		t.Fatalf("message type = %q, want %q", got.Status.Kind(), "success")
	}
}

// TestTabSwitchClearsSwitchedToGoStatus regression-tests the UX rule
// that the "Switched to Go X" success message is scoped to the tab it
// was produced on: once the user moves to another tab the message must
// disappear. The SwitchCompletedMsg handler previously used
// Status.SetGlobal, so the message survived tab switches and felt like
// a stale warning stuck on screen.
func TestTabSwitchClearsSwitchedToGoStatus(t *testing.T) {
	m := newTestModel(t)
	// Seed the switch target as an installed (but inactive) version so
	// the SwitchCompletedMsg handler can activate it directly through
	// the catalog without entering reconciliation.
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Filename: "go1.24.4.darwin-arm64.tar.gz", Installed: true, Active: true, Path: "/p/1.24.4"},
		{Version: "1.26.5", Filename: "go1.26.5.darwin-arm64.tar.gz", Installed: true, Active: false, Path: "/p/1.26.5"},
	})
	m = m.BindVersionOperations(VersionOperations{
		Activate: func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
			return lifecycle.ActivationResult{Version: version}, nil
		},
		ShimInPath: func() bool { return true },
	})
	m = applyFilter(t, m, "1.26.5")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'u'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if v, _ := m.projection.lookup("1.26.5"); !v.Active || m.Status.Kind() != "success" {
		t.Fatalf("activation = %+v, status=%q", v, m.Status.Text())
	}

	// Switching tabs must tear down the tab-scoped success message.
	updated, _ = m.Update(tea.KeyPressMsg{Code: '\t'})
	got := updated.(Model)

	if got.Status.Text() != "" || got.Status.Kind() != "" {
		t.Fatalf("status after tab switch = (%q, %q), want empty", got.Status.Text(), got.Status.Kind())
	}
}

func TestTabSwitchCancelsPendingDelete(t *testing.T) {
	m := newVersionCacheTestModel(t)
	deletes := 0
	m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
		deletes++
		return lifecycle.DeletionResult{Version: version}, nil
	}})
	m = applyFilter(t, m, "1.26.0")
	m = press(t, m, tea.KeyPressMsg{Code: 'd'})
	if m.inputContext() != inputDeleteConfirm || !strings.Contains(m.Status.Text(), "delete Go 1.26.0?") {
		t.Fatal("delete confirmation did not open for the selected inactive version")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := updated.(Model)

	if got.CurrentTab != InstalledTab {
		t.Fatalf("current tab = %d, want %d", got.CurrentTab, InstalledTab)
	}
	if got.inputContext() == inputDeleteConfirm {
		t.Fatal("delete confirmation must not claim input after leaving Available")
	}
	if got.Status.Text() != "" || got.Status.Kind() != "" {
		t.Fatalf("delete status = (%q, %q), want empty", got.Status.Text(), got.Status.Kind())
	}
	got = press(t, got, shiftTab())
	updated, cmd := got.Update(tea.KeyPressMsg{Code: 'Y'})
	got = runCatalogTestCmd(t, updated.(Model), cmd)
	if deletes != 0 || got.inputContext() == inputDeleteConfirm {
		t.Fatal("returning to Available must not restore the cancelled delete")
	}
}
