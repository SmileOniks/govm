package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/utils"
)

func TestUpdateKeyStartsFreshPreflight(t *testing.T) {
	m := loadDeps(t, newTestModel(t), testLib())
	var issued deps.Intent
	fakeDepsExecutor{execute: func(intent deps.Intent) (deps.Event, error) {
		issued = intent
		return nil, nil
	}}.bind(&m)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'u'})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("expected fresh check command")
	}
	cmd()
	if got.deps.cycle.Phase() != deps.PhaseChecking {
		t.Fatalf("cycle phase = %s, want checking", got.deps.cycle.Phase())
	}
	if _, ok := issued.(deps.IntentCheckUpdates); !ok {
		t.Fatalf("issued intent = %T, want IntentCheckUpdates", issued)
	}
	if got.deps.dialog.active() {
		t.Fatal("update dialog must wait for the fresh preflight result")
	}
}

func TestFreshPreflightResultOpensUpdateDialog(t *testing.T) {
	m := feed(t, loadDeps(t, newTestModel(t), testLib()), tea.KeyPressMsg{Code: 'u'})

	fresh := []deps.ModuleDependency{
		{Path: "github.com/example/lib", Version: "v1.0.1", Latest: "v1.2.0"},
	}
	updated, _ := m.Update(deps.CheckUpdatesDoneEvent{Dependencies: fresh})
	got := updated.(Model)

	if got.deps.dialog.kind != dialogUpdate {
		t.Fatalf("dialog kind = %v, want dialogUpdate", got.deps.dialog.kind)
	}
	entries := got.deps.cycle.Entries()
	if len(entries) != 1 || entries[0].OldVersion != "v1.0.1" || entries[0].NewVersion != "v1.2.0" {
		t.Fatalf("fresh entries = %+v", entries)
	}
	if !reflect.DeepEqual(got.deps.cycle.Dependencies(), fresh) {
		t.Fatalf("dependencies = %+v, want %+v", got.deps.cycle.Dependencies(), fresh)
	}
	if !got.deps.dialog.choiceYes {
		t.Fatal("expected default choice Yes")
	}
	if !reflect.DeepEqual(got.deps.dialog.updateEntries, entries) {
		t.Fatalf("dialog entries = %+v, want %+v", got.deps.dialog.updateEntries, entries)
	}
}

func TestUnknownCycleIntentReportsError(t *testing.T) {
	m := feed(t, loadDeps(t, newTestModel(t), testLib()), tea.KeyPressMsg{Code: 'u'})

	_, status := m.deps.applyCycleIntent(nil)
	m.applyDepsStatus(status)
	got := m
	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if got.Status.Kind() != "error" || !strings.Contains(got.Status.Text(), "Unhandled dependency cycle intent") {
		t.Fatalf("status = %q (%s)", got.Status.Text(), got.Status.Kind())
	}
}

func TestWindowSizeMsgKeepsContentSizesPositive(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	got := updated.(Model)

	if got.projection.availableModel().Width() <= 0 || got.projection.availableModel().Height() <= 0 {
		t.Fatalf("expected positive list size, got %dx%d", got.projection.availableModel().Width(), got.projection.availableModel().Height())
	}

	if got.projection.installedModel().Width() <= 0 || got.projection.installedModel().Height() <= 0 {
		t.Fatalf("expected positive table size, got %dx%d", got.projection.installedModel().Width(), got.projection.installedModel().Height())
	}
}

func TestWindowSizeMsgResizesDepsTable(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := updated.(Model)

	if got.deps.table.Width() <= 0 || got.deps.table.Height() <= 0 {
		t.Fatalf("expected positive deps table size, got %dx%d", got.deps.table.Width(), got.deps.table.Height())
	}
}

func TestWindowSizeMsgUsesNormalContentWidth(t *testing.T) {
	tests := []struct {
		name      string
		termWidth int
		wantWidth int
	}{
		{name: "minimum terminal", termWidth: 64, wantWidth: 60},
		{name: "normal terminal", termWidth: 80, wantWidth: 76},
		{name: "wide breakpoint", termWidth: 130, wantWidth: 124},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.termWidth, Height: 24})
			got := updated.(Model)

			if got.Width != tt.wantWidth {
				t.Fatalf("content width = %d, want %d", got.Width, tt.wantWidth)
			}
			if got.deps.table.Width() != tt.wantWidth {
				t.Fatalf("deps table width = %d, want %d", got.deps.table.Width(), tt.wantWidth)
			}
		})
	}
}

