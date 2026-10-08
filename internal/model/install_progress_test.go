package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/utils"
	"github.com/charmbracelet/x/ansi"
)

func TestInstallProgressMailboxKeepsLatestEvent(t *testing.T) {
	mailbox := newInstallProgressMailbox()
	mailbox.Report(install.Progress{
		Version:       "1.30.0",
		Stage:         install.StageDownload,
		BytesReceived: 1,
		BytesTotal:    10,
	})
	mailbox.Report(install.Progress{
		Version:       "1.30.0",
		Stage:         install.StageDownload,
		BytesReceived: 9,
		BytesTotal:    10,
	})

	<-mailbox.notify
	got := mailbox.snapshot()
	if got.BytesReceived != 9 || got.BytesTotal != 10 {
		t.Fatalf("latest progress = %+v, want 9/10 bytes", got)
	}
}

func TestInstallProgressSessionReturnsProgressThenOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		t.Cleanup(unblock)
		session := &installProgressSession{
			operationID: 42,
			request:     install.Request{Version: "1.30.0"},
			outcomes:    make(chan tea.Msg, 1),
			mailbox:     newInstallProgressMailbox(),
			install: func(_ context.Context, _ install.Request, reporter install.ProgressReporter) (install.Result, error) {
				reporter.Report(install.Progress{Version: "1.30.0", Stage: install.StagePrepare})
				<-release
				return install.Result{Version: "1.30.0", Path: "/p/1.30.0"}, nil
			},
		}
		session.start()
		first := session.wait(true)()
		progressMsg, ok := first.(installProgressMsg)
		if !ok {
			t.Fatalf("first message = %T, want installProgressMsg", first)
		}
		if progressMsg.progress.Stage != install.StagePrepare {
			t.Fatalf("progress stage = %s, want %s", progressMsg.progress.Stage, install.StagePrepare)
		}
		unblock()
		synctest.Wait()
		second := session.wait(false)()
		if _, ok := second.(installSuccessMsg); !ok {
			t.Fatalf("second message = %T, want installSuccessMsg", second)
		}
	})
}

func TestRenderDownloadStatusUsesBytesAndPercentage(t *testing.T) {
	m := newTestModel(t)
	status := stripANSI(m.renderDownloadStatus(install.Progress{
		Version:       "1.30.0",
		Stage:         install.StageDownload,
		BytesReceived: 512 * 1024,
		BytesTotal:    1024 * 1024,
	}, 100))

	for _, want := range []string{"Downloading Go 1.30.0", "50%", "512.0 KiB", "1.0 MiB"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status = %q, want %q", status, want)
		}
	}
}

func TestRenderDownloadStatusUnknownTotalUsesSpinnerAndBytes(t *testing.T) {
	m := newTestModel(t)
	status := stripANSI(m.renderDownloadStatus(install.Progress{
		Version:       "1.30.0",
		Stage:         install.StageDownload,
		BytesReceived: 12 * 1024,
	}, 100))

	if !strings.Contains(status, "Downloading Go 1.30.0") ||
		!strings.Contains(status, "12.0 KiB") {
		t.Fatalf("status = %q, want spinner download and bytes", status)
	}
	if strings.Contains(status, "%") {
		t.Fatalf("status = %q, did not expect percentage with unknown total", status)
	}
}

func TestRenderDownloadStatusFitsNarrowWidth(t *testing.T) {
	m := newTestModel(t)
	const width = 64
	status := m.renderDownloadStatus(install.Progress{
		Version:       "1.30.0",
		Stage:         install.StageDownload,
		BytesReceived: 512 * 1024,
		BytesTotal:    1024 * 1024,
	}, width)

	if got := ansi.StringWidth(status); got > width-2 {
		t.Fatalf("status width = %d, want <= %d: %q", got, width-2, status)
	}
}

func TestComposeStatusUsesInstallProgressStage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newVersionCacheTestModel(t)
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		t.Cleanup(unblock)
		m = m.BindVersionOperations(VersionOperations{
			InstallWithProgress: func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
				reporter.Report(install.Progress{Version: request.Version, Stage: install.StageIntegrity})
				<-release
				return install.Result{Version: request.Version, Path: "/p/" + request.Version}, nil
			},
		})
		m = applyFilter(t, m, "1.25.0")
		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if cmd == nil {
			t.Fatal("install did not dispatch")
		}
		progress, ok := cmd().(installProgressMsg)
		if !ok {
			t.Fatal("install did not report its integrity stage")
		}
		updated, continuation := m.Update(progress)
		m = updated.(Model)
		status, kind := m.composeStatus()
		if kind != "info" || !strings.Contains(stripANSI(status), "Checking integrity Go 1.25.0") {
			t.Fatalf("status = %q/%q, want the current integrity stage", stripANSI(status), kind)
		}
		unblock()
		synctest.Wait()
		m = runCatalogTestCmd(t, m, continuation)
		assertVersionViewsConsistent(t, m)
	})
}

