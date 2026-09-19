package model

import (
	"testing"

	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestDependenciesMsgPopulatesTable(t *testing.T) {
	m := newTestModel(t)

	deps := dependenciesMsg{
		{Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0"},
		{Path: "github.com/example/indirect", Version: "v0.5.0", Indirect: true},
		{Path: "github.com/example/current", Version: "v2.0.0", Latest: "v2.0.0"},
	}

	updated, _ := m.Update(deps)
	got := updated.(Model)

	if !got.deps.loaded {
		t.Fatal("expected DependenciesLoaded to be true")
	}

	if len(got.deps.dependencies) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(got.deps.dependencies))
	}

	rows := got.deps.table.Rows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 direct rows in table, got %d", len(rows))
	}

	if rows[0][3] != "update avail" {
		t.Fatalf("expected 'update avail' status, got %q", rows[0][3])
	}
	if rows[1][3] != "current" {
		t.Fatalf("expected 'current' status, got %q", rows[1][3])
	}
}

func TestDependencyTableColumns(t *testing.T) {
	cols := dependencyTableColumns(64)

	if len(cols) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(cols))
	}

	if cols[0].Width < 5 || cols[1].Width < 3 || cols[2].Width < 3 || cols[3].Width < 3 {
		t.Fatal("expected positive column widths at the minimum viewport")
	}
}

func TestDependencyTableIndirectUpdateStatus(t *testing.T) {
	m := newTestModel(t)
	m.deps.applySettings(config.Settings{DepsDisplay: config.DepsDisplayAll})

	deps := dependenciesMsg{
		{Path: "indirect-with-update", Version: "v0.5.0", Latest: "v0.6.0", Indirect: true},
	}

	updated, _ := m.Update(deps)
	got := updated.(Model)

	rows := got.deps.table.Rows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0][3] != "indirect update" {
		t.Fatalf("expected 'indirect update' status, got %q", rows[0][3])
	}
}

func TestInstalledTableColumns_AllLayouts(t *testing.T) {
	cases := []struct {
		name  string
		width int
	}{
		{"minimum", 64},
		{"standard", 100},
		{"wide", 160},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols := installedTableColumns(tc.width)
			if len(cols) != 4 {
				t.Fatalf("expected 4 columns, got %d", len(cols))
			}
			for i, c := range cols {
				if c.Width <= 0 {
					t.Fatalf("column %d (%s) has non-positive width %d", i, c.Title, c.Width)
				}
			}
		})
	}
}

func TestDependencyTableColumns_AllLayouts(t *testing.T) {
	cases := []struct {
		name  string
		width int
	}{
		{"minimum", 64},
		{"standard", 100},
		{"wide", 160},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols := dependencyTableColumns(tc.width)
			if len(cols) != 4 {
				t.Fatalf("expected 4 columns, got %d", len(cols))
			}
			for i, c := range cols {
				if c.Width < 5 {
					t.Fatalf("column %d (%s) too narrow: %d", i, c.Title, c.Width)
				}
			}
		})
	}
}

func TestUpdateDependencyTable_StatusPriorities(t *testing.T) {
	m := newTestModel(t)
	m.deps.applySettings(config.Settings{DepsDisplay: config.DepsDisplayAll})

	deps := dependenciesMsg{
		{Path: "err", Version: "v1.0.0", Latest: "v1.1.0", Error: "boom"},
		{Path: "dep", Version: "v1.0.0", Latest: "v1.1.0", Deprecated: "use v2"},
		{Path: "indirect-update", Version: "v1.0.0", Latest: "v1.1.0", Indirect: true},
		{Path: "direct-update", Version: "v1.0.0", Latest: "v1.1.0"},
		{Path: "indirect-only", Version: "v1.0.0", Indirect: true},
		{Path: "current", Version: "v1.0.0", Latest: "v1.0.0"},
	}
	updated, _ := m.Update(deps)
	got := updated.(Model)
	rows := got.deps.table.Rows()
	if len(rows) != len(deps) {
		t.Fatalf("expected %d rows, got %d", len(deps), len(rows))
	}
	want := []string{"error", "deprecated", "indirect update", "update avail", "indirect", "current"}
	for i, w := range want {
		if rows[i][3] != w {
			t.Fatalf("row %d: expected %q, got %q", i, w, rows[i][3])
		}
	}
}

func TestUpdateInstalledTable_SkipsUninstalled(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0", Installed: true, Path: "/p/1.20", DiskUsage: 2048, Active: true},
		{Version: "1.21.0", Installed: false},
		{Version: "1.22.0", Installed: true, Path: "/p/1.22"},
	})
	rows := m.projection.installedModel().Rows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 installed rows, got %d", len(rows))
	}
	if rows[0][0] != "1.20.0" || rows[0][2] != "2.0 KiB" || rows[0][3] != "active" {
		t.Fatalf("row 0 mismatch: %v", rows[0])
	}
	if rows[1][0] != "1.22.0" || rows[1][2] != "0 B" || rows[1][3] != "" {
		t.Fatalf("row 1 mismatch: %v", rows[1])
	}
}