func TestDependencyBackupsMsgOpensRestoreDialog(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(dependencyBackupsMsg{
		{
			Name:       "2026-07-09_12-00-00.json",
			ModulePath: "github.com/acme/app",
			Kind:       deps.DependencyBackupKindPreUpdate,
			Updated:    1,
		},
	})
	got := updated.(Model)

	if got.deps.phase == depsLoadingBackups {
		t.Fatal("expected LoadingBackups to be false after backups load")
	}
	if got.deps.dialog.kind != dialogRestore {
		t.Fatal("expected restore dialog to open")
	}
	if got.deps.dialog.cursor != 0 {
		t.Fatalf("expected backup cursor 0, got %d", got.deps.dialog.cursor)
	}
}

func TestApplyResultUpdatesStateAndOpensChecksDialog(t *testing.T) {
	m := feed(t, modelAtConfirmApply(t), tea.KeyPressMsg{Code: tea.KeyEnter})
	dependencies := []deps.ModuleDependency{{
		Path: "github.com/example/lib", Version: "v1.1.0", Latest: "v1.1.0",
	}}

	updated, _ := m.Update(deps.ApplyUpdatesDoneEvent{
		Snapshot: &deps.DependencySnapshot{
			ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "old"},
		},
		Backup:       &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"},
		Dependencies: dependencies,
	})
	got := updated.(Model)

	if got.deps.cycle.Phase() != deps.PhaseConfirmChecks {
		t.Fatalf("cycle phase = %s, want confirm-checks", got.deps.cycle.Phase())
	}
	if got.deps.cycle.Snapshot() == nil {
		t.Fatal("expected cycle snapshot")
	}
	if got.deps.dialog.kind != dialogChecks || !got.deps.dialog.choiceYes {
		t.Fatalf("dialog = %+v, want checks default Yes", got.deps.dialog)
	}
	if !reflect.DeepEqual(got.deps.dependencies, dependencies) {
		t.Fatalf("dependencies = %+v, want %+v", got.deps.dependencies, dependencies)
	}
}

