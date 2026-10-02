package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

// mouseModalChecks starts with the existing apply fixture but completes the
// operation with changed versions, so Keep and Roll back cannot pass merely
// because the pre-update and post-update lists happen to be identical.
func mouseModalChecks(t *testing.T) Model {
	t.Helper()
	m := mouseSized(t, modelAtConfirmApply(t), 100, 36)
	fakeDepsExecutor{execute: func(intent deps.Intent) (deps.Event, error) {
		apply, ok := intent.(deps.IntentApplyUpdates)
		if !ok {
			t.Fatalf("opening checks executed %T, want apply", intent)
		}
		if len(apply.Entries) != 1 || apply.Entries[0].Path != testLib()[0].Path || apply.Entries[0].NewVersion != "v1.1.0" {
			t.Fatalf("apply entries = %+v", apply.Entries)
		}
		updated := testLib()
		updated[0].Version = "v1.1.0"
		return deps.ApplyUpdatesDoneEvent{
			Snapshot: &deps.DependencySnapshot{
				ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n"},
			},
			Backup:       &deps.DependencyBackupInfo{Name: "before-update.json", Path: "/backups/before-update.json"},
			Dependencies: updated,
		}, nil
	}}.bind(&m)
	m = mouseClickRun(t, m, "Yes")
	if m.deps.dialog.kind != dialogChecks || m.deps.dependencies[0].Version != "v1.1.0" {
		t.Fatalf("apply did not open checks with updated dependency: dialog=%v deps=%+v",
			m.deps.dialog.kind, m.deps.dependencies)
	}
	return m
}

func mouseModalRollback(t *testing.T) Model {
	t.Helper()
	m := mouseModalChecks(t)
	fakeDepsExecutor{execute: func(intent deps.Intent) (deps.Event, error) {
		if _, ok := intent.(deps.IntentRunChecks); !ok {
			t.Fatalf("opening rollback executed %T, want checks", intent)
		}
		return deps.ChecksDoneEvent{Result: failedChecks()}, nil
	}}.bind(&m)
	m = mouseClickRun(t, m, "Yes")
	if m.deps.dialog.kind != dialogRollback {
		t.Fatalf("failed checks opened dialog %v, want rollback", m.deps.dialog.kind)
	}
	return m
}

type mouseModalRestoreExecutor struct {
	fakeDepsExecutor
	restore func(string) (deps.DependencyRestoreResult, error)
}

func (f mouseModalRestoreExecutor) Restore(name string) (deps.DependencyRestoreResult, error) {
	return f.restore(name)
}

