package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SmileOniks/govm/internal/deps"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// fakeExecutor is the Deps seam substitute. Each operational intent is
// routed to a configurable function field that returns the raw event
// the cycle should observe; execErr short-circuits every operational
// intent with an error (used to exercise the executor failure path).
// The standalone operations return the configured function's result
// or zero values.
type fakeExecutor struct {
	list    func() ([]deps.ModuleDependency, error)
	backups func() ([]deps.DependencyBackupInfo, error)
	restore func(name string) (deps.DependencyRestoreResult, error)

	checkUpdates func(deps.IntentCheckUpdates) deps.Event
	applyUpdates func(deps.IntentApplyUpdates) deps.Event
	compensate   func(deps.IntentCompensate) deps.Event
	runChecks    func(deps.IntentRunChecks) deps.Event
	rollback     func(deps.IntentRollback) deps.Event
	execErr      error

	checkCalls      int
	applyCalls      int
	compensateCalls int
	checksCalls     int
	rollbackCalls   int
	applyEntries    []deps.DependencyUpdateEntry
}

func (f *fakeExecutor) List() ([]deps.ModuleDependency, error) {
	if f.list != nil {
		return f.list()
	}
	return nil, nil
}

func (f *fakeExecutor) CheckUpdates() ([]deps.ModuleDependency, error) {
	mods, err := f.Execute(deps.IntentCheckUpdates{})
	if err != nil {
		return nil, err
	}
	done, ok := mods.(deps.CheckUpdatesDoneEvent)
	if !ok {
		return nil, nil
	}
	return done.Dependencies, done.Err
}

func (f *fakeExecutor) Backups() ([]deps.DependencyBackupInfo, error) {
	if f.backups != nil {
		return f.backups()
	}
	return nil, nil
}

func (f *fakeExecutor) Restore(name string) (deps.DependencyRestoreResult, error) {
	if f.restore != nil {
		return f.restore(name)
	}
	return deps.DependencyRestoreResult{}, nil
}

func (f *fakeExecutor) Execute(intent deps.Intent) (deps.Event, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	switch i := intent.(type) {
	case deps.IntentCheckUpdates:
		f.checkCalls++
		if f.checkUpdates != nil {
			return f.checkUpdates(i), nil
		}
		return deps.CheckUpdatesDoneEvent{}, nil
	case deps.IntentApplyUpdates:
		f.applyCalls++
		f.applyEntries = i.Entries
		if f.applyUpdates != nil {
			return f.applyUpdates(i), nil
		}
		return deps.ApplyUpdatesDoneEvent{}, nil
	case deps.IntentCompensate:
		f.compensateCalls++
		if f.compensate != nil {
			return f.compensate(i), nil
		}
		return deps.CompensateDoneEvent{}, nil
	case deps.IntentRunChecks:
		f.checksCalls++
		if f.runChecks != nil {
			return f.runChecks(i), nil
		}
		return deps.ChecksDoneEvent{}, nil
	case deps.IntentRollback:
		f.rollbackCalls++
		if f.rollback != nil {
			return f.rollback(i), nil
		}
		return deps.RollbackDoneEvent{}, nil
	default:
		return nil, fmt.Errorf("fakeExecutor: unexpected intent %T", intent)
	}
}

func updatableDeps() []deps.ModuleDependency {
	return []deps.ModuleDependency{
		{Path: "github.com/d/x", Version: "v1.0.0", Latest: "v1.1.0"},
	}
}

func currentDeps() []deps.ModuleDependency {
	return []deps.ModuleDependency{
		{Path: "github.com/d/x", Version: "v1.0.0", Latest: "v1.0.0"},
	}
}

func cliSnapshot() *deps.DependencySnapshot {
	return &deps.DependencySnapshot{
		ModFile:   deps.ModuleFileSnapshot{Exists: true, Content: "module github.com/d/m\n"},
		Updatable: []deps.DependencyUpdateEntry{{Path: "github.com/d/x", OldVersion: "v1.0.0", NewVersion: "v1.1.0"}},
	}
}

func cliBackup() *deps.DependencyBackupInfo {
	return &deps.DependencyBackupInfo{
		Name:       "2026-07-23_12-00-00.json",
		Path:       "/tmp/govm/2026-07-23_12-00-00.json",
		ModulePath: "github.com/d/m",
		Kind:       deps.DependencyBackupKindPreUpdate,
	}
}

