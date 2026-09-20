package model

import (
	"errors"
	"reflect"
	"strings"
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
	return a
}

func assertCatalogStatus(t *testing.T, got, want catalogStatus) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}

func installedSnapshot(version string) []utils.GoVersion {
	return []utils.GoVersion{{Version: version, Installed: true, Path: "/go/" + version}}
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
			op := a.startMutation(catalogMutationInstall, "1.30.0")
			copied := a
			_, status := copied.apply(tt.msg(op))
			assertCatalogStatus(t, status, tt.want)
			if a.activityState().kind != catalogActivityInstalling {
				t.Fatal("copy ended the original install")
			}
			_, status = a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/original"})
			assertCatalogStatus(t, status, catalogGlobalStatus("Successfully installed Go 1.30.0", "success"))
			if got, _ := a.lookup(op.version); !got.Installed || got.Path != "/original" {
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
			_, status = a.apply(tt.msg(id))
			assertCatalogStatus(t, status, tt.want)
			if a.activityState().kind != catalogActivityIdle {
				t.Fatal("original load did not finish")
			}
		})
	}
}

func TestCatalogProjectionAdapterRefreshDuringMutation(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  func(uint64) tea.Msg
	}{
		{
			name: "valid snapshot",
			msg: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}}}
			},
		},
		{
			name: "invalid snapshot",
			msg: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}, {Version: "1.30.0"}}}
			},
		},
		{
			name: "failure",
			msg:  func(id uint64) tea.Msg { return catalogLoadFailedMsg{RequestID: id, Err: errors.New("refresh failed")} },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			op := a.startMutation(catalogMutationInstall, "1.30.0")
			cmd, _ := a.apply(catalogRefreshMsg{})
			_, status := a.apply(tt.msg(catalogRequestID(t, cmd)))
			assertCatalogStatus(t, status, catalogStatus{})
			if activity := a.activityState(); activity.kind != catalogActivityInstalling || activity.version != op.version {
				t.Fatalf("refresh changed install activity: %+v", activity)
			}
			if other := a.startMutation(catalogMutationDeletion, op.version); other.id != 0 {
				t.Fatal("refresh allowed a second mutation")
			}
			_, status = a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"})
			assertCatalogStatus(t, status, catalogGlobalStatus("Successfully installed Go 1.30.0", "success"))
		})
	}
}

func TestCatalogProjectionAdapterMutationInvalidatesOlderLoad(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			load, _ := a.apply(catalogRefreshMsg{})
			id := catalogRequestID(t, load)
			op := a.startMutation(catalogMutationInstall, "1.30.0")
			a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/direct"})
			var msg tea.Msg = catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: op.version}}}
			if failure {
				msg = catalogLoadFailedMsg{RequestID: id, Err: errors.New("late failure")}
			}
			cmd, status := a.apply(msg)
			assertCatalogStatus(t, status, catalogStatus{})
			if cmd != nil {
				t.Fatal("stale response emitted a command")
			}
			if got, _ := a.lookup(op.version); !got.Installed || got.Path != "/direct" {
				t.Fatalf("stale response replaced installed version: %+v", got)
			}
		})
	}
}

