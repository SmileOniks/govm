package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/state"
	"github.com/SmileOniks/govm/internal/version"
)

const (
	activationStagingPrefix = ".activate-"
	activationBackupPrefix  = ".shim-backup-"
	activeBackupPrefix      = ".active-backup-"
	activeTargetPrefix      = ".active-next-"
	deletionPrefix          = ".delete-"

	privateDirMode = 0o700
	shimMode       = 0o700
	recordMode     = 0o600
)

type fileSystem struct {
	lstat     func(string) (fs.FileInfo, error)
	readDir   func(string) ([]os.DirEntry, error)
	readFile  func(string) ([]byte, error)
	mkdirAll  func(string, fs.FileMode) error
	mkdir     func(string, fs.FileMode) error
	writeFile func(string, []byte, fs.FileMode) error
	chmod     func(string, fs.FileMode) error
	rename    func(string, string) error
	replace   func(string, string) error
	remove    func(string) error
	removeAll func(string) error
	syncFile  func(string) error
	syncDir   func(string) error
	unique    func() (string, error)
	targetOS  string
}

func productionFileSystem() fileSystem {
	return fileSystem{
		lstat:     os.Lstat,
		readDir:   os.ReadDir,
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		mkdir:     os.Mkdir,
		writeFile: os.WriteFile,
		chmod:     os.Chmod,
		rename:    os.Rename,
		replace:   atomicReplace,
		remove:    os.Remove,
		removeAll: os.RemoveAll,
		syncFile:  syncFile,
		syncDir:   syncDirectory,
		unique:    uniqueID,
		targetOS:  runtime.GOOS,
	}
}

// Service transactionally activates and deletes installed Go toolchains.
type Service struct {
	resolver    *paths.Resolver
	coordinator *state.Coordinator
	fs          fileSystem
}

// New constructs a service using coordinator for the global mutation
// lock and recovery dispatch. Callers should share one coordinator with other
// installed-version mutation services.
func New(resolver *paths.Resolver, coordinator *state.Coordinator) (*Service, error) {
	if resolver == nil {
		resolver = paths.New()
	}
	if coordinator == nil {
		return nil, errors.New("lifecycle coordinator is nil")
	}
	service := &Service{
		resolver:    resolver,
		coordinator: coordinator,
		fs:          productionFileSystem(),
	}
	handler := &recoveryHandler{resolver: resolver, fs: service.fs}
	if err := coordinator.RegisterRecoveryHandler(state.OperationActivate, handler); err != nil {
		return nil, fmt.Errorf("register activation recovery: %w", err)
	}
	if err := coordinator.RegisterRecoveryHandler(state.OperationDelete, handler); err != nil {
		return nil, fmt.Errorf("register deletion recovery: %w", err)
	}
	return service, nil
}

// NewRecoveryHandler returns the operation-specific recovery dispatcher needed
// when lifecycle handlers are composed independently of Service.
func NewRecoveryHandler(resolver *paths.Resolver) state.RecoveryHandler {
	if resolver == nil {
		resolver = paths.New()
	}
	return &recoveryHandler{resolver: resolver, fs: productionFileSystem()}
}

// Activate atomically replaces the complete shim set and then commits the
// canonical active-version record. Repeating activation repairs all shims.
func (s *Service) Activate(ctx context.Context, canonical string) (ActivationResult, error) {
	if err := version.Validate(canonical); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhaseValidate, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhaseValidate, err)
	}

	var result ActivationResult
	coordinated, err := s.coordinator.Mutate(ctx, func(ctx context.Context, store *state.MarkerStore) error {
		var mutationErr error
		result, mutationErr = s.activateLocked(ctx, store, canonical)
		return mutationErr
	})
	result.Warnings = append(recoveryWarnings(coordinated.RecoveryWarnings), result.Warnings...)
	if err != nil {
		return ActivationResult{}, err
	}
	return result, nil
}