// newUpdateService wires a DepsService for RunUpdate tests. The
// fakeExecutor defaults model the happy path (updatable deps,
// successful apply, passing checks, successful restore); individual
// tests override the fields they need.
func newUpdateService(confirm func(string, bool) (bool, error)) (*DepsService, *fakeExecutor, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	fx := &fakeExecutor{
		checkUpdates: func(deps.IntentCheckUpdates) deps.Event {
			return deps.CheckUpdatesDoneEvent{Dependencies: updatableDeps()}
		},
		applyUpdates: func(deps.IntentApplyUpdates) deps.Event {
			return deps.ApplyUpdatesDoneEvent{
				Snapshot:     cliSnapshot(),
				Backup:       cliBackup(),
				Dependencies: updatableDeps(),
			}
		},
		runChecks: func(deps.IntentRunChecks) deps.Event {
			return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: true}}
		},
		compensate: func(deps.IntentCompensate) deps.Event {
			return deps.CompensateDoneEvent{Dependencies: currentDeps()}
		},
		rollback: func(deps.IntentRollback) deps.Event {
			return deps.RollbackDoneEvent{Dependencies: currentDeps()}
		},
	}
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Stdin:     &bytes.Buffer{},
		Confirm:   confirm,
		Deps:      fx,
	}
	return svc, fx, stdout
}

// One-shot commands: RunList / RunCheck / RunBackups / RunRestore

func TestRunListPrintsDependencies(t *testing.T) {
	stdout := &bytes.Buffer{}
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Deps: &fakeExecutor{list: func() ([]deps.ModuleDependency, error) {
			return []deps.ModuleDependency{
				{Path: "github.com/d/x", Version: "v1.0.0"},
				{Path: "github.com/i/y", Version: "v0.5.0", Indirect: true},
			}, nil
		}},
	}

	if err := svc.RunList(); err != nil {
		t.Fatalf("RunList: %v", err)
	}
	want := "🔍 Reading module dependencies in /tmp/m...\n\n" +
		"  github.com/d/x\tv1.0.0\tdirect\n" +
		"  github.com/i/y\tv0.5.0\tindirect\n" +
		"\n✅ 1 direct, 1 indirect dependencies.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("RunList output mismatch\nwant:\n%q\ngot:\n%q", want, got)
	}
}

func TestRunCheckShowsUpdates(t *testing.T) {
	fx := &fakeExecutor{
		checkUpdates: func(deps.IntentCheckUpdates) deps.Event {
			return deps.CheckUpdatesDoneEvent{Dependencies: []deps.ModuleDependency{
				{Path: "github.com/d/x", Version: "v1.0.0", Latest: "v1.1.0"},
				{Path: "github.com/d/y", Version: "v1.0.0", Latest: "v1.0.0"},
			}}
		},
	}
	stdout := &bytes.Buffer{}
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Deps:      fx,
	}

	if err := svc.RunCheck(deps.LevelLatest); err != nil {
		t.Fatalf("RunCheck: %v", err)
	}
	want := "🔍 Checking available updates in /tmp/m...\n\n" +
		"  github.com/d/x\tv1.0.0 → v1.1.0\tupdate available\n" +
		"  github.com/d/y\tv1.0.0\tcurrent\n" +
		"\n📦 1 direct update(s) available.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("RunCheck output mismatch\nwant:\n%q\ngot:\n%q", want, got)
	}
	if fx.checkCalls != 1 {
		t.Fatalf("expected one check call, got %d", fx.checkCalls)
	}
}

func TestRunCheckExecutorEventError(t *testing.T) {
	fx := &fakeExecutor{
		checkUpdates: func(deps.IntentCheckUpdates) deps.Event {
			return deps.CheckUpdatesDoneEvent{Err: errors.New("offline")}
		},
	}
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    &bytes.Buffer{},
		Deps:      fx,
	}
	err := svc.RunCheck(deps.LevelLatest)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to check dependencies") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "offline") {
		t.Fatalf("expected underlying error surfaced, got: %v", err)
	}
}

func TestRunCheckExecutorError(t *testing.T) {
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    &bytes.Buffer{},
		Deps:      &fakeExecutor{execErr: errors.New("boom")},
	}
	err := svc.RunCheck(deps.LevelLatest)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got: %v", err)
	}
}

