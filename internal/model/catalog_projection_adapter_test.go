package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/state"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func newCatalogProjectionAdapterTestFixture(t *testing.T, versions []utils.GoVersion) catalogProjectionAdapter {
	t.Helper()
	a := newCatalogProjectionAdapter(testTheme())
	cmd, _ := a.apply(catalogRefreshMsg{})
	_, status := a.apply(catalogLoadedMsg{RequestID: catalogRequestID(t, cmd), Versions: versions})
	if status.scope != catalogStatusUntouched {
		t.Fatalf("seeding catalog: %+v", status)
	}
	a.bindOperations(catalogTestOperations())
	return a
}

func assertCatalogStatus(t *testing.T, got, want catalogStatus) {
	t.Helper()
	if got.scope != want.scope || got.text != want.text || got.kind != want.kind {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}

func installedSnapshot(version string) []utils.GoVersion {
	return []utils.GoVersion{{Version: version, Installed: true, Path: "/go/" + version}}
}

func catalogTestOperations() VersionOperations {
	return VersionOperations{
		Install: func(_ context.Context, request install.Request) (install.Result, error) {
			return install.Result{Version: request.Version, Path: "/go/" + request.Version}, nil
		},
		Activate: func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
			return lifecycle.ActivationResult{Version: version}, nil
		},
		Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
			return lifecycle.DeletionResult{Version: version}, nil
		},
		ShimInPath: func() bool { return true },
	}
}

// The correlation identity comes from the dispatched command, never the registry.
func admitCatalogTestMutation(t *testing.T, a *catalogProjectionAdapter, kind catalogMutationKind, version string) (catalogOperation, tea.Msg) {
	t.Helper()
	action := catalogActionInstall
	switch kind {
	case catalogMutationActivation:
		action = catalogActionActivate
	case catalogMutationDeletion:
		_, status := a.apply(catalogActionMsg{kind: catalogActionRequestDelete, version: version, tab: AvailableTab})
		if status.confirmDeleteVersion != version {
			t.Fatalf("delete request was not admitted: %+v", status)
		}
		action = catalogActionConfirmDelete
	}
	cmd, status := a.apply(catalogActionMsg{kind: action, version: version, tab: AvailableTab})
	if cmd == nil {
		t.Fatalf("action was not dispatched: %+v", status)
	}
	msg := cmd()
	op := catalogOperation{kind: kind, version: version}
	switch msg := msg.(type) {
	case installSuccessMsg:
		op.id = msg.OperationID
	case activationSuccessMsg:
		op.id = msg.OperationID
	case deletionSuccessMsg:
		op.id = msg.OperationID
	default:
		t.Fatalf("mutation command returned %T", msg)
	}
	return op, msg
}

func refreshCatalogTestSnapshot(t *testing.T, a *catalogProjectionAdapter, versions []utils.GoVersion) {
	t.Helper()
	cmd, _ := a.apply(catalogRefreshMsg{})
	_, status := a.apply(catalogLoadedMsg{RequestID: catalogRequestID(t, cmd), Versions: versions})
	if status.scope != catalogStatusUntouched {
		t.Fatalf("refreshing fixture: %+v", status)
	}
}

func TestCatalogProjectionAdapterMutationRegistryValueCopyIsolation(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  func(catalogOperation) tea.Msg
		want catalogStatus
	}{
		{
			name: "completion",
			msg: func(op catalogOperation) tea.Msg {
				return installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/copy"}
			},
			want: catalogGlobalStatus("Successfully installed Go 1.30.0", "success"),
		},
		{
			name: "failure",
			msg: func(op catalogOperation) tea.Msg {
				return installFailureMsg{OperationID: op.id, Version: op.version, Err: errors.New("copy failed")}
			},
			want: catalogGlobalStatus("Failed to install Go 1.30.0: copy failed", "error"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			op, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
			copied := a
			_, status := copied.apply(tt.msg(op))
			assertCatalogStatus(t, status, tt.want)
			if a.activityState().kind != catalogActivityInstalling {
				t.Fatal("copy ended the original install")
			}
			if got, _ := a.lookup(op.version); got.Installed {
				t.Fatal("copy published into the original catalog")
			}
			_, status = a.apply(completion)
			assertCatalogStatus(t, status, catalogGlobalStatus("Successfully installed Go 1.30.0", "success"))
			if got, _ := a.lookup(op.version); !got.Installed || got.Path != "/go/1.30.0" {
				t.Fatalf("original version = %+v", got)
			}
		})
	}
}

