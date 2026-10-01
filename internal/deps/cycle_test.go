package deps

import (
	"errors"
	"testing"
)

func updatableDeps() []ModuleDependency {
	return []ModuleDependency{
		{Path: "example.com/updatable", Version: "v1.0.0", Latest: "v1.1.0"},
		{Path: "example.com/indirect", Version: "v2.0.0", Latest: "v2.1.0", Indirect: true},
		{Path: "example.com/current", Version: "v3.0.0", Latest: "v3.0.0"},
	}
}

func currentDeps() []ModuleDependency {
	return []ModuleDependency{
		{Path: "example.com/current", Version: "v3.0.0", Latest: "v3.0.0"},
	}
}

func testSnapshot() *DependencySnapshot {
	return &DependencySnapshot{
		ModFile: ModuleFileSnapshot{Exists: true, Content: "module example.com/app\n\ngo 1.26\n"},
		SumFile: ModuleFileSnapshot{Exists: true, Content: "sum\n"},
		Updatable: []DependencyUpdateEntry{
			{Path: "example.com/updatable", OldVersion: "v1.0.0", NewVersion: "v1.1.0"},
		},
	}
}

func testBackup() *DependencyBackupInfo {
	return &DependencyBackupInfo{
		Name:       "2026-07-23_12-00-00.json",
		Path:       "/tmp/backup/2026-07-23_12-00-00.json",
		ModulePath: "example.com/app",
		Kind:       DependencyBackupKindPreUpdate,
	}
}

// Initial state

func TestNewUpdateCycle_StartsIdle(t *testing.T) {
	c := NewUpdateCycle()
	assertPhase(t, c, PhaseIdle)
	if c.IsTerminal() {
		t.Fatal("fresh cycle should not be terminal")
	}
	if c.Outcome() != OutcomeNone {
		t.Fatalf("Outcome() = %s, want none", c.Outcome())
	}
	if c.Failure() != nil {
		t.Fatalf("Failure() = %v, want nil", c.Failure())
	}
}

// Happy path: UpdatedVerified

func TestHappyPath_UpdatedVerified(t *testing.T) {
	c := NewUpdateCycle()

	c, intent, err := c.Handle(StartEvent{ModuleDir: "/mod"})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseChecking)
	assertIntent[IntentCheckUpdates](t, intent)

	c, intent, err = c.Handle(CheckUpdatesDoneEvent{Dependencies: updatableDeps()})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseConfirmApply)
	confirmApply := assertIntent[IntentConfirmApply](t, intent)
	if len(confirmApply.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(confirmApply.Entries))
	}
	if !confirmApply.DefaultYes {
		t.Fatal("IntentConfirmApply.DefaultYes should be true")
	}

	c, intent, err = c.Handle(ConfirmApplyEvent{Yes: true})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseApplying)
	applyIntent := assertIntent[IntentApplyUpdates](t, intent)
	if len(applyIntent.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(applyIntent.Entries))
	}

	refreshed := []ModuleDependency{{Path: "example.com/updatable", Version: "v1.1.0", Latest: "v1.1.0"}}
	c, intent, err = c.Handle(ApplyUpdatesDoneEvent{
		Snapshot:     testSnapshot(),
		Backup:       testBackup(),
		Dependencies: refreshed,
	})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseConfirmChecks)
	ccIntent := assertIntent[IntentConfirmChecks](t, intent)
	if !ccIntent.DefaultYes {
		t.Fatal("IntentConfirmChecks.DefaultYes should be true")
	}
	if ccIntent.UpdatedCount != 1 {
		t.Fatalf("IntentConfirmChecks.UpdatedCount = %d, want 1", ccIntent.UpdatedCount)
	}

	c, intent, err = c.Handle(ConfirmChecksEvent{Yes: true})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseRunningChecks)
	assertIntent[IntentRunChecks](t, intent)

	c, intent, err = c.Handle(ChecksDoneEvent{Result: DependencyCheckResult{OK: true}})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeUpdatedVerified)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
}

func TestNoUpdates(t *testing.T) {
	c := NewUpdateCycle()
	c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})

	c, intent, err := c.Handle(CheckUpdatesDoneEvent{Dependencies: currentDeps()})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeNoUpdates)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
}

