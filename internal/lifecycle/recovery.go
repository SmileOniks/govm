package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/state"
)

type recoveryHandler struct {
	resolver *paths.Resolver
	fs       fileSystem
}

var _ state.RecoveryHandler = (*recoveryHandler)(nil)

func (h *recoveryHandler) Recover(ctx context.Context, marker state.Marker) (state.RecoveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	layout, err := h.resolveLayout()
	if err != nil {
		return state.RecoveryResult{}, err
	}
	store := state.NewMarkerStore(layout.root)
	switch marker.Operation {
	case state.OperationActivate:
		return h.recoverActivation(ctx, store, marker, layout)
	case state.OperationDelete:
		return h.recoverDeletion(store, marker, layout)
	default:
		return state.RecoveryResult{}, fmt.Errorf("unsupported lifecycle operation %q", marker.Operation)
	}
}

func (h *recoveryHandler) resolveLayout() (layout, error) {
	service := Service{resolver: h.resolver}
	return service.resolveLayout()
}

func (h *recoveryHandler) recoverActivation(
	ctx context.Context,
	store *state.MarkerStore,
	marker state.Marker,
	layout layout,
) (state.RecoveryResult, error) {
	if err := validateActivationMarker(marker); err != nil {
		return state.RecoveryResult{}, err
	}
	if err := ctx.Err(); err != nil && marker.Phase != string(PhaseActiveRecordCommitted) {
		return state.RecoveryResult{}, err
	}

	postCommit := false
	if marker.Phase == string(PhaseActiveRecordCommitting) {
		targetPath := filepath.Join(layout.root, marker.Artifacts["target"])
		targetPresent, err := pathExists(h.fs, targetPath)
		if err != nil {
			return state.RecoveryResult{}, fmt.Errorf("inspect staged active-version record: %w", err)
		}
		if !targetPresent {
			active, present, readErr := (&Service{fs: h.fs}).readActiveVersion(layout.active)
			if readErr != nil {
				return state.RecoveryResult{}, readErr
			}
			postCommit = present && active == marker.Version
		}
	}
	if marker.Phase == string(PhaseActiveRecordCommitted) {
		active, present, readErr := (&Service{fs: h.fs}).readActiveVersion(layout.active)
		if readErr != nil {
			return state.RecoveryResult{}, readErr
		}
		if !present || active != marker.Version {
			return state.RecoveryResult{}, errors.New("committed activation marker does not match active-version record")
		}
		postCommit = true
	}
	if postCommit {
		warnings := h.cleanupCommittedActivation(store, marker, layout)
		return state.RecoveryResult{Warnings: stateWarnings(warnings)}, nil
	}

	if err := h.rollbackActivation(marker, layout); err != nil {
		return state.RecoveryResult{}, err
	}
	if err := store.Delete(); err != nil {
		return state.RecoveryResult{}, err
	}
	return state.RecoveryResult{}, nil
}

func (h *recoveryHandler) rollbackActivation(marker state.Marker, layout layout) error {
	return rollbackActivation(h.fs, marker, layout)
}

func (s *Service) rollbackActivation(marker state.Marker, layout layout) error {
	return rollbackActivation(s.fs, marker, layout)
}

