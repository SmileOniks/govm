package model

import (
	"context"
	"reflect"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

func TestCatalogProjection_InstalledSelectionFollowsIdentityAfterReorder(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0", Installed: true, Path: "/go/1.20.0"},
		{Version: "1.21.0", Installed: true, Path: "/go/1.21.0"},
		{Version: "1.22.0", Installed: true, Path: "/go/1.22.0"},
	})
	m.projection.installedTable.SetCursor(1)

	if got := m.projection.selectedInstalledItem()[0]; got != "1.21.0" {
		t.Fatalf("selected installed version before reorder = %q, want 1.21.0", got)
	}

	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.22.0", Installed: true, Path: "/go/1.22.0"},
		{Version: "1.20.0", Installed: true, Path: "/go/1.20.0"},
		{Version: "1.21.0", Installed: true, Path: "/go/1.21.0"},
	})

	if got := m.projection.selectedInstalledItem()[0]; got != "1.21.0" {
		t.Fatalf("selected installed version after reorder = %q, want 1.21.0", got)
	}
}

func TestCatalogProjection_InvalidReconciliationPreservesPriorWidgets(t *testing.T) {
	m := newVersionCacheTestModel(t)
	m = m.BindVersionOperations(VersionOperations{
		Install: func(_ context.Context, r install.Request) (install.Result, error) {
			return install.Result{Version: r.Version, Path: "/go/" + r.Version}, nil
		},
	})
	m = applyFilter(t, m, "1.25.0")
	updated, operation := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = updated.(Model)
	completion := operation()
	updated, refresh := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = updated.(Model)
	updated, refilter := m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, refresh),
		Versions: []utils.GoVersion{
			{Version: "1.24.4", Installed: true, Active: true, Path: "/p/1.24.4"},
			{Version: "1.26.0", Installed: true, Path: "/p/1.26.0"},
		},
	})
	m = runCatalogTestCmd(t, updated.(Model), refilter)
	priorItems := catalogProjectionItemNames(m)
	priorRows := cloneCatalogProjectionRows(m.projection.installedModel().Rows())
	updated, verify := m.Update(completion)
	m = updated.(Model)
	request := catalogRequestID(t, verify)
	updated, _ = m.Update(catalogLoadedMsg{
		RequestID: request,
		Versions: []utils.GoVersion{
			{Version: "1.24.4", Installed: true, Active: true, Path: "/p/1.24.4"},
			{Version: "1.24.4", Installed: true, Active: true, Path: "/p/1.24.4"},
		},
	})
	got := updated.(Model)

	if !reflect.DeepEqual(catalogProjectionItemNames(got), priorItems) {
		t.Fatalf("available items changed after invalid reconciliation: got %v, want %v", catalogProjectionItemNames(got), priorItems)
	}
	gotRows := got.projection.installedModel().Rows()
	if !reflect.DeepEqual(gotRows, priorRows) {
		t.Fatalf("installed rows changed after invalid reconciliation: got %v, want %v", gotRows, priorRows)
	}
	if got.Status.Kind() != "error" {
		t.Fatalf("status kind = %q, want error", got.Status.Kind())
	}
	if got.projection.activityState().kind != catalogActivityIdle {
		t.Fatal("catalog activity remained active after invalid reconciliation")
	}
}

func TestCatalogProjection_StaleRefilterPreservesCurrentVisibleSelection(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.21.0"},
		{Version: "1.22.0"},
	})
	m.projection.setAvailableFilteringEnabled(true)
	m.projection.setAvailableFilterText("1.2")
	m.projection.selectAvailable(1)

	stale := m.projection.replaceSnapshot([]utils.GoVersion{
		{Version: "1.22.0"},
		{Version: "1.21.0"},
		{Version: "1.20.0"},
	})
	if stale.err != nil {
		t.Fatalf("first projection replace: %v", stale.err)
	}
	current := m.projection.replaceSnapshot([]utils.GoVersion{
		{Version: "1.20.0"},
		{Version: "1.22.0"},
		{Version: "1.21.0"},
	})
	if current.err != nil {
		t.Fatalf("second projection replace: %v", current.err)
	}

	currentItems := catalogProjectionVisibleItemNames(m)
	currentSelection := m.projection.selectedAvailableVersion()

	updated, _ := m.Update(stale.cmd())
	got := updated.(Model)

	if !reflect.DeepEqual(catalogProjectionVisibleItemNames(got), currentItems) {
		t.Fatalf("visible items changed after stale refilter: got %v, want %v", catalogProjectionVisibleItemNames(got), currentItems)
	}
	if got.projection.selectedAvailableVersion() != currentSelection {
		t.Fatalf("selected version changed after stale refilter: got %q, want %q", got.projection.selectedAvailableVersion(), currentSelection)
	}
}

func TestCatalogProjection_NormalRefreshDoesNotReportReconciliationSuccess(t *testing.T) {
	m := newVersionCacheTestModel(t)
	m.Status.SetGlobal("keep this status", "warning")

	outcome := m.projection.startLoad(catalogLoadPurposeRefresh)
	updated, _ := m.Update(catalogLoadedMsg{
		RequestID: outcome.loadRequest.ID,
		Versions: []utils.GoVersion{
			{Version: "1.26.0", Installed: true, Path: "/p/1.26.0"},
			{Version: "1.25.0"},
		},
	})
	got := updated.(Model)

	if got.Status.Kind() != "warning" || got.Status.Text() != "keep this status" {
		t.Fatalf("normal refresh changed status to %q (%s), want unchanged warning", got.Status.Text(), got.Status.Kind())
	}
	if got.projection.operationPhase() == catalogOperationPhaseReconciling {
		t.Fatal("normal refresh unexpectedly established reconciliation context")
	}
}

func catalogProjectionItemNames(m Model) []string {
	items := m.projection.availableModel().Items()
	names := make([]string, 0, len(items))
	for _, item := range items {
		if it, ok := item.(styles.Item); ok {
			names = append(names, it.Name)
		}
	}
	return names
}

func catalogProjectionVisibleItemNames(m Model) []string {
	items := m.projection.availableModel().VisibleItems()
	names := make([]string, 0, len(items))
	for _, item := range items {
		if it, ok := item.(styles.Item); ok {
			names = append(names, it.Name)
		}
	}
	return names
}

func cloneCatalogProjectionRows(rows []table.Row) []table.Row {
	cloned := make([]table.Row, len(rows))
	for i, row := range rows {
		cloned[i] = append(table.Row(nil), row...)
	}
	return cloned
}