func TestApplyCanceled(t *testing.T) {
	c := driveToConfirmApply(t)

	c, intent, err := c.Handle(ConfirmApplyEvent{Yes: false})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeApplyCanceled)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
}

func TestUpdatedUnchecked(t *testing.T) {
	c := driveToConfirmChecks(t)

	c, intent, err := c.Handle(ConfirmChecksEvent{Yes: false})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeUpdatedUnchecked)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
}

func TestUpdatesKeptWithFailedChecks(t *testing.T) {
	c := driveToConfirmRollback(t)

	c, intent, err := c.Handle(ConfirmRollbackEvent{Yes: false})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeUpdatesKeptWithFailedChecks)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
}

func TestRolledBack(t *testing.T) {
	c := driveToConfirmRollback(t)

	c, intent, err := c.Handle(ConfirmRollbackEvent{Yes: true})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseRollingBack)
	rbIntent := assertIntent[IntentRollback](t, intent)
	if rbIntent.Snapshot == nil {
		t.Fatal("IntentRollback.Snapshot should not be nil")
	}

	rolled := []ModuleDependency{{Path: "example.com/updatable", Version: "v1.0.0"}}
	c, intent, err = c.Handle(RollbackDoneEvent{Dependencies: rolled})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeRolledBack)
	assertNoIntent(t, intent)
	assertTerminalCleanup(t, c)
	if len(c.Dependencies()) != 1 || c.Dependencies()[0].Version != "v1.0.0" {
		t.Fatalf("Dependencies = %+v", c.Dependencies())
	}
}

// Failure context preservation

func TestFailure_CheckErrorPreserved(t *testing.T) {
	c := NewUpdateCycle()
	c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
	checkErr := errors.New("network down")

	c, _, err := c.Handle(CheckUpdatesDoneEvent{Err: checkErr})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeFailed)
	if !errors.Is(c.Failure(), checkErr) {
		t.Fatalf("Failure() = %v, want %v", c.Failure(), checkErr)
	}
}

func TestFailure_ApplyErrorBeforeMutation(t *testing.T) {
	c := driveToApplying(t)
	applyErr := errors.New("resolve failed")

	c, intent, err := c.Handle(ApplyUpdatesDoneEvent{Err: applyErr})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeFailed)
	assertNoIntent(t, intent)
	if !errors.Is(c.Failure(), applyErr) {
		t.Fatalf("Failure() = %v, want %v", c.Failure(), applyErr)
	}
	assertTerminalCleanup(t, c)
}

func TestFailure_UpdateFailedRestored_PreservesApplyError(t *testing.T) {
	c := driveToApplying(t)
	applyErr := errors.New("go get failed")

	c, _, err := c.Handle(ApplyUpdatesDoneEvent{
		Snapshot: testSnapshot(),
		Backup:   testBackup(),
		Err:      applyErr,
	})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseCompensating)

	c, _, err = c.Handle(CompensateDoneEvent{Dependencies: currentDeps()})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeUpdateFailedRestored)
	// The original apply error must be preserved.
	if !errors.Is(c.Failure(), applyErr) {
		t.Fatalf("Failure() = %v, want apply error %v", c.Failure(), applyErr)
	}
	assertTerminalCleanup(t, c)
}

func TestFailure_RecoveryRequired_CompensationErrorPreserved(t *testing.T) {
	c := driveToApplying(t)
	c, _, _ = c.Handle(ApplyUpdatesDoneEvent{
		Snapshot: testSnapshot(),
		Backup:   testBackup(),
		Err:      errors.New("go get failed"),
	})
	compErr := errors.New("compensation failed")

	c, _, err := c.Handle(CompensateDoneEvent{Err: compErr})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeRecoveryRequired)
	if !errors.Is(c.Failure(), compErr) {
		t.Fatalf("Failure() = %v, want compensation error %v", c.Failure(), compErr)
	}
	if c.Backup() == nil {
		t.Fatal("Backup() should be retained")
	}
	assertTerminalCleanup(t, c)
}