func TestMouseDialogs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		kind         depsDialogKind
		yes          bool
		inconclusive bool
	}{
		{name: "update Yes overrides No highlight", kind: dialogUpdate, yes: true},
		{name: "update No overrides Yes highlight", kind: dialogUpdate},
		{name: "checks Yes runs checks", kind: dialogChecks, yes: true},
		{name: "checks No skips checks", kind: dialogChecks},
		{name: "rollback Roll back restores snapshot", kind: dialogRollback, yes: true},
		{name: "rollback Keep retains changed dependencies", kind: dialogRollback},
		{name: "inconclusive checks Roll back", kind: dialogRollback, yes: true, inconclusive: true},
		{name: "inconclusive checks Keep", kind: dialogRollback, inconclusive: true},
		{name: "restore Restore applies selected backup", kind: dialogRestore, yes: true},
		{name: "restore Cancel preserves dependencies", kind: dialogRestore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			switch tc.kind {
			case dialogUpdate:
				m = modelAtConfirmApply(t)
			case dialogChecks:
				m = mouseModalChecks(t)
			case dialogRollback:
				if tc.inconclusive {
					m = mouseModalChecks(t)
					m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
					m = feed(t, m, deps.ChecksDoneEvent{Err: errors.New("checks could not start")})
				} else {
					m = mouseModalRollback(t)
				}
			case dialogRestore:
				m = openRestoreDialog(t, loadDeps(t, newTestModel(t), testLib()), []deps.DependencyBackupInfo{
					{Name: "saved.json", Path: "/backups/saved.json"},
				})
			}
			m = mouseSized(t, m, 100, 36)
			before := append([]deps.ModuleDependency(nil), m.deps.dependencies...)
			calls := 0
			fakeDepsExecutor{execute: func(intent deps.Intent) (deps.Event, error) {
				calls++
				switch tc.kind {
				case dialogUpdate:
					apply, ok := intent.(deps.IntentApplyUpdates)
					if !ok || len(apply.Entries) != 1 || apply.Entries[0].NewVersion != "v1.1.0" {
						t.Fatalf("update executed %T %+v", intent, intent)
					}
					updated := testLib()
					updated[0].Version = "v1.1.0"
					return deps.ApplyUpdatesDoneEvent{
						Snapshot:     &deps.DependencySnapshot{},
						Backup:       &deps.DependencyBackupInfo{Name: "saved.json"},
						Dependencies: updated,
					}, nil
				case dialogChecks:
					if _, ok := intent.(deps.IntentRunChecks); !ok {
						t.Fatalf("checks executed %T", intent)
					}
					return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: true}}, nil
				case dialogRollback:
					rollback, ok := intent.(deps.IntentRollback)
					if !ok || rollback.Snapshot == nil || rollback.Snapshot.ModFile.Content != "module example.com/app\n" {
						t.Fatalf("rollback did not use captured snapshot: %T %+v", intent, intent)
					}
					return deps.RollbackDoneEvent{Dependencies: testLib()}, nil
				default:
					t.Fatalf("unexpected cycle operation %T", intent)
					return nil, nil
				}
			}}.bind(&m)
			if tc.kind == dialogRestore {
				executor := mouseModalRestoreExecutor{restore: func(name string) (deps.DependencyRestoreResult, error) {
					calls++
					if name != "saved.json" {
						t.Fatalf("restored %q, want saved.json", name)
					}
					restored := testLib()
					restored[0].Version = "v0.9.0"
					return deps.DependencyRestoreResult{BackupName: name, Dependencies: restored}, nil
				}}
				m = m.BindDeps(func(int) deps.API { return executor })
			}
			// Intentionally highlight the other answer. A click must commit its
			// own answer, not synthesize Enter on the existing selection.
			if tc.yes {
				m = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
			}
			label, noLabel := buttonLabels(tc.kind)
			if !tc.yes {
				label = noLabel
			}
			m = mouseClickRun(t, m, label)
			wantCalls := 0
			if tc.yes {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("operation calls = %d, want %d", calls, wantCalls)
			}
			if tc.yes && tc.kind == dialogUpdate {
				if m.deps.dialog.kind != dialogChecks || m.deps.dependencies[0].Version != "v1.1.0" {
					t.Fatalf("apply outcome: dialog=%v deps=%+v", m.deps.dialog.kind, m.deps.dependencies)
				}
				return
			}
			if m.deps.dialog.active() || m.deps.busy() {
				t.Fatalf("answer left dialog or operation active: dialog=%v phase=%v", m.deps.dialog.kind, m.deps.cycle.Phase())
			}
			if !tc.yes || tc.kind == dialogChecks {
				if !reflect.DeepEqual(m.deps.dependencies, before) {
					t.Fatalf("answer changed retained dependencies: got %+v want %+v", m.deps.dependencies, before)
				}
			} else {
				want := "v1.0.0"
				if tc.kind == dialogRestore {
					want = "v0.9.0"
				}
				if m.deps.dependencies[0].Version != want || m.deps.table.Rows()[0][1] != want {
					t.Fatalf("restored version not published to table: deps=%+v rows=%v", m.deps.dependencies, m.deps.table.Rows())
				}
			}
			wantStatus := ""
			switch tc.kind {
			case dialogUpdate:
				wantStatus = "canceled"
			case dialogChecks:
				wantStatus = "skipped"
				if tc.yes {
					wantStatus = "passed"
				}
			case dialogRollback:
				wantStatus = "kept"
				if tc.yes {
					wantStatus = "Rolled back"
				}
			case dialogRestore:
				wantStatus = "canceled"
				if tc.yes {
					wantStatus = "saved.json"
				}
			}
			if !strings.Contains(m.Status.Text(), wantStatus) {
				t.Fatalf("outcome status = %q, want %q", m.Status.Text(), wantStatus)
			}
		})
	}

	for _, yes := range []bool{false, true} {
		t.Run(fmt.Sprintf("prune confirm=%v", yes), func(t *testing.T) {
			m := pruneConfirmingModel(t)
			remaining := []utils.GoVersion{{
				Version: "1.24.4", Installed: true, Active: true, Path: "/versions/go1.24.4",
			}}
			seedVersions(t, &m, append(append([]utils.GoVersion{}, remaining...), utils.GoVersion{
				Version: "1.26.1", Installed: true, Path: "/versions/go1.26.1",
			}))
			runs, loads := 0, 0
			m = m.BindVersionOperations(VersionOperations{
				Prune: func(context.Context) (prune.Result, error) {
					runs++
					return prune.Result{Removed: installedTestPlan("1.26.1", 1024).Candidates}, nil
				},
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
					loads++
					return remaining, nil
				},
				DiskUsage: func(context.Context) (prune.Summary, error) {
					return prune.Summary{}, nil
				},
			})
			m = mouseSized(t, m, 100, 36)
			label := "No"
			if yes {
				m = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
				label = "Yes"
			}
			m = mouseClickRun(t, m, label)
			wantRuns, wantStatus := 0, "canceled"
			if yes {
				wantRuns, wantStatus = 1, "freed 1.0 KiB"
			}
			if runs != wantRuns || m.inputContext() != inputTab || !strings.Contains(m.Status.Text(), wantStatus) {
				t.Fatalf("prune outcome: runs=%d context=%v status=%q", runs, m.inputContext(), m.Status.Text())
			}
			candidate, found := m.projection.lookup("1.26.1")
			candidateInstalled := found && candidate.Installed
			if candidateInstalled == yes || loads != wantRuns {
				t.Fatalf("prune did not publish the operation outcome: candidate installed=%v loads=%d",
					candidateInstalled, loads)
			}
			active, found := m.projection.lookup("1.24.4")
			if !found || !active.Installed || !active.Active {
				t.Fatal("prune changed the active version")
			}
			if !yes {
				// Cancellation must release admission, not just hide the dialog.
				m = m.BindVersionOperations(VersionOperations{PreviewPrune: func(context.Context) (prune.Result, error) {
					return installedTestPlan("1.26.1", 1024), nil
				}})
				m = mouseClickRun(t, m, "Prune p")
				if m.inputContext() != inputPruneConfirm {
					t.Fatal("cancel did not allow another prune preview")
				}
			}
		})
	}

	t.Run("level and scope preserve explicit snapshot and No choice", func(t *testing.T) {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("marked=%v", marked), func(t *testing.T) {
				list := []deps.ModuleDependency{
					{Path: "example.com/a", Version: "v1.0.0", Latest: "v2.0.0", Versions: []string{"v1.0.2", "v1.2.0", "v2.0.0"}},
					{Path: "example.com/b", Version: "v1.0.0", Latest: "v2.0.0", Versions: []string{"v1.0.3", "v1.3.0", "v2.0.0"}},
				}
				m := loadDeps(t, newTestModel(t), list)
				if marked {
					m = press(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
				}
				m = mouseSized(t, openUpdateDialog(t, m), 110, 40)
				m = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
				// Moving the presentation cursor after opening must not replace
				// the explicit scope captured for this cycle.
				m.deps.table.SetCursor(1)
				for _, level := range []struct {
					label   string
					level   deps.UpdateLevel
					version string
				}{
					{label: "Patch", level: deps.LevelPatch, version: "v1.0.2"},
					{label: "Minor", level: deps.LevelMinor, version: "v1.2.0"},
					{label: "Latest", level: deps.LevelLatest, version: "v2.0.0"},
				} {
					m = mouseClickRun(t, m, level.label)
					levelMismatch := m.deps.dialog.level != level.level
					targetMismatch := len(m.deps.dialog.updateEntries) == 0 ||
						m.deps.dialog.updateEntries[0].NewVersion != level.version
					if levelMismatch || m.deps.dialog.choiceYes || targetMismatch {
						t.Fatalf("%s plan lost level/No/target: %+v", level.label, m.deps.dialog)
					}
					before := m.deps.dialog
					var cmd tea.Cmd
					m, cmd = mouseClick(t, m, level.label)
					if cmd != nil || !reflect.DeepEqual(m.deps.dialog, before) {
						t.Fatalf("clicking selected level %s changed the pending decision", level.label)
					}
				}
				m = mouseClickRun(t, m, "All")
				if m.deps.dialog.explicit || len(m.deps.dialog.updateEntries) != 2 || m.deps.dialog.choiceYes {
					t.Fatalf("All scope = %+v", m.deps.dialog)
				}
				label := "Current"
				if marked {
					label = "Marked"
				}
				m = mouseClickRun(t, m, label)
				if got := m.deps.cycle.Selection(); !got.Explicit() || strings.Join(got.Modules, ",") != "example.com/a" {
					t.Fatalf("explicit scope re-read cursor instead of snapshot: %+v", got)
				}
				wrongExplicitPlan := len(m.deps.dialog.updateEntries) != 1 ||
					m.deps.dialog.updateEntries[0].Path != "example.com/a"
				if wrongExplicitPlan || m.deps.dialog.choiceYes {
					t.Fatalf("explicit plan = %+v", m.deps.dialog)
				}
				before := m.deps.dialog
				var cmd tea.Cmd
				m, cmd = mouseClick(t, m, label)
				if cmd != nil || !reflect.DeepEqual(before, m.deps.dialog) {
					t.Fatal("clicking selected scope changed the pending decision")
				}
			})
		}
	})

	t.Run("empty plan Yes ends without applying", func(t *testing.T) {
		m := mouseSized(t, openUpdateDialog(t, marksFixture(t)), 100, 36)
		calls := 0
		fakeDepsExecutor{execute: func(deps.Intent) (deps.Event, error) { calls++; return nil, nil }}.bind(&m)
		m = mouseClickRun(t, m, "Patch")
		if len(m.deps.dialog.updateEntries) != 0 || m.deps.dialog.kind != dialogUpdate {
			t.Fatalf("expected open empty patch plan, got %+v", m.deps.dialog)
		}
		before := append([]deps.ModuleDependency(nil), m.deps.dependencies...)
		m = mouseClickRun(t, m, "Yes")
		if calls != 0 || m.deps.dialog.active() || m.deps.busy() || !reflect.DeepEqual(before, m.deps.dependencies) {
			t.Fatalf("empty plan performed work: calls=%d dialog=%v deps=%+v", calls, m.deps.dialog.kind, m.deps.dependencies)
		}
	})

	t.Run("seventh backup selection waits for Restore", func(t *testing.T) {
		backups := make([]deps.DependencyBackupInfo, 9)
		for i := range backups {
			backups[i] = deps.DependencyBackupInfo{
				Name: fmt.Sprintf("backup-%02d.json", i+1),
				Path: fmt.Sprintf("/backups/%02d.json", i+1),
			}
		}
		m := mouseSized(t, openRestoreDialog(t, loadDeps(t, newTestModel(t), testLib()), backups), 100, 36)
		calls := 0
		executor := mouseModalRestoreExecutor{restore: func(name string) (deps.DependencyRestoreResult, error) {
			calls++
			if name != backups[6].Name {
				t.Fatalf("restored %q, want seventh backup %q", name, backups[6].Name)
			}
			return deps.DependencyRestoreResult{
				BackupName:   name,
				Dependencies: []deps.ModuleDependency{{Path: "example.com/seventh", Version: "v7.0.0"}},
			}, nil
		}}
		m = m.BindDeps(func(int) deps.API { return executor })
		for range 6 {
			m = mouseWheel(t, m, backups[m.deps.dialog.cursor].Name, 1)
		}
		if m.deps.dialog.cursor != 6 || calls != 0 {
			t.Fatalf("wheel selected cursor=%d calls=%d", m.deps.dialog.cursor, calls)
		}
		// Move away, then select the seventh visible backup directly. Neither
		// navigation path is allowed to run the restore operation.
		m = mouseWheel(t, m, backups[6].Name, 1)
		m = mouseClickRun(t, m, backups[6].Name)
		if calls != 0 || m.deps.dialog.cursor != 6 || m.deps.dialog.kind != dialogRestore {
			t.Fatalf("backup click executed restore or selected wrong row: cursor=%d calls=%d", m.deps.dialog.cursor, calls)
		}
		m = mouseClickRun(t, m, "Restore")
		wrongRestoredModule := len(m.deps.dependencies) != 1 ||
			m.deps.dependencies[0].Path != "example.com/seventh"
		notFinished := m.deps.dialog.active() || m.deps.phase != depsIdle
		if calls != 1 || notFinished || wrongRestoredModule {
			t.Fatalf("seventh restore outcome: calls=%d dialog=%v deps=%+v", calls, m.deps.dialog.kind, m.deps.dependencies)
		}
	})
}
