package deps

import (
	"errors"
	"reflect"
	"testing"
)

func TestTargetVersion(t *testing.T) {
	versions := []string{"v1.0.0", "v1.0.1", "v1.0.2", "v1.1.0", "v1.2.0", "v1.3.0-rc.1", "v2.0.0"}
	tests := []struct {
		name  string
		dep   ModuleDependency
		level UpdateLevel
		want  string
	}{
		{"latest uses go list Latest", ModuleDependency{Version: "v1.0.1", Latest: "v1.2.0", Versions: versions}, LevelLatest, "v1.2.0"},
		{"latest current", ModuleDependency{Version: "v1.2.0", Latest: "v1.2.0", Versions: versions}, LevelLatest, ""},
		{"patch picks highest same minor", ModuleDependency{Version: "v1.0.0", Latest: "v1.2.0", Versions: versions}, LevelPatch, "v1.0.2"},
		{"patch none available", ModuleDependency{Version: "v1.0.2", Latest: "v1.2.0", Versions: versions}, LevelPatch, ""},
		{"minor picks highest same major excluding prerelease", ModuleDependency{Version: "v1.0.0", Latest: "v2.0.0", Versions: versions}, LevelMinor, "v1.2.0"},
		{"minor allows prerelease when current is prerelease", ModuleDependency{Version: "v1.2.1-beta.1", Latest: "v2.0.0", Versions: versions}, LevelMinor, "v1.3.0-rc.1"},
		{"patch falls back to Latest when versions missing and inside level", ModuleDependency{Version: "v1.0.0", Latest: "v1.0.5"}, LevelPatch, "v1.0.5"},
		{"patch falls back to nothing when Latest crosses minor", ModuleDependency{Version: "v1.0.0", Latest: "v1.1.0"}, LevelPatch, ""},
		{"minor falls back to Latest when versions missing", ModuleDependency{Version: "v1.0.0", Latest: "v1.9.0"}, LevelMinor, "v1.9.0"},
		{"minor ignores major bump in Latest fallback", ModuleDependency{Version: "v1.0.0", Latest: "v2.0.0"}, LevelMinor, ""},
		{"error dependency never updates", ModuleDependency{Version: "v1.0.0", Latest: "v1.0.1", Versions: versions, Error: "boom"}, LevelPatch, ""},
		{"invalid current version", ModuleDependency{Version: "abc", Latest: "v1.0.1", Versions: versions}, LevelPatch, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TargetVersion(tt.dep, tt.level); got != tt.want {
				t.Fatalf("TargetVersion(%s) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestParseUpdateLevel(t *testing.T) {
	for name, want := range map[string]UpdateLevel{"": LevelLatest, "latest": LevelLatest, "Minor": LevelMinor, "patch": LevelPatch} {
		got, err := ParseUpdateLevel(name)
		if err != nil || got != want {
			t.Fatalf("ParseUpdateLevel(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
	if _, err := ParseUpdateLevel("major"); err == nil {
		t.Fatal("expected error for unknown level")
	}
	for _, l := range Levels {
		if l.Label() == "" || l.String() == "" {
			t.Fatalf("level %d has empty labels", l)
		}
	}
}

func planDeps() []ModuleDependency {
	return []ModuleDependency{
		{Path: "github.com/spf13/cobra", Version: "v1.0.0", Latest: "v1.2.0", Versions: []string{"v1.0.0", "v1.0.1", "v1.2.0"}},
		{Path: "github.com/other/cobra", Version: "v2.0.0", Latest: "v2.0.0"},
		{Path: "golang.org/x/text", Version: "v0.1.0", Latest: "v0.3.0", Indirect: true, Versions: []string{"v0.1.0", "v0.1.1", "v0.3.0"}},
		{Path: "example.com/broken", Version: "v1.0.0", Latest: "v1.1.0", Error: "unavailable"},
	}
}

func TestResolveModulePath(t *testing.T) {
	deps := planDeps()
	tests := []struct {
		query   string
		want    string
		wantErr any
	}{
		{"github.com/spf13/cobra", "github.com/spf13/cobra", nil},
		{"spf13/cobra", "github.com/spf13/cobra", nil},
		{"text", "golang.org/x/text", nil},
		{"cobra", "", AmbiguousModuleError{}},
		{"nope", "", UnknownModuleError{}},
		{"", "", UnknownModuleError{}},
		{"cob", "", UnknownModuleError{}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, err := ResolveModulePath(deps, tt.query)
			switch want := tt.wantErr.(type) {
			case nil:
				if err != nil || got != tt.want {
					t.Fatalf("ResolveModulePath = %q, %v; want %q", got, err, tt.want)
				}
			case AmbiguousModuleError:
				var ambiguous AmbiguousModuleError
				if !errors.As(err, &ambiguous) {
					t.Fatalf("err = %v, want AmbiguousModuleError", err)
				}
				if len(ambiguous.Candidates) != 2 {
					t.Fatalf("candidates = %v, want two", ambiguous.Candidates)
				}
			case UnknownModuleError:
				var unknown UnknownModuleError
				if !errors.As(err, &unknown) {
					t.Fatalf("err = %v, want UnknownModuleError", err)
				}
			default:
				t.Fatalf("unexpected want %T", want)
			}
		})
	}
}

func TestBuildUpdatePlan(t *testing.T) {
	deps := planDeps()
	entry := func(path, old, next string) DependencyUpdateEntry {
		return DependencyUpdateEntry{Path: path, OldVersion: old, NewVersion: next}
	}
	tests := []struct {
		name string
		sel  UpdateSelection
		want []DependencyUpdateEntry
		err  bool
	}{
		{"bulk latest covers direct only", UpdateSelection{}, []DependencyUpdateEntry{entry("github.com/spf13/cobra", "v1.0.0", "v1.2.0")}, false},
		{"bulk patch", UpdateSelection{Level: LevelPatch}, []DependencyUpdateEntry{entry("github.com/spf13/cobra", "v1.0.0", "v1.0.1")}, false},
		{"explicit indirect allowed", UpdateSelection{Modules: []string{"text"}}, []DependencyUpdateEntry{entry("golang.org/x/text", "v0.1.0", "v0.3.0")}, false},
		{"explicit current omitted", UpdateSelection{Modules: []string{"other/cobra"}}, nil, false},
		{"explicit dedupes and keeps order", UpdateSelection{Modules: []string{"text", "spf13/cobra", "golang.org/x/text"}}, []DependencyUpdateEntry{
			entry("golang.org/x/text", "v0.1.0", "v0.3.0"),
			entry("github.com/spf13/cobra", "v1.0.0", "v1.2.0"),
		}, false},
		{"explicit unknown fails", UpdateSelection{Modules: []string{"missing"}}, nil, true},
		{"explicit ambiguous fails", UpdateSelection{Modules: []string{"cobra"}}, nil, true},
		{"explicit error dependency omitted", UpdateSelection{Modules: []string{"broken"}}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildUpdatePlan(deps, tt.sel)
			if tt.err {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("plan = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCyclePlanIsolatedFromVersionsMutation(t *testing.T) {
	for _, name := range []string{"event-input", "dependencies-output"} {
		t.Run(name, func(t *testing.T) {
			catalog := planDeps()
			c, intent, err := NewUpdateCycle().Handle(StartEvent{})
			assertNoErr(t, err)
			assertIntent[IntentCheckUpdates](t, intent)

			assertPlan := func(intent Intent, level UpdateLevel, want []DependencyUpdateEntry) {
				t.Helper()
				confirm := assertIntent[IntentConfirmApply](t, intent)
				if confirm.Level != level {
					t.Fatalf("confirmation level = %s, want %s", confirm.Level, level)
				}
				if !reflect.DeepEqual(confirm.Entries, want) {
					t.Fatalf("confirmation entries = %#v, want %#v", confirm.Entries, want)
				}
				if got := c.Entries(); !reflect.DeepEqual(got, want) {
					t.Fatalf("cycle entries = %#v, want %#v", got, want)
				}
			}

			c, intent, err = c.Handle(CheckUpdatesDoneEvent{Dependencies: catalog})
			assertNoErr(t, err)
			wantLatest := []DependencyUpdateEntry{
				{Path: "github.com/spf13/cobra", OldVersion: "v1.0.0", NewVersion: "v1.2.0"},
			}
			assertPlan(intent, LevelLatest, wantLatest)

			mutable := catalog
			if name == "dependencies-output" {
				mutable = c.Dependencies()
			}
			mutable[0].Versions[1] = "v1.0.9"
			mutable[0].Versions[2] = "v1.9.0"
			mutable[2].Versions[2] = "v0.9.0"

			c, intent, err = c.Handle(ChangeLevelEvent{Level: LevelPatch})
			assertNoErr(t, err)
			assertPlan(intent, LevelPatch, []DependencyUpdateEntry{
				{Path: "github.com/spf13/cobra", OldVersion: "v1.0.0", NewVersion: "v1.0.1"},
			})

			c, intent, err = c.Handle(ChangeLevelEvent{Level: LevelMinor})
			assertNoErr(t, err)
			assertPlan(intent, LevelMinor, wantLatest)

			c, intent, err = c.Handle(ChangeScopeEvent{Modules: []string{"text"}})
			assertNoErr(t, err)
			wantText := []DependencyUpdateEntry{
				{Path: "golang.org/x/text", OldVersion: "v0.1.0", NewVersion: "v0.3.0"},
			}
			assertPlan(intent, LevelMinor, wantText)
			if !assertIntent[IntentConfirmApply](t, intent).Explicit {
				t.Fatal("confirmation should have explicit scope")
			}

			c, intent, err = c.Handle(ConfirmApplyEvent{Yes: true})
			assertNoErr(t, err)
			apply := assertIntent[IntentApplyUpdates](t, intent)
			if !reflect.DeepEqual(apply.Entries, wantText) {
				t.Fatalf("apply entries = %#v, want %#v", apply.Entries, wantText)
			}
		})
	}
}

func TestCycleChangeLevelRebuildsPlan(t *testing.T) {
	c, _, _ := NewUpdateCycle().Handle(StartEvent{ModuleDir: "/mod"})
	c, intent, err := c.Handle(CheckUpdatesDoneEvent{Dependencies: planDeps()})
	if err != nil {
		t.Fatal(err)
	}
	confirm, ok := intent.(IntentConfirmApply)
	if !ok || confirm.Level != LevelLatest || len(confirm.Entries) != 1 || confirm.Entries[0].NewVersion != "v1.2.0" {
		t.Fatalf("intent = %#v, want latest plan with one entry", intent)
	}

	c, intent, err = c.Handle(ChangeLevelEvent{Level: LevelPatch})
	if err != nil {
		t.Fatal(err)
	}
	confirm, ok = intent.(IntentConfirmApply)
	if !ok || confirm.Level != LevelPatch || len(confirm.Entries) != 1 || confirm.Entries[0].NewVersion != "v1.0.1" {
		t.Fatalf("intent after patch = %#v", intent)
	}
	if c.Phase() != PhaseConfirmApply || c.Selection().Level != LevelPatch {
		t.Fatalf("phase/level = %s/%s", c.Phase(), c.Selection().Level)
	}

	// A level with no candidates yields an empty plan; confirming ends
	// the cycle as no-updates instead of applying nothing.
	deps := []ModuleDependency{{Path: "a", Version: "v1.0.0", Latest: "v2.0.0", Versions: []string{"v1.0.0", "v2.0.0"}}}
	c2, _, _ := NewUpdateCycle().Handle(StartEvent{})
	c2, _, _ = c2.Handle(CheckUpdatesDoneEvent{Dependencies: deps})
	c2, intent, _ = c2.Handle(ChangeLevelEvent{Level: LevelMinor})
	if confirm := intent.(IntentConfirmApply); len(confirm.Entries) != 0 {
		t.Fatalf("expected empty plan at minor, got %v", confirm.Entries)
	}
	c2, intent, _ = c2.Handle(ConfirmApplyEvent{Yes: true})
	if !c2.IsTerminal() || c2.Outcome() != OutcomeNoUpdates {
		t.Fatalf("outcome = %s, want no-updates", c2.Outcome())
	}
	if _, ok := intent.(NoIntent); !ok {
		t.Fatalf("intent = %T, want NoIntent", intent)
	}
}

func TestCycleChangeScopeRebuildsPlanAndKeepsLevel(t *testing.T) {
	c, _, _ := NewUpdateCycle().Handle(StartEvent{Selection: UpdateSelection{Modules: []string{"spf13/cobra"}, Level: LevelPatch}})
	c, intent, err := c.Handle(CheckUpdatesDoneEvent{Dependencies: planDeps()})
	if err != nil {
		t.Fatal(err)
	}
	confirm, ok := intent.(IntentConfirmApply)
	if !ok || !confirm.Explicit || len(confirm.Entries) != 1 || confirm.Entries[0].NewVersion != "v1.0.1" {
		t.Fatalf("intent = %#v, want explicit patch plan", intent)
	}

	c, intent, err = c.Handle(ChangeScopeEvent{})
	if err != nil {
		t.Fatal(err)
	}
	confirm, ok = intent.(IntentConfirmApply)
	if !ok || confirm.Explicit || confirm.Level != LevelPatch {
		t.Fatalf("intent after widening = %#v, want all-direct patch plan", intent)
	}
	if len(confirm.Entries) != 1 || confirm.Entries[0].Path != "github.com/spf13/cobra" || confirm.Entries[0].NewVersion != "v1.0.1" {
		t.Fatalf("entries = %#v", confirm.Entries)
	}
	if sel := c.Selection(); sel.Explicit() || sel.Level != LevelPatch {
		t.Fatalf("selection = %#v", sel)
	}

	c, intent, _ = c.Handle(ChangeScopeEvent{Modules: []string{"text"}})
	confirm = intent.(IntentConfirmApply)
	if !confirm.Explicit || len(confirm.Entries) != 1 || confirm.Entries[0].Path != "golang.org/x/text" || confirm.Entries[0].NewVersion != "v0.1.1" {
		t.Fatalf("intent after narrowing = %#v", intent)
	}
	if sel := c.Selection(); len(sel.Modules) != 1 || sel.Modules[0] != "text" || sel.Level != LevelPatch {
		t.Fatalf("selection = %#v", sel)
	}

	// Narrowing to a module that is already current leaves the
	// confirmation pending with an empty plan so the scope can be
	// widened again; confirming it ends as no-updates.
	c, intent, _ = c.Handle(ChangeScopeEvent{Modules: []string{"other/cobra"}})
	if confirm = intent.(IntentConfirmApply); len(confirm.Entries) != 0 || c.Phase() != PhaseConfirmApply {
		t.Fatalf("intent = %#v phase = %s, want empty pending plan", intent, c.Phase())
	}

	if _, _, err := NewUpdateCycle().Handle(ChangeScopeEvent{}); err == nil {
		t.Fatal("expected invalid transition outside confirm-apply")
	}
}

func TestCycleChangeLevelInvalidOutsideConfirmApply(t *testing.T) {
	c := NewUpdateCycle()
	_, _, err := c.Handle(ChangeLevelEvent{Level: LevelPatch})
	var invalid InvalidTransitionError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want InvalidTransitionError", err)
	}
}

func TestCycleExplicitSelectionErrorsTerminateAsFailed(t *testing.T) {
	c, _, _ := NewUpdateCycle().Handle(StartEvent{Selection: UpdateSelection{Modules: []string{"cobra"}}})
	c, intent, err := c.Handle(CheckUpdatesDoneEvent{Dependencies: planDeps()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := intent.(NoIntent); !ok || c.Outcome() != OutcomeFailed {
		t.Fatalf("outcome = %s intent = %T, want failed/NoIntent", c.Outcome(), intent)
	}
	var ambiguous AmbiguousModuleError
	if !errors.As(c.Failure(), &ambiguous) {
		t.Fatalf("failure = %v, want AmbiguousModuleError", c.Failure())
	}
}

func TestCycleSelectionIsDefensivelyCopied(t *testing.T) {
	modules := []string{"a"}
	c, _, _ := NewUpdateCycle().Handle(StartEvent{Selection: UpdateSelection{Modules: modules}})
	modules[0] = "mutated"
	if got := c.Selection().Modules[0]; got != "a" {
		t.Fatalf("selection leaked caller mutation: %q", got)
	}
	got := c.Selection()
	got.Modules[0] = "again"
	if c.Selection().Modules[0] != "a" {
		t.Fatal("Selection() returned internal slice")
	}
}

func TestListDependencyArgsAddsVersionsOnlyOnline(t *testing.T) {
	offline := listDependencyArgs(false)
	online := listDependencyArgs(true)
	if !reflect.DeepEqual(offline, []string{"list", "-mod=readonly", "-m", "-json", "all"}) {
		t.Fatalf("offline args = %v", offline)
	}
	if !reflect.DeepEqual(online, []string{"list", "-mod=readonly", "-m", "-json", "-e", "-u", "-versions", "all"}) {
		t.Fatalf("online args = %v", online)
	}
}