func TestCatalogFlowProgressSurvivesNavigation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newVersionCacheTestModel(t)
		advance, release := make(chan struct{}), make(chan struct{})
		var advanceOnce, releaseOnce sync.Once
		nextMeasurement := func() { advanceOnce.Do(func() { close(advance) }) }
		unblock := func() {
			nextMeasurement()
			releaseOnce.Do(func() { close(release) })
		}
		defer unblock()
		t.Cleanup(unblock)
		calls := 0
		m = m.BindVersionOperations(VersionOperations{
			InstallWithProgress: func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
				calls++
				reporter.Report(install.Progress{
					Version: request.Version, Stage: install.StageDownload,
					BytesReceived: 256 * 1024, BytesTotal: 1024 * 1024,
				})
				<-advance
				reporter.Report(install.Progress{
					Version: request.Version, Stage: install.StageDownload,
					BytesReceived: 768 * 1024, BytesTotal: 1024 * 1024,
				})
				<-release
				return install.Result{Version: request.Version, Path: "/installed/" + request.Version}, nil
			},
		})
		m = applyFilter(t, m, "1.25.0")
		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if cmd == nil {
			t.Fatal("install did not dispatch")
		}
		first, ok := cmd().(installProgressMsg)
		if !ok {
			t.Fatal("install did not report its first measurement")
		}
		updated, continuation := m.Update(first)
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = updated.(Model)
		if m.CurrentTab != InstalledTab {
			t.Fatalf("navigation selected tab %d, want Installed", m.CurrentTab)
		}
		assertDownload := func(percent string) {
			t.Helper()
			status, kind := m.composeStatus()
			view := ansi.Strip(m.View().Content)
			if kind != "info" || !strings.Contains(ansi.Strip(status), percent) ||
				!strings.Contains(view, "Downloading Go 1.25.0") || !strings.Contains(view, percent) {
				t.Fatalf("download %s missing after navigation: status=%q/%q, view=%q", percent, ansi.Strip(status), kind, view)
			}
		}
		assertDownload("25%")
		if continuation == nil {
			t.Fatal("accepted progress did not schedule continuation")
		}
		poll, ok := continuation().(installProgressPollMsg)
		if !ok {
			t.Fatal("no-new-measurement continuation did not return a poll")
		}
		updated, continuation = m.Update(poll)
		m = updated.(Model)
		assertDownload("25%")
		nextMeasurement()
		synctest.Wait()
		if continuation == nil {
			t.Fatal("accepted poll did not schedule continuation")
		}
		second, ok := continuation().(installProgressMsg)
		if !ok || second.progress.BytesReceived != 768*1024 {
			t.Fatalf("next measurement = %+v, valid=%v", second, ok)
		}
		updated, continuation = m.Update(second)
		m = updated.(Model)
		assertDownload("75%")
		unblock()
		synctest.Wait()
		m = runCatalogTestCmd(t, m, continuation)
		if got, found := m.projection.lookup("1.25.0"); !found || !got.Installed || got.Path != "/installed/1.25.0" {
			t.Fatalf("completion on Installed tab = %+v, found=%v", got, found)
		}
		if calls != 1 {
			t.Fatalf("installer calls = %d, want 1", calls)
		}
		assertVersionViewsConsistent(t, m)
		for _, stale := range []tea.Msg{first, second, poll} {
			updated, cmd = m.Update(stale)
			m = updated.(Model)
			if cmd != nil {
				t.Fatalf("completed install retained continuation for %T", stale)
			}
			status, _ := m.composeStatus()
			if strings.Contains(ansi.Strip(status), "Downloading") || strings.Contains(ansi.Strip(m.View().Content), "Downloading Go 1.25.0") {
				t.Fatalf("stale %T restored download status", stale)
			}
		}
	})
}

