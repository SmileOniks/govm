package deps

import (
	"errors"
	"testing"
)

// mockOps is a configurable Operations implementation for testing the
// Executor. Each function field defaults to a no-op or zero return;
// tests set the fields they need and optionally inspect recorded calls.
type mockOps struct {
	checkUpdatesFn func(moduleContext) ([]ModuleDependency, error)
	applyUpdatesFn func(moduleContext, []DependencyUpdateEntry, int) (
		*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
	)
	restoreExactFn  func(moduleContext, *DependencySnapshot) ([]ModuleDependency, error)
	runChecksFn     func(moduleContext) (DependencyCheckResult, error)
	listBackupsFn   func(moduleContext) ([]DependencyBackupInfo, error)
	restoreBackupFn func(moduleContext, string, int) (DependencyRestoreResult, error)

	// checkUpdatesCalls records the roots of Load calls made online;
	// listCalls those made offline.
	checkUpdatesCalls []string
	listCalls         []string
	applyEntries      []DependencyUpdateEntry
	applyLimits       []int
	restoreCalls      []restoreCall
	checksCalls       []string
	listBackupsCalls  []string
	restoreBackups    []restoreBackupCall
}

type restoreCall struct {
	moduleDir string
	snapshot  *DependencySnapshot
}

type restoreBackupCall struct {
	moduleDir string
	name      string
	limit     int
}

func (m *mockOps) Load(context moduleContext, checkUpdates bool) ([]ModuleDependency, error) {
	if !checkUpdates {
		m.listCalls = append(m.listCalls, context.Root)
		return nil, nil
	}
	m.checkUpdatesCalls = append(m.checkUpdatesCalls, context.Root)
	if m.checkUpdatesFn != nil {
		return m.checkUpdatesFn(context)
	}
	return nil, nil
}

func (m *mockOps) ListBackups(context moduleContext) ([]DependencyBackupInfo, error) {
	m.listBackupsCalls = append(m.listBackupsCalls, context.Root)
	if m.listBackupsFn != nil {
		return m.listBackupsFn(context)
	}
	return nil, nil
}

func (m *mockOps) RestoreBackup(context moduleContext, name string, limit int) (DependencyRestoreResult, error) {
	m.restoreBackups = append(m.restoreBackups, restoreBackupCall{moduleDir: context.Root, name: name, limit: limit})
	if m.restoreBackupFn != nil {
		return m.restoreBackupFn(context, name, limit)
	}
	return DependencyRestoreResult{}, nil
}

func (m *mockOps) ApplyUpdates(
	context moduleContext,
	entries []DependencyUpdateEntry,
	backupLimit int,
) (*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error) {
	m.applyEntries = entries
	m.applyLimits = append(m.applyLimits, backupLimit)
	if m.applyUpdatesFn != nil {
		return m.applyUpdatesFn(context, entries, backupLimit)
	}
	return nil, nil, nil, nil
}

func (m *mockOps) RestoreExact(
	context moduleContext,
	snapshot *DependencySnapshot,
) ([]ModuleDependency, error) {
	m.restoreCalls = append(m.restoreCalls, restoreCall{moduleDir: context.Root, snapshot: snapshot})
	if m.restoreExactFn != nil {
		return m.restoreExactFn(context, snapshot)
	}
	return nil, nil
}

func (m *mockOps) RunChecks(context moduleContext) (DependencyCheckResult, error) {
	m.checksCalls = append(m.checksCalls, context.Root)
	if m.runChecksFn != nil {
		return m.runChecksFn(context)
	}
	return DependencyCheckResult{}, nil
}

func mustExecute(t *testing.T, exec *Executor, intent Intent) Event {
	t.Helper()
	event, err := exec.Execute(intent)
	if err != nil {
		t.Fatalf("Execute(%T) returned error: %v", intent, err)
	}
	return event
}

