package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/deps"
)

func activeStandalonePhases() []depsPhase {
	return []depsPhase{
		depsChecking,
		depsLoadingBackups,
		depsRestoringBackup,
	}
}

func TestDepsOperationKeysIgnoreInputWhileOperationInProgress(t *testing.T) {
	keys := []string{"u", "r", "b"}

	for _, phase := range activeStandalonePhases() {
		for _, key := range keys {
			t.Run(standalonePhaseName(phase)+"/"+key, func(t *testing.T) {
				m := modelAtStandalonePhase(t, phase)
				m.Status.SetTab("unchanged", "info")

				updated, cmd := m.Update(tea.KeyPressMsg{Code: rune(key[0])})
				got := updated.(Model)

				if cmd != nil {
					t.Fatal("expected no command while a dependency operation is in progress")
				}
				if got.Status.Text() != m.Status.Text() || got.Status.Kind() != m.Status.Kind() {
					t.Fatalf("message = (%q, %q), want (%q, %q)", got.Status.Text(), got.Status.Kind(), m.Status.Text(), m.Status.Kind())
				}
				if got.deps.phase != phase {
					t.Fatalf("phase = %v, want %v", got.deps.phase, phase)
				}
			})
		}
	}

	for _, at := range activeUpdateCycles() {
		for _, key := range keys {
			t.Run(at.name+"/"+key, func(t *testing.T) {
				m := at.build(t)
				want := m.deps.cycle.Phase()

				updated, cmd := m.Update(tea.KeyPressMsg{Code: rune(key[0])})
				got := updated.(Model)

				if cmd != nil {
					t.Fatal("expected no command while an update cycle is active")
				}
				if got.deps.cycle.Phase() != want {
					t.Fatalf("cycle phase = %s, want %s", got.deps.cycle.Phase(), want)
				}
			})
		}
	}
}

func TestDependencyErrMsgClearsStandaloneOperation(t *testing.T) {
	for _, phase := range activeStandalonePhases() {
		t.Run(standalonePhaseName(phase), func(t *testing.T) {
			m := modelAtStandalonePhase(t, phase)

			updated, _ := m.Update(dependencyErrMsg{Err: errors.New("operation failed")})
			got := updated.(Model)

			if got.deps.phase != depsIdle {
				t.Fatalf("phase = %v, want depsIdle", got.deps.phase)
			}
		})
	}
}

// activeUpdateCycles builds a Model in every operational cycle phase
// (the confirmation phases open a dialog, which owns the keyboard
// itself), each reached through the tab's own keys and result
// messages.
func activeUpdateCycles() []struct {
	name  string
	build func(testing.TB) Model
} {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	snapshot := &deps.DependencySnapshot{
		ModFile: deps.ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n"},
	}
	backup := &deps.DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"}
	started := func(tb testing.TB) Model {
		return feed(tb, loadDeps(tb, newTestModel(tb), testLib()), tea.KeyPressMsg{Code: 'u'})
	}
	applying := func(tb testing.TB) Model { return feed(tb, modelAtConfirmApply(tb), enter) }
	compensating := func(tb testing.TB) Model {
		return feed(tb, applying(tb), deps.ApplyUpdatesDoneEvent{
			Snapshot: snapshot, Backup: backup, Err: errors.New("apply failed"),
		})
	}
	runningChecks := func(tb testing.TB) Model { return feed(tb, modelAtConfirmChecks(tb), enter) }
	rollingBack := func(tb testing.TB) Model { return feed(tb, modelAtConfirmRollback(tb), enter) }
	return []struct {
		name  string
		build func(testing.TB) Model
	}{
		{"checking", started},
		{"applying", applying},
		{"compensating", compensating},
		{"running-checks", runningChecks},
		{"rolling-back", rollingBack},
	}
}

func standalonePhaseName(phase depsPhase) string {
	switch phase {
	case depsChecking:
		return "checking"
	case depsLoadingBackups:
		return "loading-backups"
	case depsRestoringBackup:
		return "restoring-backup"
	default:
		return "idle"
	}
}

// A Model built without BindDeps must degrade to a status message,
// never a nil dereference: the unavailable executor fails every
// operation and the ordinary error path renders it.
func TestDepsOperationsUnavailableUntilBound(t *testing.T) {
	m := newTestModel(t)
	m = m.BindDeps(nil)
	m.CurrentTab = InstalledTab

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.CurrentTab != DepsTab || cmd == nil {
		t.Fatalf("expected the tab switch to start the lazy load, tab=%d cmd=%v", m.CurrentTab, cmd)
	}

	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.Status.Text() != errDepsUnavailable.Error() || m.Status.Kind() != "error" {
		t.Fatalf("status = (%q, %q), want (%q, error)", m.Status.Text(), m.Status.Kind(), errDepsUnavailable)
	}
	if m.deps.phase != depsIdle {
		t.Fatalf("phase = %v, want idle after the failed load", m.deps.phase)
	}
}