func TestRunBackupsPrintsNewestBackups(t *testing.T) {
	stdout := &bytes.Buffer{}
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Deps: &fakeExecutor{backups: func() ([]deps.DependencyBackupInfo, error) {
			return []deps.DependencyBackupInfo{{
				Name:       "2026-07-09_12-00-00.json",
				CreatedAt:  time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
				ModulePath: "github.com/acme/app",
				Kind:       deps.DependencyBackupKindPreUpdate,
				Updated:    2,
			}}, nil
		}},
	}

	if err := svc.RunBackups(); err != nil {
		t.Fatalf("RunBackups: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"2026-07-09_12-00-00.json", "github.com/acme/app", "pre-update", "2 update"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunRestoreUsesProvidedBackupName(t *testing.T) {
	confirmCalls := 0
	stdout := &bytes.Buffer{}
	var gotName string
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Confirm: func(string, bool) (bool, error) {
			confirmCalls++
			return true, nil
		},
		Deps: &fakeExecutor{restore: func(name string) (deps.DependencyRestoreResult, error) {
			gotName = name
			return deps.DependencyRestoreResult{
				BackupName:    name,
				BackupCreated: time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
			}, nil
		}},
	}

	if err := svc.RunRestore("2026-07-09_12-00-00.json"); err != nil {
		t.Fatalf("RunRestore: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("expected one confirmation, got %d", confirmCalls)
	}
	if gotName != "2026-07-09_12-00-00.json" {
		t.Fatalf("restore name = %q", gotName)
	}
	if !strings.Contains(stdout.String(), "Restored dependencies") {
		t.Fatalf("expected restore success, got:\n%s", stdout.String())
	}
}

func TestRunRestoreDeclineCancels(t *testing.T) {
	stdout := &bytes.Buffer{}
	restoreCalls := 0
	svc := &DepsService{
		ModuleDir: "/tmp/m",
		Stdout:    stdout,
		Confirm:   func(string, bool) (bool, error) { return false, nil },
		Deps: &fakeExecutor{restore: func(string) (deps.DependencyRestoreResult, error) {
			restoreCalls++
			return deps.DependencyRestoreResult{}, nil
		}},
	}
	if err := svc.RunRestore("x.json"); err != nil {
		t.Fatalf("RunRestore: %v", err)
	}
	if restoreCalls != 0 {
		t.Fatalf("expected restore skipped, got %d", restoreCalls)
	}
	if !strings.Contains(stdout.String(), "Restore canceled") {
		t.Fatalf("expected cancel message, got:\n%s", stdout.String())
	}
}

// RunUpdate driver loop

func TestRunUpdateNoDirectUpdates(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) {
		t.Fatal("Confirm should not be called when no updates")
		return false, nil
	})
	fx.checkUpdates = func(deps.IntentCheckUpdates) deps.Event {
		return deps.CheckUpdatesDoneEvent{Dependencies: currentDeps()}
	}

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if !strings.Contains(stdout.String(), "No direct dependency updates available") {
		t.Fatalf("expected message about no updates, got:\n%s", stdout.String())
	}
	if fx.applyCalls != 0 || fx.checksCalls != 0 || fx.rollbackCalls != 0 {
		t.Fatalf("expected no operations, got apply=%d checks=%d rollback=%d",
			fx.applyCalls, fx.checksCalls, fx.rollbackCalls)
	}
}

func TestRunUpdateDeclineUpdate(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) {
		t.Fatal("Confirm should use defaultConfirm")
		return false, nil
	})
	svc.Stdin = strings.NewReader("n\n")
	svc.Confirm = defaultConfirm(svc.Stdin, stdout)

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	want := "🔍 Checking available updates in /tmp/m...\n\n" +
		"⚠️  1 direct dependency will be updated:\n" +
		"  - github.com/d/x  v1.0.0 → v1.1.0\n" +
		"\nApply these updates? [Y/n]: 🛑 Update canceled.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("RunUpdate cancellation output mismatch\nwant:\n%q\ngot:\n%q", want, got)
	}
	if fx.applyCalls != 0 {
		t.Fatalf("expected apply to be skipped, got %d calls", fx.applyCalls)
	}
}

func TestRunUpdateAcceptUpdateDeclineChecks(t *testing.T) {
	confirmCalls := 0
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) {
		confirmCalls++
		return confirmCalls == 1, nil // accept apply, decline checks
	})

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Updated 1 direct dependency") {
		t.Fatalf("expected success message, got:\n%s", out)
	}
	if !strings.Contains(out, "Checks skipped") {
		t.Fatalf("expected checks skipped message, got:\n%s", out)
	}
	if fx.applyCalls != 1 || fx.checksCalls != 0 || fx.rollbackCalls != 0 {
		t.Fatalf("unexpected calls apply=%d checks=%d rollback=%d",
			fx.applyCalls, fx.checksCalls, fx.rollbackCalls)
	}
}

