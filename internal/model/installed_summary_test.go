package model

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/utils"
)

func TestInstalledTabSummaryInterruptedDownloads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes int64
	}{
		{name: "absent"},
		{name: "present", bytes: 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t).BindVersionOperations(VersionOperations{DiskUsage: func(context.Context) (prune.Summary, error) {
				return prune.Summary{}, nil
			}})
			m = resizeModel(t, m, 120, 40)
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			m, _ = installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{
				InstalledBytes: 4096, DownloadBytes: tc.bytes, ReclaimableBytes: 2048,
			}})
			view := stripANSI(m.View().Content)
			for _, want := range []string{"Installed: 4.0 KiB", "Reclaimable: 2.0 KiB"} {
				if !strings.Contains(view, want) {
					t.Errorf("summary missing %q:\n%s", want, view)
				}
			}
			if tc.bytes == 0 {
				if strings.Contains(view, "Interrupted") || strings.Contains(view, "Downloads") {
					t.Fatalf("summary shows absent download debris:\n%s", view)
				}
			} else if !strings.Contains(view, "Interrupted: 1.0 KiB") {
				t.Fatalf("summary lost interrupted download size:\n%s", view)
			}
		})
	}
}

func TestInstalledTabSummaryReplaysVersionSizesAfterReload(t *testing.T) {
	loads, usages := 0, 0
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
			loads++
			return []utils.GoVersion{{Version: "1.24.4", Installed: true, Active: true, Path: "/versions/go1.24.4"}}, nil
		},
		DiskUsage: func(context.Context) (prune.Summary, error) { usages++; return prune.Summary{}, nil },
	})
	m = resizeModel(t, m, 120, 40)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{
		InstalledBytes: 4096, ReclaimableBytes: 1024, VersionBytes: map[string]int64{"1.24.4": 2048},
	}})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'r'})
	m = runCatalogTestCmd(t, m, cmd)
	view := stripANSI(m.View().Content)
	for _, want := range []string{"1.24.4", "2.0 KiB", "Installed: 4.0 KiB", "Reclaimable: 1.0 KiB"} {
		if !strings.Contains(view, want) {
			t.Errorf("reloaded view lost %q:\n%s", want, view)
		}
	}
	if loads != 1 || usages != 0 {
		t.Fatalf("reload load/usage calls = %d/%d, want cached usage replay", loads, usages)
	}
	installedTestAssertVersionSize(t, m, "1.24.4", "2.0 KiB")
	assertVersionViewsConsistent(t, m)
}

func TestInstalledTabSummaryRetainsPartialUsageSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		warnings []prune.Warning
	}{
		{name: "error", err: errors.New("disk scan denied")},
		{name: "warning", warnings: []prune.Warning{{Path: "/unreadable", Err: errors.New("permission denied")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t).BindVersionOperations(VersionOperations{DiskUsage: func(context.Context) (prune.Summary, error) {
				return prune.Summary{}, nil
			}})
			m = resizeModel(t, m, 120, 40)
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			m, _ = installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{InstalledBytes: 8192}})
			m, cmd := installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{
				InstalledBytes: 4096, ReclaimableBytes: 2048, DownloadBytes: 1024,
				VersionBytes: map[string]int64{"1.24.4": 2048}, Warnings: tc.warnings,
			}, Err: tc.err})
			view := stripANSI(m.View().Content)
			for _, want := range []string{"Installed: 4.0 KiB", "Reclaimable: 2.0 KiB", "Interrupted: 1.0 KiB", "1.24.4", "2.0 KiB"} {
				if !strings.Contains(view, want) {
					t.Errorf("partial usage lost %q:\n%s", want, view)
				}
			}
			if cmd != nil || strings.Contains(view, "Installed: 8.0 KiB") || m.Status.Kind() != "warning" || m.Status.Scope() != statusScopeTab {
				t.Fatalf("partial usage outcome: command=%v status=%+v\n%s", cmd != nil, m.Status, view)
			}
			if tc.err != nil && !strings.Contains(m.Status.Text(), tc.err.Error()) {
				t.Fatal("disk usage lost its error diagnostic")
			}
			installedTestAssertVersionSize(t, m, "1.24.4", "2.0 KiB")
			assertVersionViewsConsistent(t, m)
		})
	}
}

func TestInstalledTabSummaryHiddenWithoutAdapter(t *testing.T) {
	m := newTestModel(t).BindVersionOperations(VersionOperations{})
	m = resizeModel(t, m, 120, 40)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{
		InstalledBytes: 4096, ReclaimableBytes: 2048, DownloadBytes: 1024,
	}})
	view := stripANSI(m.View().Content)
	for _, hidden := range []string{"Installed:", "Reclaimable:", "Interrupted:"} {
		if strings.Contains(view, hidden) {
			t.Errorf("summary rendered without disk adapter: %q\n%s", hidden, view)
		}
	}
}
