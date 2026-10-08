// Package doctor runs read-only diagnostics over a govm installation
// and returns a Report the CLI renders. Every Check is a pure function
// over the injected Deps: no state lock, no writes under the govm
// root. The only network access is the source-reachability Check,
// which --offline skips. Checks run independently, so a failing Check
// never hides the others.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/utils"
)

// Verdict is the outcome of one Check. Only VerdictFail affects the
// doctor exit code; VerdictWarn means govm works but something is
// degraded.
type Verdict string

const (
	VerdictOK   Verdict = "ok"
	VerdictWarn Verdict = "warn"
	VerdictFail Verdict = "fail"
)

// Check names, in report order. Name identifies a Check in tests and
// callers; Detail carries the human-readable line.
const (
	CheckShimInPath             = "shim-in-path"
	CheckGoResolvesToShim       = "go-resolves-to-shim"
	CheckActiveVersion          = "active-version"
	CheckShimTargets            = "shim-targets"
	CheckGoVersion              = "go-version"
	CheckNoInterruptedOperation = "no-interrupted-operation"
	CheckSettings               = "settings"
	CheckSource                 = "source"
	CheckDisk                   = "disk"
)

// Check is one named diagnostic result. Detail is a complete
// one-line sentence; Hint is set only when Verdict is not ok.
type Check struct {
	Name    string
	Verdict Verdict
	Detail  string
	Hint    string
}

// Report is the ordered set of Checks plus the environment header.
type Report struct {
	GovmVersion string
	OS          string
	Arch        string
	// Root is the govm root directory, empty when it cannot be resolved.
	Root   string
	Checks []Check
}

// Failed reports whether any Check yielded VerdictFail.
func (r Report) Failed() bool {
	fails, _ := r.Counts()
	return fails > 0
}

// Counts returns the number of fail and warn verdicts.
func (r Report) Counts() (fails, warns int) {
	for _, c := range r.Checks {
		switch c.Verdict {
		case VerdictFail:
			fails++
		case VerdictWarn:
			warns++
		}
	}
	return fails, warns
}

// FileSystem is the read-only file-system seam the Checks use.
type FileSystem struct {
	Lstat    func(string) (fs.FileInfo, error)
	ReadFile func(string) ([]byte, error)
	ReadDir  func(string) ([]os.DirEntry, error)
}

// OSFileSystem returns a FileSystem backed by package os.
func OSFileSystem() FileSystem {
	return FileSystem{Lstat: os.Lstat, ReadFile: os.ReadFile, ReadDir: os.ReadDir}
}

// Deps are the injected dependencies of Run. Zero fields fall back to
// production values (see DefaultDeps), except Resolver which is
// required. Arch and GovmVersion only feed the Report header.
type Deps struct {
	Resolver *paths.Resolver
	// Path is the PATH environment variable value.
	Path string
	// LookPath resolves a binary name the way the shell would.
	LookPath func(string) (string, error)
	FS       FileSystem
	// RunVersion executes `<binary> version` and returns its stdout.
	RunVersion func(ctx context.Context, binary string) (string, error)
	// TargetOS selects shim naming ("go" vs "go.bat"); defaults to
	// runtime.GOOS.
	TargetOS    string
	Arch        string
	GovmVersion string
	// HTTPClient fetches the release catalog; the request timeout is
	// applied through the context, not the client.
	HTTPClient utils.Doer
	// Source overrides the distribution source; empty reads
	// settings.json and falls back to the go.dev default.
	Source string
	// Offline skips the source-reachability Check.
	Offline bool
	// SourceTimeout bounds the catalog fetch; zero means 5 s.
	SourceTimeout time.Duration
	// DiskUsage reports the versions and downloads footprint; defaults
	// to prune.Service.DiskUsage over Resolver.
	DiskUsage func(context.Context) (prune.Summary, error)
}

// DefaultDeps returns the production dependencies.
func DefaultDeps() Deps {
	return Deps{Resolver: paths.New(), Path: os.Getenv("PATH")}.withDefaults()
}

func runVersion(ctx context.Context, binary string) (string, error) {
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func (d Deps) withDefaults() Deps {
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.FS.Lstat == nil {
		d.FS.Lstat = os.Lstat
	}
	if d.FS.ReadFile == nil {
		d.FS.ReadFile = os.ReadFile
	}
	if d.FS.ReadDir == nil {
		d.FS.ReadDir = os.ReadDir
	}
	if d.RunVersion == nil {
		d.RunVersion = runVersion
	}
	if d.TargetOS == "" {
		d.TargetOS = runtime.GOOS
	}
	if d.Arch == "" {
		d.Arch = runtime.GOARCH
	}
	if d.GovmVersion == "" {
		d.GovmVersion = utils.GetVersion()
	}
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{}
	}
	if d.SourceTimeout == 0 {
		d.SourceTimeout = defaultSourceTimeout
	}
	if d.DiskUsage == nil {
		d.DiskUsage = diskUsage(d.Resolver)
	}
	return d
}

// Run executes the Checks in report order and returns the
// Report. It never returns an error: every failure to observe the
// environment is itself a Check result.
func Run(ctx context.Context, deps Deps) Report {
	deps = deps.withDefaults()
	e := probe(deps)
	report := Report{
		GovmVersion: deps.GovmVersion,
		OS:          deps.TargetOS,
		Arch:        deps.Arch,
		Root:        e.root,
	}
	for _, check := range checks {
		report.Checks = append(report.Checks, check(ctx, e))
	}
	return report
}

// checks lists every Check in report order: cause before effect,
// network and disk last.
var checks = []func(context.Context, *env) Check{
	checkShimInPath,
	checkGoResolvesToShim,
	checkActiveVersion,
	checkShimTargets,
	checkGoVersion,
	checkNoInterruptedOperation,
	checkSettings,
	checkSource,
	checkDisk,
}
