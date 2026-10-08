// Package install provides the transactional Go toolchain installer.
//
// The public Service performs an atomic install: download, integrity
// check, extract, validate the extracted toolchain, and swap it into
// place with rollback on failure. All side effects are coordinated
// through a cross-process lock and unique staging paths so a crash
// never leaves a half-installed version visible to the rest of govm.
package install

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/state"
	"github.com/SmileOniks/govm/internal/version"
)

const (
	// stagingPrefix marks in-progress extraction directories created
	// under the versions directory. Orphans carrying this prefix are
	// removed at the start of every install.
	stagingPrefix = ".install-"
	// partPrefix + partSuffix mark in-progress downloads stored under
	// the downloads directory.
	partPrefix = ".govm-install-"
	partSuffix = ".part"
	// backupPrefix marks the temporary copy of a pre-existing install
	// held during the commit swap so it can be restored on failure.
	backupPrefix = ".govm-backup-"
	// recoveryPrefix marks a backup that could not be restored after a
	// failed commit. It is preserved on disk and never cleaned by govm.
	recoveryPrefix = ".recovery-"

	// dirMode is the restrictive mode used for every govm-owned
	// directory created during an install.
	dirMode = 0o700
	// fileMode is the restrictive mode used for the download part file.
	fileMode = 0o600
)

// httpDoer executes a single HTTP request below the redirect/policy
// layer. Production wires an *http.Client whose CheckRedirect enforces
// the official-host policy; tests inject a fake to bypass it.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type extractor func(ctx context.Context, archivePath, destination string) error

// commandRunner runs the extracted toolchain's "go version" command
// and returns its combined output.
type commandRunner func(ctx context.Context, binary string) ([]byte, error)

// renamer renames old to new. It is os.Rename in production; tests
// inject failures to exercise the rollback paths deterministically.
type renamer func(old, new string) error

// remover removes a path tree. It is only used for post-commit
// cleanup, whose failures become non-fatal Result warnings.
// Failure-path cleanup always uses os.RemoveAll directly.
type remover func(path string) error

// Service is the transactional Go toolchain installer.
type Service struct {
	resolver *paths.Resolver
	state    *state.Coordinator
	doer     httpDoer
	extract  extractor
	verify   commandRunner
	rename   renamer
	cleanup  remover

	coordinatorOnce sync.Once
	coordinatorErr  error
}

// New returns a production-configured Service. Callers share one
// resolver and one coordinator with the other services that mutate
// installed-version state, so every mutation takes the same lock.
func New(resolver *paths.Resolver, coordinator *state.Coordinator) *Service {
	if resolver == nil {
		resolver = paths.New()
	}
	if coordinator == nil {
		coordinator = state.NewCoordinator(resolver)
	}
	s := &Service{
		resolver: resolver,
		state:    coordinator,
		doer:     newProductionHTTPClient(),
		extract:  extractArchive,
		verify:   defaultCommandRunner,
		rename:   os.Rename,
		cleanup:  os.RemoveAll,
	}
	s.coordinatorOnce.Do(func() {
		s.coordinatorErr = coordinator.RegisterRecoveryHandler(state.OperationInstall, s.RecoveryHandler())
	})
	return s
}

// RecoveryHandler exposes the install-specific transaction recovery seam for
// composition with a shared state coordinator.
func (s *Service) RecoveryHandler() state.RecoveryHandler {
	return installRecoveryHandler{service: s}
}

func (s *Service) ensureCoordinator() error {
	s.coordinatorOnce.Do(func() {
		if s.resolver == nil {
			s.resolver = paths.New()
		}
		if s.state == nil {
			s.state = state.NewCoordinator(s.resolver)
		}
		s.coordinatorErr = s.state.RegisterRecoveryHandler(state.OperationInstall, s.RecoveryHandler())
	})
	return s.coordinatorErr
}