func TestCatalogFlowStaleCompletionHasNoEffects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newVersionCacheTestModel(t)
		releaseA, releaseB := make(chan struct{}), make(chan struct{})
		var onceA, onceB sync.Once
		unblockA := func() { onceA.Do(func() { close(releaseA) }) }
		unblockB := func() { onceB.Do(func() { close(releaseB) }) }
		defer unblockA()
		defer unblockB()
		t.Cleanup(unblockA)
		t.Cleanup(unblockB)
		calls, diskCalls := 0, 0
		staleDiagnostics := false
		m = m.BindVersionOperations(VersionOperations{
			LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
				return projectionVersions(m, utils.GoVersion{
					Version: "1.27.0", Filename: "go1.27.0.darwin-arm64.tar.gz",
				}), nil
			},
			InstallWithProgress: func(_ context.Context, request install.Request, reporter install.ProgressReporter) (install.Result, error) {
				calls++
				reporter.Report(install.Progress{
					Version: request.Version, Stage: install.StageDownload,
					BytesReceived: 25, BytesTotal: 100,
				})
				if request.Version == "1.25.0" {
					<-releaseA
				} else {
					<-releaseB
				}
				return install.Result{Version: request.Version, Path: "/new/" + request.Version}, nil
			},
			DiskUsage: func(context.Context) (prune.Summary, error) {
				diskCalls++
				if staleDiagnostics {
					return prune.Summary{}, errors.New("stale diagnostics")
				}
				return prune.Summary{VersionBytes: map[string]int64{"1.25.0": 4096, "1.27.0": 8192}}, nil
			},
		})
		m = applyFilter(t, m, "1.25.0")
		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if cmd == nil {
			t.Fatal("install A did not dispatch")
		}
		progressA, ok := cmd().(installProgressMsg)
		if !ok {
			t.Fatal("install A did not report progress")
		}
		updated, continuation := m.Update(progressA)
		m = updated.(Model)
		if continuation == nil {
			t.Fatal("install A progress did not schedule continuation")
		}
		pollA, ok := continuation().(installProgressPollMsg)
		if !ok {
			t.Fatal("install A did not return an idle progress poll")
		}
		updated, continuation = m.Update(pollA)
		m = updated.(Model)
		unblockA()
		synctest.Wait()
		if continuation == nil {
			t.Fatal("install A poll did not schedule continuation")
		}
		successA, ok := continuation().(installSuccessMsg)
		if !ok {
			t.Fatal("install A did not complete successfully")
		}
		updated, cmd = m.Update(successA)
		m = runCatalogTestCmd(t, updated.(Model), cmd)
		if diskCalls != 1 {
			t.Fatalf("accepted install A disk calls = %d, want 1", diskCalls)
		}
		updated, cmd = m.Update(tea.KeyPressMsg{Code: 'r'})
		m = runCatalogTestCmd(t, updated.(Model), cmd)
		updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		m = runCatalogTestCmd(t, updated.(Model), cmd)
		m = applyFilter(t, m, "1.27.0")
		updated, cmd = m.Update(tea.KeyPressMsg{Code: 'i'})
		m = updated.(Model)
		if cmd == nil {
			t.Fatal("install B did not dispatch")
		}
		progressB, ok := cmd().(installProgressMsg)
		if !ok {
			t.Fatal("install B did not report progress")
		}
		updated, continuationB := m.Update(progressB)
		m = updated.(Model)
		before := projectionVersions(m)
		status, kind := m.composeStatus()
		statusText, statusKind := m.Status.Text(), m.Status.Kind()
		staleDiagnostics = true
		for _, stale := range []tea.Msg{
			successA,
			installFailureMsg{OperationID: successA.OperationID, Version: successA.Version, Err: errors.New("late failure")},
			progressA,
			pollA,
		} {
			updated, cmd = m.Update(stale)
			switch stale.(type) {
			case installProgressMsg, installProgressPollMsg:
				if cmd != nil {
					t.Fatalf("stale %T scheduled a new continuation", stale)
				}
			}
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if diskCalls != 1 {
				t.Errorf("stale %T triggered disk diagnostics: calls = %d, want 1", stale, diskCalls)
			}
			if gotStatus, gotKind := m.composeStatus(); gotStatus != status || gotKind != kind {
				t.Errorf("stale %T changed B status to %q/%q, want %q/%q", stale, gotStatus, gotKind, status, kind)
			}
			if m.Status.Text() != statusText || m.Status.Kind() != statusKind {
				t.Errorf("stale %T changed B stored status to %q/%q, want %q/%q", stale, m.Status.Text(), m.Status.Kind(), statusText, statusKind)
			}
			if !reflect.DeepEqual(projectionVersions(m), before) {
				t.Errorf("stale %T changed catalog during install B", stale)
			}
			if progress, active := m.projection.activityState().installProgress(); !active || progress != progressB.progress {
				t.Errorf("stale %T changed B progress: %+v, active=%v", stale, progress, active)
			}
			assertVersionViewsConsistent(t, m)
		}
		staleDiagnostics = false
		unblockB()
		synctest.Wait()
		if continuationB == nil {
			t.Fatal("install B did not schedule continuation")
		}
		successB, ok := continuationB().(installSuccessMsg)
		if !ok {
			t.Fatal("install B did not complete successfully")
		}
		updated, cmd = m.Update(successB)
		m = runCatalogTestCmd(t, updated.(Model), cmd)
		if calls != 2 || diskCalls != 2 {
			t.Fatalf("installer/disk calls = %d/%d, want 2/2", calls, diskCalls)
		}
		for _, version := range []string{"1.25.0", "1.27.0"} {
			got, found := m.projection.lookup(version)
			if !found || !got.Installed || got.Path != "/new/"+version {
				t.Errorf("completed version %s = %+v, found=%v", version, got, found)
			}
		}
		assertVersionViewsConsistent(t, m)
	})
}