func TestFailure_RecoveryRequired_RollbackErrorPreserved(t *testing.T) {
	c := driveToConfirmRollback(t)
	c, _, _ = c.Handle(ConfirmRollbackEvent{Yes: true})
	rbErr := errors.New("restore failed")

	c, _, err := c.Handle(RollbackDoneEvent{Err: rbErr})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeRecoveryRequired)
	if !errors.Is(c.Failure(), rbErr) {
		t.Fatalf("Failure() = %v, want rollback error %v", c.Failure(), rbErr)
	}
	if c.Backup() == nil {
		t.Fatal("Backup() should be retained")
	}
	assertTerminalCleanup(t, c)
}

func TestFailure_RecoveryRequired_MissingSnapshot(t *testing.T) {
	c := driveToConfirmRollback(t)
	c.snapshot = nil

	c, _, err := c.Handle(ConfirmRollbackEvent{Yes: true})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeRecoveryRequired)
	if !errors.Is(c.Failure(), ErrMissingRollbackSnapshot) {
		t.Fatalf("Failure() = %v, want ErrMissingRollbackSnapshot", c.Failure())
	}
}

// Apply error classification

func TestApplyClassification_ErrNilSnapshot_NoCompensation(t *testing.T) {
	c := driveToApplying(t)

	c, intent, err := c.Handle(ApplyUpdatesDoneEvent{Err: errors.New("resolve failed")})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeFailed)
	assertNoIntent(t, intent)
}

func TestApplyClassification_ErrNonNilSnapshot_Compensates(t *testing.T) {
	c := driveToApplying(t)

	c, intent, err := c.Handle(ApplyUpdatesDoneEvent{
		Snapshot: testSnapshot(),
		Backup:   testBackup(),
		Err:      errors.New("go get failed"),
	})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseCompensating)
	assertIntent[IntentCompensate](t, intent)
}

func TestApplyClassification_SuccessNilSnapshot_Failed(t *testing.T) {
	c := driveToApplying(t)

	c, intent, err := c.Handle(ApplyUpdatesDoneEvent{
		Backup:       testBackup(),
		Dependencies: updatableDeps(),
	})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeFailed)
	assertNoIntent(t, intent)
	if !errors.Is(c.Failure(), ErrMissingApplySnapshot) {
		t.Fatalf("Failure() = %v, want ErrMissingApplySnapshot", c.Failure())
	}
}

func TestApplyClassification_SuccessNilBackup_Failed(t *testing.T) {
	c := driveToApplying(t)

	c, intent, err := c.Handle(ApplyUpdatesDoneEvent{
		Snapshot:     testSnapshot(),
		Dependencies: updatableDeps(),
	})
	assertNoErr(t, err)
	assertTerminal(t, c, OutcomeFailed)
	assertNoIntent(t, intent)
	if !errors.Is(c.Failure(), ErrMissingApplyBackup) {
		t.Fatalf("Failure() = %v, want ErrMissingApplyBackup", c.Failure())
	}
}

// ChecksDone: failed vs inconclusive -> ConfirmRollback

func TestChecksDone_Failure_ConfirmRollback(t *testing.T) {
	c := driveToRunningChecks(t)

	c, intent, err := c.Handle(ChecksDoneEvent{
		Result: DependencyCheckResult{OK: false, Command: "go test ./...", Output: "boom"},
	})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseConfirmRollback)
	cr := assertIntent[IntentConfirmRollback](t, intent)
	if !cr.DefaultYes {
		t.Fatal("DefaultYes should be true")
	}
	if cr.CheckErr != nil {
		t.Fatalf("CheckErr = %v, want nil for failed checks", cr.CheckErr)
	}
	if cr.CheckResult == nil || cr.CheckResult.OK {
		t.Fatal("CheckResult should be non-nil and not OK")
	}
}

func TestChecksDone_Inconclusive_ConfirmRollback(t *testing.T) {
	c := driveToRunningChecks(t)
	checkErr := errors.New("resolve failed")

	c, intent, err := c.Handle(ChecksDoneEvent{Err: checkErr})
	assertNoErr(t, err)
	assertPhase(t, c, PhaseConfirmRollback)
	cr := assertIntent[IntentConfirmRollback](t, intent)
	if !cr.DefaultYes {
		t.Fatal("DefaultYes should be true")
	}
	if !errors.Is(cr.CheckErr, checkErr) {
		t.Fatalf("CheckErr = %v, want %v", cr.CheckErr, checkErr)
	}
}