// Install performs a transactional install of req.Version without progress
// reporting.
//
// Every failure path returns an *Error describing the stage; cleanup
// of the current staging/download artefacts is always attempted and
// any cleanup failure is joined onto the original error.
func (s *Service) Install(ctx context.Context, req Request) (Result, error) {
	return s.InstallWithProgress(ctx, req, nil)
}

// InstallWithProgress performs a transactional install and reports the
// current stage and download byte counts to reporter.
func (s *Service) InstallWithProgress(
	ctx context.Context,
	req Request,
	reporter ProgressReporter,
) (Result, error) {
	reportProgress(reporter, req, StageValidate)
	if err := validateRequest(req); err != nil {
		return Result{}, &Error{Stage: StageValidate, Err: err}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, &Error{Stage: StageValidate, Err: err}
	}
	reportProgress(reporter, req, StageLock)
	if err := s.ensureCoordinator(); err != nil {
		return Result{}, &Error{Stage: StageLock, Err: err}
	}

	reportProgress(reporter, req, StagePrepare)
	var result Result
	coordinated, mutateErr := s.state.Mutate(ctx, func(ctx context.Context, store *state.MarkerStore) error {
		root, err := s.resolver.RootDir()
		if err != nil {
			return &Error{Stage: StagePrepare, Err: err}
		}
		versionsDir, err := s.resolver.VersionsDir()
		if err != nil {
			return &Error{Stage: StagePrepare, Err: err}
		}
		downloadsDir, err := s.resolver.DownloadsDir()
		if err != nil {
			return &Error{Stage: StagePrepare, Err: err}
		}
		if !paths.IsDirectChild(root, versionsDir) || !paths.IsDirectChild(root, downloadsDir) {
			return &Error{Stage: StagePrepare, Err: errors.New("resolved install paths are outside the canonical root")}
		}
		for _, dir := range []string{root, versionsDir, downloadsDir} {
			if err := os.MkdirAll(dir, dirMode); err != nil {
				return &Error{Stage: StagePrepare, Err: fmt.Errorf("create directory %q: %w", dir, err)}
			}
			if err := requireRealInstallDirectory(dir); err != nil {
				return &Error{Stage: StagePrepare, Err: err}
			}
		}

		stagingDir := filepath.Join(versionsDir, stagingPrefix+uniqueID())
		if err := os.Mkdir(stagingDir, dirMode); err != nil {
			return &Error{Stage: StagePrepare, Err: fmt.Errorf("create staging directory: %w", err)}
		}
		partPath := filepath.Join(downloadsDir, partPrefix+uniqueID()+partSuffix)
		failPreparation := func(cause error) error {
			reportProgress(reporter, req, StageCleanup)
			joinFailureCleanup(partPath, stagingDir, &cause)
			return cause
		}
		finalDir := filepath.Join(versionsDir, "go"+req.Version)
		finalInfo, finalStatErr := os.Lstat(finalDir)
		finalExisted := finalStatErr == nil
		if finalStatErr != nil && !errors.Is(finalStatErr, os.ErrNotExist) {
			return failPreparation(&Error{Stage: StagePrepare, Err: fmt.Errorf("inspect existing install: %w", finalStatErr)})
		}
		if finalExisted && (finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.IsDir()) {
			return failPreparation(&Error{Stage: StagePrepare, Err: fmt.Errorf("existing install is not a real directory: %q", finalDir)})
		}

		result, err = s.installLocked(
			ctx,
			store,
			req,
			versionsDir,
			downloadsDir,
			stagingDir,
			partPath,
			finalExisted,
			reporter,
		)
		if err != nil {
			reportProgress(reporter, req, StageCleanup)
			joinFailureCleanup(partPath, stagingDir, &err)
		}
		return err
	})
	if mutateErr != nil {
		return result, installCoordinatorError(mutateErr)
	}
	for _, warning := range coordinated.RecoveryWarnings {
		result.Warnings = append(result.Warnings, Warning{
			Kind: WarningCleanup,
			Err:  errors.New(warning.Error()),
		})
	}
	return result, nil
}