func TestExecutor_CheckUpdates(t *testing.T) {
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return updatableDeps(), nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentCheckUpdates{}).(CheckUpdatesDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if done.Err != nil {
		t.Fatalf("Err = %v", done.Err)
	}
	if len(done.Dependencies) != 3 {
		t.Fatalf("Dependencies = %d, want 3", len(done.Dependencies))
	}
	if len(ops.checkUpdatesCalls) != 1 || ops.checkUpdatesCalls[0] != "/test/root" {
		t.Fatalf("checkUpdatesCalls = %v, want [/test/root]", ops.checkUpdatesCalls)
	}
}

func TestExecutor_CheckUpdates_PropagatesError(t *testing.T) {
	checkErr := errors.New("network down")
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return nil, checkErr
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentCheckUpdates{}).(CheckUpdatesDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if !errors.Is(done.Err, checkErr) {
		t.Fatalf("Err = %v, want %v", done.Err, checkErr)
	}
}

func TestExecutor_ApplyUpdates_Success(t *testing.T) {
	entries := []DependencyUpdateEntry{
		{Path: "example.com/dep", OldVersion: "v1.0.0", NewVersion: "v1.1.0"},
	}
	snap := testSnapshot()
	backup := DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"}
	refreshed := []ModuleDependency{{Path: "example.com/dep", Version: "v1.1.0"}}

	ops := &mockOps{
		applyUpdatesFn: func(_ moduleContext, _ []DependencyUpdateEntry, _ int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			return snap, &backup, refreshed, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(7)

	done, ok := mustExecute(t, exec, IntentApplyUpdates{Entries: entries}).(ApplyUpdatesDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if done.Err != nil {
		t.Fatalf("Err = %v", done.Err)
	}
	if done.Snapshot != snap {
		t.Fatal("Snapshot mismatch")
	}
	if done.Backup == nil || done.Backup.Name != "backup.json" {
		t.Fatalf("Backup = %+v, want backup.json", done.Backup)
	}
	if len(done.Dependencies) != 1 || done.Dependencies[0].Version != "v1.1.0" {
		t.Fatalf("Dependencies = %+v", done.Dependencies)
	}
	if len(ops.applyEntries) != 1 || ops.applyEntries[0].Path != "example.com/dep" {
		t.Fatalf("applyEntries = %+v", ops.applyEntries)
	}
	if len(ops.applyLimits) != 1 || ops.applyLimits[0] != 7 {
		t.Fatalf("applyLimits = %+v, want [7]", ops.applyLimits)
	}
}

func TestExecutor_ApplyUpdates_Failure_StillReturnsSnapshotAndBackup(t *testing.T) {
	entries := []DependencyUpdateEntry{
		{Path: "example.com/dep", OldVersion: "v1.0.0", NewVersion: "v1.1.0"},
	}
	snap := testSnapshot()
	backup := DependencyBackupInfo{Name: "backup.json", Path: "/tmp/backup.json"}
	applyErr := errors.New("go get failed")

	ops := &mockOps{
		applyUpdatesFn: func(moduleContext, []DependencyUpdateEntry, int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			return snap, &backup, nil, applyErr
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(3)

	done, ok := mustExecute(t, exec, IntentApplyUpdates{Entries: entries}).(ApplyUpdatesDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if !errors.Is(done.Err, applyErr) {
		t.Fatalf("Err = %v, want %v", done.Err, applyErr)
	}
	if done.Snapshot == nil {
		t.Fatal("Snapshot should be set even on failure")
	}
	if done.Backup == nil {
		t.Fatal("Backup should be set even on failure")
	}
}

func TestExecutor_Compensate(t *testing.T) {
	snap := testSnapshot()
	restored := []ModuleDependency{{Path: "example.com/dep", Version: "v1.0.0"}}
	ops := &mockOps{
		restoreExactFn: func(_ moduleContext, _ *DependencySnapshot) ([]ModuleDependency, error) {
			return restored, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentCompensate{Snapshot: snap}).(CompensateDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if done.Err != nil {
		t.Fatalf("Err = %v", done.Err)
	}
	if len(done.Dependencies) != 1 {
		t.Fatalf("Dependencies = %d, want 1", len(done.Dependencies))
	}
	if len(ops.restoreCalls) != 1 {
		t.Fatalf("restoreCalls = %d, want 1", len(ops.restoreCalls))
	}
	if ops.restoreCalls[0].snapshot != snap {
		t.Fatal("snapshot not forwarded to RestoreExact")
	}
}

func TestExecutor_RunChecks(t *testing.T) {
	ops := &mockOps{
		runChecksFn: func(moduleContext) (DependencyCheckResult, error) {
			return DependencyCheckResult{OK: true}, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentRunChecks{}).(ChecksDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if done.Err != nil {
		t.Fatalf("Err = %v", done.Err)
	}
	if !done.Result.OK {
		t.Fatal("Result.OK = false, want true")
	}
}

func TestExecutor_Rollback(t *testing.T) {
	snap := testSnapshot()
	restored := []ModuleDependency{{Path: "example.com/dep", Version: "v1.0.0"}}
	ops := &mockOps{
		restoreExactFn: func(_ moduleContext, _ *DependencySnapshot) ([]ModuleDependency, error) {
			return restored, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentRollback{Snapshot: snap}).(RollbackDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if done.Err != nil {
		t.Fatalf("Err = %v", done.Err)
	}
	if len(done.Dependencies) != 1 {
		t.Fatalf("Dependencies = %d, want 1", len(done.Dependencies))
	}
}

func TestExecutor_Rollback_PropagatesError(t *testing.T) {
	snap := testSnapshot()
	restoreErr := errors.New("restore failed")
	ops := &mockOps{
		restoreExactFn: func(moduleContext, *DependencySnapshot) ([]ModuleDependency, error) {
			return nil, restoreErr
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	done, ok := mustExecute(t, exec, IntentRollback{Snapshot: snap}).(RollbackDoneEvent)
	if !ok {
		t.Fatalf("event type mismatch")
	}
	if !errors.Is(done.Err, restoreErr) {
		t.Fatalf("Err = %v, want %v", done.Err, restoreErr)
	}
}

func TestExecutor_NonOperationalIntentReturnsError(t *testing.T) {
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, nil).WithBackupLimit(5)

	tests := []Intent{
		nil,
		NoIntent{},
		IntentConfirmApply{},
		IntentConfirmChecks{},
		IntentConfirmRollback{},
	}
	for _, intent := range tests {
		event, err := exec.Execute(intent)
		if err == nil {
			t.Errorf("Execute(%T) error = nil, want InvalidIntentError", intent)
			continue
		}
		var iie InvalidIntentError
		if !errors.As(err, &iie) {
			t.Errorf("Execute(%T) error = %T, want InvalidIntentError", intent, err)
		}
		if event != nil {
			t.Errorf("Execute(%T) event = %T, want nil", intent, event)
		}
	}
}

func TestNewExecutor_NilOpsUsesDefault(t *testing.T) {
	exec := NewExecutor(t.TempDir(), nil)
	if exec.ops == nil {
		t.Fatal("ops should be defaultOperations, not nil")
	}
	if exec.backupLimit != DefaultBackupLimit {
		t.Fatalf("backupLimit = %d, want %d", exec.backupLimit, DefaultBackupLimit)
	}
}

func TestNewExecutor_RespectsBackupLimit(t *testing.T) {
	ops := &mockOps{}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(42)
	if exec.backupLimit != 42 {
		t.Fatalf("backupLimit = %d, want 42", exec.backupLimit)
	}
	if low := exec.WithBackupLimit(0); low.backupLimit != DefaultBackupLimit {
		t.Fatalf("WithBackupLimit(0) = %d, want %d", low.backupLimit, DefaultBackupLimit)
	}
}

func TestExecutor_StandaloneOperationsDelegateToOps(t *testing.T) {
	ops := &mockOps{
		listBackupsFn: func(moduleContext) ([]DependencyBackupInfo, error) {
			return []DependencyBackupInfo{{Name: "b.json"}}, nil
		},
		restoreBackupFn: func(_ moduleContext, name string, _ int) (DependencyRestoreResult, error) {
			return DependencyRestoreResult{BackupName: name}, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(7)

	if _, err := exec.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := exec.CheckUpdates(); err != nil {
		t.Fatalf("CheckUpdates: %v", err)
	}
	backups, err := exec.Backups()
	if err != nil || len(backups) != 1 || backups[0].Name != "b.json" {
		t.Fatalf("Backups = %v, %v", backups, err)
	}
	restored, err := exec.Restore("b.json")
	if err != nil || restored.BackupName != "b.json" {
		t.Fatalf("Restore = %+v, %v", restored, err)
	}

	if len(ops.listCalls) != 1 || ops.listCalls[0] != "/test/root" {
		t.Fatalf("listCalls = %v, want [/test/root]", ops.listCalls)
	}
	if len(ops.checkUpdatesCalls) != 1 {
		t.Fatalf("checkUpdatesCalls = %v, want one call", ops.checkUpdatesCalls)
	}
	if len(ops.listBackupsCalls) != 1 {
		t.Fatalf("listBackupsCalls = %v, want one call", ops.listBackupsCalls)
	}
	want := restoreBackupCall{moduleDir: "/test/root", name: "b.json", limit: 7}
	if len(ops.restoreBackups) != 1 || ops.restoreBackups[0] != want {
		t.Fatalf("restoreBackups = %+v, want [%+v]", ops.restoreBackups, want)
	}
}

func TestExecutor_ResolvesModuleOnceAndSharesAcrossCopies(t *testing.T) {
	resolves := 0
	module := &lazyModuleContext{resolve: func() (moduleContext, error) {
		resolves++
		return moduleContext{Root: "/test/root", Path: "example.com/test"}, nil
	}}
	exec := newExecutor(module, &mockOps{})

	if _, err := exec.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := exec.WithBackupLimit(3).Backups(); err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if _, err := exec.Execute(IntentRunChecks{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resolves != 1 {
		t.Fatalf("module resolved %d times, want 1", resolves)
	}
}

func TestExecutor_UnresolvedModuleErrorIsStickyAndSurfacesFromEveryOperation(t *testing.T) {
	resolves := 0
	resolveErr := errors.New("not in a Go module")
	module := &lazyModuleContext{resolve: func() (moduleContext, error) {
		resolves++
		return moduleContext{}, resolveErr
	}}
	ops := &mockOps{}
	exec := newExecutor(module, ops)

	if _, err := exec.List(); !errors.Is(err, resolveErr) {
		t.Fatalf("List error = %v, want %v", err, resolveErr)
	}
	if _, err := exec.CheckUpdates(); !errors.Is(err, resolveErr) {
		t.Fatalf("CheckUpdates error = %v, want %v", err, resolveErr)
	}
	if _, err := exec.Backups(); !errors.Is(err, resolveErr) {
		t.Fatalf("Backups error = %v, want %v", err, resolveErr)
	}
	if _, err := exec.Restore("b.json"); !errors.Is(err, resolveErr) {
		t.Fatalf("Restore error = %v, want %v", err, resolveErr)
	}
	event, err := exec.Execute(IntentCheckUpdates{})
	if !errors.Is(err, resolveErr) || event != nil {
		t.Fatalf("Execute = %v, %v, want nil event and %v", event, err, resolveErr)
	}
	if resolves != 1 {
		t.Fatalf("module resolved %d times, want 1", resolves)
	}
	if len(ops.listCalls)+len(ops.checkUpdatesCalls)+len(ops.listBackupsCalls)+len(ops.restoreBackups) != 0 {
		t.Fatal("no operation may reach ops when the module is unresolved")
	}
}

func TestNewExecutor_OutsideModuleFailsOnFirstOperation(t *testing.T) {
	exec := NewExecutor(t.TempDir(), &mockOps{})
	if _, err := exec.List(); err == nil {
		t.Fatal("List outside a Go module should fail")
	}
}

func TestIsOperational(t *testing.T) {
	operational := []Intent{
		IntentCheckUpdates{},
		IntentApplyUpdates{},
		IntentCompensate{},
		IntentRunChecks{},
		IntentRollback{},
	}
	for _, intent := range operational {
		if !IsOperational(intent) {
			t.Errorf("IsOperational(%T) = false, want true", intent)
		}
	}

	nonOperational := []Intent{
		NoIntent{},
		IntentConfirmApply{},
		IntentConfirmChecks{},
		IntentConfirmRollback{},
	}
	for _, intent := range nonOperational {
		if IsOperational(intent) {
			t.Errorf("IsOperational(%T) = true, want false", intent)
		}
	}
}

// End-to-end cycle + executor integration with mock Operations.

func TestEndToEnd_UpdatedVerified(t *testing.T) {
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return updatableDeps(), nil
		},
		applyUpdatesFn: func(_ moduleContext, entries []DependencyUpdateEntry, _ int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			snap := testSnapshot()
			snap.Updatable = entries
			backup := DependencyBackupInfo{Name: "b.json", Path: "/b.json"}
			refreshed := []ModuleDependency{{Path: "example.com/updatable", Version: "v1.1.0"}}
			return snap, &backup, refreshed, nil
		},
		runChecksFn: func(moduleContext) (DependencyCheckResult, error) {
			return DependencyCheckResult{OK: true}, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	c := NewUpdateCycle()
	c, intent, _ := c.Handle(StartEvent{ModuleDir: "/mod"})

	c, intent = step(t, exec, c, intent) // check -> confirm-apply
	if c.Phase() != PhaseConfirmApply {
		t.Fatalf("after check: phase = %s, want confirm-apply", c.Phase())
	}

	c, intent, _ = c.Handle(ConfirmApplyEvent{Yes: true})
	c, intent = step(t, exec, c, intent) // apply -> confirm-checks
	if c.Phase() != PhaseConfirmChecks {
		t.Fatalf("after apply: phase = %s, want confirm-checks", c.Phase())
	}

	c, intent, _ = c.Handle(ConfirmChecksEvent{Yes: true})
	c, intent = step(t, exec, c, intent) // checks -> terminal
	if !c.IsTerminal() {
		t.Fatalf("after checks: phase = %s, want terminal", c.Phase())
	}
	if c.Outcome() != OutcomeUpdatedVerified {
		t.Fatalf("outcome = %s, want updated-verified", c.Outcome())
	}
}

func TestEndToEnd_RolledBack(t *testing.T) {
	restoredDeps := []ModuleDependency{{Path: "example.com/updatable", Version: "v1.0.0"}}
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return updatableDeps(), nil
		},
		applyUpdatesFn: func(_ moduleContext, entries []DependencyUpdateEntry, _ int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			snap := testSnapshot()
			snap.Updatable = entries
			backup := DependencyBackupInfo{Name: "b.json", Path: "/b.json"}
			return snap, &backup, updatableDeps(), nil
		},
		runChecksFn: func(moduleContext) (DependencyCheckResult, error) {
			return DependencyCheckResult{OK: false, Command: "go test ./..."}, nil
		},
		restoreExactFn: func(moduleContext, *DependencySnapshot) ([]ModuleDependency, error) {
			return restoredDeps, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	c := NewUpdateCycle()
	c, intent, _ := c.Handle(StartEvent{ModuleDir: "/mod"})

	c, intent = step(t, exec, c, intent) // check
	c, _, _ = c.Handle(ConfirmApplyEvent{Yes: true})
	c, intent = stepApply(t, exec, c)
	c, _, _ = c.Handle(ConfirmChecksEvent{Yes: true})
	c, intent = stepChecks(t, exec, c) // checks fail -> confirm-rollback
	c, intent, _ = c.Handle(ConfirmRollbackEvent{Yes: true})
	c, intent = step(t, exec, c, intent) // rollback

	if !c.IsTerminal() {
		t.Fatalf("phase = %s, want terminal", c.Phase())
	}
	if c.Outcome() != OutcomeRolledBack {
		t.Fatalf("outcome = %s, want rolled-back", c.Outcome())
	}
	if len(c.Dependencies()) != 1 || c.Dependencies()[0].Version != "v1.0.0" {
		t.Fatalf("Dependencies = %+v, want restored v1.0.0", c.Dependencies())
	}
}

func TestEndToEnd_UpdateFailedRestored(t *testing.T) {
	restoredDeps := []ModuleDependency{{Path: "example.com/updatable", Version: "v1.0.0"}}
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return updatableDeps(), nil
		},
		applyUpdatesFn: func(_ moduleContext, entries []DependencyUpdateEntry, _ int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			snap := testSnapshot()
			snap.Updatable = entries
			backup := DependencyBackupInfo{Name: "b.json", Path: "/b.json"}
			return snap, &backup, nil, errors.New("go get failed: exit 1")
		},
		restoreExactFn: func(moduleContext, *DependencySnapshot) ([]ModuleDependency, error) {
			return restoredDeps, nil
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	c := NewUpdateCycle()
	c, intent, _ := c.Handle(StartEvent{ModuleDir: "/mod"})

	c, intent = step(t, exec, c, intent) // check
	c, _, _ = c.Handle(ConfirmApplyEvent{Yes: true})
	c, intent = stepApply(t, exec, c)    // apply fails -> compensating
	c, intent = step(t, exec, c, intent) // compensate succeeds -> terminal

	if !c.IsTerminal() {
		t.Fatalf("phase = %s, want terminal", c.Phase())
	}
	if c.Outcome() != OutcomeUpdateFailedRestored {
		t.Fatalf("outcome = %s, want update-failed-restored", c.Outcome())
	}
}

func TestEndToEnd_RecoveryRequired_CompensationFailed(t *testing.T) {
	ops := &mockOps{
		checkUpdatesFn: func(moduleContext) ([]ModuleDependency, error) {
			return updatableDeps(), nil
		},
		applyUpdatesFn: func(_ moduleContext, entries []DependencyUpdateEntry, _ int) (
			*DependencySnapshot, *DependencyBackupInfo, []ModuleDependency, error,
		) {
			snap := testSnapshot()
			snap.Updatable = entries
			backup := DependencyBackupInfo{Name: "b.json", Path: "/b.json"}
			return snap, &backup, nil, errors.New("go get failed")
		},
		restoreExactFn: func(moduleContext, *DependencySnapshot) ([]ModuleDependency, error) {
			return nil, errors.New("restore failed")
		},
	}
	context := moduleContext{Root: "/test/root", Path: "example.com/test"}
	exec := NewExecutorWithContext(context, ops).WithBackupLimit(5)

	c := NewUpdateCycle()
	c, intent, _ := c.Handle(StartEvent{ModuleDir: "/mod"})

	c, intent = step(t, exec, c, intent) // check
	c, _, _ = c.Handle(ConfirmApplyEvent{Yes: true})
	c, intent = stepApply(t, exec, c)    // apply fails -> compensating
	c, intent = step(t, exec, c, intent) // compensate fails -> terminal

	if !c.IsTerminal() {
		t.Fatalf("phase = %s, want terminal", c.Phase())
	}
	if c.Outcome() != OutcomeRecoveryRequired {
		t.Fatalf("outcome = %s, want recovery-required", c.Outcome())
	}
	if c.Backup() == nil || c.Backup().Name != "b.json" {
		t.Fatalf("Backup = %+v, want b.json retained", c.Backup())
	}
}

// step executes one operational intent through the executor and feeds
// the resulting event back into the cycle. For non-operational intents
// it is a no-op.
func step(t *testing.T, exec *Executor, c UpdateCycle, intent Intent) (UpdateCycle, Intent) {
	t.Helper()
	if !IsOperational(intent) {
		return c, intent
	}
	event, err := exec.Execute(intent)
	if err != nil {
		t.Fatalf("step: Execute(%T) error: %v", intent, err)
	}
	next, nextIntent, _ := c.Handle(event)
	return next, nextIntent
}

func stepApply(t *testing.T, exec *Executor, c UpdateCycle) (UpdateCycle, Intent) {
	t.Helper()
	return step(t, exec, c, IntentApplyUpdates{Entries: c.Entries()})
}

func stepChecks(t *testing.T, exec *Executor, c UpdateCycle) (UpdateCycle, Intent) {
	t.Helper()
	return step(t, exec, c, IntentRunChecks{})
}