func (s *Service) activateLocked(ctx context.Context, store *state.MarkerStore, canonical string) (ActivationResult, error) {
	layout, err := s.resolveLayout()
	if err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, err)
	}
	if err := s.fs.mkdirAll(layout.root, privateDirMode); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("create root directory: %w", err))
	}
	if err := requireRealDirectory(s.fs, layout.root); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("validate root directory: %w", err))
	}
	if err := s.fs.mkdirAll(layout.versions, privateDirMode); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("create versions directory: %w", err))
	}
	if err := requireRealDirectory(s.fs, layout.versions); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("validate versions directory: %w", err))
	}

	versionDir := filepath.Join(layout.versions, "go"+canonical)
	binDir, err := s.validateInstalledVersion(layout.versions, versionDir)
	if err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, err)
	}
	candidates, err := s.shimCandidates(binDir)
	if err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, err)
	}

	id, err := s.fs.unique()
	if err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("generate transaction id: %w", err))
	}
	stagingName := activationStagingPrefix + id
	backupName := activationBackupPrefix + id
	activeTargetName := activeTargetPrefix + id
	activeBackupName := activeBackupPrefix + id
	stagingPath := filepath.Join(layout.root, stagingName)
	backupPath := filepath.Join(layout.root, backupName)
	activeTargetPath := filepath.Join(layout.root, activeTargetName)
	activeBackupPath := filepath.Join(layout.root, activeBackupName)

	if err := s.fs.mkdir(stagingPath, privateDirMode); err != nil {
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("create shim staging directory: %w", err))
	}
	prepared := []string{stagingPath}
	cleanupPrepared := func() {
		for _, path := range prepared {
			_ = s.fs.removeAll(path)
		}
	}

	shimNames := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, err)
		}
		outputName, content := renderShim(s.fs.targetOS, candidate.name, candidate.path)
		shimPath := filepath.Join(stagingPath, outputName)
		if !paths.IsDirectChild(stagingPath, shimPath) {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("unsafe shim name %q", outputName))
		}
		if err := s.fs.writeFile(shimPath, content, shimMode); err != nil {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("write shim %q: %w", outputName, err))
		}
		if err := s.fs.chmod(shimPath, shimMode); err != nil {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("set shim mode %q: %w", outputName, err))
		}
		if err := s.fs.syncFile(shimPath); err != nil {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("sync shim %q: %w", outputName, err))
		}
		shimNames = append(shimNames, outputName)
	}
	if err := s.fs.syncDir(stagingPath); err != nil {
		cleanupPrepared()
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("sync shim staging directory: %w", err))
	}

	oldActive, activePresent, err := s.readActiveVersion(layout.active)
	if err != nil {
		cleanupPrepared()
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, err)
	}
	if activePresent {
		if err := writeDurableFile(s.fs, activeBackupPath, []byte(oldActive), recordMode); err != nil {
			cleanupPrepared()
			return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("stage active-version backup: %w", err))
		}
		prepared = append(prepared, activeBackupPath)
	}
	if err := writeDurableFile(s.fs, activeTargetPath, []byte(canonical), recordMode); err != nil {
		cleanupPrepared()
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepare, fmt.Errorf("stage active-version record: %w", err))
	}
	prepared = append(prepared, activeTargetPath)

	artifacts := map[string]string{
		"staging": stagingName,
		"backup":  backupName,
		"target":  activeTargetName,
	}
	if activePresent {
		artifacts["source"] = activeBackupName
	}
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationActivate,
		Phase:         string(PhasePrepared),
		Version:       canonical,
		Artifacts:     artifacts,
	}
	if err := store.Write(marker); err != nil {
		cleanupPrepared()
		return ActivationResult{}, operationError(state.OperationActivate, PhasePrepared, err)
	}

	fail := func(phase Phase, cause error) (ActivationResult, error) {
		rollbackErr := s.rollbackActivation(marker, layout)
		if rollbackErr == nil {
			rollbackErr = store.Delete()
		}
		if rollbackErr != nil {
			cause = errors.Join(cause, fmt.Errorf("rollback activation: %w", rollbackErr))
		}
		return ActivationResult{}, operationError(state.OperationActivate, phase, cause)
	}

	livePresent, err := requireOptionalDirectory(s.fs, layout.shims)
	if err != nil {
		return fail(PhasePrepared, fmt.Errorf("validate live shim directory: %w", err))
	}
	if livePresent {
		if err := s.fs.rename(layout.shims, backupPath); err != nil {
			return fail(PhasePrepared, fmt.Errorf("back up live shim directory: %w", err))
		}
		if err := s.fs.syncDir(layout.root); err != nil {
			return fail(PhasePrepared, fmt.Errorf("sync shim backup rename: %w", err))
		}
	}
	marker.Phase = string(PhaseLiveBackedUp)
	if err := store.Write(marker); err != nil {
		return fail(PhaseLiveBackedUp, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(PhaseLiveBackedUp, err)
	}

	if err := s.fs.rename(stagingPath, layout.shims); err != nil {
		return fail(PhaseLiveBackedUp, fmt.Errorf("install staged shim directory: %w", err))
	}
	if err := s.fs.syncDir(layout.root); err != nil {
		return fail(PhaseLiveBackedUp, fmt.Errorf("sync live shim rename: %w", err))
	}
	marker.Phase = string(PhaseLiveInstalled)
	if err := store.Write(marker); err != nil {
		return fail(PhaseLiveInstalled, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(PhaseLiveInstalled, err)
	}

	marker.Phase = string(PhaseActiveRecordCommitting)
	if err := store.Write(marker); err != nil {
		return fail(PhaseActiveRecordCommitting, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(PhaseActiveRecordCommitting, err)
	}
	if err := s.fs.replace(activeTargetPath, layout.active); err != nil {
		return fail(PhaseActiveRecordCommitting, fmt.Errorf("commit active-version record: %w", err))
	}
	if err := s.fs.syncDir(layout.root); err != nil {
		return fail(PhaseActiveRecordCommitting, fmt.Errorf("sync active-version commit: %w", err))
	}

	marker.Phase = string(PhaseActiveRecordCommitted)
	if err := store.Write(marker); err != nil {
		warning := &CleanupWarning{Operation: state.OperationActivate, Path: store.Path(), Err: err}
		return ActivationResult{Version: canonical, ShimDir: layout.shims, Shims: shimNames, Warnings: []Warning{warning}}, nil
	}

	warnings := s.cleanupCommittedActivation(store, marker, layout)
	return ActivationResult{
		Version:  canonical,
		ShimDir:  layout.shims,
		Shims:    shimNames,
		Warnings: warnings,
	}, nil
}

// Delete commits by atomically renaming the canonical version directory to a
// quarantine basename. Cleanup after that rename is best effort.
func (s *Service) Delete(ctx context.Context, canonical string) (DeletionResult, error) {
	if err := version.Validate(canonical); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhaseValidate, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhaseValidate, err)
	}

	var result DeletionResult
	coordinated, err := s.coordinator.Mutate(ctx, func(ctx context.Context, store *state.MarkerStore) error {
		var mutationErr error
		result, mutationErr = s.deleteLocked(ctx, store, canonical)
		return mutationErr
	})
	result.Warnings = append(recoveryWarnings(coordinated.RecoveryWarnings), result.Warnings...)
	if err != nil {
		return DeletionResult{}, err
	}
	return result, nil
}