// installLocked performs the complete install lifecycle while the coordinator
// lock is held. This includes creation and cleanup of all temporary artifacts.
func (s *Service) installLocked(
	ctx context.Context,
	store *state.MarkerStore,
	req Request,
	versionsDir,
	downloadsDir,
	stagingDir,
	partPath string,
	finalExisted bool,
	reporter ProgressReporter,
) (Result, error) {
	var warnings []Warning

	reportProgress(reporter, req, StageDownload)
	_, digest, err := s.download(ctx, req, partPath, reporter)
	if err != nil {
		return Result{}, &Error{Stage: StageDownload, Err: err}
	}
	reportProgress(reporter, req, StageIntegrity)
	if req.SHA256 == "" {
		warnings = append(warnings, Warning{Kind: WarningIntegrityUnavailable})
	} else {
		expected, err := hex.DecodeString(req.SHA256)
		if err != nil {
			return Result{}, &Error{Stage: StageIntegrity, Err: fmt.Errorf("decode archive checksum: %w", err)}
		}
		if subtle.ConstantTimeCompare(digest, expected) != 1 {
			return Result{}, &Error{Stage: StageIntegrity, Err: errors.New("archive checksum mismatch")}
		}
	}

	reportProgress(reporter, req, StageExtract)
	if err := s.extract(ctx, partPath, stagingDir); err != nil {
		return Result{}, &Error{Stage: StageExtract, Err: fmt.Errorf("extract archive: %w", err)}
	}
	reportProgress(reporter, req, StageVerify)
	binaryPath := filepath.Join(stagingDir, "go", "bin", binaryName())
	if _, err := os.Stat(binaryPath); err != nil {
		return Result{}, &Error{Stage: StageVerify, Err: fmt.Errorf("extracted binary missing: %w", err)}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, &Error{Stage: StageVerify, Err: err}
	}
	out, err := s.verify(ctx, binaryPath)
	if err != nil {
		return Result{}, &Error{Stage: StageVerify, Err: fmt.Errorf("run go version: %w", err)}
	}
	if err := verifyVersionOutput(out, req.Version); err != nil {
		return Result{}, &Error{Stage: StageVerify, Err: err}
	}

	finalDir := filepath.Join(versionsDir, "go"+req.Version)
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationInstall,
		Phase:         "prepared",
		Version:       req.Version,
		Artifacts: map[string]string{
			"staging":  filepath.Base(stagingDir),
			"download": filepath.Base(partPath),
			"target":   filepath.Base(finalDir),
		},
	}

	reportProgress(reporter, req, StageCommit)
	var committedWarnings []Warning
	installErr := func() error {
		if err := requireRealInstallDirectory(versionsDir); err != nil {
			return &Error{Stage: StageCommit, Err: err}
		}
		if err := requireRealInstallDirectory(downloadsDir); err != nil {
			return &Error{Stage: StageCommit, Err: err}
		}
		orphanWarnings := cleanOrphans(ctx, versionsDir, downloadsDir, map[string]struct{}{
			filepath.Base(stagingDir): {},
			filepath.Base(partPath):   {},
		})
		committedWarnings = append(committedWarnings, orphanWarnings...)
		reportProgress(reporter, req, StageCleanup)
		if err := ctx.Err(); err != nil {
			return &Error{Stage: StageCommit, Err: err}
		}
		if err := validatePreparedToolchain(stagingDir); err != nil {
			return &Error{Stage: StageCommit, Err: fmt.Errorf("prepared toolchain is no longer available: %w", err)}
		}
		if !finalExisted {
			if _, err := os.Lstat(finalDir); err == nil {
				// Another installer won the race while this request was
				// preparing. Keep its complete installation.
				if err := validateInstalledToolchain(finalDir); err != nil {
					return &Error{Stage: StageCommit, Err: fmt.Errorf("revalidate winning install: %w", err)}
				}
				var cleanupErrs []error
				for _, path := range []string{stagingDir, partPath} {
					if cleanupErr := s.cleanup(path); cleanupErr != nil {
						cleanupErrs = append(cleanupErrs, cleanupErr)
					}
				}
				if len(cleanupErrs) > 0 {
					committedWarnings = append(committedWarnings, Warning{
						Kind: WarningCleanup,
						Err:  errors.Join(cleanupErrs...),
					})
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return &Error{Stage: StageCommit, Err: fmt.Errorf("revalidate target: %w", err)}
			}
		}

		marker.Phase = "committing"
		backupDir := ""
		if _, err := os.Lstat(finalDir); err == nil {
			if err := validateInstalledToolchain(finalDir); err != nil {
				return &Error{Stage: StageCommit, Err: fmt.Errorf("revalidate target: %w", err)}
			}
			backupDir = filepath.Join(versionsDir, backupPrefix+uniqueID())
			marker.Artifacts["backup"] = filepath.Base(backupDir)
		} else if !errors.Is(err, os.ErrNotExist) {
			return &Error{Stage: StageCommit, Err: fmt.Errorf("revalidate target: %w", err)}
		}
		if err := store.Write(marker); err != nil {
			return &Error{Stage: StageCommit, Err: err}
		}
		backupDir, commitErr := s.commitWithBackup(stagingDir, finalDir, backupDir)
		if commitErr != nil {
			var stageErr *Error
			if errors.As(commitErr, &stageErr) {
				return stageErr
			}
			return &Error{Stage: StageCommit, Err: commitErr}
		}
		if err := syncInstallDirectory(versionsDir); err != nil {
			committedWarnings = append(committedWarnings, Warning{Kind: WarningCleanup, Err: err})
			return nil
		}

		var cleanupErrs []error
		for _, p := range []string{backupDir, stagingDir, partPath} {
			if p != "" {
				if rmErr := s.cleanup(p); rmErr != nil {
					cleanupErrs = append(cleanupErrs, rmErr)
				}
			}
		}
		if len(cleanupErrs) > 0 {
			committedWarnings = append(committedWarnings, Warning{Kind: WarningCleanup, Err: errors.Join(cleanupErrs...)})
			return nil
		}
		if err := syncInstallDirectory(versionsDir); err != nil {
			committedWarnings = append(committedWarnings, Warning{Kind: WarningCleanup, Err: err})
			return nil
		}
		if err := store.Delete(); err != nil {
			committedWarnings = append(committedWarnings, Warning{Kind: WarningCleanup, Err: err})
		}
		return nil
	}()
	if installErr != nil {
		return Result{}, installErr
	}
	warnings = append(warnings, committedWarnings...)
	return Result{Version: req.Version, Path: finalDir, Warnings: warnings}, nil
}

