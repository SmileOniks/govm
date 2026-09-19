package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
)

// marksList is the dependency list the mark tests work on: one hidden
// indirect module between three direct ones.
func marksList() []deps.ModuleDependency {
	return []deps.ModuleDependency{
		{Path: "example.com/a", Version: "v1.0.0", Latest: "v1.1.0"},
		{Path: "example.com/hidden", Version: "v0.1.0", Latest: "v0.2.0", Indirect: true},
		{Path: "example.com/b", Version: "v2.0.0", Latest: "v2.0.0"},
		{Path: "example.com/c", Version: "v3.0.0", Latest: "v3.1.0"},
	}
}

func marksFixture(t *testing.T) Model {
	t.Helper()
	return loadDeps(t, newTestModel(t), marksList())
}

func press(tb testing.TB, m Model, keys ...tea.KeyPressMsg) Model {
	tb.Helper()
	for _, k := range keys {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	return m
}

func startedSelection(t *testing.T, m Model) deps.UpdateSelection {
	t.Helper()
	if m.deps.cycle.Phase() != deps.PhaseChecking {
		t.Fatalf("cycle phase = %s, want checking", m.deps.cycle.Phase())
	}
	return m.deps.cycle.Selection()
}

func TestSpaceTogglesMarkOnCursorRowAndRendersGlyph(t *testing.T) {
	m := marksFixture(t)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.deps.marked("example.com/a") {
		t.Fatal("expected cursor row to be marked")
	}
	if got := m.deps.table.Rows()[0][0]; got != markFilled+"example.com/a" {
		t.Fatalf("row 0 = %q, want filled glyph prefix", got)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.deps.marked("example.com/a") {
		t.Fatal("expected second space to clear the mark")
	}
	if got := m.deps.table.Rows()[0][0]; got != markEmpty+"example.com/a" {
		t.Fatalf("row 0 = %q, want empty glyph prefix", got)
	}
}

func TestMarkOnRowWithoutUpdateStillRendersFilledGlyph(t *testing.T) {
	m := marksFixture(t)
	// Hidden indirect row is skipped: row 1 is example.com/b.
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.deps.marked("example.com/b") {
		t.Fatalf("expected example.com/b marked, marks = %v", m.deps.marks)
	}
	if got := m.deps.table.Rows()[1][0]; !strings.HasPrefix(got, markFilled) {
		t.Fatalf("row 1 = %q, want filled glyph", got)
	}
	// Unmarked rows keep the empty glyph so the column reads as a checklist.
	if got := m.deps.table.Rows()[0][0]; !strings.HasPrefix(got, markEmpty) {
		t.Fatalf("row 0 = %q, want empty glyph", got)
	}
}

func TestUpdateScopeIsAllDirectUnlessMarked(t *testing.T) {
	m := marksFixture(t)
	fakeDepsExecutor{}.bind(&m)

	// No marks: every direct dependency, regardless of the cursor.
	got := startedSelection(t, press(t, m, tea.KeyPressMsg{Code: 'j'}, tea.KeyPressMsg{Code: 'u'}))
	if got.Explicit() {
		t.Fatalf("selection = %#v, want all direct dependencies", got)
	}

	// Marks win over the cursor and keep dependency order.
	m2 := press(t, m,
		tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeySpace}, // c
		tea.KeyPressMsg{Code: tea.KeyUp}, tea.KeyPressMsg{Code: tea.KeyUp}, tea.KeyPressMsg{Code: tea.KeySpace}, // a
		tea.KeyPressMsg{Code: tea.KeyDown}, // cursor on b, which is not marked
		tea.KeyPressMsg{Code: 'u'},
	)
	got = startedSelection(t, m2)
	if len(got.Modules) != 2 || got.Modules[0] != "example.com/a" || got.Modules[1] != "example.com/c" {
		t.Fatalf("selection = %#v, want [a c]", got.Modules)
	}
	if got.Level != deps.LevelLatest {
		t.Fatalf("level = %s, want latest", got.Level)
	}
}

func TestUpdateOnEmptyTableReportsNothingToUpdate(t *testing.T) {
	m := loadDeps(t, marksFixture(t), nil)
	m = press(t, m, tea.KeyPressMsg{Code: 'u'})
	if m.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle started with nothing under the cursor: %s", m.deps.cycle.Phase())
	}
	if !strings.Contains(m.Status.Text(), "Nothing to update") {
		t.Fatalf("status = %q", m.Status.Text())
	}
}