// DefaultYes policy

func TestDefaultYes_AllConfirmIntents(t *testing.T) {
	t.Run("confirm-apply", func(t *testing.T) {
		c := driveToConfirmApply(t)
		_, intent, _ := c.Handle(ConfirmApplyEvent{Yes: true})
		// The intent emitted when reaching confirm-apply was already
		// consumed; re-derive by driving from check.
		c2 := NewUpdateCycle()
		c2, _, _ = c2.Handle(StartEvent{ModuleDir: "/mod"})
		_, intent2, _ := c2.Handle(CheckUpdatesDoneEvent{Dependencies: updatableDeps()})
		ca := assertIntent[IntentConfirmApply](t, intent2)
		if !ca.DefaultYes {
			t.Fatal("IntentConfirmApply.DefaultYes should be true")
		}
		_ = intent
	})

	t.Run("confirm-checks", func(t *testing.T) {
		c := driveToConfirmChecks(t)
		// Re-derive: drive to confirm-checks and check the intent that
		// was emitted on the apply-done transition.
		c2 := driveToApplying(t)
		_, intent, _ := c2.Handle(ApplyUpdatesDoneEvent{
			Snapshot:     testSnapshot(),
			Backup:       testBackup(),
			Dependencies: updatableDeps(),
		})
		cc := assertIntent[IntentConfirmChecks](t, intent)
		if !cc.DefaultYes {
			t.Fatal("IntentConfirmChecks.DefaultYes should be true")
		}
		_ = c
	})

	t.Run("confirm-rollback", func(t *testing.T) {
		c := driveToRunningChecks(t)
		_, intent, _ := c.Handle(ChecksDoneEvent{
			Result: DependencyCheckResult{OK: false, Command: "go test ./..."},
		})
		cr := assertIntent[IntentConfirmRollback](t, intent)
		if !cr.DefaultYes {
			t.Fatal("IntentConfirmRollback.DefaultYes should be true")
		}
	})
}

// Terminal lifecycle: snapshot and check context cleared

func TestTerminalCleanup_AllBranches(t *testing.T) {
	tests := []struct {
		name   string
		drive  func(t *testing.T) UpdateCycle
		events []Event
		want   Outcome
	}{
		{
			name: "no-updates",
			drive: func(t *testing.T) UpdateCycle {
				c := NewUpdateCycle()
				c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
				return c
			},
			events: []Event{CheckUpdatesDoneEvent{Dependencies: currentDeps()}},
			want:   OutcomeNoUpdates,
		},
		{
			name:   "apply-canceled",
			drive:  driveToConfirmApply,
			events: []Event{ConfirmApplyEvent{Yes: false}},
			want:   OutcomeApplyCanceled,
		},
		{
			name:   "updated-unchecked",
			drive:  driveToConfirmChecks,
			events: []Event{ConfirmChecksEvent{Yes: false}},
			want:   OutcomeUpdatedUnchecked,
		},
		{
			name:   "updated-verified",
			drive:  driveToRunningChecks,
			events: []Event{ChecksDoneEvent{Result: DependencyCheckResult{OK: true}}},
			want:   OutcomeUpdatedVerified,
		},
		{
			name:   "updates-kept",
			drive:  driveToConfirmRollback,
			events: []Event{ConfirmRollbackEvent{Yes: false}},
			want:   OutcomeUpdatesKeptWithFailedChecks,
		},
		{
			name:   "rolled-back",
			drive:  driveToConfirmRollback,
			events: []Event{ConfirmRollbackEvent{Yes: true}, RollbackDoneEvent{Dependencies: currentDeps()}},
			want:   OutcomeRolledBack,
		},
		{
			name: "failed-check",
			drive: func(t *testing.T) UpdateCycle {
				c := NewUpdateCycle()
				c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
				return c
			},
			events: []Event{CheckUpdatesDoneEvent{Err: errors.New("net")}},
			want:   OutcomeFailed,
		},
		{
			name:  "update-failed-restored",
			drive: driveToApplying,
			events: []Event{
				ApplyUpdatesDoneEvent{Snapshot: testSnapshot(), Backup: testBackup(), Err: errors.New("get")},
				CompensateDoneEvent{Dependencies: currentDeps()},
			},
			want: OutcomeUpdateFailedRestored,
		},
		{
			name:  "recovery-compensation",
			drive: driveToApplying,
			events: []Event{
				ApplyUpdatesDoneEvent{Snapshot: testSnapshot(), Backup: testBackup(), Err: errors.New("get")},
				CompensateDoneEvent{Err: errors.New("comp")},
			},
			want: OutcomeRecoveryRequired,
		},
		{
			name:  "recovery-rollback",
			drive: driveToConfirmRollback,
			events: []Event{
				ConfirmRollbackEvent{Yes: true},
				RollbackDoneEvent{Err: errors.New("restore")},
			},
			want: OutcomeRecoveryRequired,
		},
		{
			name:   "failed-apply-nil-snapshot",
			drive:  driveToApplying,
			events: []Event{ApplyUpdatesDoneEvent{Err: errors.New("resolve")}},
			want:   OutcomeFailed,
		},
		{
			name:   "failed-apply-success-nil-snapshot",
			drive:  driveToApplying,
			events: []Event{ApplyUpdatesDoneEvent{Backup: testBackup(), Dependencies: currentDeps()}},
			want:   OutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.drive(t)
			for _, e := range tt.events {
				c, _, _ = c.Handle(e)
			}
			if !c.IsTerminal() {
				t.Fatalf("not terminal: phase = %s", c.Phase())
			}
			if c.Outcome() != tt.want {
				t.Fatalf("outcome = %s, want %s", c.Outcome(), tt.want)
			}
			assertTerminalCleanup(t, c)
		})
	}
}