type installRecoveryHandler struct {
	service *Service
}

func (h installRecoveryHandler) Recover(ctx context.Context, marker state.Marker) (state.RecoveryResult, error) {
	s := h.service
	if err := validateInstallMarker(marker); err != nil {
		return state.RecoveryResult{}, err
	}
	versionsDir, err := s.resolver.VersionsDir()
	if err != nil {
		return state.RecoveryResult{}, err
	}
	downloadsDir, err := s.resolver.DownloadsDir()
	if err != nil {
		return state.RecoveryResult{}, err
	}
	pathFor := func(dir, name string) string {
		if name == "" {
			return ""
		}
		return filepath.Join(dir, name)
	}
	staging := pathFor(versionsDir, marker.Artifacts["staging"])
	download := pathFor(downloadsDir, marker.Artifacts["download"])
	target := pathFor(versionsDir, marker.Artifacts["target"])
	backup := ""
	if name := marker.Artifacts["backup"]; name != "" {
		backup = pathFor(versionsDir, name)
	}
	remove := func(path string) error {
		if path == "" {
			return nil
		}
		if err := s.cleanup(path); err != nil {
			return err
		}
		return nil
	}
	switch marker.Phase {
	case "prepared":
		if err := ctx.Err(); err != nil {
			return state.RecoveryResult{}, err
		}
		for _, path := range []string{staging, download} {
			if removeErr := remove(path); removeErr != nil {
				return state.RecoveryResult{}, removeErr
			}
		}
	case "committing":
		targetPresent, targetErr := realInstallDirectoryPresent(target)
		if targetErr != nil {
			return state.RecoveryResult{}, fmt.Errorf("inspect install target: %w", targetErr)
		}
		backupPresent := false
		if backup != "" {
			backupPresent, err = realInstallDirectoryPresent(backup)
			if err != nil {
				return state.RecoveryResult{}, fmt.Errorf("inspect install backup: %w", err)
			}
		}
		switch {
		case !targetPresent && backupPresent:
			if err := os.Rename(backup, target); err != nil {
				return state.RecoveryResult{}, fmt.Errorf("restore install backup: %w", err)
			}
		case !targetPresent && !backupPresent:
			return state.RecoveryResult{}, errors.New("install marker has neither target nor backup")
		case targetPresent && backupPresent:
			// The new target committed; backup cleanup remains.
		case targetPresent:
			// Either a first install committed or the old target remained.
		}
		for _, path := range []string{staging, download, backup} {
			if err := remove(path); err != nil {
				return state.RecoveryResult{Warnings: []state.Warning{{
					Message: "install recovery cleanup failed",
					Err:     err,
				}}}, nil
			}
		}
	default:
		return state.RecoveryResult{}, fmt.Errorf("unsupported install recovery phase %q", marker.Phase)
	}
	storeRoot, err := s.resolver.RootDir()
	if err != nil {
		return state.RecoveryResult{}, err
	}
	if err := state.NewMarkerStore(storeRoot).Delete(); err != nil {
		return state.RecoveryResult{}, err
	}
	return state.RecoveryResult{}, nil
}