func TestMarkAllMarksEveryListedRowInBothDisplayModes(t *testing.T) {
	wants := map[config.DepsDisplayMode][]string{
		config.DepsDisplayDirect: {"example.com/a", "example.com/b", "example.com/c"},
		config.DepsDisplayAll:    {"example.com/a", "example.com/hidden", "example.com/b", "example.com/c"},
	}
	for display, want := range wants {
		m := marksFixture(t)
		m.deps.applySettings(config.Settings{DepsDisplay: display})
		m = press(t, m, tea.KeyPressMsg{Code: 'a'})
		if got := m.deps.markedPaths(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("display %v: marks = %v, want %v", display, got, want)
		}
		for _, row := range m.deps.table.Rows() {
			if !strings.HasPrefix(row[0], markFilled) {
				t.Fatalf("display %v: row %q not rendered as marked", display, row[0])
			}
		}
		// Second press clears everything.
		m = press(t, m, tea.KeyPressMsg{Code: 'a'})
		if len(m.deps.marks) != 0 {
			t.Fatalf("display %v: expected marks cleared, got %v", display, m.deps.marks)
		}
	}
}

func TestMarkAllAfterManualMarkClearsEverything(t *testing.T) {
	m := marksFixture(t)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace}, tea.KeyPressMsg{Code: 'a'})
	if len(m.deps.marks) != 0 {
		t.Fatalf("marks = %v, want none: a with any mark present clears all", m.deps.marks)
	}
	if !strings.Contains(m.Status.Text(), "Marks cleared") {
		t.Fatalf("status = %q", m.Status.Text())
	}
}

func TestMarksSurviveRefreshAndDisplayToggle(t *testing.T) {
	m := marksFixture(t)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	updated, _ := m.Update(dependenciesMsg(m.deps.dependencies))
	m = updated.(Model)
	if !m.deps.marked("example.com/a") {
		t.Fatal("mark lost after dependenciesMsg")
	}
	m.deps.applySettings(config.Settings{DepsDisplay: config.DepsDisplayAll})
	if !m.deps.marked("example.com/a") || !strings.HasPrefix(m.deps.table.Rows()[0][0], markFilled) {
		t.Fatal("mark lost after display toggle")
	}
	// A module that vanished from the list loses its mark once the
	// list is refreshed without it.
	m = loadDeps(t, m, marksList()[1:])
	if m.deps.marked("example.com/a") {
		t.Fatal("stale mark should be pruned")
	}
}