func rollbackActivation(fs fileSystem, marker state.Marker, layout layout) error {
	if err := validateActivationMarker(marker); err != nil {
		return err
	}
	staging := filepath.Join(layout.root, marker.Artifacts["staging"])
	backup := filepath.Join(layout.root, marker.Artifacts["backup"])
	target := filepath.Join(layout.root, marker.Artifacts["target"])
	var errs []error

	switch marker.Phase {
	case string(PhasePrepared):
		backupPresent, err := pathExists(fs, backup)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect shim backup: %w", err))
			break
		}
		if backupPresent {
			livePresent, err := pathExists(fs, layout.shims)
			if err != nil {
				errs = append(errs, fmt.Errorf("inspect live shims: %w", err))
				break
			}
			if livePresent {
				errs = append(errs, errors.New("prepared activation has both live shims and backup"))
				break
			}
			if err := restoreDirectoryIfPresent(fs, backup, layout.shims); err != nil {
				errs = append(errs, fmt.Errorf("restore shim backup: %w", err))
			}
		}
	case string(PhaseLiveBackedUp):
		if err := removePathIfPresent(fs, layout.shims); err != nil {
			errs = append(errs, fmt.Errorf("remove partial live shims: %w", err))
		}
		if err := restoreDirectoryIfPresent(fs, backup, layout.shims); err != nil {
			errs = append(errs, fmt.Errorf("restore shim backup: %w", err))
		}
	case string(PhaseLiveInstalled), string(PhaseActiveRecordCommitting):
		if err := removePathIfPresent(fs, layout.shims); err != nil {
			errs = append(errs, fmt.Errorf("remove new live shims: %w", err))
		}
		if err := restoreDirectoryIfPresent(fs, backup, layout.shims); err != nil {
			errs = append(errs, fmt.Errorf("restore shim backup: %w", err))
		}
		if source, ok := marker.Artifacts["source"]; ok {
			sourcePath := filepath.Join(layout.root, source)
			if err := restoreActiveRecord(fs, sourcePath, layout.active); err != nil {
				errs = append(errs, fmt.Errorf("restore active-version record: %w", err))
			}
		} else if err := removeFileIfPresent(fs, layout.active); err != nil {
			errs = append(errs, fmt.Errorf("remove active-version record: %w", err))
		}
	default:
		return fmt.Errorf("unknown activation phase %q", marker.Phase)
	}

	for _, path := range []string{staging, target} {
		if err := removePathIfPresent(fs, path); err != nil {
			errs = append(errs, fmt.Errorf("remove activation artifact %q: %w", path, err))
		}
	}
	if source, ok := marker.Artifacts["source"]; ok {
		path := filepath.Join(layout.root, source)
		if err := removePathIfPresent(fs, path); err != nil {
			errs = append(errs, fmt.Errorf("remove active backup %q: %w", path, err))
		}
	}
	if err := fs.syncDir(layout.root); err != nil {
		errs = append(errs, fmt.Errorf("sync activation rollback: %w", err))
	}
	return errors.Join(errs...)
}

func (h *recoveryHandler) cleanupCommittedActivation(
	store *state.MarkerStore,
	marker state.Marker,
	layout layout,
) []Warning {
	return cleanupCommittedActivation(h.fs, store, marker, layout)
}

func (s *Service) cleanupCommittedActivation(
	store *state.MarkerStore,
	marker state.Marker,
	layout layout,
) []Warning {
	return cleanupCommittedActivation(s.fs, store, marker, layout)
}

func cleanupCommittedActivation(
	fs fileSystem,
	store *state.MarkerStore,
	marker state.Marker,
	layout layout,
) []Warning {
	var warnings []Warning
	for _, role := range []string{"backup", "staging", "target", "source"} {
		name, ok := marker.Artifacts[role]
		if !ok {
			continue
		}
		path := filepath.Join(layout.root, name)
		if err := removePathIfPresent(fs, path); err != nil {
			warnings = append(warnings, &CleanupWarning{
				Operation: state.OperationActivate,
				Path:      path,
				Err:       err,
			})
		}
	}
	if len(warnings) > 0 {
		return warnings
	}
	if err := fs.syncDir(layout.root); err != nil {
		return []Warning{&CleanupWarning{
			Operation: state.OperationActivate,
			Path:      layout.root,
			Err:       err,
		}}
	}
	if err := store.Delete(); err != nil {
		return []Warning{&CleanupWarning{
			Operation: state.OperationActivate,
			Path:      store.Path(),
			Err:       err,
		}}
	}
	return nil
}

