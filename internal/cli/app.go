package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/application"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/SmileOniks/govm/internal/doctor"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/utils"
)

// loadCatalogFunc returns the available Go version catalog. The CLI
// depends on this narrow seam rather than on the composition root, so
// version matching can be exercised without wiring real services.
type loadCatalogFunc func(context.Context) ([]utils.GoVersion, error)

type activateFunc func(context.Context, string) (lifecycle.ActivationResult, error)
type deleteFunc func(context.Context, string) (lifecycle.DeletionResult, error)
type prunePreviewFunc func(context.Context) (prune.Result, error)
type pruneFunc func(context.Context) (prune.Result, error)
type changeDistributionSourceFunc func(context.Context, string) (application.DistributionSourceResult, error)

// doctorFunc runs the read-only diagnostics; offline skips the
// source-reachability Check.
type doctorFunc func(ctx context.Context, offline bool) doctor.Report

// InstallVersion resolves and installs a Go version from the configured
// distribution source.
func (a *App) InstallVersion(version string) {
	fmt.Fprintf(a.out, "🔍 Looking for Go version matching %s...\n", version)
	matchedVersion, err := findMatchingVersion(a.operations.LoadCatalog, version)
	if err != nil {
		fmt.Fprintf(a.out, "❌ %s\n", err)
		return
	}
	fmt.Fprintf(a.out, "📥 Installing Go %s...\n", matchedVersion.Version)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	newInstallAdapter(matchedVersion, a.operations.Install, a.out).run(ctx)
}

// Operations contains the narrow core seams used by App.
type Operations struct {
	LoadCatalog              loadCatalogFunc
	Install                  installFunc
	Activate                 activateFunc
	Delete                   deleteFunc
	PreviewPrune             prunePreviewFunc
	Prune                    pruneFunc
	Registry                 local.Registry
	ShimInPath               func() bool
	ChangeDistributionSource changeDistributionSourceFunc
	Doctor                   doctorFunc
	// Deps runs the dependency operations of the module at ModuleDir
	// for the `govm deps` commands; main.go binds the same executor
	// the TUI uses. A nil Deps (and ModuleDir) makes `govm deps`
	// report that it is not configured, as the doctor-only App does.
	Deps      deps.API
	ModuleDir string
}

func (a *App) ChangeDistributionSource(source string) bool {
	if a.operations.ChangeDistributionSource == nil {
		fmt.Fprintln(a.out, "Error: distribution source operation is not configured")
		return false
	}
	result, err := a.operations.ChangeDistributionSource(context.Background(), source)
	if err != nil {
		fmt.Fprintf(a.out, "Error: %v\n", err)
		var changeErr *application.ChangeError
		if !errors.As(err, &changeErr) || changeErr.SourcePreserved {
			fmt.Fprintln(a.out, "Previous distribution source was preserved.")
		} else {
			fmt.Fprintln(a.out, "Previous distribution source could not be guaranteed preserved.")
		}
		return false
	}
	fmt.Fprintf(a.out, "Distribution source changed to %s.\n", result.Source)
	fmt.Fprintln(a.out, "Matching archive verified for the current platform.")
	return true
}

// App maps CLI commands and process I/O onto core operations.
type App struct {
	operations Operations
	in         io.Reader
	out        io.Writer
	errOut     io.Writer
}

// NewApp constructs a testable CLI adapter.
func NewApp(operations Operations, in io.Reader, out, errOut io.Writer) *App {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	if operations.Registry == nil {
		operations.Registry = local.NewRegistry(paths.New())
	}
	if operations.ShimInPath == nil {
		operations.ShimInPath = utils.IsShimInPath
	}
	return &App{operations: operations, in: in, out: out, errOut: errOut}
}

