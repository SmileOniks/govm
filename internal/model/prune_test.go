package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func installedTestPlan(version string, bytes int64) prune.Result {
	return prune.Result{Candidates: []prune.Candidate{{
		Path: "/versions/go" + version, Version: version, Bytes: bytes, Kind: prune.CandidateVersion,
	}}}
}

func installedTestUpdate(t testing.TB, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

// Prune commands may be wrapped in the root's effect batch. Hold their emitted
// messages so lifecycle tests exercise the same identities as real execution.
func installedTestCommandResult(t testing.TB, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an emitted prune command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var result tea.Msg
		for _, child := range batch {
			if child == nil {
				continue
			}
			if result != nil {
				t.Fatal("expected one prune command, got multiple batch children")
			}
			result = installedTestCommandResult(t, child)
		}
		return result
	}
	return msg
}

func installedTestPreview(t testing.TB, cmd tea.Cmd) prunePreviewMsg {
	t.Helper()
	msg := installedTestCommandResult(t, cmd)
	preview, ok := msg.(prunePreviewMsg)
	if !ok {
		t.Fatalf("preview command returned %T", msg)
	}
	return preview
}

func installedTestDone(t testing.TB, cmd tea.Cmd) pruneDoneMsg {
	t.Helper()
	msg := installedTestCommandResult(t, cmd)
	done, ok := msg.(pruneDoneMsg)
	if !ok {
		t.Fatalf("prune command returned %T", msg)
	}
	return done
}

func installedTestAssertNoop(t testing.TB, m Model, msg tea.Msg) Model {
	t.Helper()
	beforeStatus, beforeContext := m.Status, m.inputContext()
	next, cmd := installedTestUpdate(t, m, msg)
	if cmd != nil || next.Status != beforeStatus || next.inputContext() != beforeContext {
		t.Fatalf("%T changed the current flow: command=%v context=%v status=%q", msg, cmd != nil, next.inputContext(), next.Status.Text())
	}
	return next
}

func TestInstalledPrunePreviewAndConfirmation(t *testing.T) {
	previews, runs := 0, 0
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) {
			previews++
			return installedTestPlan("1.23.0", 1024), nil
		},
		Prune: func(context.Context) (prune.Result, error) {
			runs++
			return prune.Result{Removed: installedTestPlan("1.23.0", 1024).Candidates}, nil
		},
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: 'p'})
	m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
	if m.inputContext() != inputPruneConfirm {
		t.Fatal("preview did not open the prune dialog")
	}
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'y'})
	if m.Status.Scope() != statusScopeGlobal || m.Status.Kind() != "info" {
		t.Fatalf("running status = %+v", m.Status)
	}
	m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: 'p'})
	m, _ = installedTestUpdate(t, m, installedTestDone(t, cmd))
	if previews != 1 || runs != 1 || m.inputContext() != inputTab {
		t.Fatalf("preview/run calls = %d/%d, context = %v", previews, runs, m.inputContext())
	}
	if m.Status.Kind() != "success" || m.Status.Scope() != statusScopeGlobal || !strings.Contains(m.Status.Text(), "freed 1.0 KiB") {
		t.Fatalf("completion status = %+v", m.Status)
	}
}

func TestPruneCommandsApplyOperationDeadlines(t *testing.T) {
	var preview, run, usage time.Duration
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(ctx context.Context) (prune.Result, error) {
			preview = budget(t, ctx)
			return installedTestPlan("1.23.0", 1024), nil
		},
		Prune: func(ctx context.Context) (prune.Result, error) {
			run = budget(t, ctx)
			return prune.Result{}, nil
		},
		DiskUsage: func(ctx context.Context) (prune.Summary, error) {
			usage = budget(t, ctx)
			return prune.Summary{}, nil
		},
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = installedTestUpdate(t, m, installedTestDone(t, cmd))
	runCatalogTestCmd(t, m, cmd)
	for _, tc := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"preview", preview, 30 * time.Second},
		{"prune", run, 30 * time.Minute},
		{"disk usage", usage, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got <= tc.want-time.Second || tc.got > tc.want {
				t.Errorf("budget = %v, want approximately %v", tc.got, tc.want)
			}
		})
	}
}

func budget(t *testing.T, ctx context.Context) time.Duration {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("prune operation received a context without a deadline")
	}
	return time.Until(deadline)
}

func TestInstalledTabPreviewOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     prune.Result
		err        error
		kind       string
		confirming bool
	}{
		{name: "empty", kind: "info"},
		{name: "error", err: errors.New("preview denied"), kind: "error"},
		{name: "partial with error", result: installedTestPlan("1.23.0", 1024), err: errors.New("one directory unreadable"), kind: "warning", confirming: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previews, runs := 0, 0
			m := newTestModel(t).BindVersionOperations(VersionOperations{
				PreviewPrune: func(context.Context) (prune.Result, error) {
					previews++
					return tc.result, tc.err
				},
				Prune: func(context.Context) (prune.Result, error) { runs++; return prune.Result{}, nil },
			})
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
			m, cmd = installedTestUpdate(t, m, installedTestPreview(t, cmd))
			if cmd != nil || m.Status.Kind() != tc.kind || m.Status.Scope() != statusScopeTab || (m.inputContext() == inputPruneConfirm) != tc.confirming {
				t.Fatalf("preview outcome: command=%v status=%+v context=%v", cmd != nil, m.Status, m.inputContext())
			}
			if tc.err != nil && !strings.Contains(m.Status.Text(), tc.err.Error()) {
				t.Fatalf("preview lost diagnostic: %q", m.Status.Text())
			}
			if tc.confirming {
				if !strings.Contains(stripANSI(m.View().Content), "1.23.0") {
					t.Fatal("partial preview lost its candidate")
				}
				m = press(t, m, tea.KeyPressMsg{Code: 'n'})
			}
			m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
			installedTestPreview(t, cmd)
			if previews != 2 || runs != 0 {
				t.Fatalf("preview/run calls = %d/%d", previews, runs)
			}
		})
	}
}

func TestInstalledTabMissingAdapters(t *testing.T) {
	t.Run("preview", func(t *testing.T) {
		m := newTestModel(t).BindVersionOperations(VersionOperations{})
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
		if cmd != nil || m.inputContext() != inputTab || m.Status.Kind() != "error" {
			t.Fatalf("missing preview: command=%v context=%v status=%+v", cmd != nil, m.inputContext(), m.Status)
		}
		m = m.BindVersionOperations(VersionOperations{PreviewPrune: func(context.Context) (prune.Result, error) {
			return installedTestPlan("1.23.0", 1024), nil
		}})
		m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
		m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
		if m.inputContext() != inputPruneConfirm {
			t.Fatal("missing adapter left a stuck prune flow")
		}
	})
	t.Run("execution", func(t *testing.T) {
		loads, usages := 0, 0
		m := newTestModel(t).BindVersionOperations(VersionOperations{
			PreviewPrune: func(context.Context) (prune.Result, error) { return installedTestPlan("1.23.0", 1024), nil },
			LoadCatalog:  func(context.Context) ([]utils.GoVersion, error) { loads++; return nil, nil },
			DiskUsage:    func(context.Context) (prune.Summary, error) { usages++; return prune.Summary{}, nil },
		})
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
		m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
		m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		done := installedTestDone(t, cmd)
		if done.Err == nil || !strings.Contains(done.Err.Error(), "no prune service configured") {
			t.Fatalf("missing execution adapter result = %+v", done)
		}
		m, cmd = installedTestUpdate(t, m, done)
		m = runCatalogTestCmd(t, m, cmd)
		if m.Status.Kind() != "warning" || m.Status.Scope() != statusScopeGlobal || loads != 1 || usages != 1 {
			t.Fatalf("missing execution completion: status=%+v load/usage=%d/%d", m.Status, loads, usages)
		}
		m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
		m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
		if m.inputContext() != inputPruneConfirm {
			t.Fatal("failed execution did not release prune admission")
		}
	})
}

