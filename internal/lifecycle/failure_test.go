package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/state"
)

func TestActivationFailureBoundariesRollbackPrecommit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		failPhase Phase
		match     func(oldPath, newPath string) bool
	}{
		{
			name:      "live backup rename",
			failPhase: PhasePrepared,
			match: func(oldPath, newPath string) bool {
				return filepath.Base(oldPath) == "shim" &&
					strings.HasPrefix(filepath.Base(newPath), activationBackupPrefix)
			},
		},
		{
			name:      "staging install rename",
			failPhase: PhaseLiveBackedUp,
			match: func(oldPath, newPath string) bool {
				return strings.HasPrefix(filepath.Base(oldPath), activationStagingPrefix) &&
					filepath.Base(newPath) == "shim"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.install(t, "1.25.0")
			f.install(t, "1.26.1")
			if _, err := f.service.Activate(t.Context(), "1.25.0"); err != nil {
				t.Fatal(err)
			}
			oldShim := mustRead(t, filepath.Join(f.shims, hostShimName("go")))
			realRename := f.service.fs.rename
			injected := errors.New("rename failed")
			f.service.fs.rename = func(oldPath, newPath string) error {
				if tt.match(oldPath, newPath) {
					return injected
				}
				return realRename(oldPath, newPath)
			}
			_, err := f.service.Activate(t.Context(), "1.26.1")
			if !errors.Is(err, injected) {
				t.Fatalf("Activate() error = %v", err)
			}
			var phaseErr *Error
			if !errors.As(err, &phaseErr) || phaseErr.Phase != tt.failPhase {
				t.Fatalf("Activate() phase error = %#v, want %s", err, tt.failPhase)
			}
			if got := mustRead(t, f.active); got != "1.25.0" {
				t.Fatalf("active after rollback = %q", got)
			}
			if got := mustRead(t, filepath.Join(f.shims, hostShimName("go"))); got != oldShim {
				t.Fatal("shim set changed after rollback")
			}
			if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || present {
				t.Fatalf("marker present = %t, error %v", present, err)
			}
		})
	}
}

func TestDeleteRenameFailureIsPrecommitAndKeepsVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.install(t, "1.25.0")
	injected := errors.New("rename failed")
	f.service.fs.rename = func(_, _ string) error { return injected }

	_, err := f.service.Delete(t.Context(), "1.25.0")
	if !errors.Is(err, injected) {
		t.Fatalf("Delete() error = %v", err)
	}
	var phaseErr *Error
	if !errors.As(err, &phaseErr) || phaseErr.Phase != PhasePrepared {
		t.Fatalf("Delete() phase error = %#v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("version changed after rename failure: %v", err)
	}
	if _, present, err := state.NewMarkerStore(f.root).Read(); err != nil || present {
		t.Fatalf("marker present = %t, error %v", present, err)
	}
}
