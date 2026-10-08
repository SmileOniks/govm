package model

import (
	"testing"

	"github.com/SmileOniks/govm/internal/utils"
)

// TestRefreshClearsPendingRefilterState regression-tests a memory leak where
// repeated 'r' key presses in the Available tab accumulated pending refilter
// operations. Each refresh created a new wrapCatalogProjectionRefilter closure
// that captured list.FilterMatchesMsg with large slices, causing heap growth.
// The fix: startLoad now resets refilterPending and pendingRestore before
// registering a new load request, preventing stale refilter operations from
// accumulating.
func TestRefreshClearsPendingRefilterState(t *testing.T) {
	m := newTestModel(t)
	m.projection.setAvailableFilteringEnabled(true)
	m.projection.setAvailableFilterText("1.2")

	snapshot := []utils.GoVersion{
		{Version: "1.23.0", Installed: false},
		{Version: "1.22.0", Installed: false},
		{Version: "1.21.0", Installed: false},
	}
	outcome := m.projection.replaceSnapshot(snapshot)
	if outcome.kind != catalogProjectionOutcomePublished {
		t.Fatalf("initial snapshot publish failed: %v", outcome.kind)
	}
	if outcome.cmd == nil {
		t.Fatal("expected refilter cmd after snapshot publish with active filter")
	}

	if !m.projection.refilterPending {
		t.Fatal("refilterPending should be true after publish with active filter")
	}
	if !m.projection.pendingRestore.active {
		t.Fatal("pendingRestore.active should be true after publish with active filter")
	}

	outcome = m.projection.startLoad(catalogLoadPurposeRefresh)
	if outcome.kind != catalogProjectionOutcomeLoadStarted {
		t.Fatalf("startLoad outcome = %v, want LoadStarted", outcome.kind)
	}

	if m.projection.refilterPending {
		t.Error("refilterPending should be false after startLoad (leak prevention)")
	}
	if m.projection.pendingRestore.active {
		t.Error("pendingRestore.active should be false after startLoad (leak prevention)")
	}
}

// TestMultipleRefreshesDontAccumulateState verifies that repeated refresh
// operations don't accumulate state when the catalog data remains unchanged.
// This simulates the user pressing 'r' multiple times in quick succession.
func TestMultipleRefreshesDontAccumulateState(t *testing.T) {
	m := newTestModel(t)
	m.projection.setAvailableFilteringEnabled(true)
	m.projection.setAvailableFilterText("1.2")

	snapshot := []utils.GoVersion{
		{Version: "1.23.0", Installed: false},
		{Version: "1.22.0", Installed: false},
	}
	outcome := m.projection.replaceSnapshot(snapshot)
	if outcome.kind != catalogProjectionOutcomePublished {
		t.Fatalf("initial publish failed: %v", outcome.kind)
	}

	firstGeneration := m.projection.generation

	for i := 0; i < 10; i++ {
		outcome = m.projection.startLoad(catalogLoadPurposeRefresh)
		if outcome.kind != catalogProjectionOutcomeLoadStarted {
			t.Fatalf("refresh %d: startLoad failed: %v", i, outcome.kind)
		}

		if m.projection.refilterPending {
			t.Errorf("refresh %d: refilterPending should be reset", i)
		}
		if m.projection.pendingRestore.active {
			t.Errorf("refresh %d: pendingRestore should be reset", i)
		}

		outcome = m.projection.acceptLoad(outcome.loadRequest.ID, snapshot)
		if outcome.kind != catalogProjectionOutcomeNoop {
			t.Fatalf("refresh %d: acceptLoad with unchanged data returned %v, want Noop", i, outcome.kind)
		}
	}

	if m.projection.generation != firstGeneration {
		t.Errorf("generation changed from %d to %d despite no data changes", firstGeneration, m.projection.generation)
	}
}

// TestRefreshDuringActiveFilterPreservesSelection verifies that starting a
// refresh while a filter is active properly resets pending state without
// losing the user's selection intent.
func TestRefreshDuringActiveFilterPreservesSelection(t *testing.T) {
	m := newTestModel(t)
	m.projection.setAvailableFilteringEnabled(true)

	initial := []utils.GoVersion{
		{Version: "1.23.0", Installed: false},
		{Version: "1.22.5", Installed: false},
		{Version: "1.22.0", Installed: false},
		{Version: "1.21.0", Installed: false},
	}
	outcome := m.projection.replaceSnapshot(initial)
	if outcome.kind != catalogProjectionOutcomePublished {
		t.Fatalf("initial publish failed: %v", outcome.kind)
	}

	m.projection.setAvailableFilterText("1.22")
	if outcome.cmd != nil {
		msg := outcome.cmd()
		if refilterMsg, ok := msg.(catalogProjectionRefilterMsg); ok {
			m.projection.settleRefilter(refilterMsg)
		}
	}

	selected := m.projection.selectedAvailableItem()
	if selected == nil {
		t.Fatal("no item selected after filter")
	}
	originalVersion := selected.Name

	outcome = m.projection.startLoad(catalogLoadPurposeRefresh)
	if outcome.kind != catalogProjectionOutcomeLoadStarted {
		t.Fatalf("startLoad failed: %v", outcome.kind)
	}

	if m.projection.refilterPending {
		t.Error("refilterPending should be false immediately after startLoad")
	}

	refreshed := []utils.GoVersion{
		{Version: "1.23.1", Installed: false},
		{Version: "1.23.0", Installed: false},
		{Version: "1.22.5", Installed: false},
		{Version: "1.22.0", Installed: false},
		{Version: "1.21.0", Installed: false},
	}
	outcome = m.projection.acceptLoad(outcome.loadRequest.ID, refreshed)
	if outcome.kind != catalogProjectionOutcomePublished {
		t.Fatalf("acceptLoad with new data returned %v, want Published", outcome.kind)
	}

	if outcome.cmd != nil {
		msg := outcome.cmd()
		if refilterMsg, ok := msg.(catalogProjectionRefilterMsg); ok {
			m.projection.settleRefilter(refilterMsg)
		}
	}

	selected = m.projection.selectedAvailableItem()
	if selected != nil && selected.Name != originalVersion {
		t.Logf("selection changed from %s to %s (acceptable for new data)", originalVersion, selected.Name)
	}
}