func TestRunUpdateRunChecksSuccess(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if !strings.Contains(stdout.String(), "Checks passed") {
		t.Fatalf("expected success message, got:\n%s", stdout.String())
	}
	if fx.applyCalls != 1 || fx.checksCalls != 1 || fx.rollbackCalls != 0 {
		t.Fatalf("unexpected calls apply=%d checks=%d rollback=%d",
			fx.applyCalls, fx.checksCalls, fx.rollbackCalls)
	}
}

func TestRunUpdateChecksFailAcceptRollback(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.runChecks = func(deps.IntentRunChecks) deps.Event {
		return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: false, Command: "go test ./...", Output: "FAIL: x"}}
	}

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Checks failed", "go test ./...", "FAIL: x", "Rolled back to pre-update state"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if fx.rollbackCalls != 1 {
		t.Fatalf("expected rollback once, got %d", fx.rollbackCalls)
	}
}

func TestRunUpdateChecksFailDeclineRollback(t *testing.T) {
	confirmCalls := 0
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) {
		confirmCalls++
		return confirmCalls != 3, nil // decline the rollback confirm
	})
	fx.runChecks = func(deps.IntentRunChecks) deps.Event {
		return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: false, Command: "go test ./...", Output: "FAIL: x"}}
	}

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if !strings.Contains(stdout.String(), "Update kept. Failed checks were not rolled back") {
		t.Fatalf("expected kept-updates message, got:\n%s", stdout.String())
	}
	if fx.rollbackCalls != 0 {
		t.Fatalf("expected rollback skipped, got %d calls", fx.rollbackCalls)
	}
}

func TestRunUpdateCheckError(t *testing.T) {
	svc, fx, _ := newUpdateService(func(string, bool) (bool, error) {
		t.Fatal("Confirm should not be called when check errors")
		return false, nil
	})
	fx.checkUpdates = func(deps.IntentCheckUpdates) deps.Event {
		return deps.CheckUpdatesDoneEvent{Err: errors.New("boom")}
	}

	err := svc.RunUpdate(UpdateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error to surface, got: %v", err)
	}
	if fx.applyCalls != 0 {
		t.Fatalf("expected apply skipped, got %d", fx.applyCalls)
	}
}

func TestRunUpdateApplyErrorBeforeMutation(t *testing.T) {
	svc, fx, _ := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.applyUpdates = func(i deps.IntentApplyUpdates) deps.Event {
		want := []deps.DependencyUpdateEntry{{
			Path:       "github.com/d/x",
			OldVersion: "v1.0.0",
			NewVersion: "v1.1.0",
		}}
		if !reflect.DeepEqual(i.Entries, want) {
			t.Fatalf("apply entries = %#v, want %#v", i.Entries, want)
		}
		return deps.ApplyUpdatesDoneEvent{Err: errors.New("go get failed")}
	}

	if err := svc.RunUpdate(UpdateOptions{}); err == nil {
		t.Fatal("expected error, got nil")
	}
	if fx.checksCalls != 0 || fx.rollbackCalls != 0 || fx.compensateCalls != 0 {
		t.Fatalf("expected no further ops, got checks=%d rollback=%d compensate=%d",
			fx.checksCalls, fx.rollbackCalls, fx.compensateCalls)
	}
}

