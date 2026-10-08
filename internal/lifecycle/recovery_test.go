package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/state"
)

func TestActivationRecoveryRollsBackEveryPrecommitPhase(t *testing.T) {
	t.Parallel()
	for _, phase := range []Phase{
		PhasePrepared,
		PhaseLiveBackedUp,
		PhaseLiveInstalled,
		PhaseActiveRecordCommitting,
	} {
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.install(t, "1.25.0")
			f.install(t, "1.26.1")
			if _, err := f.service.Activate(t.Context(), "1.25.0"); err != nil {
				t.Fatal(err)
			}
			oldShim := mustRead(t, filepath.Join(f.shims, hostShimName("go")))
			marker := activationMarkerForTest("1.26.1", phase)
			stageActivationCrash(t, f, marker)
			if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
				t.Fatal(err)
			}

			handler := NewRecoveryHandler(f.resolver)
			result, err := handler.Recover(context.Background(), marker)
			if err != nil {
				t.Fatalf("Recover() error = %v", err)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("Recover() warnings = %#v", result.Warnings)
			}
			if got := mustRead(t, f.active); got != "1.25.0" {
				t.Fatalf("active after recovery = %q", got)
			}
			if got := mustRead(t, filepath.Join(f.shims, hostShimName("go"))); got != oldShim {
				t.Fatal("old shim set not restored")
			}
			if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || present {
				t.Fatalf("marker present = %t, error %v", present, err)
			}
		})
	}
}

func TestActivationRecoveryTreatsMissingTargetDuringCommittingAsPostcommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.26.1")
	marker := activationMarkerForTest("1.26.1", PhaseActiveRecordCommitting)
	if err := os.Mkdir(f.shims, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.shims, hostShimName("go")), []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.active, []byte("1.25.0"), recordMode); err != nil {
		t.Fatal(err)
	}
	stageActivationCrash(t, f, marker)
	if err := os.WriteFile(f.active, []byte("1.26.1"), recordMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, marker.Artifacts["target"])); err != nil {
		t.Fatal(err)
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if got := mustRead(t, f.active); got != "1.26.1" {
		t.Fatalf("active after recovery = %q", got)
	}
	if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || present {
		t.Fatalf("marker present = %t, error %v", present, err)
	}
}

func TestActivationRecoveryPreparedMarkerRestoresBackupAfterPhaseWriteFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.25.0")
	if _, err := f.service.Activate(t.Context(), "1.25.0"); err != nil {
		t.Fatal(err)
	}
	oldShim := mustRead(t, filepath.Join(f.shims, hostShimName("go")))
	marker := activationMarkerForTest("1.26.1", PhasePrepared)
	if err := os.Rename(f.shims, filepath.Join(f.root, marker.Artifacts["backup"])); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, marker.Artifacts["staging"]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, marker.Artifacts["target"]), []byte(marker.Version), recordMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, marker.Artifacts["source"]), []byte("1.25.0"), recordMode); err != nil {
		t.Fatal(err)
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if got := mustRead(t, filepath.Join(f.shims, hostShimName("go"))); got != oldShim {
		t.Fatal("prepared recovery did not restore renamed live shims")
	}
}

func TestActivationRecoveryPostcommitCleanupWarningPreservesMarker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	marker := activationMarkerForTest("1.26.1", PhaseActiveRecordCommitted)
	if err := os.WriteFile(f.active, []byte(marker.Version), recordMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, marker.Artifacts["backup"]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}
	handler := &recoveryHandler{resolver: f.resolver, fs: productionFileSystem()}
	cleanupErr := errors.New("cleanup failed")
	handler.fs.removeAll = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), activationBackupPrefix) {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}
	result, err := handler.Recover(t.Context(), marker)
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if len(result.Warnings) != 1 || !errors.Is(result.Warnings[0].Err, cleanupErr) {
		t.Fatalf("Recover() warnings = %#v", result.Warnings)
	}
	if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || !present {
		t.Fatalf("marker present = %t, error %v", present, err)
	}
}

func TestDeletionRecoveryNeverRestoresAfterRename(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.install(t, "1.25.0")
	quarantineName := deletionPrefix + "test"
	quarantine := filepath.Join(f.versions, quarantineName)
	if err := os.Rename(source, quarantine); err != nil {
		t.Fatal(err)
	}
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationDelete,
		Phase:         string(PhasePrepared),
		Version:       "1.25.0",
		Artifacts: map[string]string{
			"source":     "go1.25.0",
			"quarantine": quarantineName,
		},
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source restored after committed delete: %v", err)
	}
	if _, err := os.Lstat(quarantine); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("quarantine remains: %v", err)
	}
}