// DeleteLocked deletes canonical while the shared coordinator lock is held.
// Callers must invoke this only from a coordinator mutation callback.
func (s *Service) DeleteLocked(ctx context.Context, store *state.MarkerStore, canonical string) (DeletionResult, error) {
	if err := version.Validate(canonical); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhaseValidate, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhaseValidate, err)
	}
	if store == nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, errors.New("state marker store is nil"))
	}
	return s.deleteLocked(ctx, store, canonical)
}

// ValidateInstalledVersion verifies the canonical on-disk toolchain layout
// without mutating it.
func (s *Service) ValidateInstalledVersion(canonical string) error {
	if err := version.Validate(canonical); err != nil {
		return operationError(state.OperationDelete, PhaseValidate, err)
	}
	layout, err := s.resolveLayout()
	if err != nil {
		return operationError(state.OperationDelete, PhasePrepare, err)
	}
	_, err = s.validateInstalledVersion(layout.versions, filepath.Join(layout.versions, "go"+canonical))
	return err
}

func (s *Service) deleteLocked(ctx context.Context, store *state.MarkerStore, canonical string) (DeletionResult, error) {
	layout, err := s.resolveLayout()
	if err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, err)
	}
	active, present, err := s.readActiveVersion(layout.active)
	if err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, err)
	}
	if present && active == canonical {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, &ActiveVersionError{Version: canonical})
	}

	versionDir := filepath.Join(layout.versions, "go"+canonical)
	if _, err := s.validateInstalledVersion(layout.versions, versionDir); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, err)
	}
	if err := ctx.Err(); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, err)
	}
	id, err := s.fs.unique()
	if err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepare, fmt.Errorf("generate transaction id: %w", err))
	}
	quarantineName := deletionPrefix + id
	quarantinePath := filepath.Join(layout.versions, quarantineName)
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationDelete,
		Phase:         string(PhasePrepared),
		Version:       canonical,
		Artifacts: map[string]string{
			"source":     "go" + canonical,
			"quarantine": quarantineName,
		},
	}
	if err := store.Write(marker); err != nil {
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepared, err)
	}
	if err := ctx.Err(); err != nil {
		if deleteErr := store.Delete(); deleteErr != nil {
			err = errors.Join(err, deleteErr)
		}
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepared, err)
	}

	if err := s.fs.rename(versionDir, quarantinePath); err != nil {
		deleteErr := store.Delete()
		if deleteErr != nil {
			return DeletionResult{}, operationError(
				state.OperationDelete,
				PhasePrepared,
				errors.Join(fmt.Errorf("quarantine version directory: %w", err), deleteErr),
			)
		}
		return DeletionResult{}, operationError(state.OperationDelete, PhasePrepared, fmt.Errorf("quarantine version directory: %w", err))
	}
	if err := s.fs.syncDir(layout.versions); err != nil {
		return DeletionResult{
			Version:  canonical,
			Warnings: []Warning{&CleanupWarning{Operation: state.OperationDelete, Path: quarantinePath, Err: err}},
		}, nil
	}

	marker.Phase = string(PhaseQuarantined)
	if err := store.Write(marker); err != nil {
		return DeletionResult{
			Version:  canonical,
			Warnings: []Warning{&CleanupWarning{Operation: state.OperationDelete, Path: store.Path(), Err: err}},
		}, nil
	}
	if err := s.fs.removeAll(quarantinePath); err != nil {
		return DeletionResult{
			Version:  canonical,
			Warnings: []Warning{&CleanupWarning{Operation: state.OperationDelete, Path: quarantinePath, Err: err}},
		}, nil
	}
	if err := s.fs.syncDir(layout.versions); err != nil {
		return DeletionResult{
			Version:  canonical,
			Warnings: []Warning{&CleanupWarning{Operation: state.OperationDelete, Path: quarantinePath, Err: err}},
		}, nil
	}
	if err := store.Delete(); err != nil {
		return DeletionResult{
			Version:  canonical,
			Warnings: []Warning{&CleanupWarning{Operation: state.OperationDelete, Path: store.Path(), Err: err}},
		}, nil
	}
	return DeletionResult{Version: canonical}, nil
}