func TestRunUpdateRollbackErrorRecoveryRequired(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.runChecks = func(deps.IntentRunChecks) deps.Event {
		return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: false, Command: "go test", Output: "boom"}}
	}
	fx.rollback = func(deps.IntentRollback) deps.Event {
		return deps.RollbackDoneEvent{Err: errors.New("disk full")}
	}

	err := svc.RunUpdate(UpdateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(stdout.String(), "Checks failed") {
		t.Fatalf("expected failure context, got:\n%s", stdout.String())
	}
	// RecoveryRequired must surface the persistent backup metadata.
	if !strings.Contains(err.Error(), "2026-07-23_12-00-00.json") {
		t.Fatalf("expected error to include backup name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "/tmp/govm/2026-07-23_12-00-00.json") {
		t.Fatalf("expected error to include backup path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected rollback cause surfaced, got: %v", err)
	}
}

// New branches: inconclusive checks, compensation, recovery

func TestRunUpdateChecksInconclusivePromptsRollback(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.runChecks = func(deps.IntentRunChecks) deps.Event {
		return deps.ChecksDoneEvent{Err: errors.New("resolve failed")}
	}

	if err := svc.RunUpdate(UpdateOptions{}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Checks could not run") {
		t.Fatalf("expected inconclusive output, got:\n%s", out)
	}
	if strings.Contains(out, "Checks failed:") {
		t.Fatalf("inconclusive output must be distinct from failed checks, got:\n%s", out)
	}
	if fx.rollbackCalls != 1 {
		t.Fatalf("expected rollback once, got %d", fx.rollbackCalls)
	}
}

func TestRunUpdateCompensationRestoredIsError(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.applyUpdates = func(deps.IntentApplyUpdates) deps.Event {
		return deps.ApplyUpdatesDoneEvent{
			Snapshot: cliSnapshot(),
			Backup:   cliBackup(),
			Err:      errors.New("go get failed"),
		}
	}

	err := svc.RunUpdate(UpdateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	out := stdout.String()
	if !strings.Contains(out, "automatically restored") {
		t.Fatalf("expected restored message, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "go get failed") {
		t.Fatalf("expected apply error surfaced, got: %v", err)
	}
	if fx.compensateCalls != 1 {
		t.Fatalf("expected compensation once, got %d", fx.compensateCalls)
	}
	if fx.checksCalls != 0 || fx.rollbackCalls != 0 {
		t.Fatalf("expected no checks/rollback, got checks=%d rollback=%d", fx.checksCalls, fx.rollbackCalls)
	}
}

func TestRunUpdateRecoveryRequiredCompensationFailed(t *testing.T) {
	svc, fx, stdout := newUpdateService(func(string, bool) (bool, error) { return true, nil })
	fx.applyUpdates = func(deps.IntentApplyUpdates) deps.Event {
		return deps.ApplyUpdatesDoneEvent{
			Snapshot: cliSnapshot(),
			Backup:   cliBackup(),
			Err:      errors.New("go get failed"),
		}
	}
	fx.compensate = func(deps.IntentCompensate) deps.Event {
		return deps.CompensateDoneEvent{Err: errors.New("restore failed")}
	}

	err := svc.RunUpdate(UpdateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	out := stdout.String()
	if strings.Contains(out, "automatically restored") {
		t.Fatalf("must not claim restoration on recovery, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("expected compensation error surfaced, got: %v", err)
	}
	if !strings.Contains(err.Error(), "2026-07-23_12-00-00.json") {
		t.Fatalf("expected backup name in error, got: %v", err)
	}
	if fx.compensateCalls != 1 {
		t.Fatalf("expected compensation once, got %d", fx.compensateCalls)
	}
}

func TestRunUpdateExecutorError(t *testing.T) {
	svc, fx, _ := newUpdateService(func(string, bool) (bool, error) {
		t.Fatal("Confirm should not be called when executor errors")
		return false, nil
	})
	fx.execErr = errors.New("executor unavailable")

	err := svc.RunUpdate(UpdateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "executor unavailable") {
		t.Fatalf("expected executor error surfaced, got: %v", err)
	}
	if fx.applyCalls != 0 {
		t.Fatalf("expected apply skipped after check executor error, got %d", fx.applyCalls)
	}
}

// defaultConfirm and NewDepsService wiring

func TestDefaultConfirmReadsBufferedAnswersAcrossPrompts(t *testing.T) {
	stdin := strings.NewReader("y\nn\nn\n")
	stdout := &bytes.Buffer{}
	confirm := defaultConfirm(stdin, stdout)

	first, err := confirm("Apply updates?", true)
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	second, err := confirm("Run checks?", true)
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	third, err := confirm("Roll back?", true)
	if err != nil {
		t.Fatalf("third confirm: %v", err)
	}

	if first != true || second != false || third != false {
		t.Fatalf("expected true, false, false; got %t, %t, %t", first, second, third)
	}
}

func TestNewDepsServiceWiresDefaults(t *testing.T) {
	fx := &fakeExecutor{}
	svc := NewDepsService("/tmp/m", fx, &bytes.Buffer{}, &bytes.Buffer{})
	if svc.Deps != deps.API(fx) {
		t.Fatalf("Deps = %T, want the bound executor", svc.Deps)
	}
	if svc.Confirm == nil {
		t.Fatal("Confirm should be wired")
	}
	if svc.ModuleDir != "/tmp/m" {
		t.Fatalf("ModuleDir = %q, want %q", svc.ModuleDir, "/tmp/m")
	}
}