func TestCatalogProjectionAdapterLoadRegistryValueCopyIsolation(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  func(uint64) tea.Msg
		want catalogStatus
	}{
		{
			name: "success",
			msg: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}}}
			},
		},
		{
			name: "failure",
			msg:  func(id uint64) tea.Msg { return catalogLoadFailedMsg{RequestID: id, Err: errors.New("load failed")} },
			want: catalogGlobalStatus("load failed", "error"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapter(testTheme())
			cmd, _ := a.apply(catalogRefreshMsg{})
			id := catalogRequestID(t, cmd)
			copied := a
			_, status := copied.apply(tt.msg(id))
			assertCatalogStatus(t, status, tt.want)
			if a.activityState().kind != catalogActivityLoading {
				t.Fatal("copy ended the original load")
			}
			if _, ok := a.lookup("1.30.0"); ok {
				t.Fatal("copy published into the original catalog")
			}
			_, status = a.apply(tt.msg(id))
			assertCatalogStatus(t, status, tt.want)
			if a.activityState().kind != catalogActivityIdle {
				t.Fatal("original load did not finish")
			}
		})
	}
}

func TestCatalogFlowRefreshDuringMutation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		versions []utils.GoVersion
		err      error
	}{
		{name: "valid snapshot", versions: []utils.GoVersion{{Version: "1.25.0"}}},
		{name: "duplicate-version invalid snapshot", versions: []utils.GoVersion{{Version: "1.25.0"}, {Version: "1.25.0"}}},
		{name: "loader error", err: errors.New("refresh failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := applyFilter(t, newVersionCacheTestModel(t), "1.25.0")
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			calls := make(chan string, 4)
			m = m.BindVersionOperations(VersionOperations{
				InstallWithProgress: func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
					calls <- "install"
					reporter.Report(install.Progress{Version: request.Version, Stage: install.StageDownload, BytesReceived: 25, BytesTotal: 100})
					<-release
					return install.Result{Version: request.Version, Path: "/installed/1.25.0"}, nil
				},
				Activate: func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
					calls <- "activate"
					return lifecycle.ActivationResult{Version: version}, nil
				},
				Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
					calls <- "delete"
					return lifecycle.DeletionResult{Version: version}, nil
				},
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) { return tt.versions, tt.err },
			})
			updated, installCmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
			m = updated.(Model)
			progress, ok := installCmd().(installProgressMsg)
			if !ok {
				t.Fatal("installer did not report progress")
			}
			updated, continuation := m.Update(progress)
			m = updated.(Model)
			if continuation == nil {
				t.Fatal("progress lost continuation")
			}
			updated, refresh := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			m = runCatalogTestCmd(t, updated.(Model), refresh)
			if got, ok := m.projection.activityState().installProgress(); !ok || got != progress.progress {
				t.Fatalf("refresh lost progress: %+v, present=%v", got, ok)
			}
			for _, key := range []rune{'i', 'u', 'd'} {
				updated, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
				m = updated.(Model)
				if cmd != nil || m.ConfirmingDelete {
					t.Fatalf("%c dispatched while installing", key)
				}
			}
			if got := <-calls; got != "install" {
				t.Fatalf("first operation=%q", got)
			}
			select {
			case got := <-calls:
				t.Fatalf("unexpected operation %q", got)
			default:
			}
			unblock()
			m = runCatalogTestCmd(t, m, continuation)
			if got, _ := m.projection.lookup("1.25.0"); !got.Installed || got.Path != "/installed/1.25.0" {
				t.Fatalf("original completion was lost: %+v", got)
			}
			assertVersionViewsConsistent(t, m)
		})
	}
	t.Run("manual refreshes can supersede each other during mutation", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
		loads := 0
		operations := catalogTestOperations()
		operations.LoadCatalog = func(context.Context) ([]utils.GoVersion, error) {
			loads++
			extra := "1.29.0"
			if loads == 2 {
				extra = "1.31.0"
			}
			return []utils.GoVersion{{Version: "1.30.0"}, {Version: extra}}, nil
		}
		a.bindOperations(operations)
		_, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
		first, _ := a.apply(catalogRefreshMsg{manual: true})
		if first == nil {
			t.Fatal("first refresh was blocked")
		}
		older := first()
		second, _ := a.apply(catalogRefreshMsg{manual: true})
		if second == nil {
			t.Fatal("second refresh was blanket-blocked")
		}
		latest := second()
		if cmd, status := a.apply(older); cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatal("superseded refresh had effects")
		}
		a.apply(latest)
		_, status := a.apply(completion)
		if loads != 2 || status.kind != "success" {
			t.Fatalf("loads=%d completion=%+v", loads, status)
		}
		if _, ok := a.lookup("1.31.0"); !ok {
			t.Fatal("latest refresh was not accepted")
		}
		if _, ok := a.lookup("1.29.0"); ok {
			t.Fatal("superseded refresh was accepted")
		}
		if got, _ := a.lookup("1.30.0"); !got.Installed {
			t.Fatal("refresh supersession lost the original install")
		}
	})
}