type layout struct {
	root     string
	versions string
	shims    string
	active   string
}

func (s *Service) resolveLayout() (layout, error) {
	root, err := s.resolver.RootDir()
	if err != nil {
		return layout{}, fmt.Errorf("resolve root directory: %w", err)
	}
	versions, err := s.resolver.VersionsDir()
	if err != nil {
		return layout{}, fmt.Errorf("resolve versions directory: %w", err)
	}
	shims, err := s.resolver.ShimDir()
	if err != nil {
		return layout{}, fmt.Errorf("resolve shim directory: %w", err)
	}
	active, err := s.resolver.ActiveVersionFile()
	if err != nil {
		return layout{}, fmt.Errorf("resolve active-version file: %w", err)
	}
	if !paths.IsDirectChild(root, versions) || !paths.IsDirectChild(root, shims) || !paths.IsDirectChild(root, active) {
		return layout{}, errors.New("resolved lifecycle paths are outside the canonical root")
	}
	return layout{root: root, versions: versions, shims: shims, active: active}, nil
}

func (s *Service) validateInstalledVersion(versionsDir, candidate string) (string, error) {
	if err := requireRealDirectory(s.fs, versionsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &NotInstalledError{Version: strings.TrimPrefix(filepath.Base(candidate), "go")}
		}
		return "", fmt.Errorf("versions path is not a real directory: %w", err)
	}
	if !paths.IsDirectChild(versionsDir, candidate) {
		return "", fmt.Errorf("unsafe installed-version path %q", candidate)
	}
	info, err := s.fs.lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", &NotInstalledError{Version: strings.TrimPrefix(filepath.Base(candidate), "go")}
	}
	if err != nil {
		return "", fmt.Errorf("inspect installed version: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("installed-version path is not a real directory: %q", candidate)
	}
	binDir := filepath.Join(candidate, "bin")
	info, err = s.fs.lstat(binDir)
	if err != nil {
		return "", fmt.Errorf("inspect installed-version bin directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("installed-version bin path is not a real directory: %q", binDir)
	}
	goName := "go"
	if s.fs.targetOS == "windows" {
		goName = "go.exe"
	}
	goPath := filepath.Join(binDir, goName)
	info, err = s.fs.lstat(goPath)
	if err != nil {
		return "", fmt.Errorf("inspect Go binary: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Go binary is not a regular file: %q", goPath)
	}
	return binDir, nil
}

type shimCandidate struct {
	name string
	path string
}

func (s *Service) shimCandidates(binDir string) ([]shimCandidate, error) {
	entries, err := s.fs.readDir(binDir)
	if err != nil {
		return nil, fmt.Errorf("read installed-version bin directory: %w", err)
	}
	candidates := make([]shimCandidate, 0, len(entries))
	outputNames := make(map[string]string)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect shim candidate %q: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(binDir, entry.Name())
		if !paths.IsDirectChild(binDir, path) {
			return nil, fmt.Errorf("unsafe shim candidate %q", entry.Name())
		}
		outputName, _ := renderShim(s.fs.targetOS, entry.Name(), path)
		collisionKey := outputName
		if s.fs.targetOS == "windows" {
			collisionKey = strings.ToLower(outputName)
		}
		if previous, exists := outputNames[collisionKey]; exists {
			return nil, fmt.Errorf("shim name collision between %q and %q", previous, entry.Name())
		}
		outputNames[collisionKey] = entry.Name()
		candidates = append(candidates, shimCandidate{name: entry.Name(), path: path})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })
	return candidates, nil
}