// UseVersion resolves and activates an installed Go version.
func (a *App) UseVersion(version string) {
	fmt.Fprintf(a.out, "🔍 Looking for installed Go version matching %s...\n", version)
	matchedToolchain, err := a.operations.Registry.Find(context.Background(), version)
	if err != nil {
		if errors.Is(err, local.ErrNotFound) {
			fmt.Fprintf(a.out, "❌ no installed version matching '%s' found\n", version)
		} else {
			fmt.Fprintf(a.out, "❌ failed to read installed versions: %v\n", err)
		}
		return
	}
	matchedVersion := utils.GoVersion{Version: matchedToolchain.Version, Path: matchedToolchain.Path, Installed: true}
	fmt.Fprintf(a.out, "🔄 Switching to Go %s...\n", matchedVersion.Version)
	if a.operations.Activate == nil {
		fmt.Fprintln(a.out, "❌ Failed to switch version: no lifecycle activator configured")
		return
	}
	result, err := a.operations.Activate(context.Background(), matchedVersion.Version)
	if err != nil {
		fmt.Fprintf(a.out, "❌ Failed to switch version: %v\n", err)
		return
	}
	fmt.Fprintf(a.out, "✅ Switched to Go %s\n", matchedVersion.Version)
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.out, "⚠️  %s\n", warning)
	}
	if !a.operations.ShimInPath() {
		fmt.Fprintln(a.out, "\n⚠️  GoVM is not in your PATH")
		fmt.Fprintln(a.out, utils.GetShimPathInstructions())
		return
	}
	fmt.Fprintln(a.out, "🚀 Run 'go version' in a new terminal to verify")
}

// ListVersions prints installed Go versions.
func (a *App) ListVersions() {
	fmt.Fprintln(a.out, "📋 Installed Go Versions:")
	toolchains, err := a.operations.Registry.List(context.Background())
	if err != nil {
		fmt.Fprintf(a.out, "❌ Error reading installed versions: %v\n", err)
		return
	}
	activeVersion, err := a.operations.Registry.Active(context.Background())
	if err != nil {
		fmt.Fprintf(a.out, "❌ Error reading active version: %v\n", err)
		return
	}
	if len(toolchains) == 0 {
		fmt.Fprintln(a.out, "  No versions installed yet")
		return
	}
	for _, toolchain := range toolchains {
		if toolchain.Version == activeVersion {
			fmt.Fprintf(a.out, "  %s ✓ (active)\n", toolchain.Version)
		} else {
			fmt.Fprintf(a.out, "  %s\n", toolchain.Version)
		}
	}
	fmt.Fprintln(a.out, "\nTo install a new version: govm install <version>")
	fmt.Fprintln(a.out, "To switch versions: govm use <version>")
}

// DeleteVersion confirms and deletes an installed Go version.
func (a *App) DeleteVersion(version string) {
	fmt.Fprintf(a.out, "🔍 Looking for installed Go version matching %s...\n", version)
	matchedToolchain, err := a.operations.Registry.Find(context.Background(), version)
	if err != nil {
		if errors.Is(err, local.ErrNotFound) {
			fmt.Fprintf(a.out, "❌ no installed version matching '%s' found\n", version)
		} else {
			fmt.Fprintf(a.out, "❌ failed to read installed versions: %v\n", err)
		}
		return
	}
	matchedVersion := utils.GoVersion{Version: matchedToolchain.Version, Path: matchedToolchain.Path, Installed: true}

	activeVersion, err := a.operations.Registry.Active(context.Background())
	if err != nil {
		fmt.Fprintf(a.out, "❌ Failed to read active version: %v\n", err)
		return
	}
	if matchedVersion.Version == activeVersion {
		fmt.Fprintln(a.out, "❌ Cannot delete active version. Switch to another version first using 'govm use'.")
		return
	}

	fmt.Fprintf(a.out, "⚠️  Are you sure you want to delete Go %s? (y/N): ", matchedVersion.Version)
	var response string
	if _, err := fmt.Fscan(a.in, &response); err != nil && err != io.EOF {
		fmt.Fprintf(a.errOut, "Failed to read confirmation: %v\n", err)
	}
	if !strings.EqualFold(response, "y") {
		fmt.Fprintln(a.out, "🛑 Operation canceled.")
		return
	}

	fmt.Fprintf(a.out, "🗑️  Deleting Go %s...\n", matchedVersion.Version)
	if a.operations.Delete == nil {
		fmt.Fprintln(a.out, "❌ Failed to delete version: no lifecycle deleter configured")
		return
	}
	result, err := a.operations.Delete(context.Background(), matchedVersion.Version)
	if err != nil {
		fmt.Fprintf(a.out, "❌ Failed to delete version: %v\n", err)
		return
	}
	fmt.Fprintf(a.out, "✅ Successfully deleted Go %s\n", matchedVersion.Version)
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.out, "⚠️  %s\n", warning)
	}
}