func TestCatalogProjectionAdapterMutationInvalidatesOlderLoad(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			op, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
			load, _ := a.apply(catalogRefreshMsg{})
			id := catalogRequestID(t, load)
			a.apply(completion)
			var msg tea.Msg = catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: op.version}}}
			if failure {
				msg = catalogLoadFailedMsg{RequestID: id, Err: errors.New("late failure")}
			}
			cmd, status := a.apply(msg)
			assertCatalogStatus(t, status, catalogStatus{})
			if cmd != nil {
				t.Fatal("stale response emitted a command")
			}
			if got, _ := a.lookup(op.version); !got.Installed || got.Path != "/go/1.30.0" {
				t.Fatalf("stale response replaced installed version: %+v", got)
			}
		})
	}
}

func TestCatalogProjectionAdapterReconciliationPreservesWarnings(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
		warnings := []install.Warning{{Kind: install.WarningCleanup, Err: errors.New("original cleanup")}}
		operations := catalogTestOperations()
		operations.Install = func(_ context.Context, request install.Request) (install.Result, error) {
			return install.Result{Version: request.Version, Path: "/go/1.30.0", Warnings: warnings}, nil
		}
		a.bindOperations(operations)
		op, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
		refreshCatalogTestSnapshot(t, &a, nil)
		cmd, status := a.apply(completion)
		assertCatalogStatus(t, status, catalogGlobalStatus("Installed Go 1.30.0; verifying catalog...", "warning"))
		id := catalogRequestID(t, cmd)
		warnings[0] = install.Warning{Kind: install.WarningIntegrityUnavailable}
		_, status = a.apply(catalogLoadedMsg{RequestID: id, Versions: installedSnapshot(op.version)})
		if status.kind != "warning" || !strings.Contains(status.text, "original cleanup") {
			t.Fatalf("lost copied install warning: %+v", status)
		}
	})
	t.Run("activation", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, installedSnapshot("1.30.0"))
		warnings := []lifecycle.Warning{&lifecycle.CleanupWarning{
			Operation: state.OperationActivate, Path: "/old", Err: errors.New("original cleanup"),
		}}
		operations := catalogTestOperations()
		operations.Activate = func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
			return lifecycle.ActivationResult{Version: version, Warnings: warnings}, nil
		}
		a.bindOperations(operations)
		op, completion := admitCatalogTestMutation(t, &a, catalogMutationActivation, "1.30.0")
		refreshCatalogTestSnapshot(t, &a, nil)
		cmd, _ := a.apply(completion)
		id := catalogRequestID(t, cmd)
		warnings[0] = &lifecycle.CleanupWarning{Err: errors.New("replacement")}
		versions := installedSnapshot(op.version)
		versions[0].Active = true
		_, status := a.apply(catalogLoadedMsg{RequestID: id, Versions: versions})
		if status.scope != catalogStatusGlobal || status.kind != "warning" || !strings.Contains(status.text, "original cleanup") {
			t.Fatalf("lost copied lifecycle warning: %+v", status)
		}
	})
}