// Immutability

func TestImmutability_MutatingEventDependenciesDoesNotAffectCycle(t *testing.T) {
	deps := updatableDeps()
	c := NewUpdateCycle()
	c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
	c, _, _ = c.Handle(CheckUpdatesDoneEvent{Dependencies: deps})

	deps[0].Version = "v9.9.9"

	got := c.Dependencies()
	if got[0].Version != "v1.0.0" {
		t.Fatalf("Dependencies() = %v, cycle state mutated by caller", got)
	}
}

func TestImmutability_MutatingReturnedDependenciesDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmApply(t)

	got := c.Dependencies()
	got[0].Version = "v9.9.9"

	got2 := c.Dependencies()
	if got2[0].Version == "v9.9.9" {
		t.Fatal("mutating returned Dependencies() affected cycle state")
	}
}

func TestImmutability_MutatingReturnedEntriesDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmApply(t)

	got := c.Entries()
	got[0].NewVersion = "v9.9.9"

	got2 := c.Entries()
	if got2[0].NewVersion == "v9.9.9" {
		t.Fatal("mutating returned Entries() affected cycle state")
	}
}

func TestImmutability_MutatingReturnedSnapshotDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmChecks(t)

	snap := c.Snapshot()
	snap.ModFile.Content = "mutated"

	snap2 := c.Snapshot()
	if snap2.ModFile.Content == "mutated" {
		t.Fatal("mutating returned Snapshot() affected cycle state")
	}
}

func TestImmutability_MutatingSnapshotUpdatableDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmChecks(t)

	snap := c.Snapshot()
	snap.Updatable[0].NewVersion = "v9.9.9"

	snap2 := c.Snapshot()
	if snap2.Updatable[0].NewVersion == "v9.9.9" {
		t.Fatal("mutating returned Snapshot().Updatable affected cycle state")
	}
}

func TestImmutability_MutatingIntentEntriesDoesNotAffectCycle(t *testing.T) {
	c := NewUpdateCycle()
	c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
	c, intent, _ := c.Handle(CheckUpdatesDoneEvent{Dependencies: updatableDeps()})
	ca := intent.(IntentConfirmApply)

	ca.Entries[0].NewVersion = "v9.9.9"

	if c.Entries()[0].NewVersion == "v9.9.9" {
		t.Fatal("mutating IntentConfirmApply.Entries affected cycle state")
	}
}

func TestImmutability_MutatingReturnedBackupDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmChecks(t)

	b := c.Backup()
	b.Name = "mutated.json"

	b2 := c.Backup()
	if b2.Name == "mutated.json" {
		t.Fatal("mutating returned Backup() affected cycle state")
	}
}