// PruneVersions previews and optionally removes inactive toolchains and
// govm-owned temporary downloads.
func (a *App) PruneVersions(args ...string) bool {
	yes, dryRun, err := parsePruneArgs(args)
	if err != nil {
		fmt.Fprintf(a.out, "Error: %v\n", err)
		return false
	}
	if a.operations.PreviewPrune == nil || a.operations.Prune == nil {
		fmt.Fprintln(a.out, "Error: prune service is not configured")
		return false
	}

	result, previewErr := a.operations.PreviewPrune(context.Background())
	printPrunePlan(a.out, result, dryRun)
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.out, "Warning: %s\n", warning)
	}
	if previewErr != nil {
		fmt.Fprintf(a.out, "Error: cannot safely prune versions: %v\n", previewErr)
		if len(result.Candidates) == 0 {
			return false
		}
	}
	if len(result.Candidates) == 0 {
		fmt.Fprintln(a.out, "Nothing to prune.")
		return previewErr == nil
	}
	if dryRun {
		return previewErr == nil
	}
	if !yes {
		fmt.Fprint(a.out, "Are you sure? (y/N): ")
	}
	if !yes && !confirm(a.in) {
		fmt.Fprintln(a.out, "Operation canceled.")
		return true
	}

	fmt.Fprintln(a.out, "Pruning...")
	removed, pruneErr := a.operations.Prune(context.Background())
	for _, candidate := range removed.Removed {
		fmt.Fprintf(a.out, "Removed %s (%d bytes)\n", candidate.Path, candidate.Bytes)
	}
	for _, warning := range removed.Warnings {
		fmt.Fprintf(a.out, "Warning: %s\n", warning)
	}
	if pruneErr != nil {
		fmt.Fprintf(a.out, "Error: prune completed with warnings: %v\n", pruneErr)
		return false
	}
	fmt.Fprintf(a.out, "Freed %d bytes.\n", pruneRemovedBytes(removed))
	return true
}

func parsePruneArgs(args []string) (yes, dryRun bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dryRun = true
		case "--help", "-h":
			return false, false, errors.New("usage: govm prune [--yes] [--dry-run]")
		default:
			return false, false, fmt.Errorf("unknown prune option %q", arg)
		}
	}
	return yes, dryRun, nil
}

func confirm(in io.Reader) bool {
	var response string
	if _, err := fmt.Fscan(in, &response); err != nil {
		return false
	}
	return strings.EqualFold(response, "y")
}

func printPrunePlan(out io.Writer, result prune.Result, dryRun bool) {
	action := "Would remove"
	if !dryRun {
		action = "Plan to remove"
	}
	fmt.Fprintf(out, "%s %d object(s), %d bytes:\n", action, len(result.Candidates), pruneResultBytes(result))
	for _, candidate := range result.Candidates {
		fmt.Fprintf(out, "  %s (%d bytes)\n", candidate.Path, candidate.Bytes)
	}
}

func pruneResultBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Candidates {
		total += candidate.Bytes
	}
	return total
}

func pruneRemovedBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Removed {
		total += candidate.Bytes
	}
	return total
}

// DepsCommand routes `govm deps <subcommand>` and reports whether it
// succeeded so the caller can map failure to a non-zero exit code.
func (a *App) DepsCommand(args ...string) bool {
	subcommand := "help"
	if len(args) > 0 {
		subcommand = args[0]
	}
	switch subcommand {
	case "help", "-h", "--help":
		printDepsUsage(a.out)
		return true
	case "list", "check", "update", "backups", "restore":
	default:
		fmt.Fprintf(a.out, "Unknown deps subcommand: %s\n", subcommand)
		fmt.Fprintln(a.out, "Run 'govm deps help' for usage.")
		return false
	}
	// Each subcommand parses its own arguments first, so an invalid
	// invocation is reported even when no executor is configured.
	var run func(*DepsService) error
	switch subcommand {
	case "list":
		run = (*DepsService).RunList
	case "check":
		level, parseErr := parseDepsCheckArgs(args[1:])
		if parseErr != nil {
			fmt.Fprintf(a.out, "Error: %v\n", parseErr)
			return false
		}
		run = func(s *DepsService) error { return s.RunCheck(level) }
	case "update":
		opts, parseErr := parseDepsUpdateArgs(args[1:])
		if parseErr != nil {
			fmt.Fprintf(a.out, "Error: %v\n", parseErr)
			return false
		}
		run = func(s *DepsService) error { return s.RunUpdate(opts) }
	case "backups":
		run = (*DepsService).RunBackups
	case "restore":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		run = func(s *DepsService) error { return s.RunRestore(name) }
	}
	if a.operations.Deps == nil {
		fmt.Fprintln(a.out, "Error: dependency operations are not configured")
		return false
	}
	service := NewDepsService(a.operations.ModuleDir, a.operations.Deps, a.out, a.in)
	if err := run(service); err != nil {
		fmt.Fprintf(a.out, "❌ %s\n", err)
		return false
	}
	return true
}

func printDepsUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  govm deps list                          List current module dependencies")
	fmt.Fprintln(out, "  govm deps check [--patch|--minor]       Check for available dependency updates")
	fmt.Fprintln(out, "  govm deps update [options] [module...]  Update dependencies (interactive)")
	fmt.Fprintln(out, "  govm deps backups                       List dependency backups")
	fmt.Fprintln(out, "  govm deps restore <file>                Restore dependency backup")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Update options:")
	fmt.Fprintln(out, "  --patch        Only patch updates (same major.minor)")
	fmt.Fprintln(out, "  --minor        Only minor and patch updates (same major)")
	fmt.Fprintln(out, "  --dry-run      Print the update plan without changing anything")
	fmt.Fprintln(out, "  -y, --yes      Answer yes to every prompt (apply, checks, rollback)")
	fmt.Fprintln(out, "  module...      Update only these modules (full path or unique suffix,")
	fmt.Fprintln(out, "                 e.g. spf13/cobra); without modules every direct dependency")
}

// parseDepsUpdateArgs parses `govm deps update` options and module
// queries. Flags may appear anywhere; --patch and --minor are mutually
// exclusive.
func parseDepsUpdateArgs(args []string) (UpdateOptions, error) {
	var opts UpdateOptions
	level, modules, err := parseDepsLevelArgs(args, func(arg string) (bool, error) {
		switch arg {
		case "--dry-run":
			opts.DryRun = true
		case "--yes", "-y":
			opts.Yes = true
		case "--help", "-h":
			return false, errors.New("usage: govm deps update [--patch|--minor] [--dry-run] [--yes] [module...]")
		default:
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return UpdateOptions{}, err
	}
	opts.Level = level
	opts.Modules = modules
	return opts, nil
}

// parseDepsCheckArgs parses `govm deps check` options (level only).
func parseDepsCheckArgs(args []string) (deps.UpdateLevel, error) {
	level, modules, err := parseDepsLevelArgs(args, func(arg string) (bool, error) {
		if arg == "--help" || arg == "-h" {
			return false, errors.New("usage: govm deps check [--patch|--minor]")
		}
		return false, nil
	})
	if err != nil {
		return deps.LevelLatest, err
	}
	if len(modules) > 0 {
		return deps.LevelLatest, fmt.Errorf("unexpected argument %q", modules[0])
	}
	return level, nil
}

// parseDepsLevelArgs handles the shared --patch/--minor flags, hands
// every other dash-prefixed argument to extra, and collects the rest
// as positional module queries.
func parseDepsLevelArgs(args []string, extra func(string) (bool, error)) (deps.UpdateLevel, []string, error) {
	level := deps.LevelLatest
	levelSet := false
	var modules []string
	setLevel := func(l deps.UpdateLevel) error {
		if levelSet && l != level {
			return errors.New("--patch and --minor are mutually exclusive")
		}
		level, levelSet = l, true
		return nil
	}
	for _, arg := range args {
		switch {
		case arg == "--patch":
			if err := setLevel(deps.LevelPatch); err != nil {
				return deps.LevelLatest, nil, err
			}
		case arg == "--minor":
			if err := setLevel(deps.LevelMinor); err != nil {
				return deps.LevelLatest, nil, err
			}
		case strings.HasPrefix(arg, "-"):
			handled, err := extra(arg)
			if err != nil {
				return deps.LevelLatest, nil, err
			}
			if !handled {
				return deps.LevelLatest, nil, fmt.Errorf("unknown deps option %q", arg)
			}
		default:
			modules = append(modules, arg)
		}
	}
	return level, modules, nil
}