func realInstallDirectoryPresent(path string) (bool, error) {
	info, err := os.Lstat(path)
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

func validateInstallMarker(marker state.Marker) error {
	switch marker.Phase {
	case "prepared":
		if len(marker.Artifacts) != 3 {
			return fmt.Errorf("prepared install marker has unexpected artifacts")
		}
	case "committing":
		if len(marker.Artifacts) != 3 && len(marker.Artifacts) != 4 {
			return fmt.Errorf("committing install marker has unexpected artifacts")
		}
	default:
		return fmt.Errorf("unsupported install recovery phase %q", marker.Phase)
	}
	if staging := marker.Artifacts["staging"]; !strings.HasPrefix(staging, stagingPrefix) || len(staging) == len(stagingPrefix) {
		return fmt.Errorf("missing or unexpected install staging artifact %q", staging)
	}
	if download := marker.Artifacts["download"]; !strings.HasPrefix(download, partPrefix) ||
		!strings.HasSuffix(download, partSuffix) ||
		len(download) == len(partPrefix)+len(partSuffix) {
		return fmt.Errorf("missing or unexpected install download artifact %q", download)
	}
	if target := marker.Artifacts["target"]; target != "go"+marker.Version {
		return fmt.Errorf("unexpected install target artifact %q", target)
	}
	backup, hasBackup := marker.Artifacts["backup"]
	if marker.Phase == "prepared" && hasBackup {
		return fmt.Errorf("prepared install marker unexpectedly contains backup %q", backup)
	}
	if hasBackup && (!strings.HasPrefix(backup, backupPrefix) || len(backup) == len(backupPrefix)) {
		return fmt.Errorf("unexpected install backup artifact %q", backup)
	}
	return nil
}

func requireRealInstallDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect install directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("install path is not a real directory: %q", path)
	}
	return nil
}

