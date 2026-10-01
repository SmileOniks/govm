package model

// Install flow tests drive key admission and execute the returned commands
// with controlled adapters, without touching SDK files or the network.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestCatalogFlowDuplicateInstall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newVersionCacheTestModel(t)
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		t.Cleanup(unblock)
		calls := 0
		m = m.BindVersionOperations(VersionOperations{
			InstallWithProgress: func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
				calls++
				reporter.Report(install.Progress{
					Version: request.Version, Stage: install.StageDownload,
					BytesReceived: 25, BytesTotal: 100,
				})
				<-release
				return install.Result{Version: request.Version, Path: "/installed/" + request.Version}, nil
			},
		})
		m = applyFilter(t, m, "1.25.0")
		updated, first := m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if first == nil {
			t.Fatal("first install did not dispatch")
		}
		updated, duplicate := m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if duplicate != nil {
			t.Fatal("duplicate install dispatched before the first command ran")
		}
		progress, ok := first().(installProgressMsg)
		if !ok || progress.progress.Version != "1.25.0" {
			t.Fatalf("first install progress = %+v, valid=%v", progress, ok)
		}
		updated, continuation := m.Update(progress)
		m = updated.(Model)
		updated, duplicate = m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if duplicate != nil {
			t.Fatal("duplicate install dispatched after the first measurement")
		}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("installer calls = %d, want 1", calls)
		}
		unblock()
		synctest.Wait()
		if continuation == nil {
			t.Fatal("accepted progress did not schedule continuation")
		}
		m = runCatalogTestCmd(t, m, continuation)
		got, found := m.projection.lookup("1.25.0")
		if !found || !got.Installed || got.Path != "/installed/1.25.0" {
			t.Fatalf("installed version = %+v, found=%v", got, found)
		}
		if calls != 1 {
			t.Fatalf("completed installer calls = %d, want 1", calls)
		}
		assertVersionViewsConsistent(t, m)
	})
}

func TestInstallPlainInstallerFallback(t *testing.T) {
	m := newVersionCacheTestModel(t)
	var installed string
	m = m.BindVersionOperations(VersionOperations{
		Install: func(_ context.Context, request install.Request) (install.Result, error) {
			installed = request.Version
			return install.Result{Version: request.Version, Path: "/p/" + request.Version}, nil
		},
	})
	m = applyFilter(t, m, "1.25.0")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if installed != "1.25.0" {
		t.Fatalf("plain installer target = %q, want the selected identity", installed)
	}
	if got, ok := m.projection.lookup("1.25.0"); !ok || !got.Installed || got.Path != "/p/1.25.0" {
		t.Fatalf("installed version = %+v, found=%v", got, ok)
	}
	assertVersionViewsConsistent(t, m)
}

func TestInstallFailureAllowsRetry(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installer installFunc
		wantError string
		wantStage string
	}{
		{name: "missing installer", wantError: "no installer configured"},
		{
			name: "installer error",
			installer: func(context.Context, install.Request) (install.Result, error) {
				return install.Result{}, &install.Error{Stage: install.StageExtract, Err: errors.New("corrupt archive")}
			},
			wantError: "corrupt archive", wantStage: "extraction",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newVersionCacheTestModel(t)
			m = m.BindVersionOperations(VersionOperations{Install: tt.installer})
			m = applyFilter(t, m, "1.25.0")
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
			if cmd == nil {
				t.Fatal("admitted install did not return a command")
			}
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if m.Status.Kind() != "error" || !strings.Contains(m.Status.Text(), tt.wantError) || !strings.Contains(m.Status.Text(), tt.wantStage) {
				t.Fatalf("failure status = %q/%q, want error containing %q and %q", m.Status.Text(), m.Status.Kind(), tt.wantError, tt.wantStage)
			}
			if got, found := m.projection.lookup("1.25.0"); !found || got.Installed {
				t.Fatalf("failed install published an installed target: %+v, found=%v", got, found)
			}
			assertVersionViewsConsistent(t, m)
			calls := 0
			m = m.BindVersionOperations(VersionOperations{
				Install: func(_ context.Context, request install.Request) (install.Result, error) {
					calls++
					return install.Result{Version: request.Version, Path: "/retry/" + request.Version}, nil
				},
			})
			updated, cmd = m.Update(tea.KeyPressMsg{Code: 'i'})
			if cmd == nil {
				t.Fatal("failure left the next install blocked")
			}
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if got, found := m.projection.lookup("1.25.0"); calls != 1 || !found || !got.Installed || got.Path != "/retry/1.25.0" {
				t.Fatalf("retry result = %+v, found=%v, installer calls=%d", got, found, calls)
			}
			if m.Status.Kind() != "success" {
				t.Fatalf("retry status = %q/%q, want success", m.Status.Text(), m.Status.Kind())
			}
			assertVersionViewsConsistent(t, m)
		})
	}
}

