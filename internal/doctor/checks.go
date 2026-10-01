package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/state"
	"github.com/smileoniks-ctrl/govm/internal/utils"
	"github.com/smileoniks-ctrl/govm/internal/version"
)

// goVersionTimeout bounds the `go version` probe so a hung shim cannot
// stall the whole report.
const goVersionTimeout = 5 * time.Second

// env is the environment snapshot shared by the Checks. probe fills it
// once so the Checks do not repeat the same reads and disagree.
type env struct {
	deps Deps

	root, shim, versions, activeFile, settingsFile, markerFile string
	// layoutErr is set when the resolver cannot produce the layout
	// (typically no home directory). Every Check reports it.
	layoutErr error

	// installed maps a bare version to its version directory.
	installed map[string]string
	scanErr   error

	// active is the canonical active version, empty when the
	// active_version file is absent or invalid.
	active string

	source string
}

func probe(deps Deps) *env {
	e := &env{deps: deps, installed: map[string]string{}}
	r := deps.Resolver
	var err error
	if e.root, err = r.RootDir(); err != nil {
		e.layoutErr = err
		e.source = resolveSource(deps, "")
		return e
	}
	e.shim, _ = r.ShimDir()
	e.versions, _ = r.VersionsDir()
	e.activeFile, _ = r.ActiveVersionFile()
	e.settingsFile, _ = r.SettingsFile()
	e.markerFile, _ = r.StateTransactionFile()
	e.installed, e.scanErr = scanInstalled(deps, e.versions)
	if data, err := deps.FS.ReadFile(e.activeFile); err == nil && version.Validate(string(data)) == nil {
		e.active = string(data)
	}
	e.source = resolveSource(deps, e.settingsFile)
	return e
}

func scanInstalled(deps Deps, versionsDir string) (map[string]string, error) {
	installed := map[string]string{}
	entries, err := deps.FS.ReadDir(versionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return installed, nil
	}
	if err != nil {
		return installed, err
	}
	for _, entry := range entries {
		v := strings.TrimPrefix(entry.Name(), "go")
		if !entry.IsDir() || v == "" || v == entry.Name() {
			continue
		}
		dir := filepath.Join(versionsDir, entry.Name())
		if _, err := deps.FS.Lstat(filepath.Join(dir, "bin", goBinaryName(deps.TargetOS))); err != nil {
			continue
		}
		installed[v] = dir
	}
	return installed, nil
}

func goBinaryName(targetOS string) string {
	if targetOS == "windows" {
		return "go.exe"
	}
	return "go"
}

func goShimName(targetOS string) string {
	if targetOS == "windows" {
		return "go.bat"
	}
	return "go"
}

func (e *env) exists(path string) bool {
	_, err := e.deps.FS.Lstat(path)
	return err == nil
}

func (e *env) isDir(path string) bool {
	info, err := e.deps.FS.Lstat(path)
	return err == nil && info.IsDir()
}

// displayPath shortens a path under the user's home to $HOME/... so a
// hint pasted into a shell profile stays portable.
func (e *env) displayPath(path string) string {
	home, err := e.deps.Resolver.HomeDir()
	if err != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	prefix := "$HOME"
	if e.deps.TargetOS == "windows" {
		prefix = "%USERPROFILE%"
	}
	return prefix + string(filepath.Separator) + rel
}

// shimPathInstructions mirrors utils.GetShimPathInstructions but keys
// on the injected target OS instead of runtime.GOOS.
func shimPathInstructions(targetOS string) string {
	if targetOS == "windows" {
		return `Add to PATH: %USERPROFILE%\.govm\shim`
	}
	return `Add to your shell config: export PATH="$HOME/.govm/shim:$PATH"`
}

func layoutFailure(name string, err error) Check {
	return Check{
		Name:    name,
		Verdict: VerdictFail,
		Detail:  fmt.Sprintf("cannot resolve govm root: %v", err),
		Hint:    "set HOME so govm can locate ~/.govm",
	}
}

func checkShimInPath(_ context.Context, e *env) Check {
	const name = CheckShimInPath
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	createHint := "run `govm` or `govm use <version>` to create it"
	if !e.isDir(e.root) {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("govm root %s does not exist", e.root),
			Hint:    createHint,
		}
	}
	if !e.isDir(e.shim) {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("shim directory %s does not exist", e.shim),
			Hint:    createHint,
		}
	}
	if !utils.ShimInPathList(e.shim, e.deps.Path) {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("shim directory %s is not in PATH", e.shim),
			Hint:    shimPathInstructions(e.deps.TargetOS),
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: "shim directory is in PATH"}
}

func checkGoResolvesToShim(_ context.Context, e *env) Check {
	const name = CheckGoResolvesToShim
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	resolved, err := e.deps.LookPath("go")
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("`go` not found in PATH: %v", err),
			Hint:    shimPathInstructions(e.deps.TargetOS),
		}
	}
	resolved = filepath.Clean(resolved)
	if filepath.Dir(resolved) == filepath.Clean(e.shim) {
		return Check{Name: name, Verdict: VerdictOK, Detail: "`go` resolves to the govm shim"}
	}
	return Check{
		Name:    name,
		Verdict: VerdictFail,
		Detail:  fmt.Sprintf("`go` resolves to %s, not the govm shim", resolved),
		Hint:    fmt.Sprintf("move %q before %s in PATH", e.displayPath(e.shim), filepath.Dir(resolved)),
	}
}