func validatePreparedToolchain(stagingDir string) error {
	if err := requireRealInstallDirectory(stagingDir); err != nil {
		return err
	}
	return validateInstalledToolchain(filepath.Join(stagingDir, "go"))
}

func validateInstalledToolchain(toolchainDir string) error {
	if err := requireRealInstallDirectory(toolchainDir); err != nil {
		return err
	}
	binDir := filepath.Join(toolchainDir, "bin")
	if err := requireRealInstallDirectory(binDir); err != nil {
		return err
	}
	binaryPath := filepath.Join(binDir, binaryName())
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect Go binary %q: %w", binaryPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("Go binary is not a regular file: %q", binaryPath)
	}
	return nil
}

func syncInstallDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open install directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync install directory: %w", err)
	}
	return nil
}

func installCoordinatorError(err error) error {
	var busy *state.BusyError
	if errors.As(err, &busy) {
		return &Error{Stage: StageLock, Err: errors.New("another installation is in progress")}
	}
	var stageErr *Error
	if errors.As(err, &stageErr) {
		return stageErr
	}
	return &Error{Stage: StageCommit, Err: err}
}

// download streams the archive into partPath exactly once. It returns
// the computed SHA-256 digest of the bytes written so callers can
// verify integrity without re-reading the file.
func (s *Service) download(
	ctx context.Context,
	req Request,
	partPath string,
	reporter ProgressReporter,
) (int64, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := s.doer.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, nil, fmt.Errorf("download failed: HTTP %s", resp.Status)
	}

	partFile, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return 0, nil, fmt.Errorf("create download file: %w", err)
	}
	hasher := sha256.New()
	// Hard cap: read at most one byte beyond the maximum so an
	// overshoot is detectable without an unbounded copy.
	limited := io.LimitReader(&ctxReader{ctx: ctx, r: resp.Body}, MaxDownloadSize+1)
	writer := &progressWriter{
		writer:   io.MultiWriter(partFile, hasher),
		reporter: reporter,
		progress: Progress{
			Version:    req.Version,
			Stage:      StageDownload,
			BytesTotal: req.Size,
		},
	}
	written, copyErr := io.Copy(writer, limited)
	closeErr := partFile.Close()
	if copyErr != nil {
		return 0, nil, copyErr
	}
	if closeErr != nil {
		return 0, nil, fmt.Errorf("close download file: %w", closeErr)
	}
	if written > MaxDownloadSize {
		return 0, nil, fmt.Errorf("download exceeds maximum compressed size %d bytes", MaxDownloadSize)
	}
	if written == 0 {
		return 0, nil, errors.New("downloaded archive is empty")
	}
	if req.Size > 0 && written != req.Size {
		return 0, nil, fmt.Errorf("download size mismatch: got %d bytes, want %d", written, req.Size)
	}
	return written, hasher.Sum(nil), nil
}

type progressWriter struct {
	writer   io.Writer
	reporter ProgressReporter
	progress Progress
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.progress.BytesReceived += int64(n)
		reportProgressValue(w.reporter, w.progress)
	}
	return n, err
}

func reportProgress(reporter ProgressReporter, req Request, stage Stage) {
	progress := Progress{
		Version: req.Version,
		Stage:   stage,
	}
	if stage == StageDownload {
		progress.BytesTotal = req.Size
	}
	reportProgressValue(reporter, progress)
}

func reportProgressValue(reporter ProgressReporter, progress Progress) {
	if reporter != nil {
		reporter.Report(progress)
	}
}