func (s *Service) readActiveVersion(path string) (string, bool, error) {
	info, err := s.fs.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect active-version file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("active-version path is not a regular file: %q", path)
	}
	data, err := s.fs.readFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read active-version file: %w", err)
	}
	value := string(data)
	if err := version.Validate(value); err != nil {
		return "", false, fmt.Errorf("validate active-version file: %w", err)
	}
	return value, true, nil
}

func renderShim(targetOS, binaryName, targetPath string) (string, []byte) {
	if targetOS == "windows" {
		outputName := binaryName
		if strings.EqualFold(filepath.Ext(outputName), ".exe") {
			outputName = outputName[:len(outputName)-len(filepath.Ext(outputName))]
		}
		outputName += ".bat"
		escaped := strings.ReplaceAll(targetPath, "%", "%%")
		content := "@echo off\r\nsetlocal DisableDelayedExpansion\r\n@\"" + escaped + "\" %*\r\n"
		return outputName, []byte(content)
	}
	escaped := "'" + strings.ReplaceAll(targetPath, "'", "'\"'\"'") + "'"
	content := "#!/bin/sh\nexec " + escaped + " \"$@\"\n"
	return binaryName, []byte(content)
}

func recoveryWarnings(warnings []state.Warning) []Warning {
	result := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, &RecoveryWarning{Warning: warning})
	}
	return result
}

func uniqueID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func writeDurableFile(fs fileSystem, path string, data []byte, mode fs.FileMode) error {
	if err := fs.writeFile(path, data, mode); err != nil {
		return err
	}
	if err := fs.chmod(path, mode); err != nil {
		return err
	}
	return fs.syncFile(path)
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func requireOptionalDirectory(fs fileSystem, path string) (bool, error) {
	info, err := fs.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("path is not a real directory: %q", path)
	}
	return true, nil
}

func requireRealDirectory(fs fileSystem, path string) error {
	info, err := fs.lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path is not a real directory: %q", path)
	}
	return nil
}