func TestImmutability_MutatingReturnedCheckResultDoesNotAffectCycle(t *testing.T) {
	c := driveToConfirmRollback(t)

	cr := c.CheckResult()
	cr.Command = "mutated"

	cr2 := c.CheckResult()
	if cr2.Command == "mutated" {
		t.Fatal("mutating returned CheckResult() affected cycle state")
	}
}

func TestImmutability_MutatingEventSnapshotDoesNotAffectCycle(t *testing.T) {
	snap := testSnapshot()
	c := driveToApplying(t)
	c, _, _ = c.Handle(ApplyUpdatesDoneEvent{
		Snapshot:     snap,
		Backup:       testBackup(),
		Dependencies: updatableDeps(),
	})

	snap.ModFile.Content = "mutated"

	got := c.Snapshot()
	if got.ModFile.Content == "mutated" {
		t.Fatal("mutating event Snapshot affected cycle state")
	}
}

// Invalid transitions

func TestInvalidTransition_StateUnchanged(t *testing.T) {
	c := NewUpdateCycle()
	original := c

	next, intent, err := c.Handle(CheckUpdatesDoneEvent{Dependencies: updatableDeps()})
	if err == nil {
		t.Fatal("expected InvalidTransitionError")
	}
	var ite InvalidTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("error = %v, want InvalidTransitionError", err)
	}
	if ite.Phase != PhaseIdle || ite.Event != "check-updates-done" {
		t.Fatalf("InvalidTransitionError = %+v", ite)
	}
	if next.Phase() != original.Phase() {
		t.Fatalf("phase changed: %s -> %s", original.Phase(), next.Phase())
	}
	assertNoIntent(t, intent)
}

func TestHandleNilEventReturnsInvalidTransition(t *testing.T) {
	c := NewUpdateCycle()

	next, intent, err := c.Handle(nil)
	var transitionErr InvalidTransitionError
	if !errors.As(err, &transitionErr) || transitionErr.Event != "<nil>" {
		t.Fatalf("Handle(nil) error = %v", err)
	}
	if next.Phase() != c.Phase() {
		t.Fatalf("phase changed: %s -> %s", c.Phase(), next.Phase())
	}
	assertNoIntent(t, intent)
}

func TestInvalidTransitions_FromEveryPhase(t *testing.T) {
	tests := []struct {
		name  string
		cycle UpdateCycle
		event Event
	}{
		{"idle rejects confirm-apply", NewUpdateCycle(), ConfirmApplyEvent{Yes: true}},
		{"idle rejects start twice", func() UpdateCycle {
			c, _, _ := NewUpdateCycle().Handle(StartEvent{ModuleDir: "/mod"})
			return c
		}(), StartEvent{ModuleDir: "/mod"}},
		{"checking rejects confirm-apply", func() UpdateCycle {
			c, _, _ := NewUpdateCycle().Handle(StartEvent{ModuleDir: "/mod"})
			return c
		}(), ConfirmApplyEvent{Yes: true}},
		{"confirm-apply rejects checks-done", driveToConfirmApply(t), ChecksDoneEvent{}},
		{"applying rejects confirm-checks", driveToApplying(t), ConfirmChecksEvent{Yes: true}},
		{"compensating rejects start", func() UpdateCycle {
			c := driveToApplying(t)
			c, _, _ = c.Handle(ApplyUpdatesDoneEvent{
				Snapshot: testSnapshot(),
				Backup:   testBackup(),
				Err:      errors.New("fail"),
			})
			return c
		}(), StartEvent{}},
		{"confirm-checks rejects rollback-done", driveToConfirmChecks(t), RollbackDoneEvent{}},
		{"running-checks rejects confirm-rollback", driveToRunningChecks(t), ConfirmRollbackEvent{Yes: true}},
		{"confirm-rollback rejects check-updates-done", driveToConfirmRollback(t), CheckUpdatesDoneEvent{}},
		{"rolling-back rejects confirm-apply", func() UpdateCycle {
			c := driveToConfirmRollback(t)
			c, _, _ = c.Handle(ConfirmRollbackEvent{Yes: true})
			return c
		}(), ConfirmApplyEvent{Yes: true}},
		{"terminal rejects start", func() UpdateCycle {
			c := NewUpdateCycle()
			c, _, _ = c.Handle(StartEvent{ModuleDir: "/mod"})
			c, _, _ = c.Handle(CheckUpdatesDoneEvent{Dependencies: currentDeps()})
			return c
		}(), StartEvent{ModuleDir: "/mod"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevPhase := tt.cycle.Phase()
			next, intent, err := tt.cycle.Handle(tt.event)
			if err == nil {
				t.Fatal("expected InvalidTransitionError")
			}
			var ite InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Fatalf("error type = %T, want InvalidTransitionError", err)
			}
			if next.Phase() != prevPhase {
				t.Fatalf("phase changed: %s -> %s", prevPhase, next.Phase())
			}
			if _, ok := intent.(NoIntent); !ok {
				t.Fatalf("intent = %T, want NoIntent", intent)
			}
		})
	}
}