func checkActiveVersion(_ context.Context, e *env) Check {
	const name = CheckActiveVersion
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	if e.scanErr != nil {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("cannot list installed versions: %v", e.scanErr),
			Hint:    fmt.Sprintf("check permissions on %s", e.versions),
		}
	}
	data, err := e.deps.FS.ReadFile(e.activeFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if len(e.installed) == 0 {
			return Check{Name: name, Verdict: VerdictOK, Detail: "no active version (nothing activated)"}
		}
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("no active version, %d installed", len(e.installed)),
			Hint:    "run `govm use <version>` to activate one",
		}
	case err != nil:
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("cannot read active_version: %v", err),
			Hint:    fmt.Sprintf("check permissions on %s", e.activeFile),
		}
	}
	raw := string(data)
	if err := version.Validate(raw); err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("active_version contains invalid content %q", raw),
			Hint:    "run `govm use <version>` to rewrite it",
		}
	}
	if _, ok := e.installed[raw]; !ok {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("active version %s is not installed", raw),
			Hint:    "run `govm use <version>` to activate an installed version",
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: fmt.Sprintf("active version %s is installed", raw)}
}

func skipped(name, reason string) Check {
	return Check{Name: name, Verdict: VerdictOK, Detail: "skipped: " + reason}
}

func checkShimTargets(_ context.Context, e *env) Check {
	const name = CheckShimTargets
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	if e.active == "" {
		return skipped(name, "no valid active version to compare shims against")
	}
	hint := fmt.Sprintf("run `govm use %s` to rewrite all shims", e.active)
	fail := func(detail string) Check {
		return Check{Name: name, Verdict: VerdictFail, Detail: detail, Hint: hint}
	}
	entries, err := e.deps.FS.ReadDir(e.shim)
	if err != nil {
		return fail(fmt.Sprintf("shim directory missing or unreadable: %v", err))
	}
	expectedBin := filepath.Join(e.versions, "go"+e.active, "bin")
	goShim := goShimName(e.deps.TargetOS)
	var goProblem string
	var others []string
	for _, entry := range entries {
		// Mirror lifecycle.shimCandidates: only regular files count, and
		// hidden files (.DS_Store, editor backups) are not shims.
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		problem := e.shimProblem(filepath.Join(e.shim, entry.Name()), expectedBin)
		if problem == "" {
			continue
		}
		if entry.Name() == goShim {
			goProblem = problem
		} else {
			others = append(others, entry.Name())
		}
	}
	sort.Strings(others)
	if goProblem == "" && !e.exists(filepath.Join(e.shim, goShim)) {
		goProblem = fmt.Sprintf("`%s` shim is missing", goShim)
	}
	target := "go" + e.active
	switch {
	case goProblem != "" && len(others) > 0:
		return fail(fmt.Sprintf("%s; also inconsistent: %s", goProblem, strings.Join(others, ", ")))
	case goProblem != "":
		return fail(goProblem)
	case len(others) > 0:
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("`go` shim points to %s, but %s inconsistent", target, describeShims(others)),
			Hint:    hint,
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: "shim scripts point to " + target}
}

func describeShims(names []string) string {
	if len(names) == 1 {
		return names[0] + " is"
	}
	return strings.Join(names, ", ") + " are"
}

// shimProblem returns an empty string when the shim at path targets a
// binary inside expectedBin that exists, otherwise a description.
func (e *env) shimProblem(path, expectedBin string) string {
	label := fmt.Sprintf("`%s` shim", filepath.Base(path))
	data, err := e.deps.FS.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("%s is unreadable: %v", label, err)
	}
	target, ok := parseShimTarget(e.deps.TargetOS, string(data))
	if !ok {
		return label + " is not a govm shim"
	}
	if filepath.Dir(filepath.Clean(target)) != filepath.Clean(expectedBin) {
		return fmt.Sprintf("%s points to %s, not go%s", label, target, e.active)
	}
	if !e.exists(target) {
		return fmt.Sprintf("%s targets missing binary %s", label, target)
	}
	return ""
}

// parseShimTarget extracts the binary path from a shim rendered by
// lifecycle.renderShim. It is the inverse of that renderer.
func parseShimTarget(targetOS, content string) (string, bool) {
	if targetOS == "windows" {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.HasPrefix(line, `@"`) && strings.HasSuffix(line, `" %*`) {
				return strings.ReplaceAll(line[2:len(line)-4], "%%", "%"), true
			}
		}
		return "", false
	}
	const prefix, suffix = "#!/bin/sh\nexec '", "' \"$@\"\n"
	if !strings.HasPrefix(content, prefix) || !strings.HasSuffix(content, suffix) || len(content) < len(prefix)+len(suffix) {
		return "", false
	}
	quoted := content[len(prefix) : len(content)-len(suffix)]
	return strings.ReplaceAll(quoted, `'"'"'`, "'"), true
}