func TestCatalogProjectionAdapterReconciliationPreservesWarnings(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		a := newCatalogProjectionAdapter(testTheme())
		op := a.startMutation(catalogMutationInstall, "1.30.0")
		warnings := []install.Warning{{Kind: install.WarningCleanup, Err: errors.New("original cleanup")}}
		cmd, status := a.apply(installSuccessMsg{
			OperationID: op.id, Version: op.version, Path: "/go/1.30.0", Warnings: warnings,
		})
		assertCatalogStatus(t, status, catalogGlobalStatus("Installed Go 1.30.0; verifying catalog...", "warning"))
		id := catalogRequestID(t, cmd)
		warnings[0] = install.Warning{Kind: install.WarningIntegrityUnavailable}
		_, status = a.apply(catalogLoadedMsg{RequestID: id, Versions: installedSnapshot(op.version)})
		if status.kind != "warning" || !strings.Contains(status.text, "original cleanup") {
			t.Fatalf("lost copied install warning: %+v", status)
		}
	})
	t.Run("activation", func(t *testing.T) {
		a := newCatalogProjectionAdapter(testTheme())
		op := a.startMutation(catalogMutationActivation, "1.30.0")
		warnings := []lifecycle.Warning{&lifecycle.CleanupWarning{
			Operation: state.OperationActivate, Path: "/old", Err: errors.New("original cleanup"),
		}}
		cmd, _ := a.apply(activationSuccessMsg{
			OperationID: op.id,
			Result:      lifecycle.ActivationResult{Version: op.version, Warnings: warnings},
			ShimInPath:  true,
		})
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
		complete func(catalogOperation) tea.Msg
		snapshot []utils.GoVersion
		want     catalogStatus
	}{
		{
			name: "install", kind: catalogMutationInstall, snapshot: installedSnapshot("1.30.0"),
			complete: func(op catalogOperation) tea.Msg {
				return installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"}
			},
			want: catalogGlobalStatus("Successfully installed Go 1.30.0", "success"),
		},
		{
			name: "activation", kind: catalogMutationActivation,
			snapshot: []utils.GoVersion{{Version: "1.30.0", Installed: true, Active: true, Path: "/go/1.30.0"}},
			complete: func(op catalogOperation) tea.Msg {
				return activationSuccessMsg{
					OperationID: op.id, Result: lifecycle.ActivationResult{Version: op.version}, ShimInPath: true,
				}
			},
			want: catalogTabStatus("Switched to Go 1.30.0! Run 'go version' to verify.", "success"),
		},
		{
			name: "delete", kind: catalogMutationDeletion, snapshot: []utils.GoVersion{},
			complete: func(op catalogOperation) tea.Msg {
				return deletionSuccessMsg{OperationID: op.id, Result: lifecycle.DeletionResult{Version: op.version}}
			},
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
					if mode == "unchanged" {
						versions = tt.snapshot
						if tt.kind == catalogMutationDeletion {
							versions = []utils.GoVersion{{Version: "1.30.0"}}
						}
					}
					if mode == "reconciled" {
						versions = []utils.GoVersion{}
					}
					a := newCatalogProjectionAdapterTestFixture(t, versions)
					op := a.startMutation(tt.kind, "1.30.0")
					cmd, status := a.apply(tt.complete(op))
					if mode == "reconciled" {
						if status.kind != "warning" || !strings.Contains(status.text, "verifying catalog...") {
							t.Fatalf("verification status = %+v", status)
						}
						id := catalogRequestID(t, cmd)
						if refresh, effect := a.apply(catalogRefreshMsg{}); refresh != nil || effect.scope != catalogStatusUntouched {
							t.Fatal("refresh interrupted reconciliation")
						}
						if other := a.startMutation(tt.kind, "1.29.0"); other.id != 0 {
							t.Fatal("second mutation interrupted reconciliation")
						}
						_, status = a.apply(catalogLoadedMsg{RequestID: id, Versions: tt.snapshot})
					}
					assertCatalogStatus(t, status, tt.want)
					if a.activityState().kind != catalogActivityIdle || a.activeOperationID() != 0 {
						t.Fatal("completed operation remained active")
					}
				})
			}
		})
	}
}

func TestCatalogProjectionAdapterCommittedWarningPreservesProjection(t *testing.T) {
	for _, tt := range []struct {
		name     string
		kind     catalogMutationKind
		versions []utils.GoVersion
		complete func(catalogOperation) tea.Msg
	}{
		{
			name: "invalid install path", kind: catalogMutationInstall,
			versions: []utils.GoVersion{{Version: "1.30.0"}},
			complete: func(op catalogOperation) tea.Msg {
				return installSuccessMsg{OperationID: op.id, Version: op.version}
			},
		},
		{
			name: "activation of uninstalled version", kind: catalogMutationActivation,
			versions: []utils.GoVersion{{Version: "1.30.0"}},
			complete: func(op catalogOperation) tea.Msg {
				return activationSuccessMsg{OperationID: op.id, Result: lifecycle.ActivationResult{Version: op.version}}
			},
		},
		{
			name: "deletion of active version", kind: catalogMutationDeletion,
			versions: []utils.GoVersion{{Version: "1.30.0", Installed: true, Active: true, Path: "/go/1.30.0"}},
			complete: func(op catalogOperation) tea.Msg {
				return deletionSuccessMsg{OperationID: op.id, Result: lifecycle.DeletionResult{Version: op.version}}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, tt.versions)
			before := a.projection()
			op := a.startMutation(tt.kind, "1.30.0")
			cmd, status := a.apply(tt.complete(op))
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
			a := newCatalogProjectionAdapter(testTheme())
			op := a.startMutation(catalogMutationInstall, "1.30.0")
			verify, _ := a.apply(installSuccessMsg{
				OperationID: op.id, Version: op.version, Path: "/original",
				Warnings: []install.Warning{{Kind: install.WarningCleanup, Err: errors.New("original")}},
			})
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
			a := newCatalogProjectionAdapter(testTheme())
			op := a.startMutation(catalogMutationInstall, "1.30.0")
			if _, ok := a.activityState().installProgress(); ok {
				t.Fatal("progress exists before first measurement")
			}
			measurement := install.Progress{Version: op.version, Stage: install.StageDownload, BytesReceived: 10}
			if !a.applyProgress(op.id, measurement) || a.applyProgress(op.id+1, install.Progress{Stage: install.StageVerify}) {
				t.Fatal("progress correlation failed")
			}
			switch action {
			case "refresh":
				a.apply(catalogRefreshMsg{})
			case "failure":
				a.apply(installFailureMsg{OperationID: op.id, Version: op.version, Err: errors.New("failed")})
			case "reconcile":
				a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"})
			}
			got, ok := a.activityState().installProgress()
			if action == "refresh" {
				if !ok || got != measurement {
					t.Fatalf("refresh lost progress: %+v, ok=%v", got, ok)
				}
			} else if ok {
				t.Fatalf("progress outlived install: %+v", got)
			}
		})
	}
}
