package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/prune"
)

func pruneConfirmingModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) { return installedTestPlan("1.26.1", 1024), nil },
		Prune:        func(context.Context) (prune.Result, error) { return prune.Result{}, nil },
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
	if m.inputContext() != inputPruneConfirm {
		t.Fatal("expected prune confirmation")
	}
	return m
}

func TestPruneDialogRendersWarningTitleAndButtons(t *testing.T) {
	m := pruneConfirmingModel(t)
	got := stripANSI(m.View().Content)
	for _, want := range []string{"⚠", "Prune inactive Go versions?", "Reclaimable: 1.0 KiB", "1.26.1", "Yes", "No"} {
		if !strings.Contains(got, want) {
			t.Errorf("prune dialog missing %q:\n%s", want, got)
		}
	}
}

func TestInstalledTabDialogChoices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keys    []tea.KeyPressMsg
		confirm bool
	}{
		{name: "default Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyEnter}}, confirm: true},
		{name: "Y", keys: []tea.KeyPressMsg{{Code: 'Y'}}, confirm: true},
		{name: "Left Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyLeft}, {Code: tea.KeyEnter}}},
		{name: "N", keys: []tea.KeyPressMsg{{Code: 'n'}}},
		{name: "Escape", keys: []tea.KeyPressMsg{{Code: tea.KeyEscape}}},
		{name: "Tab Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyEnter}}},
		{name: "Shift Tab Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyTab, Mod: tea.ModShift}, {Code: tea.KeyEnter}}},
		{name: "Left Right Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyEnter}}, confirm: true},
		{name: "Tab twice Enter", keys: []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyTab}, {Code: tea.KeyEnter}}, confirm: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previews, runs := 0, 0
			m := newTestModel(t).BindVersionOperations(VersionOperations{
				PreviewPrune: func(context.Context) (prune.Result, error) { previews++; return installedTestPlan("1.23.0", 1024), nil },
				Prune:        func(context.Context) (prune.Result, error) { runs++; return prune.Result{}, nil },
			})
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
			m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
			for i, key := range tc.keys {
				m, cmd = installedTestUpdate(t, m, key)
				if m.CurrentTab != InstalledTab {
					t.Fatal("dialog key changed the tab")
				}
				if i < len(tc.keys)-1 && (cmd != nil || m.inputContext() != inputPruneConfirm) {
					t.Fatalf("choice key %q closed the dialog or executed work", key.String())
				}
			}
			if tc.confirm {
				installedTestDone(t, cmd)
				if runs != 1 || m.Status.Scope() != statusScopeGlobal {
					t.Fatalf("confirmation: runs=%d status=%+v", runs, m.Status)
				}
			} else {
				if cmd != nil || runs != 0 || m.Status.Kind() != "info" || m.Status.Scope() != statusScopeTab {
					t.Fatalf("cancellation: command=%v runs=%d status=%+v", cmd != nil, runs, m.Status)
				}
				m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
				installedTestPreview(t, cmd)
				if previews != 2 {
					t.Fatal("cancel did not release prune admission")
				}
			}
		})
	}
}

func TestInstalledTabDialogHelpBlocksExecution(t *testing.T) {
	previews, runs, deletes := 0, 0, 0
	m := newTestModel(t).BindVersionOperations(VersionOperations{
		PreviewPrune: func(context.Context) (prune.Result, error) { previews++; return installedTestPlan("1.23.0", 1024), nil },
		Prune:        func(context.Context) (prune.Result, error) { runs++; return prune.Result{}, nil },
		Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
			deletes++
			return lifecycle.DeletionResult{Version: version}, nil
		},
	})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, cmd := installedTestUpdate(t, m, tea.KeyPressMsg{Code: 'p'})
	m, _ = installedTestUpdate(t, m, installedTestPreview(t, cmd))
	for _, key := range []rune{'p', 'd', 'u', 'i', 'r'} {
		m = installedTestAssertNoop(t, m, tea.KeyPressMsg{Code: key})
	}
	m = press(t, m, tea.KeyPressMsg{Code: '?'})
	if m.inputContext() != inputHelpOverlay || !strings.Contains(stripANSI(m.View().Content), "Keyboard Shortcuts") {
		t.Fatal("Help did not open over the prune dialog")
	}
	for _, key := range []tea.KeyPressMsg{{Code: 'y'}, {Code: tea.KeyEnter}, {Code: 'p'}, {Code: 'd'}, {Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift}} {
		m, cmd = installedTestUpdate(t, m, key)
		m = runCatalogTestCmd(t, m, cmd)
		if m.inputContext() != inputHelpOverlay || m.CurrentTab != InstalledTab {
			t.Fatalf("Help key %q changed context/tab", key.String())
		}
		if previews != 1 || runs != 0 || deletes != 0 {
			t.Fatalf("Help key %q executed work: preview/run/delete=%d/%d/%d", key.String(), previews, runs, deletes)
		}
	}
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.inputContext() != inputPruneConfirm || !strings.Contains(stripANSI(m.View().Content), "Prune inactive Go versions?") {
		t.Fatal("Escape closed the underlying dialog instead of only Help")
	}
	m, cmd = installedTestUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	installedTestDone(t, cmd)
	if runs != 1 || previews != 1 || deletes != 0 {
		t.Fatalf("after Help: preview/run/delete=%d/%d/%d", previews, runs, deletes)
	}
}