// Drive helpers

func driveToConfirmApply(t *testing.T) UpdateCycle {
	t.Helper()
	c := NewUpdateCycle()
	c, _, err := c.Handle(StartEvent{ModuleDir: "/mod"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	c, _, err = c.Handle(CheckUpdatesDoneEvent{Dependencies: updatableDeps()})
	if err != nil {
		t.Fatalf("check-done: %v", err)
	}
	assertPhase(t, c, PhaseConfirmApply)
	return c
}

func driveToApplying(t *testing.T) UpdateCycle {
	t.Helper()
	c := driveToConfirmApply(t)
	c, _, err := c.Handle(ConfirmApplyEvent{Yes: true})
	if err != nil {
		t.Fatalf("confirm-apply: %v", err)
	}
	assertPhase(t, c, PhaseApplying)
	return c
}

func driveToConfirmChecks(t *testing.T) UpdateCycle {
	t.Helper()
	c := driveToApplying(t)
	c, _, err := c.Handle(ApplyUpdatesDoneEvent{
		Snapshot:     testSnapshot(),
		Backup:       testBackup(),
		Dependencies: updatableDeps(),
	})
	if err != nil {
		t.Fatalf("apply-done: %v", err)
	}
	assertPhase(t, c, PhaseConfirmChecks)
	return c
}

func driveToRunningChecks(t *testing.T) UpdateCycle {
	t.Helper()
	c := driveToConfirmChecks(t)
	c, _, err := c.Handle(ConfirmChecksEvent{Yes: true})
	if err != nil {
		t.Fatalf("confirm-checks: %v", err)
	}
	assertPhase(t, c, PhaseRunningChecks)
	return c
}

func driveToConfirmRollback(t *testing.T) UpdateCycle {
	t.Helper()
	c := driveToRunningChecks(t)
	c, _, err := c.Handle(ChecksDoneEvent{
		Result: DependencyCheckResult{OK: false, Command: "go test ./..."},
	})
	if err != nil {
		t.Fatalf("checks-done: %v", err)
	}
	assertPhase(t, c, PhaseConfirmRollback)
	return c
}

// Assert helpers

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertPhase(t *testing.T, c UpdateCycle, want Phase) {
	t.Helper()
	if c.Phase() != want {
		t.Fatalf("Phase() = %s, want %s", c.Phase(), want)
	}
}

func assertTerminal(t *testing.T, c UpdateCycle, want Outcome) {
	t.Helper()
	if !c.IsTerminal() {
		t.Fatalf("not terminal, phase = %s", c.Phase())
	}
	if c.Outcome() != want {
		t.Fatalf("Outcome() = %s, want %s", c.Outcome(), want)
	}
}

func assertTerminalCleanup(t *testing.T, c UpdateCycle) {
	t.Helper()
	if c.Snapshot() != nil {
		t.Fatalf("Snapshot() should be nil after terminal transition")
	}
	if c.CheckResult() != nil {
		t.Fatalf("CheckResult() should be nil after terminal transition")
	}
}

func assertNoIntent(t *testing.T, intent Intent) {
	t.Helper()
	if _, ok := intent.(NoIntent); !ok {
		t.Fatalf("intent = %T, want NoIntent", intent)
	}
}

func assertIntent[T Intent](t *testing.T, intent Intent) T {
	t.Helper()
	got, ok := intent.(T)
	if !ok {
		t.Fatalf("intent = %T, want %T", intent, *new(T))
	}
	return got
}