func (h *recoveryHandler) recoverDeletion(
	store *state.MarkerStore,
	marker state.Marker,
	layout layout,
) (state.RecoveryResult, error) {
	if err := validateDeletionMarker(marker); err != nil {
		return state.RecoveryResult{}, err
	}
	source := filepath.Join(layout.versions, marker.Artifacts["source"])
	quarantine := filepath.Join(layout.versions, marker.Artifacts["quarantine"])
	sourcePresent, err := pathExists(h.fs, source)
	if err != nil {
		return state.RecoveryResult{}, fmt.Errorf("inspect deletion source: %w", err)
	}
	quarantinePresent, err := pathExists(h.fs, quarantine)
	if err != nil {
		return state.RecoveryResult{}, fmt.Errorf("inspect deletion quarantine: %w", err)
	}

	committed := marker.Phase == string(PhaseQuarantined) || (!sourcePresent && quarantinePresent)
	if !committed {
		if !sourcePresent && !quarantinePresent {
			return state.RecoveryResult{}, errors.New("deletion marker has neither source nor quarantine")
		}
		if sourcePresent && quarantinePresent {
			return state.RecoveryResult{}, errors.New("deletion marker has both source and quarantine")
		}
		if err := store.Delete(); err != nil {
			return state.RecoveryResult{}, err
		}
		return state.RecoveryResult{}, nil
	}
	if marker.Phase == string(PhaseQuarantined) && sourcePresent {
		return state.RecoveryResult{}, errors.New("quarantined deletion marker still has source")
	}

	if quarantinePresent {
		if err := h.fs.removeAll(quarantine); err != nil {
			warning := state.Warning{Message: "delete recovery cleanup failed", Err: err}
			return state.RecoveryResult{Warnings: []state.Warning{warning}}, nil
		}
		if err := h.fs.syncDir(layout.versions); err != nil {
			warning := state.Warning{Message: "delete recovery directory sync failed", Err: err}
			return state.RecoveryResult{Warnings: []state.Warning{warning}}, nil
		}
	}
	if err := store.Delete(); err != nil {
		warning := state.Warning{Message: "delete recovery marker cleanup failed", Err: err}
		return state.RecoveryResult{Warnings: []state.Warning{warning}}, nil
	}
	return state.RecoveryResult{}, nil
}

func validateActivationMarker(marker state.Marker) error {
	allowed := map[string]struct{}{
		string(PhasePrepared):               {},
		string(PhaseLiveBackedUp):           {},
		string(PhaseLiveInstalled):          {},
		string(PhaseActiveRecordCommitting): {},
		string(PhaseActiveRecordCommitted):  {},
	}
	if _, ok := allowed[marker.Phase]; !ok {
		return fmt.Errorf("unknown activation phase %q", marker.Phase)
	}
	if err := requireArtifactPrefix(marker, "staging", activationStagingPrefix); err != nil {
		return err
	}
	if err := requireArtifactPrefix(marker, "backup", activationBackupPrefix); err != nil {
		return err
	}
	if err := requireArtifactPrefix(marker, "target", activeTargetPrefix); err != nil {
		return err
	}
	if source, ok := marker.Artifacts["source"]; ok && !strings.HasPrefix(source, activeBackupPrefix) {
		return fmt.Errorf("unexpected activation source artifact %q", source)
	}
	expectedArtifacts := 3
	if _, ok := marker.Artifacts["source"]; ok {
		expectedArtifacts++
	}
	if len(marker.Artifacts) != expectedArtifacts {
		return errors.New("activation marker has unexpected artifacts")
	}
	return nil
}

func validateDeletionMarker(marker state.Marker) error {
	if marker.Phase != string(PhasePrepared) && marker.Phase != string(PhaseQuarantined) {
		return fmt.Errorf("unknown deletion phase %q", marker.Phase)
	}
	if source := marker.Artifacts["source"]; source != "go"+marker.Version {
		return fmt.Errorf("unexpected deletion source artifact %q", source)
	}
	if len(marker.Artifacts) != 2 {
		return errors.New("deletion marker has unexpected artifacts")
	}
	return requireArtifactPrefix(marker, "quarantine", deletionPrefix)
}

func requireArtifactPrefix(marker state.Marker, role, prefix string) error {
	name, ok := marker.Artifacts[role]
	if !ok || !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return fmt.Errorf("missing or unexpected %s artifact %q", role, name)
	}
	return nil
}

func stateWarnings(warnings []Warning) []state.Warning {
	result := make([]state.Warning, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, state.Warning{Message: "lifecycle recovery cleanup failed", Err: warning})
	}
	return result
}

func pathExists(fs fileSystem, path string) (bool, error) {
	_, err := fs.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func removePathIfPresent(fs fileSystem, path string) error {
	_, err := fs.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fs.removeAll(path)
}

func removeFileIfPresent(fs fileSystem, path string) error {
	err := fs.remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func restoreDirectoryIfPresent(fs fileSystem, backup, live string) error {
	present, err := pathExists(fs, backup)
	if err != nil || !present {
		return err
	}
	if err := fs.rename(backup, live); err != nil {
		return err
	}
	return fs.syncDir(filepath.Dir(live))
}

func restoreActiveRecord(fs fileSystem, backup, active string) error {
	present, err := pathExists(fs, backup)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("active-version backup is missing")
	}
	if err := fs.replace(backup, active); err != nil {
		return err
	}
	return fs.syncDir(filepath.Dir(active))
}