func TestInstallSuccessRefreshesDiskUsage(t *testing.T) {
	m := newVersionCacheTestModel(t)
	calls := 0
	m = m.BindVersionOperations(VersionOperations{
		Install: func(_ context.Context, request install.Request) (install.Result, error) {
			return install.Result{Version: request.Version, Path: "/new/1.25"}, nil
		},
		DiskUsage: func(context.Context) (prune.Summary, error) {
			calls++
			return prune.Summary{VersionBytes: map[string]int64{"1.25.0": 4096}}, nil
		},
	})
	m = applyFilter(t, m, "1.25.0")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if calls != 1 {
		t.Fatalf("disk usage calls = %d, want 1", calls)
	}
	assertVersionViewsConsistent(t, m)
	for _, row := range m.projection.installedModel().Rows() {
		if row[0] == "1.25.0" {
			if row[2] != "4.0 KiB" {
				t.Fatalf("size column = %q, want 4.0 KiB", row[2])
			}
			return
		}
	}
	t.Fatal("installed version 1.25.0 not found")
}

func TestInstallSuccess_WarningsProduceWarningStatus(t *testing.T) {
	m := newVersionCacheTestModel(t)
	m = m.BindVersionOperations(VersionOperations{
		Install: func(_ context.Context, request install.Request) (install.Result, error) {
			return install.Result{Version: request.Version, Path: "/new/1.25", Warnings: []install.Warning{{Kind: install.WarningIntegrityUnavailable}}}, nil
		},
	})
	m = applyFilter(t, m, "1.25.0")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if m.Status.Kind() != "warning" || !strings.Contains(m.Status.Text(), "with warnings") {
		t.Fatalf("status = %q/%q, want committed install warning", m.Status.Text(), m.Status.Kind())
	}
	if got, found := m.projection.lookup("1.25.0"); !found || !got.Installed || got.Path != "/new/1.25" {
		t.Fatalf("warning install did not publish its result: %+v, found=%v", got, found)
	}
	assertVersionViewsConsistent(t, m)
}

func TestUnknownCompletionReconciliationPreservesWarnings(t *testing.T) {
	m := newVersionCacheTestModel(t)
	snapshot := projectionVersions(m)
	calls := 0
	m = m.BindVersionOperations(VersionOperations{
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) { return snapshot, nil },
		Install: func(_ context.Context, request install.Request) (install.Result, error) {
			calls++
			return install.Result{
				Version: request.Version, Path: "/p/" + request.Version,
				Warnings: []install.Warning{{Kind: install.WarningCleanup, Err: errors.New("temp file busy")}},
			}, nil
		},
	})
	m = applyFilter(t, m, "1.25.0")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("install did not dispatch")
	}
	completion := cmd()
	original := snapshot
	snapshot = nil
	for _, version := range original {
		if version.Version != "1.25.0" {
			snapshot = append(snapshot, version)
		}
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'r'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if _, found := m.projection.lookup("1.25.0"); found {
		t.Fatal("refresh did not remove the in-flight identity")
	}
	snapshot = append(snapshot,
		utils.GoVersion{Version: "1.25.0", Filename: "go1.25.0.darwin-arm64.tar.gz", Installed: true, Path: "/p/1.25.0"},
		utils.GoVersion{Version: "1.27.0", Filename: "go1.27.0.darwin-arm64.tar.gz"},
	)
	updated, cmd = m.Update(completion)
	if cmd == nil {
		t.Fatal("unconfirmed completion did not schedule verification")
	}
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if m.Status.Kind() != "warning" || !strings.Contains(m.Status.Text(), "with warnings") || !strings.Contains(m.Status.Text(), "temp file busy") {
		t.Fatalf("status = %q/%q, want warnings preserved across verification", m.Status.Text(), m.Status.Kind())
	}
	if got, found := m.projection.lookup("1.25.0"); !found || !got.Installed || got.Path != "/p/1.25.0" {
		t.Fatalf("verified install = %+v, found=%v", got, found)
	}
	assertVersionViewsConsistent(t, m)
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	m = applyFilter(t, m, "1.27.0")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'i'})
	m = runCatalogTestCmd(t, updated.(Model), cmd)
	if got, found := m.projection.lookup("1.27.0"); calls != 2 || !found || !got.Installed {
		t.Fatalf("next install after verification = %+v, found=%v, calls=%d", got, found, calls)
	}
	assertVersionViewsConsistent(t, m)
}