func TestDisplayToggleNeverTouchesMarks(t *testing.T) {
	m := marksFixture(t)
	m.deps.applySettings(config.Settings{DepsDisplay: config.DepsDisplayAll})
	// Mark the indirect module, then hide it again.
	m = press(t, m, tea.KeyPressMsg{Code: 'j'}, tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.deps.marked("example.com/hidden") {
		t.Fatalf("marks = %v, want hidden marked", m.deps.marks)
	}
	m.deps.applySettings(config.Settings{DepsDisplay: config.DepsDisplayDirect})
	if !m.deps.marked("example.com/hidden") {
		t.Fatal("hiding a marked module must not drop its mark")
	}
	// Only a refreshed list without the module drops the mark.
	m = loadDeps(t, m, marksList()[:1])
	if m.deps.marked("example.com/hidden") {
		t.Fatal("a module that left the list must lose its mark")
	}
}

func TestMarksClearedAfterCycleEndsButKeptOnCancel(t *testing.T) {
	m := marksFixture(t)
	fakeDepsExecutor{}.bind(&m)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace}, tea.KeyPressMsg{Code: 'u'})
	updated, _ := m.Update(deps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	m = updated.(Model)
	if m.deps.dialog.kind != dialogUpdate || !m.deps.dialog.explicit {
		t.Fatalf("dialog = %#v, want explicit update dialog", m.deps.dialog)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.deps.marked("example.com/a") {
		t.Fatal("cancel must keep marks")
	}

	// Run to a terminal outcome: no updates for the marked module.
	upToDate := marksList()
	upToDate[0].Latest = "v1.0.0"
	m = loadDeps(t, m, upToDate)
	m = press(t, m, tea.KeyPressMsg{Code: 'u'})
	updated, _ = m.Update(deps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	m = updated.(Model)
	if m.deps.cycle.Phase() != deps.PhaseIdle || len(m.deps.marks) != 0 {
		t.Fatalf("phase = %s marks = %v, want idle and no marks", m.deps.cycle.Phase(), m.deps.marks)
	}
	if !strings.Contains(m.Status.Text(), "Already up to date: example.com/a") {
		t.Fatalf("status = %q", m.Status.Text())
	}
}

func entryPaths(entries []deps.DependencyUpdateEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	return paths
}

func TestDialogSpaceTogglesScopeBetweenAllAndCursorModule(t *testing.T) {
	m := marksFixture(t)
	fakeDepsExecutor{}.bind(&m)
	// Cursor on example.com/b (current); nothing marked.
	m = openUpdateDialog(t, press(t, m, tea.KeyPressMsg{Code: 'j'}))
	if m.deps.dialog.explicit {
		t.Fatal("scope should start at all direct dependencies")
	}
	if got := entryPaths(m.deps.dialog.updateEntries); strings.Join(got, ",") != "example.com/a,example.com/c" {
		t.Fatalf("all-scope entries = %v", got)
	}
	if got := m.deps.dialog.explicitModules; len(got) != 1 || got[0] != "example.com/b" {
		t.Fatalf("explicit modules = %v, want cursor module", got)
	}
	rendered := stripANSI(m.deps.dialog.render(testTheme(), m.deps, viewportSize{Width: 64, Height: 24}))
	for _, want := range []string{"Scope:", "All", "Current"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("dialog missing %q:\n%s", want, rendered)
		}
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.deps.dialog.explicit || m.deps.dialog.kind != dialogUpdate {
		t.Fatalf("dialog after space = %#v, want explicit scope", m.deps.dialog)
	}
	if sel := m.deps.cycle.Selection(); len(sel.Modules) != 1 || sel.Modules[0] != "example.com/b" {
		t.Fatalf("cycle selection = %#v, want cursor module", sel)
	}
	if len(m.deps.dialog.updateEntries) != 0 {
		t.Fatalf("expected empty plan for current module, got %v", m.deps.dialog.updateEntries)
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.deps.dialog.explicit || m.deps.cycle.Selection().Explicit() {
		t.Fatal("second space should return to all direct dependencies")
	}
	if got := entryPaths(m.deps.dialog.updateEntries); strings.Join(got, ",") != "example.com/a,example.com/c" {
		t.Fatalf("entries after toggling back = %v", got)
	}
}

func TestDialogScopeStartsMarkedAndKeepsLevelAcrossToggle(t *testing.T) {
	m := marksFixture(t)
	fakeDepsExecutor{}.bind(&m)
	m = openUpdateDialog(t, press(t, m, tea.KeyPressMsg{Code: tea.KeySpace}))
	if !m.deps.dialog.explicit {
		t.Fatal("scope should start at the marked set")
	}
	if got := entryPaths(m.deps.dialog.updateEntries); strings.Join(got, ",") != "example.com/a" {
		t.Fatalf("marked-scope entries = %v", got)
	}
	rendered := stripANSI(m.deps.dialog.render(testTheme(), m.deps, viewportSize{Width: 64, Height: 24}))
	if !strings.Contains(rendered, "Marked (1)") {
		t.Fatalf("dialog missing marked label:\n%s", rendered)
	}

	// Level change survives a scope toggle in both directions.
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyUp}, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.deps.dialog.explicit {
		t.Fatal("space should switch to all direct dependencies")
	}
	if got := entryPaths(m.deps.dialog.updateEntries); strings.Join(got, ",") != "example.com/a,example.com/c" {
		t.Fatalf("all-scope entries = %v", got)
	}
	if sel := m.deps.cycle.Selection(); sel.Level != deps.LevelMinor || sel.Explicit() {
		t.Fatalf("cycle selection = %#v, want minor level over all direct", sel)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	if sel := m.deps.cycle.Selection(); sel.Level != deps.LevelMinor || len(sel.Modules) != 1 || sel.Modules[0] != "example.com/a" {
		t.Fatalf("cycle selection = %#v, want minor level over marked", sel)
	}
	// Marks are untouched by scope toggling.
	if !m.deps.marked("example.com/a") || len(m.deps.marks) != 1 {
		t.Fatalf("marks = %v, want only a", m.deps.marks)
	}
}

func TestMarkKeysInertWhileOperationInProgress(t *testing.T) {
	m := press(t, marksFixture(t), tea.KeyPressMsg{Code: 'r'})
	if m.deps.phase != depsChecking {
		t.Fatalf("phase = %v, want checking", m.deps.phase)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace}, tea.KeyPressMsg{Code: 'a'})
	if len(m.deps.marks) != 0 {
		t.Fatalf("marks changed during operation: %v", m.deps.marks)
	}
}

func TestUpdateDialogArrowsCycleLevelAndRebuildPlan(t *testing.T) {
	withVersions := marksList()
	withVersions[0].Versions = []string{"v1.0.0", "v1.0.5", "v1.1.0"}
	m := loadDeps(t, newTestModel(t), withVersions)
	m = press(t, m, tea.KeyPressMsg{Code: 'u'})
	updated, _ := m.Update(deps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	m = updated.(Model)
	if m.deps.dialog.level != deps.LevelLatest || m.deps.dialog.updateEntries[0].NewVersion != "v1.1.0" {
		t.Fatalf("initial dialog = %#v", m.deps.dialog)
	}
	// Highlight "No", then change level: the highlight must survive.
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyRight}, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.deps.dialog.level != deps.LevelMinor || m.deps.dialog.choiceYes {
		t.Fatalf("after up: level = %s choiceYes = %v", m.deps.dialog.level, m.deps.dialog.choiceYes)
	}
	m = press(t, m, tea.KeyPressMsg{Code: 'k'})
	if m.deps.dialog.level != deps.LevelPatch || m.deps.dialog.updateEntries[0].NewVersion != "v1.0.5" {
		t.Fatalf("after k: dialog = %#v", m.deps.dialog)
	}
	if m.deps.cycle.Selection().Level != deps.LevelPatch {
		t.Fatalf("cycle level = %s, want patch", m.deps.cycle.Selection().Level)
	}
	// Wrap around: patch -> up -> latest.
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.deps.dialog.level != deps.LevelLatest {
		t.Fatalf("expected wrap to latest, got %s", m.deps.dialog.level)
	}
	view := stripANSI(m.deps.dialog.render(testTheme(), m.deps, viewportSize{Width: 80, Height: 24}))
	if !strings.Contains(view, "Level:") || !strings.Contains(view, "v1.1.0") {
		t.Fatalf("dialog view missing level line or entry:\n%s", view)
	}
}

func TestUpdateDialogEmptyPlanConfirmEndsAsNoUpdates(t *testing.T) {
	withVersions := marksList()
	withVersions[0].Versions = []string{"v1.0.0", "v1.1.0"}
	m := loadDeps(t, newTestModel(t), withVersions)
	m = press(t, m, tea.KeyPressMsg{Code: 'u'})
	updated, _ := m.Update(deps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	m = updated.(Model)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // latest -> patch (wrap)
	if m.deps.dialog.level != deps.LevelPatch || len(m.deps.dialog.updateEntries) != 0 {
		t.Fatalf("dialog = %#v, want empty patch plan", m.deps.dialog)
	}
	view := stripANSI(m.deps.dialog.render(testTheme(), m.deps, viewportSize{Width: 80, Height: 24}))
	if !strings.Contains(view, "No updates available at the patch level") {
		t.Fatalf("dialog view:\n%s", view)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.deps.dialog.active() || m.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("dialog active = %v phase = %s", m.deps.dialog.active(), m.deps.cycle.Phase())
	}
	if !strings.Contains(m.Status.Text(), "at the patch level") {
		t.Fatalf("status = %q", m.Status.Text())
	}
}

func TestDepsHintBarAndOverlayDocumentMarkKeys(t *testing.T) {
	bar := stripANSI(renderHelpBar(testTheme(), Model{CurrentTab: DepsTab}, 120))
	for _, want := range []string{"space mark", "a mark all / none", "u update"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("hint bar missing %q: %s", want, bar)
		}
	}
	if !hasBinding(tabKeyBindings(DepsTab), "a") {
		t.Fatal("Deps section must document the a key")
	}
	if !hasBinding(dialogKeyBindings(depsDialog{kind: dialogUpdate}), "↑/↓ k/j") {
		t.Fatal("update dialog section must document the level keys")
	}
	// The scope key is offered only when there is an explicit set to switch to.
	if hasBinding(dialogKeyBindings(depsDialog{kind: dialogUpdate}), "space") {
		t.Fatal("update dialog without an explicit set must not offer the scope key")
	}
	withScope := depsDialog{kind: dialogUpdate, explicitModules: []string{"example.com/a"}}
	if !hasBinding(dialogKeyBindings(withScope), "space") {
		t.Fatal("update dialog with an explicit set must document the scope key")
	}
	dialogBar := stripANSI(renderHelpBar(testTheme(), Model{CurrentTab: DepsTab, deps: depsTab{dialog: withScope}}, 120))
	if !strings.Contains(dialogBar, "space scope") {
		t.Fatalf("dialog hint bar missing scope key: %s", dialogBar)
	}
}