func TestCatalogProjectionAdapterCompletionAndReconciliation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     catalogMutationKind
		snapshot []utils.GoVersion
		want     catalogStatus
	}{
		{
			name: "install", kind: catalogMutationInstall, snapshot: installedSnapshot("1.30.0"),
			want: catalogGlobalStatus("Successfully installed Go 1.30.0", "success"),
		},
		{
			name: "activation", kind: catalogMutationActivation,
			snapshot: []utils.GoVersion{{Version: "1.30.0", Installed: true, Active: true, Path: "/go/1.30.0"}},
			want:     catalogTabStatus("Switched to Go 1.30.0! Run 'go version' to verify.", "success"),
		},
		{
			name: "delete", kind: catalogMutationDeletion, snapshot: []utils.GoVersion{},
			want: catalogGlobalStatus("Successfully deleted Go 1.30.0", "success"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"direct", "unchanged", "reconciled"} {
				t.Run(mode, func(t *testing.T) {
					versions := []utils.GoVersion{{Version: "1.30.0"}}
					if tt.kind != catalogMutationInstall {
						versions = installedSnapshot("1.30.0")
					}
					a := newCatalogProjectionAdapterTestFixture(t, versions)
					_, completion := admitCatalogTestMutation(t, &a, tt.kind, "1.30.0")
					switch mode {
					case "unchanged":
						snapshot := tt.snapshot
						if tt.kind == catalogMutationDeletion {
							snapshot = []utils.GoVersion{{Version: "1.30.0"}}
						}
						refreshCatalogTestSnapshot(t, &a, snapshot)
					case "reconciled":
						refreshCatalogTestSnapshot(t, &a, nil)
					}
					cmd, status := a.apply(completion)
					if mode == "reconciled" {
						if status.kind != "warning" || !strings.Contains(status.text, "verifying catalog...") {
							t.Fatalf("verification status = %+v", status)
						}
						id := catalogRequestID(t, cmd)
						if refresh, effect := a.apply(catalogRefreshMsg{}); refresh != nil || effect.scope != catalogStatusUntouched {
							t.Fatal("refresh interrupted reconciliation")
						}
						if other, effect := a.apply(catalogActionMsg{kind: catalogActionInstall, version: "1.29.0", tab: AvailableTab}); other != nil || effect.scope != catalogStatusUntouched {
							t.Fatal("second mutation interrupted reconciliation")
						}
						_, status = a.apply(catalogLoadedMsg{RequestID: id, Versions: tt.snapshot})
					}
					assertCatalogStatus(t, status, tt.want)
					model := Model{projection: a, theme: testTheme()}
					assertVersionViewsConsistent(t, model)
					refreshCatalogTestSnapshot(t, &a, []utils.GoVersion{{Version: "1.31.0"}})
					_, next := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.31.0")
					a.apply(next)
					if got, _ := a.lookup("1.31.0"); !got.Installed {
						t.Fatal("next install did not complete")
					}
				})
			}
		})
	}
}

func catalogTestMutationResult(t *testing.T, a *catalogProjectionAdapter, cmd tea.Cmd) tea.Msg {
	t.Helper()
	for {
		if cmd == nil {
			t.Fatal("mutation lost continuation")
		}
		msg := cmd()
		switch msg.(type) {
		case installProgressMsg, installProgressPollMsg:
			cmd, _ = a.apply(msg)
		default:
			return msg
		}
	}
}

func TestCatalogProjectionAdapterCommittedWarningPreservesProjection(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     catalogMutationKind
		versions []utils.GoVersion
	}{
		{
			name: "invalid install path", kind: catalogMutationInstall,
			versions: []utils.GoVersion{{Version: "1.30.0"}},
		},
		{
			name: "activation of uninstalled version", kind: catalogMutationActivation,
			versions: []utils.GoVersion{{Version: "1.30.0"}},
		},
		{
			name: "deletion of active version", kind: catalogMutationDeletion,
			versions: []utils.GoVersion{{Version: "1.30.0", Installed: true, Active: true, Path: "/go/1.30.0"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			versions := []utils.GoVersion{{Version: "1.30.0"}}
			if tt.kind != catalogMutationInstall {
				versions = installedSnapshot("1.30.0")
			}
			a := newCatalogProjectionAdapterTestFixture(t, versions)
			if tt.kind == catalogMutationInstall {
				operations := catalogTestOperations()
				operations.Install = func(_ context.Context, request install.Request) (install.Result, error) {
					return install.Result{Version: request.Version}, nil
				}
				a.bindOperations(operations)
			}
			_, completion := admitCatalogTestMutation(t, &a, tt.kind, "1.30.0")
			refreshCatalogTestSnapshot(t, &a, tt.versions)
			before := a.projection()
			cmd, status := a.apply(completion)
			if cmd != nil || status.kind != "warning" || !strings.Contains(status.text, "Refresh to synchronize.") {
				t.Fatalf("committed warning = %+v, command present=%v", status, cmd != nil)
			}
			if !reflect.DeepEqual(a.projection(), before) || a.activityState().kind != catalogActivityIdle {
				t.Fatal("projection changed or mutation remained active after warning")
			}
		})
	}
}