// commitWithBackup performs the transactional swap of staging/go ->
// finalDir.
//
// If finalDir already exists it is first renamed to a unique backup. On
// commit failure the backup is restored; if restoration also fails the
// backup is preserved under a .recovery-* path and the returned *Error
// carries RecoveryPath.
func (s *Service) commitWithBackup(stagingDir, finalDir, backupDir string) (string, error) {
	goDir := filepath.Join(stagingDir, "go")
	if backupDir == "" {
		if _, statErr := os.Stat(finalDir); statErr == nil {
			backupDir = filepath.Join(filepath.Dir(finalDir), backupPrefix+uniqueID())
		}
	}
	if backupDir != "" {
		if mvErr := s.rename(finalDir, backupDir); mvErr != nil {
			return "", fmt.Errorf("back up existing install: %w", mvErr)
		}
	}
	if mvErr := s.rename(goDir, finalDir); mvErr != nil {
		if backupDir != "" {
			if rbErr := s.rename(backupDir, finalDir); rbErr != nil {
				return s.preserveBackup(backupDir, mvErr, rbErr)
			}
		}
		// Rollback succeeded (or there was nothing to roll back): the
		// backup no longer occupies backupDir, so drop the path.
		return "", mvErr
	}
	return backupDir, nil
}

// preserveBackup moves a rollback-failed backup to a distinct
// .recovery-* path and returns a StageCommit *Error carrying the
// recovery location. The rename to the recovery path uses os.Rename
// directly (not the test hook) so disaster recovery does not depend on
// the same hook that just failed twice.
func (s *Service) preserveBackup(backupDir string, commitErr, rollbackErr error) (string, error) {
	recoveryPath := filepath.Join(filepath.Dir(backupDir), recoveryPrefix+uniqueID())
	preserved := recoveryPath
	if mvErr := os.Rename(backupDir, recoveryPath); mvErr != nil {
		// Could not move: leave the backup where it is so the user can
		// recover it manually.
		preserved = backupDir
	}
	return "", &Error{
		Stage: StageCommit,
		Err: fmt.Errorf("commit failed: %w; rollback failed: %v",
			commitErr, rollbackErr),
		RecoveryPath: preserved,
	}
}