func TestDeletionRecoveryPreparedBeforeRenameOnlyDeletesMarker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.install(t, "1.25.0")
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationDelete,
		Phase:         string(PhasePrepared),
		Version:       "1.25.0",
		Artifacts: map[string]string{
			"source":     "go1.25.0",
			"quarantine": deletionPrefix + "test",
		},
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed before commit: %v", err)
	}
}

func TestRecoveryRejectsArtifactRoleWithUnexpectedPrefix(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	marker := activationMarkerForTest("1.26.1", PhasePrepared)
	marker.Artifacts["staging"] = "innocent-file"
	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err == nil {
		t.Fatal("Recover() error = nil")
	}
}

func TestActivationRecoveryCommittedMarkerMustMatchActiveRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	marker := activationMarkerForTest("1.26.1", PhaseActiveRecordCommitted)
	if err := os.WriteFile(f.active, []byte("1.25.0"), recordMode); err != nil {
		t.Fatal(err)
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err == nil {
		t.Fatal("Recover() error = nil for contradictory committed marker")
	}
	if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || !present {
		t.Fatalf("marker present = %t, error %v", present, err)
	}
}

func TestDeletionRecoveryQuarantinedMarkerRejectsVisibleSource(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.install(t, "1.25.0")
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationDelete,
		Phase:         string(PhaseQuarantined),
		Version:       "1.25.0",
		Artifacts: map[string]string{
			"source":     "go1.25.0",
			"quarantine": deletionPrefix + "missing",
		},
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err == nil {
		t.Fatal("Recover() error = nil for contradictory quarantined marker")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed during failed recovery: %v", err)
	}
}

func TestLifecycleRecoveryRejectsUnexpectedArtifactRoles(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	marker := activationMarkerForTest("1.26.1", PhasePrepared)
	marker.Artifacts["download"] = ".govm-install-test.part"
	if _, err := NewRecoveryHandler(f.resolver).Recover(t.Context(), marker); err == nil {
		t.Fatal("Recover() error = nil for unexpected artifact role")
	}
}

func TestCoordinatorAutomaticallyDispatchesLifecycleRecovery(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.25.0")
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationDelete,
		Phase:         string(PhasePrepared),
		Version:       "1.25.0",
		Artifacts: map[string]string{
			"source":     "go1.25.0",
			"quarantine": deletionPrefix + "pending",
		},
	}
	if err := state.NewMarkerStore(f.root).Write(marker); err != nil {
		t.Fatal(err)
	}
	f.install(t, "1.26.1")
	if _, err := f.service.Activate(t.Context(), "1.26.1"); err != nil {
		t.Fatalf("Activate() after recovery error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.versions, "go1.25.0")); err != nil {
		t.Fatalf("prepared deletion source changed: %v", err)
	}
}

func activationMarkerForTest(version string, phase Phase) state.Marker {
	return state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationActivate,
		Phase:         string(phase),
		Version:       version,
		Artifacts: map[string]string{
			"staging": activationStagingPrefix + "test",
			"backup":  activationBackupPrefix + "test",
			"target":  activeTargetPrefix + "test",
			"source":  activeBackupPrefix + "test",
		},
	}
}

func stageActivationCrash(t *testing.T, f *fixture, marker state.Marker) {
	t.Helper()
	staging := filepath.Join(f.root, marker.Artifacts["staging"])
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, hostShimName("go")), []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, marker.Artifacts["target"]), []byte(marker.Version), 0o600); err != nil {
		t.Fatal(err)
	}
	oldActive := mustRead(t, f.active)
	if err := os.WriteFile(filepath.Join(f.root, marker.Artifacts["source"]), []byte(oldActive), 0o600); err != nil {
		t.Fatal(err)
	}
	if marker.Phase == string(PhasePrepared) {
		return
	}
	backup := filepath.Join(f.root, marker.Artifacts["backup"])
	if err := os.Rename(f.shims, backup); err != nil {
		t.Fatal(err)
	}
	if marker.Phase == string(PhaseLiveBackedUp) {
		return
	}
	if err := os.Rename(staging, f.shims); err != nil {
		t.Fatal(err)
	}
}