func TestCatalogProjectionAdapterStaleCompletionPreservesReconciliation(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  func(catalogOperation) tea.Msg
	}{
		{
			name: "wrong kind",
			msg: func(op catalogOperation) tea.Msg {
				return activationSuccessMsg{OperationID: op.id, Result: lifecycle.ActivationResult{Version: op.version}}
			},
		},
		{
			name: "wrong id",
			msg: func(op catalogOperation) tea.Msg {
				return installSuccessMsg{OperationID: op.id + 1, Version: op.version, Path: "/late"}
			},
		},
		{
			name: "wrong version",
			msg: func(op catalogOperation) tea.Msg {
				return installSuccessMsg{OperationID: op.id, Version: "1.29.0", Path: "/late"}
			},
		},
		{
			name: "late failure",
			msg: func(op catalogOperation) tea.Msg {
				return installFailureMsg{OperationID: op.id + 1, Version: op.version, Err: errors.New("late")}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			operations := catalogTestOperations()
			operations.Install = func(_ context.Context, request install.Request) (install.Result, error) {
				return install.Result{Version: request.Version, Path: "/original", Warnings: []install.Warning{{Kind: install.WarningCleanup, Err: errors.New("original")}}}, nil
			}
			a.bindOperations(operations)
			op, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
			refreshCatalogTestSnapshot(t, &a, nil)
			verify, _ := a.apply(completion)
			id := catalogRequestID(t, verify)
			cmd, status := a.apply(tt.msg(op))
			assertCatalogStatus(t, status, catalogStatus{})
			if cmd != nil {
				t.Fatal("stale completion emitted a command")
			}
			_, status = a.apply(catalogLoadedMsg{RequestID: id, Versions: installedSnapshot(op.version)})
			if status.kind != "warning" || !strings.Contains(status.text, "original") {
				t.Fatalf("stale completion overwrote receipt: %+v", status)
			}
		})
	}
}

func TestCatalogProjectionAdapterProgressLifetime(t *testing.T) {
	for _, action := range []string{"refresh", "failure", "reconcile"} {
		t.Run(action, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			operations := catalogTestOperations()
			operations.InstallWithProgress = func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
				reporter.Report(install.Progress{Version: request.Version, Stage: install.StageDownload, BytesReceived: 10})
				<-release
				if action == "failure" {
					return install.Result{}, errors.New("failed")
				}
				return install.Result{Version: request.Version, Path: "/go/1.30.0"}, nil
			}
			a.bindOperations(operations)
			cmd, _ := a.apply(catalogActionMsg{kind: catalogActionInstall, version: "1.30.0", tab: AvailableTab})
			if _, ok := a.activityState().installProgress(); ok {
				t.Fatal("progress exists before first measurement")
			}
			msg, ok := cmd().(installProgressMsg)
			if !ok {
				t.Fatal("missing progress message")
			}
			continuation, _ := a.apply(msg)
			if continuation == nil {
				t.Fatal("missing progress continuation")
			}
			if stale, status := a.apply(installProgressMsg{operationID: msg.operationID + 1, progress: install.Progress{Stage: install.StageVerify}, session: msg.session}); stale != nil || status.scope != catalogStatusUntouched {
				t.Fatal("stale progress continued")
			}
			if action == "refresh" {
				refreshCatalogTestSnapshot(t, &a, []utils.GoVersion{{Version: "1.30.0"}})
				if got, ok := a.activityState().installProgress(); !ok || got != msg.progress {
					t.Fatalf("refresh lost progress: %+v", got)
				}
			} else if action == "reconcile" {
				refreshCatalogTestSnapshot(t, &a, nil)
			}
			unblock()
			outcome := catalogTestMutationResult(t, &a, continuation)
			next, _ := a.apply(outcome)
			if got, ok := a.activityState().installProgress(); ok {
				t.Fatalf("progress outlived install: %+v", got)
			}
			if action == "reconcile" {
				a.apply(catalogLoadedMsg{RequestID: catalogRequestID(t, next), Versions: installedSnapshot("1.30.0")})
			}
		})
	}
}

func TestCatalogFlowDeleteRevalidatesConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot []utils.GoVersion
		kind     string
		text     string
		calls    int
	}{
		{name: "removed", snapshot: installedSnapshot("1.24.4"), kind: "error", text: "no longer available"},
		{name: "uninstalled", snapshot: []utils.GoVersion{{Version: "1.26.0"}}, kind: "info", text: "no longer installed"},
		{name: "became active", snapshot: []utils.GoVersion{{Version: "1.26.0", Installed: true, Active: true, Path: "/p/1.26.0"}}, kind: "error", text: "Cannot delete active"},
		{name: "still deletable", snapshot: installedSnapshot("1.26.0"), kind: "success", text: "Successfully deleted", calls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := applyFilter(t, newVersionCacheTestModel(t), "1.26.0")
			deletes := 0
			m = m.BindVersionOperations(VersionOperations{
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) { return tt.snapshot, nil },
				Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
					deletes++
					if version != "1.26.0" {
						t.Errorf("deleted identity=%q", version)
					}
					return lifecycle.DeletionResult{Version: version}, nil
				},
			})
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
			m = updated.(Model)
			if cmd != nil || !m.ConfirmingDelete || m.DeleteVersion != "1.26.0" || m.Status.Kind() != "warning" {
				t.Fatalf("delete request: confirmation=%v target=%q status=%q", m.ConfirmingDelete, m.DeleteVersion, m.Status.Text())
			}
			updated, refresh := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			m = updated.(Model)
			if refresh == nil {
				t.Fatal("confirmation blocked refresh")
			}
			snapshot := refresh()
			updated, cmd = m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
			m = updated.(Model)
			if cmd != nil || !m.ConfirmingDelete || m.DeleteVersion != "1.26.0" || deletes != 0 {
				t.Fatal("Y dispatched or closed confirmation while refresh was pending")
			}
			updated, refilter := m.Update(snapshot)
			m = runCatalogTestCmd(t, updated.(Model), refilter)
			updated, cmd = m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if m.ConfirmingDelete || m.DeleteVersion != "" || deletes != tt.calls {
				t.Fatalf("after confirmation: open=%v target=%q calls=%d", m.ConfirmingDelete, m.DeleteVersion, deletes)
			}
			wantScope := statusScopeTab
			if tt.calls != 0 {
				wantScope = statusScopeGlobal
			}
			if m.Status.Kind() != tt.kind || !strings.Contains(m.Status.Text(), tt.text) || m.Status.Scope() != wantScope {
				t.Fatalf("confirmation status=%q kind=%q scope=%v", m.Status.Text(), m.Status.Kind(), m.Status.Scope())
			}
			if tt.calls != 0 {
				if v, _ := m.projection.lookup("1.26.0"); v.Installed {
					t.Fatal("confirmed deletion did not publish")
				}
			}
			assertVersionViewsConsistent(t, m)
		})
	}
	for _, tt := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "cancel", key: tea.KeyPressMsg{Code: 'N', Text: "N"}},
		{name: "tab switch", key: tea.KeyPressMsg{Code: tea.KeyTab}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := applyFilter(t, newVersionCacheTestModel(t), "1.26.0")
			deletes := 0
			m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
				deletes++
				return lifecycle.DeletionResult{Version: version}, nil
			}})
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
			m = updated.(Model)
			if cmd != nil || !m.ConfirmingDelete {
				t.Fatal("missing confirmation")
			}
			updated, cmd = m.Update(tt.key)
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			updated, cmd = m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if m.ConfirmingDelete || m.DeleteVersion != "" || deletes != 0 {
				t.Fatal("cancelled confirmation dispatched deletion")
			}
			if v, _ := m.projection.lookup("1.26.0"); !v.Installed {
				t.Fatal("cancelled deletion changed catalog")
			}
			assertVersionViewsConsistent(t, m)
		})
	}
}