// verifyVersionOutput ensures the toolchain reports exactly the
// expected version and production platform.
func verifyVersionOutput(out []byte, version string) error {
	fields := strings.Fields(string(out))
	want := []string{
		"go",
		"version",
		"go" + version,
		runtime.GOOS + "/" + runtime.GOARCH,
	}
	if len(fields) != 4 ||
		fields[0] != want[0] ||
		fields[1] != want[1] ||
		fields[2] != want[2] ||
		fields[3] != want[3] {
		return fmt.Errorf("unexpected toolchain version output %q", strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultCommandRunner runs the extracted go binary with the
// production platform's binary name.
func defaultCommandRunner(ctx context.Context, binary string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	return cmd.CombinedOutput()
}

// binaryName returns the production go binary name for the host.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "go.exe"
	}
	return "go"
}

// newProductionHTTPClient returns a client restricted to HTTPS for every
// redirect hop. The initial archive host comes from the configured
// distribution source, and mirrors may redirect to HTTPS CDN hosts.
func newProductionHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-https scheme: %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// validateRequest checks every invariant that can be verified without
// side effects.
func validateRequest(req Request) error {
	if req.Version == "" {
		return errors.New("version is required")
	}
	if err := version.Validate(req.Version); err != nil {
		return fmt.Errorf("malformed version %q", req.Version)
	}
	expectedExt := "tar.gz"
	if runtime.GOOS == "windows" {
		expectedExt = "zip"
	}
	expectedFilename := "go" + req.Version + "." + runtime.GOOS + "-" + runtime.GOARCH + "." + expectedExt
	if req.Filename == "" {
		return errors.New("filename is required")
	}
	if req.Filename != expectedFilename {
		return fmt.Errorf("filename %q does not match expected %q for this platform", req.Filename, expectedFilename)
	}
	if req.Size < 0 {
		return fmt.Errorf("invalid size %d", req.Size)
	}
	if req.Size > MaxDownloadSize {
		return fmt.Errorf("declared size %d exceeds maximum %d", req.Size, MaxDownloadSize)
	}
	if req.SHA256 != "" {
		if len(req.SHA256) != sha256.Size*2 {
			return fmt.Errorf("invalid sha256 length %d", len(req.SHA256))
		}
		if _, err := hex.DecodeString(req.SHA256); err != nil {
			return fmt.Errorf("invalid sha256 hex: %w", err)
		}
	}
	if err := validateURL(req.URL, req.Filename); err != nil {
		return err
	}
	return nil
}

// validateURL enforces the configured distribution source URL shape.
func validateURL(raw, filename string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("url scheme must be https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("url host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("url must not contain user information, query parameters, or a fragment")
	}
	if u.Path == "" || !strings.HasSuffix(u.Path, "/"+filename) {
		return fmt.Errorf("url path must end with %q, got %q", filename, u.Path)
	}
	base := u.Path
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if base != filename {
		return fmt.Errorf("url basename %q does not match filename %q", base, filename)
	}
	return nil
}

// cleanOrphans removes recognisable in-progress artefacts left by
// previously crashed installs and preserves interrupted commit backups
// under recovery names. Recovery backups are never removed.
func cleanOrphans(ctx context.Context, versionsDir, downloadsDir string, owned map[string]struct{}) []Warning {
	var warnings []Warning
	if err := ctx.Err(); err != nil {
		return warnings
	}
	if entries, err := os.ReadDir(versionsDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(versionsDir, name)
			if !paths.IsDirectChild(versionsDir, full) {
				continue
			}
			switch {
			case strings.HasPrefix(name, stagingPrefix):
				if _, ok := owned[name]; ok {
					continue
				}
				if err := os.RemoveAll(full); err != nil {
					warnings = append(warnings, Warning{
						Kind: WarningCleanup,
						Err:  fmt.Errorf("remove orphan staging %q: %w", full, err),
					})
				}
			case strings.HasPrefix(name, backupPrefix):
				recoveryPath := filepath.Join(versionsDir, recoveryPrefix+uniqueID())
				if err := os.Rename(full, recoveryPath); err != nil {
					warnings = append(warnings, Warning{
						Kind: WarningCleanup,
						Err:  fmt.Errorf("preserve interrupted installation backup %q: %w", full, err),
					})
					continue
				}
				warnings = append(warnings, Warning{
					Kind: WarningCleanup,
					Err:  fmt.Errorf("interrupted installation backup preserved at %s", recoveryPath),
				})
			}
		}
	}
	if entries, err := os.ReadDir(downloadsDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, partPrefix) || !strings.HasSuffix(name, partSuffix) {
				continue
			}
			full := filepath.Join(downloadsDir, name)
			if !paths.IsDirectChild(downloadsDir, full) {
				continue
			}
			if _, ok := owned[name]; ok {
				continue
			}
			if err := os.RemoveAll(full); err != nil {
				warnings = append(warnings, Warning{
					Kind: WarningCleanup,
					Err:  fmt.Errorf("remove orphan download %q: %w", full, err),
				})
			}
		}
	}
	return warnings
}

// joinFailureCleanup removes the current download and staging
// artefacts after a failed install, joining any removal errors onto
// the original failure so they remain observable.
func joinFailureCleanup(partPath, stagingDir string, err *error) {
	var errs []error
	if *err != nil {
		errs = append(errs, *err)
	}
	if partPath != "" {
		if rmErr := os.RemoveAll(partPath); rmErr != nil {
			errs = append(errs, fmt.Errorf("remove download: %w", rmErr))
		}
	}
	if stagingDir != "" {
		if rmErr := os.RemoveAll(stagingDir); rmErr != nil {
			errs = append(errs, fmt.Errorf("remove staging: %w", rmErr))
		}
	}
	switch len(errs) {
	case 0:
	case 1:
		*err = errs[0]
	default:
		*err = errors.Join(errs...)
	}
}

// uniqueID returns a short hex identifier suitable for disambiguating
// staging, backup and recovery paths.
func uniqueID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ctxReader aborts reads as soon as the context is cancelled, so long
// downloads do not outlive a cancelled install.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