func TestChecksPassedCompletesCycle(t *testing.T) {
	m := feed(t, modelAtConfirmChecks(t), tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(deps.ChecksDoneEvent{
		Result: deps.DependencyCheckResult{OK: true},
	})
	got := updated.(Model)

	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if got.deps.dialog.active() {
		t.Fatal("expected dialog closed")
	}
	if got.Status.Kind() != "success" {
		t.Fatalf("status kind = %q, want success", got.Status.Kind())
	}
}

func TestChecksFailedOpensRollbackDialog(t *testing.T) {
	m := feed(t, modelAtConfirmChecks(t), tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(deps.ChecksDoneEvent{
		Result: deps.DependencyCheckResult{
			Command: "go test ./...",
			Output:  "FAIL",
		},
	})
	got := updated.(Model)

	if got.deps.dialog.kind != dialogRollback || !got.deps.dialog.choiceYes {
		t.Fatalf("dialog = %+v, want rollback default Yes", got.deps.dialog)
	}
	if got.deps.dialog.inconclusive {
		t.Fatal("failed command should not be marked inconclusive")
	}
	if got.deps.dialog.checkResult == nil || got.deps.dialog.checkResult.Command != "go test ./..." {
		t.Fatalf("dialog check result = %+v", got.deps.dialog.checkResult)
	}
	if got.deps.cycle.CheckResult() == nil || got.deps.cycle.CheckResult().Command != "go test ./..." {
		t.Fatalf("check result = %+v", got.deps.cycle.CheckResult())
	}
}

func TestChecksInconclusiveOpensDistinctRollbackDialog(t *testing.T) {
	m := feed(t, modelAtConfirmChecks(t), tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(deps.ChecksDoneEvent{Err: errors.New("could not start go test")})
	got := updated.(Model)

	if got.deps.dialog.kind != dialogRollback || !got.deps.dialog.inconclusive {
		t.Fatalf("dialog = %+v, want inconclusive rollback", got.deps.dialog)
	}
	if !strings.Contains(got.Status.Text(), "could not start go test") {
		t.Fatalf("status = %q", got.Status.Text())
	}
}

func TestRollbackResultUpdatesState(t *testing.T) {
	m := feed(t, modelAtConfirmRollback(t), tea.KeyPressMsg{Code: tea.KeyEnter})
	dependencies := []deps.ModuleDependency{{
		Path: "github.com/example/lib", Version: "v1.0.0", Latest: "v1.1.0",
	}}

	updated, _ := m.Update(deps.RollbackDoneEvent{Dependencies: dependencies})
	got := updated.(Model)

	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if !reflect.DeepEqual(got.deps.dependencies, dependencies) {
		t.Fatalf("dependencies = %+v, want %+v", got.deps.dependencies, dependencies)
	}
	if got.Status.Kind() != "success" {
		t.Fatalf("status kind = %q, want success", got.Status.Kind())
	}
}

func TestSuccessfulCompensationReportsRestoredUpdateFailure(t *testing.T) {
	m := feed(t, modelAtConfirmApply(t), tea.KeyPressMsg{Code: tea.KeyEnter})
	m = feed(t, m, deps.ApplyUpdatesDoneEvent{
		Snapshot: &deps.DependencySnapshot{
			ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "old"},
		},
		Backup: &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"},
		Err:    errors.New("go get failed"),
	})

	updated, _ := m.Update(deps.CompensateDoneEvent{
		Dependencies: []deps.ModuleDependency{{Path: "github.com/example/lib", Version: "v1.0.0"}},
	})
	got := updated.(Model)

	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if !strings.Contains(got.Status.Text(), "go get failed") || !strings.Contains(got.Status.Text(), "reverted") {
		t.Fatalf("status = %q", got.Status.Text())
	}
}

func TestRecoveryRequiredShowsBackupLocation(t *testing.T) {
	m := feed(t, modelAtConfirmRollback(t), tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(deps.RollbackDoneEvent{Err: errors.New("disk full")})
	got := updated.(Model)

	if !strings.Contains(got.Status.Text(), "backup.json") ||
		!strings.Contains(got.Status.Text(), "/tmp/backup.json") {
		t.Fatalf("status = %q, want backup name and path", got.Status.Text())
	}
}

func TestCycleExecutionErrorResetsState(t *testing.T) {
	m := feed(t, modelAtConfirmApply(t), tea.KeyPressMsg{Code: tea.KeyEnter})

	updated, _ := m.Update(dependencyExecutionErrMsg{Err: errors.New("invalid executor intent")})
	got := updated.(Model)

	if got.deps.cycle.Phase() != deps.PhaseIdle {
		t.Fatalf("cycle phase = %s, want idle", got.deps.cycle.Phase())
	}
	if got.Status.Kind() != "error" {
		t.Fatalf("status kind = %q, want error", got.Status.Kind())
	}
}

// newVersionCacheTestModel builds a Model with a multi-version catalog
// (installed, uninstalled, active) so that Download/Switch/Delete
// handlers have a non-trivial starting state to mutate. The list and
// table are populated via replaceVersions (through seedVersions) so the
// model starts in a consistent state.
func newVersionCacheTestModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{
		{Version: "1.24.4", Filename: "go1.24.4.darwin-arm64.tar.gz", Installed: true, Active: true, Path: "/p/1.24.4"},
		{Version: "1.25.0", Filename: "go1.25.0.darwin-arm64.tar.gz", Installed: false},
		{Version: "1.26.0", Filename: "go1.26.0.darwin-arm64.tar.gz", Installed: true, Active: false, Path: "/p/1.26.0"},
	})
	return m
}

func TestVersionHandlersKeepCachesConsistent(t *testing.T) {
	for _, tt := range []struct {
		name      string
		key       rune
		version   string
		installed bool
		active    bool
		path      string
	}{
		{name: "refresh", key: 'r', version: "1.25.0", installed: true, active: true, path: "/new/1.25.0"},
		{name: "install", key: 'i', version: "1.25.0", installed: true, path: "/new/1.25.0"},
		{name: "activation", key: 'u', version: "1.26.0", installed: true, active: true, path: "/p/1.26.0"},
		{name: "deletion", key: 'd', version: "1.26.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newVersionCacheTestModel(t)
			calls := 0
			m = m.BindVersionOperations(VersionOperations{
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
					calls++
					return []utils.GoVersion{
						{Version: "1.25.0", Installed: true, Active: true, Path: "/new/1.25.0"},
						{Version: "1.27.0"},
					}, nil
				},
				Install: func(_ context.Context, r install.Request) (install.Result, error) {
					calls++
					return install.Result{Version: r.Version, Path: "/new/" + r.Version}, nil
				},
				Activate: func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
					calls++
					return lifecycle.ActivationResult{Version: version}, nil
				},
				Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
					calls++
					return lifecycle.DeletionResult{Version: version}, nil
				},
				ShimInPath: func() bool { return true },
			})
			m = applyFilter(t, m, tt.version)
			key := tt.key
			if key == 'd' {
				m = press(t, m, tea.KeyPressMsg{Code: 'd'})
				key = 'y'
			}
			updated, cmd := m.Update(tea.KeyPressMsg{Code: key})
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			v, found := m.projection.lookup(tt.version)
			if calls != 1 || !found || v.Installed != tt.installed || v.Active != tt.active || v.Path != tt.path {
				t.Fatalf("calls=%d, version=%+v, found=%v", calls, v, found)
			}
			assertVersionViewsConsistent(t, m)
		})
	}
}