func TestInstalledTabDeleteBlockedDuringPrune(t *testing.T) {
	for _, stage := range []string{"previewing", "running"} {
		t.Run(stage, func(t *testing.T) {
			deletes, previews, runs := 0, 0, 0
			m := newTestModel(t).BindVersionOperations(VersionOperations{
				PreviewPrune: func(context.Context) (prune.Result, error) { previews++; return installedTestPlan("1.26.0", 1024), nil },
				Prune:        func(context.Context) (prune.Result, error) { runs++; return prune.Result{}, nil },
				Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
					deletes++
					return lifecycle.DeletionResult{Version: version}, nil
				},
			})
			seedVersions(t, &m, []utils.GoVersion{{Version: "1.26.0", Installed: true, Path: "/versions/go1.26.0"}})
			m = press(t, m, tea.KeyPressMsg{Code: 'd'})
			if m.inputContext() != inputDeleteConfirm {
				t.Fatal("Available fixture must select a deletable version before prune")
			}
			m = press(t, m, tea.KeyPressMsg{Code: 'n'})
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			m = press(t, m, tea.KeyPressMsg{Code: 'd'})
			if m.inputContext() != inputDeleteConfirm {
				t.Fatal("Installed fixture must select a deletable version before prune")
			}
			m = press(t, m, tea.KeyPressMsg{Code: 'n'})
			m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
			preview := installedTestPreview(t, cmd)
			if stage == "running" {
				m, _ = installedTestUpdate(t, m, preview)
				m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
				installedTestDone(t, cmd)
			}
			for _, key := range []rune{'d', 'y'} {
				m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: key})
			}
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			if stage == "running" {
				for _, key := range []rune{'d', 'y'} {
					m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: key})
				}
			} else {
				// Leaving invalidates the preview, so Available can request deletion again.
				m = press(t, m, tea.KeyPressMsg{Code: 'd'})
				if m.inputContext() != inputDeleteConfirm {
					t.Fatal("leaving preview did not release deletion admission")
				}
				m = press(t, m, tea.KeyPressMsg{Code: 'n'})
			}
			if deletes != 0 || previews != 1 || (stage == "running" && runs != 1) {
				t.Fatalf("delete/preview/run calls = %d/%d/%d", deletes, previews, runs)
			}
		})
	}
}

func TestDiskUsageUpdatesInstalledSizeColumn(t *testing.T) {
	m := newTestModel(t)
	m = resizeModel(t, m, 120, 40)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = installedTestUpdate(t, m, diskUsageMsg{Summary: prune.Summary{
		InstalledBytes: 4096, VersionBytes: map[string]int64{"1.24.4": 2048},
	}})
	installedTestAssertVersionSize(t, m, "1.24.4", "2.0 KiB")
	assertVersionViewsConsistent(t, m)
}

func installedTestAssertVersionSize(t testing.TB, m Model, version, size string) {
	t.Helper()
	view := stripANSI(m.View().Content)
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, version) && strings.Contains(line, size) {
			return
		}
	}
	t.Fatalf("installed row for %s lost size %s:\n%s", version, size, view)
}

func TestInstalledTabLatePreview(t *testing.T) {
	previews := 0
	m := newTestModel(t).BindVersionOperations(VersionOperations{PreviewPrune: func(context.Context) (prune.Result, error) {
		previews++
		if previews == 1 {
			return installedTestPlan("1.23.0", 1024), nil
		}
		return installedTestPlan("1.22.0", 2048), nil
	}})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	late := installedTestPreview(t, cmd)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	current := installedTestPreview(t, cmd)
	zero, foreign := current, current
	zero.RequestID = 0
	foreign.RequestID ^= 1 << 63
	for _, msg := range []prunePreviewMsg{late, zero, foreign} {
		m = installedTestAssertNoop(t, m, msg)
	}
	if m.inputContext() != inputTab {
		t.Fatal("rejected preview opened a dialog")
	}
	m, _ = installedTestUpdate(t, m, current)
	view := stripANSI(m.View().Content)
	if m.inputContext() != inputPruneConfirm || !strings.Contains(view, "1.22.0") || strings.Contains(view, "1.23.0") || !strings.Contains(view, "Reclaimable: 2.0 KiB") {
		t.Fatalf("new dialog did not exclusively show its own plan:\n%s", view)
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	for _, msg := range []prunePreviewMsg{current, late, zero, foreign} {
		m = installedTestAssertNoop(t, m, msg)
	}
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.inputContext() != inputTab || m.Status.Kind() != "info" {
		t.Fatal("duplicate preview reset the No choice or prevented cancellation")
	}
	if previews != 2 {
		t.Fatalf("preview calls = %d, want two independent requests", previews)
	}
}

func TestInstalledTabLatePreviewErrorDoesNotPolluteAnotherTab(t *testing.T) {
	m := newTestModel(t).BindVersionOperations(VersionOperations{PreviewPrune: func(context.Context) (prune.Result, error) {
		return prune.Result{}, errors.New("obsolete preview failure")
	}})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	late := installedTestPreview(t, cmd)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = installedTestAssertNoop(t, m, late)
	if m.CurrentTab != AvailableTab || strings.Contains(stripANSI(m.View().Content), "obsolete preview failure") {
		t.Fatal("late preview error leaked into Available")
	}
}

func TestInstalledTabLeaveRunning(t *testing.T) {
	previews, runs, loads, usages := 0, 0, 0, 0
	versions := []utils.GoVersion{{Version: "1.24.4", Installed: true, Active: true, Path: "/versions/go1.24.4"}}
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) {
			previews++
			return installedTestPlan("1.23.0", 1024), nil
		},
		Prune: func(context.Context) (prune.Result, error) {
			runs++
			return prune.Result{Removed: []prune.Candidate{{Bytes: 1024}, {Bytes: 2048}}}, nil
		},
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) { loads++; return versions, nil },
		DiskUsage:   func(context.Context) (prune.Summary, error) { usages++; return prune.Summary{}, nil },
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	preview := installedTestPreview(t, cmd)
	m, _ = installedTestUpdate(t, m, preview)
	m, run := installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = installedTestAssertNoop(t, m, preview)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, tea.KeyPressMsg{Code: tea.KeyTab})
	m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: 'p'})
	if previews != 1 || runs != 0 {
		t.Fatalf("leaving issued another operation: previews/runs=%d/%d", previews, runs)
	}
	done := installedTestDone(t, run)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	zero, foreign := done, done
	zero.RequestID = 0
	foreign.RequestID ^= 1 << 63
	for _, msg := range []pruneDoneMsg{zero, foreign} {
		m = installedTestAssertNoop(t, m, msg)
	}
	m, cmd = installedTestUpdate(t, m, done)
	if m.CurrentTab != AvailableTab || m.Status.Scope() != statusScopeGlobal || m.Status.Kind() != "success" || !strings.Contains(m.Status.Text(), "Pruned 2 object(s), freed 3.0 KiB") {
		t.Fatalf("cross-tab completion: tab=%d status=%+v", m.CurrentTab, m.Status)
	}
	m = runCatalogTestCmd(t, m, cmd)
	if m.Status.Scope() != statusScopeGlobal || m.Status.Kind() != "success" {
		t.Fatalf("refresh replaced prune completion: %+v", m.Status)
	}
	if loads != 1 || usages != 1 || runs != 1 {
		t.Fatalf("completion load/usage/run calls = %d/%d/%d", loads, usages, runs)
	}
	m = installedTestAssertNoop(t, m, done)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	current := installedTestPreview(t, cmd)
	wrongPhase := done
	wrongPhase.RequestID = current.RequestID
	for _, msg := range []tea.Msg{done, wrongPhase, preview} {
		m = installedTestAssertNoop(t, m, msg)
	}
	m, _ = installedTestUpdate(t, m, current)
	m = installedTestAssertNoop(t, m, wrongPhase)
	if m.inputContext() != inputPruneConfirm || previews != 2 || runs != 1 || loads != 1 || usages != 1 {
		t.Fatalf("old completion damaged next flow: context=%v preview/run/load/usage=%d/%d/%d/%d", m.inputContext(), previews, runs, loads, usages)
	}
	assertVersionViewsConsistent(t, m)
}

