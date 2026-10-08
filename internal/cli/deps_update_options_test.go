package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/deps"
)

func selectiveDeps() []deps.ModuleDependency {
	return []deps.ModuleDependency{
		{Path: "github.com/d/x", Version: "v1.0.0", Latest: "v1.2.0", Versions: []string{"v1.0.0", "v1.0.3", "v1.2.0"}},
		{Path: "github.com/d/y", Version: "v2.0.0", Latest: "v2.0.0"},
		{Path: "golang.org/x/z", Version: "v0.1.0", Latest: "v0.2.0", Indirect: true},
	}
}

func newSelectiveService(t *testing.T, confirm func(string, bool) (bool, error)) (*DepsService, *fakeExecutor, *bytes.Buffer) {
	t.Helper()
	svc, fx, stdout := newUpdateService(confirm)
	fx.checkUpdates = func(deps.IntentCheckUpdates) deps.Event {
		return deps.CheckUpdatesDoneEvent{Dependencies: selectiveDeps()}
	}
	return svc, fx, stdout
}

func TestParseDepsUpdateArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    UpdateOptions
		wantErr string
	}{
		{"empty", nil, UpdateOptions{}, ""},
		{"modules and flags anywhere", []string{"a", "--patch", "b", "-y", "--dry-run"},
			UpdateOptions{Modules: []string{"a", "b"}, Level: deps.LevelPatch, DryRun: true, Yes: true}, ""},
		{"minor", []string{"--minor"}, UpdateOptions{Level: deps.LevelMinor}, ""},
		{"repeat same level ok", []string{"--patch", "--patch"}, UpdateOptions{Level: deps.LevelPatch}, ""},
		{"patch and minor conflict", []string{"--patch", "--minor"}, UpdateOptions{}, "mutually exclusive"},
		{"unknown flag", []string{"--force"}, UpdateOptions{}, `unknown deps option "--force"`},
		{"help", []string{"-h"}, UpdateOptions{}, "usage: govm deps update"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDepsUpdateArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("opts = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseDepsCheckArgs(t *testing.T) {
	if level, err := parseDepsCheckArgs([]string{"--patch"}); err != nil || level != deps.LevelPatch {
		t.Fatalf("got %v, %v", level, err)
	}
	if _, err := parseDepsCheckArgs([]string{"cobra"}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected positional rejection, got %v", err)
	}
	if _, err := parseDepsCheckArgs([]string{"--dry-run"}); err == nil {
		t.Fatal("expected --dry-run to be rejected on check")
	}
}

func TestRunUpdateNamedModuleNarrowsPlan(t *testing.T) {
	confirmCalls := 0
	svc, fx, stdout := newSelectiveService(t, func(string, bool) (bool, error) {
		confirmCalls++
		return confirmCalls == 1, nil
	})

	if err := svc.RunUpdate(UpdateOptions{Modules: []string{"d/x"}}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	want := []deps.DependencyUpdateEntry{{Path: "github.com/d/x", OldVersion: "v1.0.0", NewVersion: "v1.2.0"}}
	if !reflect.DeepEqual(fx.applyEntries, want) {
		t.Fatalf("apply entries = %#v, want %#v", fx.applyEntries, want)
	}
	if !strings.Contains(stdout.String(), "⚠️  1 dependency will be updated:") {
		t.Fatalf("expected explicit plan header, got:\n%s", stdout.String())
	}
}

func TestRunUpdateNamedIndirectModuleIsAllowed(t *testing.T) {
	svc, fx, _ := newSelectiveService(t, func(string, bool) (bool, error) { return false, nil })
	_ = svc.RunUpdate(UpdateOptions{Modules: []string{"x/z"}, DryRun: true})
	if fx.applyCalls != 0 {
		t.Fatal("dry run must not apply")
	}
	confirmCalls := 0
	svc, fx, _ = newSelectiveService(t, func(string, bool) (bool, error) {
		confirmCalls++
		return confirmCalls == 1, nil
	})
	if err := svc.RunUpdate(UpdateOptions{Modules: []string{"x/z"}}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if len(fx.applyEntries) != 1 || fx.applyEntries[0].Path != "golang.org/x/z" {
		t.Fatalf("apply entries = %#v, want the indirect module", fx.applyEntries)
	}
}

func TestRunUpdateUnknownModuleFails(t *testing.T) {
	svc, fx, _ := newSelectiveService(t, func(string, bool) (bool, error) {
		t.Fatal("Confirm must not be called")
		return false, nil
	})
	err := svc.RunUpdate(UpdateOptions{Modules: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), `unknown module "nope"`) {
		t.Fatalf("err = %v, want unknown module", err)
	}
	if fx.applyCalls != 0 {
		t.Fatal("apply must not run")
	}
}

func TestRunUpdateAmbiguousModuleListsCandidates(t *testing.T) {
	svc, _, _ := newSelectiveService(t, func(string, bool) (bool, error) { return false, nil })
	err := svc.RunUpdate(UpdateOptions{Modules: []string{"z"}})
	if err != nil {
		t.Fatalf("unique suffix z should resolve, got %v", err)
	}
	deps2 := append(selectiveDeps(), deps.ModuleDependency{Path: "other.org/x", Version: "v1.0.0"})
	svc, fx, _ := newUpdateService(func(string, bool) (bool, error) { return false, nil })
	fx.checkUpdates = func(deps.IntentCheckUpdates) deps.Event {
		return deps.CheckUpdatesDoneEvent{Dependencies: deps2}
	}
	err = svc.RunUpdate(UpdateOptions{Modules: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous module \"x\" matches: github.com/d/x, other.org/x") {
		t.Fatalf("err = %v, want ambiguity listing", err)
	}
}

func TestRunUpdateNamedModuleAlreadyUpToDateSucceeds(t *testing.T) {
	svc, fx, stdout := newSelectiveService(t, func(string, bool) (bool, error) {
		t.Fatal("Confirm must not be called")
		return false, nil
	})
	if err := svc.RunUpdate(UpdateOptions{Modules: []string{"d/y"}}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if !strings.Contains(stdout.String(), "Already up to date: d/y") {
		t.Fatalf("expected already up to date message, got:\n%s", stdout.String())
	}
	if fx.applyCalls != 0 {
		t.Fatal("apply must not run")
	}
}

func TestRunUpdateDryRunPrintsPlanWithoutPrompting(t *testing.T) {
	svc, fx, stdout := newSelectiveService(t, func(string, bool) (bool, error) {
		t.Fatal("Confirm must not be called during dry run")
		return false, nil
	})
	if err := svc.RunUpdate(UpdateOptions{DryRun: true, Yes: true, Level: deps.LevelPatch}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	want := "🔍 Checking available updates in /tmp/m...\n\n" +
		"  github.com/d/x\tv1.0.0 → v1.0.3\tupdate available\n" +
		"\n📦 1 dependency would be updated (patch) (dry run).\n"
	if got := stdout.String(); got != want {
		t.Fatalf("dry-run output mismatch\nwant:\n%q\ngot:\n%q", want, got)
	}
	if fx.applyCalls != 0 || fx.checksCalls != 0 {
		t.Fatalf("dry run ran operations: apply=%d checks=%d", fx.applyCalls, fx.checksCalls)
	}
}

func TestRunUpdateYesAnswersEveryPrompt(t *testing.T) {
	svc, fx, stdout := newSelectiveService(t, func(string, bool) (bool, error) {
		t.Fatal("Confirm must not be called with --yes")
		return false, nil
	})
	fx.runChecks = func(deps.IntentRunChecks) deps.Event {
		return deps.ChecksDoneEvent{Result: deps.DependencyCheckResult{OK: false, Command: "go test ./..."}}
	}
	err := svc.RunUpdate(UpdateOptions{Yes: true})
	if err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if fx.applyCalls != 1 || fx.checksCalls != 1 || fx.rollbackCalls != 1 {
		t.Fatalf("expected apply, checks and rollback, got apply=%d checks=%d rollback=%d",
			fx.applyCalls, fx.checksCalls, fx.rollbackCalls)
	}
	if !strings.Contains(stdout.String(), "Rolled back to pre-update state.") {
		t.Fatalf("expected rollback message, got:\n%s", stdout.String())
	}
}

func TestRunUpdatePatchLevelPicksSameMinorTarget(t *testing.T) {
	confirmCalls := 0
	svc, fx, stdout := newSelectiveService(t, func(string, bool) (bool, error) {
		confirmCalls++
		return confirmCalls == 1, nil
	})
	if err := svc.RunUpdate(UpdateOptions{Level: deps.LevelPatch}); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	if len(fx.applyEntries) != 1 || fx.applyEntries[0].NewVersion != "v1.0.3" {
		t.Fatalf("apply entries = %#v, want patch target v1.0.3", fx.applyEntries)
	}
	if !strings.Contains(stdout.String(), "will be updated (patch):") {
		t.Fatalf("expected level in plan header, got:\n%s", stdout.String())
	}
}

func TestRunCheckHonoursLevel(t *testing.T) {
	fx := &fakeExecutor{
		checkUpdates: func(deps.IntentCheckUpdates) deps.Event {
			return deps.CheckUpdatesDoneEvent{Dependencies: selectiveDeps()}
		},
	}
	stdout := &bytes.Buffer{}
	svc := &DepsService{ModuleDir: "/tmp/m", Stdout: stdout, Deps: fx}
	if err := svc.RunCheck(deps.LevelPatch); err != nil {
		t.Fatalf("RunCheck: %v", err)
	}
	want := "🔍 Checking available updates in /tmp/m...\n\n" +
		"  github.com/d/x\tv1.0.0 → v1.0.3\tupdate available\n" +
		"  github.com/d/y\tv2.0.0\tcurrent\n" +
		"  golang.org/x/z\tv0.1.0\tcurrent\n" +
		"\n📦 1 direct update(s) available (patch).\n"
	if got := stdout.String(); got != want {
		t.Fatalf("RunCheck output mismatch\nwant:\n%q\ngot:\n%q", want, got)
	}
}

func TestDepsCommandReportsFailure(t *testing.T) {
	out := &bytes.Buffer{}
	app := &App{out: out, in: strings.NewReader("")}
	if app.DepsCommand("bogus") {
		t.Fatal("unknown subcommand must report failure")
	}
	if !strings.Contains(out.String(), "Unknown deps subcommand: bogus") {
		t.Fatalf("unexpected output: %s", out.String())
	}
	out.Reset()
	if !app.DepsCommand("help") {
		t.Fatal("help must succeed")
	}
	for _, want := range []string{"govm deps update [options] [module...]", "--dry-run", "--patch", "-y, --yes"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if app.DepsCommand("update", "--patch", "--minor") {
		t.Fatal("conflicting flags must fail")
	}
	if !strings.Contains(out.String(), "mutually exclusive") {
		t.Fatalf("unexpected output: %s", out.String())
	}
	out.Reset()
	if app.DepsCommand("list") {
		t.Fatal("deps must report failure when no executor is configured")
	}
	if !strings.Contains(out.String(), "not configured") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

// DepsCommand runs end-to-end through a fake executor: the App maps
// the subcommand onto the bound deps.API without touching the
// filesystem or the go toolchain.
func TestDepsCommandRunsListThroughFakeExecutor(t *testing.T) {
	out := &bytes.Buffer{}
	fx := &fakeExecutor{list: func() ([]deps.ModuleDependency, error) {
		return []deps.ModuleDependency{{Path: "github.com/d/x", Version: "v1.0.0"}}, nil
	}}
	app := NewApp(Operations{Deps: fx, ModuleDir: "/tmp/m"}, strings.NewReader(""), out, out)
	if !app.DepsCommand("list") {
		t.Fatal("list must succeed")
	}
	for _, want := range []string{
		"Reading module dependencies in /tmp/m",
		"github.com/d/x\tv1.0.0\tdirect",
		"1 direct, 0 indirect",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}