func checkGoVersion(ctx context.Context, e *env) Check {
	const name = CheckGoVersion
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	if e.active == "" {
		return skipped(name, "no valid active version to compare `go version` against")
	}
	hint := fmt.Sprintf("run `govm use %s` to rewrite all shims", e.active)
	ctx, cancel := context.WithTimeout(ctx, goVersionTimeout)
	defer cancel()
	out, err := e.deps.RunVersion(ctx, filepath.Join(e.shim, goShimName(e.deps.TargetOS)))
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("`go version` via shim failed: %v", err),
			Hint:    hint,
		}
	}
	reported, ok := parseGoVersionOutput(out)
	if !ok {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("`go version` via shim returned unexpected output: %q", strings.TrimSpace(out)),
			Hint:    hint,
		}
	}
	if reported != e.active {
		return Check{
			Name:    name,
			Verdict: VerdictFail,
			Detail:  fmt.Sprintf("`go version` via shim reports %s, active is %s", reported, e.active),
			Hint:    hint,
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: "`go version` via shim reports " + reported}
}

// parseGoVersionOutput extracts the bare version from
// "go version go1.27.1 darwin/arm64".
func parseGoVersionOutput(out string) (string, bool) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" {
		return "", false
	}
	return strings.TrimPrefix(fields[2], "go"), true
}

func checkNoInterruptedOperation(_ context.Context, e *env) Check {
	const name = CheckNoInterruptedOperation
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	const hint = "the next govm operation will recover it; run `govm list` to trigger recovery"
	data, err := e.deps.FS.ReadFile(e.markerFile)
	if errors.Is(err, os.ErrNotExist) {
		return Check{Name: name, Verdict: VerdictOK, Detail: "no interrupted operation"}
	}
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("transaction.json is unreadable: %v", err),
			Hint:    hint,
		}
	}
	marker, err := decodeMarker(data)
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("transaction.json is invalid: %v", err),
			Hint:    hint,
		}
	}
	return Check{
		Name:    name,
		Verdict: VerdictWarn,
		Detail:  fmt.Sprintf("interrupted %s %s (phase %s) left transaction.json", marker.Operation, marker.Version, marker.Phase),
		Hint:    hint,
	}
}

// decodeMarker parses and validates a transaction marker the way
// state.MarkerStore.Read does, but over bytes read through the FS seam
// so doctor never touches the disk behind the Checks' back.
func decodeMarker(data []byte) (state.Marker, error) {
	var marker state.Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		return state.Marker{}, err
	}
	if err := marker.Validate(); err != nil {
		return state.Marker{}, err
	}
	return marker, nil
}

func checkSettings(_ context.Context, e *env) Check {
	const name = CheckSettings
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	data, err := e.deps.FS.ReadFile(e.settingsFile)
	if errors.Is(err, os.ErrNotExist) {
		return Check{Name: name, Verdict: VerdictOK, Detail: "settings.json not present, defaults in use"}
	}
	hint := fmt.Sprintf("edit %s or delete it to fall back to defaults", e.settingsFile)
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("settings.json is unreadable: %v", err),
			Hint:    hint,
		}
	}
	var settings config.Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("settings.json is not valid JSON: %v", err),
			Hint:    hint,
		}
	}
	var present map[string]json.RawMessage
	_ = json.Unmarshal(data, &present)
	invalid := invalidSettingsFields(settings, present)
	if len(invalid) > 0 {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("settings.json: invalid %s (defaults applied)", strings.Join(invalid, ", ")),
			Hint:    hint,
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: "settings.json is valid"}
}

// invalidSettingsFields names the fields config.Normalize would reset.
// Only fields present in the file count: a missing key legitimately
// takes its default.
func invalidSettingsFields(settings config.Settings, present map[string]json.RawMessage) []string {
	normalized := config.Normalize(settings)
	var invalid []string
	has := func(key string) bool { _, ok := present[key]; return ok }
	if has("depsDisplay") && normalized.DepsDisplay != settings.DepsDisplay {
		invalid = append(invalid, "depsDisplay")
	}
	if has("theme") && normalized.Theme != settings.Theme {
		invalid = append(invalid, "theme")
	}
	if has("depsBackupLimit") && normalized.DepsBackupLimit != settings.DepsBackupLimit {
		invalid = append(invalid, "depsBackupLimit")
	}
	if has("upgradeNotice") && normalized.UpgradeNotice != settings.UpgradeNotice {
		invalid = append(invalid, "upgradeNotice")
	}
	// Normalize keeps an invalid source as-is rather than resetting it,
	// so validate explicitly.
	if has("distributionSource") {
		if _, err := config.ValidateDistributionSource(settings.DistributionSource); err != nil {
			invalid = append(invalid, "distributionSource")
		}
	}
	return invalid
}