func TestInstalledTabBackgroundResultsUnderHelp(t *testing.T) {
	previews, runs, loads, usages := 0, 0, 0, 0
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) { previews++; return installedTestPlan("1.23.0", 1024), nil },
		Prune: func(context.Context) (prune.Result, error) {
			runs++
			return prune.Result{Removed: installedTestPlan("1.23.0", 1024).Candidates}, errors.New("one removal denied")
		},
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) { loads++; return nil, nil },
		DiskUsage:   func(context.Context) (prune.Summary, error) { usages++; return prune.Summary{}, nil },
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	preview := installedTestPreview(t, cmd)
	m = press(t, m, tea.KeyPressMsg{Code: '?'})
	m, cmd = installedTestUpdate(t, m, preview)
	if cmd != nil || m.inputContext() != inputHelpOverlay {
		t.Fatal("preview delivery dismissed Help or executed work")
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.inputContext() != inputPruneConfirm || !strings.Contains(stripANSI(m.View().Content), "1.23.0") {
		t.Fatal("preview was not accepted underneath Help")
	}
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	done := installedTestDone(t, cmd)
	m = press(t, m, tea.KeyPressMsg{Code: '?'})
	m, cmd = installedTestUpdate(t, m, done)
	m = runCatalogTestCmd(t, m, cmd)
	if m.inputContext() != inputHelpOverlay || m.Status.Scope() != statusScopeGlobal || m.Status.Kind() != "warning" || !strings.Contains(m.Status.Text(), "one removal denied") {
		t.Fatalf("completion under Help: context=%v status=%+v", m.inputContext(), m.Status)
	}
	m = installedTestAssertNoop(t, m, done)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.inputContext() != inputTab || strings.Contains(stripANSI(m.View().Content), "Prune inactive Go versions?") || previews != 1 || runs != 1 || loads != 1 || usages != 1 {
		t.Fatalf("Help retained completed prune: context=%v preview/run/load/usage=%d/%d/%d/%d", m.inputContext(), previews, runs, loads, usages)
	}
}
