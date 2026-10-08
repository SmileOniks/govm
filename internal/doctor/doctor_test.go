package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/paths"
)

// fixture is a temp-root govm layout plus a Deps value pointing at it.
// Every Check reads the real files under root; PATH, lookPath and the
// `go version` runner are stubbed so no test touches the real $HOME,
// the real PATH or a real Go toolchain.
type fixture struct {
	root     string
	shim     string
	versions string
	deps     Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".govm")
	shim := filepath.Join(root, "shim")
	versions := filepath.Join(root, "versions")
	f := &fixture{root: root, shim: shim, versions: versions}
	f.deps = Deps{
		Resolver: &paths.Resolver{
			HomeDir:   func() (string, error) { return home, nil },
			ConfigDir: func() (string, error) { return "", nil },
		},
		Path:     pathList("/usr/bin", shim, "/bin"),
		LookPath: func(string) (string, error) { return filepath.Join(shim, "go"), nil },
		FS:       OSFileSystem(),
		RunVersion: func(ctx context.Context, binary string) (string, error) {
			return "go version go1.27.1 darwin/arm64\n", nil
		},
		TargetOS:    "darwin",
		GovmVersion: "test",
		HTTPClient:  catalogDoer(),
	}
	return f
}

func pathList(entries ...string) string {
	return strings.Join(entries, string(os.PathListSeparator))
}

func (f *fixture) mkdirs(t *testing.T) {
	t.Helper()
	for _, dir := range []string{f.shim, f.versions} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// install creates versions/go<v>/bin/{go,gofmt} and returns the bin dir.
func (f *fixture) install(t *testing.T, v string) string {
	t.Helper()
	bin := filepath.Join(f.versions, "go"+v, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go", "gofmt"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func (f *fixture) activate(t *testing.T, v string) {
	t.Helper()
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "active_version"), []byte(v), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeShim renders a shim exactly like lifecycle does on unix.
func (f *fixture) writeShim(t *testing.T, name, target string) {
	t.Helper()
	if err := os.MkdirAll(f.shim, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "#!/bin/sh\nexec '" + target + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(f.shim, name), []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// healthy builds a fully consistent layout with 1.27.1 active.
func (f *fixture) healthy(t *testing.T) {
	t.Helper()
	f.mkdirs(t)
	bin := f.install(t, "1.27.1")
	f.activate(t, "1.27.1")
	f.writeShim(t, "go", filepath.Join(bin, "go"))
	f.writeShim(t, "gofmt", filepath.Join(bin, "gofmt"))
}

func (f *fixture) writeRoot(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, f *fixture) Report {
	t.Helper()
	return Run(context.Background(), f.deps)
}

func checkByName(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in report; have %v", name, names(r))
	return Check{}
}

func names(r Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, c.Name)
	}
	return out
}

func TestHealthyLayoutIsAllOK(t *testing.T) {
	f := newFixture(t)
	f.healthy(t)
	r := run(t, f)

	want := []string{
		CheckShimInPath, CheckGoResolvesToShim, CheckActiveVersion,
		CheckShimTargets, CheckGoVersion, CheckNoInterruptedOperation,
		CheckSettings, CheckSource, CheckDisk,
	}
	if got := names(r); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("check order = %v, want %v", got, want)
	}
	for _, c := range r.Checks {
		if c.Verdict != VerdictOK {
			t.Errorf("%s: verdict %s (%s), want ok", c.Name, c.Verdict, c.Detail)
		}
		if c.Hint != "" {
			t.Errorf("%s: unexpected hint %q on ok verdict", c.Name, c.Hint)
		}
	}
	if r.Failed() {
		t.Fatal("Failed() = true for healthy layout")
	}
	if fails, warns := r.Counts(); fails != 0 || warns != 0 {
		t.Fatalf("Counts() = %d, %d; want 0, 0", fails, warns)
	}
	if r.Root != f.root || r.GovmVersion != "test" || r.OS != "darwin" || r.Arch == "" {
		t.Fatalf("header = %+v", r)
	}
}

func TestReportCountsAndFailed(t *testing.T) {
	r := Report{Checks: []Check{
		{Verdict: VerdictOK}, {Verdict: VerdictWarn}, {Verdict: VerdictFail}, {Verdict: VerdictWarn},
	}}
	if fails, warns := r.Counts(); fails != 1 || warns != 2 {
		t.Fatalf("Counts() = %d, %d; want 1, 2", fails, warns)
	}
	if !r.Failed() {
		t.Fatal("Failed() = false with a fail verdict")
	}
	if (Report{Checks: []Check{{Verdict: VerdictWarn}}}).Failed() {
		t.Fatal("Failed() = true with only warn")
	}
}

func TestCheckShimInPath(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, f *fixture)
		verdict Verdict
		detail  string
		hint    string
	}{
		{
			name:    "trailing slash entry is ok",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t); f.deps.Path = pathList("/x", f.shim+"/", "/y") },
			verdict: VerdictOK,
		},
		{
			name:    "missing from PATH",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t); f.deps.Path = pathList("/x", "/y") },
			verdict: VerdictFail,
			detail:  "not in PATH",
			hint:    "PATH",
		},
		{
			name: "missing from PATH on windows hints USERPROFILE",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.Path = pathList("/x")
				f.deps.TargetOS = "windows"
			},
			verdict: VerdictFail,
			detail:  "not in PATH",
			hint:    "%USERPROFILE%",
		},
		{
			name:    "missing root",
			setup:   func(t *testing.T, f *fixture) {},
			verdict: VerdictFail,
			detail:  "does not exist",
			hint:    "govm use <version>",
		},
		{
			name: "missing shim dir",
			setup: func(t *testing.T, f *fixture) {
				if err := os.MkdirAll(f.root, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			verdict: VerdictFail,
			detail:  "does not exist",
			hint:    "govm use <version>",
		},
		{
			name: "home unresolvable",
			setup: func(t *testing.T, f *fixture) {
				f.deps.Resolver.HomeDir = func() (string, error) { return "", errors.New("no home") }
			},
			verdict: VerdictFail,
			detail:  "no home",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			c := checkByName(t, run(t, f), CheckShimInPath)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckGoResolvesToShim(t *testing.T) {
	cases := []struct {
		name     string
		lookPath func(f *fixture) func(string) (string, error)
		targetOS string
		verdict  Verdict
		detail   string
		hint     string
	}{
		{
			name: "shim wins",
			lookPath: func(f *fixture) func(string) (string, error) {
				return func(string) (string, error) { return filepath.Join(f.shim, "go"), nil }
			},
			verdict: VerdictOK,
		},
		{
			name: "shim wins with unclean path",
			lookPath: func(f *fixture) func(string) (string, error) {
				return func(string) (string, error) { return filepath.Join(f.shim, ".", "go"), nil }
			},
			verdict: VerdictOK,
		},
		{
			name: "windows shim is go.bat",
			lookPath: func(f *fixture) func(string) (string, error) {
				return func(string) (string, error) { return filepath.Join(f.shim, "go.bat"), nil }
			},
			targetOS: "windows",
			verdict:  VerdictOK,
		},
		{
			name: "system go shadows shim",
			lookPath: func(f *fixture) func(string) (string, error) {
				return func(string) (string, error) { return "/usr/local/go/bin/go", nil }
			},
			verdict: VerdictFail,
			detail:  "/usr/local/go/bin/go",
			hint:    "/usr/local/go/bin",
		},
		{
			name: "go not found",
			lookPath: func(f *fixture) func(string) (string, error) {
				return func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }
			},
			verdict: VerdictFail,
			detail:  "not found",
			hint:    "PATH",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.healthy(t)
			f.deps.LookPath = tc.lookPath(f)
			if tc.targetOS != "" {
				f.deps.TargetOS = tc.targetOS
			}
			c := checkByName(t, run(t, f), CheckGoResolvesToShim)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckActiveVersion(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, f *fixture)
		verdict Verdict
		detail  string
		hint    string
	}{
		{
			name:    "no file and no versions",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t) },
			verdict: VerdictOK,
			detail:  "nothing activated",
		},
		{
			name:    "no file but a version is installed",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t); f.install(t, "1.27.1") },
			verdict: VerdictWarn,
			detail:  "1 installed",
			hint:    "govm use",
		},
		{
			name:    "file names a version that is not installed",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t); f.install(t, "1.27.1"); f.activate(t, "1.99.0") },
			verdict: VerdictFail,
			detail:  "1.99.0",
			hint:    "govm use <version>",
		},
		{
			name:    "file content is not a version",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t); f.activate(t, "latest\n") },
			verdict: VerdictFail,
			detail:  "invalid",
			hint:    "govm use <version>",
		},
		{
			name:    "installed and active",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t) },
			verdict: VerdictOK,
			detail:  "1.27.1",
		},
		{
			name: "unreadable file",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.FS.ReadFile = func(name string) ([]byte, error) {
					if filepath.Base(name) == "active_version" {
						return nil, errors.New("permission denied")
					}
					return os.ReadFile(name)
				}
			},
			verdict: VerdictFail,
			detail:  "permission denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			c := checkByName(t, run(t, f), CheckActiveVersion)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckShimTargets(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, f *fixture)
		verdict Verdict
		detail  string
		hint    string
	}{
		{
			name:    "all shims consistent",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t) },
			verdict: VerdictOK,
			detail:  "go1.27.1",
		},
		{
			name: "go shim points at another version",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				old := f.install(t, "1.26.0")
				f.writeShim(t, "go", filepath.Join(old, "go"))
			},
			verdict: VerdictFail,
			detail:  "go1.26.0",
			hint:    "govm use 1.27.1",
		},
		{
			name: "go shim points at a missing binary",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.writeShim(t, "go", filepath.Join(f.versions, "go1.27.1", "bin", "gone"))
			},
			verdict: VerdictFail,
			detail:  "gone",
			hint:    "govm use 1.27.1",
		},
		{
			name: "go shim missing",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				if err := os.Remove(filepath.Join(f.shim, "go")); err != nil {
					t.Fatal(err)
				}
			},
			verdict: VerdictFail,
			detail:  "missing",
			hint:    "govm use 1.27.1",
		},
		{
			name: "go shim unparseable",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				if err := os.WriteFile(filepath.Join(f.shim, "go"), []byte("garbage"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			verdict: VerdictFail,
			detail:  "not a govm shim",
			hint:    "govm use 1.27.1",
		},
		{
			name: "only gofmt wrong",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				old := f.install(t, "1.26.0")
				f.writeShim(t, "gofmt", filepath.Join(old, "gofmt"))
			},
			verdict: VerdictWarn,
			detail:  "gofmt",
			hint:    "govm use 1.27.1",
		},
		{
			name: "hidden files in shim dir are ignored",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				if err := os.WriteFile(filepath.Join(f.shim, ".DS_Store"), []byte("junk"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			verdict: VerdictOK,
			detail:  "go1.27.1",
		},
		{
			name: "shim dir missing",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				if err := os.RemoveAll(f.shim); err != nil {
					t.Fatal(err)
				}
			},
			verdict: VerdictFail,
			detail:  "missing",
			hint:    "govm use 1.27.1",
		},
		{
			name:    "skipped without active version",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t) },
			verdict: VerdictOK,
			detail:  "skipped",
		},
		{
			name: "skipped when active version is invalid",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.activate(t, "nope")
			},
			verdict: VerdictOK,
			detail:  "skipped",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			c := checkByName(t, run(t, f), CheckShimTargets)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckShimTargetsWindows(t *testing.T) {
	f := newFixture(t)
	f.deps.TargetOS = "windows"
	f.mkdirs(t)
	bin := filepath.Join(f.versions, "go1.27.1", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.exe", "gofmt.exe"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.activate(t, "1.27.1")
	writeBat := func(name, target string) {
		content := "@echo off\r\nsetlocal DisableDelayedExpansion\r\n@\"" + strings.ReplaceAll(target, "%", "%%") + "\" %*\r\n"
		if err := os.WriteFile(filepath.Join(f.shim, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBat("go.bat", filepath.Join(bin, "go.exe"))
	writeBat("gofmt.bat", filepath.Join(bin, "gofmt.exe"))
	f.deps.LookPath = func(string) (string, error) { return filepath.Join(f.shim, "go.bat"), nil }

	r := run(t, f)
	assertCheck(t, checkByName(t, r, CheckActiveVersion), VerdictOK, "1.27.1", "")
	assertCheck(t, checkByName(t, r, CheckShimTargets), VerdictOK, "go1.27.1", "")
}

func TestCheckGoVersion(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, f *fixture)
		verdict Verdict
		detail  string
		hint    string
	}{
		{
			name:    "matches active",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t) },
			verdict: VerdictOK,
			detail:  "1.27.1",
		},
		{
			name: "reports another version",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.RunVersion = func(context.Context, string) (string, error) {
					return "go version go1.27.0 darwin/arm64\n", nil
				}
			},
			verdict: VerdictFail,
			detail:  "1.27.0",
			hint:    "govm use 1.27.1",
		},
		{
			name: "execution error",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.RunVersion = func(context.Context, string) (string, error) {
					return "", errors.New("exit status 127")
				}
			},
			verdict: VerdictFail,
			detail:  "exit status 127",
		},
		{
			name: "unparseable output",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.RunVersion = func(context.Context, string) (string, error) { return "hello\n", nil }
			},
			verdict: VerdictFail,
			detail:  "hello",
		},
		{
			name: "runner receives the shim path and a deadline",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				want := filepath.Join(f.shim, "go")
				f.deps.RunVersion = func(ctx context.Context, binary string) (string, error) {
					if binary != want {
						return "", errors.New("wrong binary " + binary)
					}
					if _, ok := ctx.Deadline(); !ok {
						return "", errors.New("no deadline")
					}
					return "go version go1.27.1 linux/amd64", nil
				}
			},
			verdict: VerdictOK,
			detail:  "1.27.1",
		},
		{
			name:    "skipped without active version",
			setup:   func(t *testing.T, f *fixture) { f.mkdirs(t) },
			verdict: VerdictOK,
			detail:  "skipped",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			c := checkByName(t, run(t, f), CheckGoVersion)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckNoInterruptedOperation(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, f *fixture)
		verdict Verdict
		detail  string
		hint    string
	}{
		{
			name:    "no marker",
			setup:   func(t *testing.T, f *fixture) { f.healthy(t) },
			verdict: VerdictOK,
		},
		{
			name: "valid marker present",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.writeRoot(t, "transaction.json", `{"schema_version":1,"operation":"activate","phase":"commit","version":"1.27.1"}`)
			},
			verdict: VerdictWarn,
			detail:  "activate 1.27.1",
			hint:    "recover",
		},
		{
			name: "marker read through the FS seam",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.deps.FS.ReadFile = func(name string) ([]byte, error) {
					if filepath.Base(name) == "transaction.json" {
						return []byte(`{"schema_version":1,"operation":"delete","phase":"commit","version":"1.26.0"}`), nil
					}
					return os.ReadFile(name)
				}
			},
			verdict: VerdictWarn,
			detail:  "delete 1.26.0",
			hint:    "recover",
		},
		{
			name: "malformed marker present",
			setup: func(t *testing.T, f *fixture) {
				f.healthy(t)
				f.writeRoot(t, "transaction.json", `{nope`)
			},
			verdict: VerdictWarn,
			detail:  "invalid",
			hint:    "recover",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			c := checkByName(t, run(t, f), CheckNoInterruptedOperation)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestCheckSettings(t *testing.T) {
	cases := []struct {
		name    string
		content string // empty = no file
		verdict Verdict
		detail  string
		hint    string
	}{
		{name: "no file", verdict: VerdictOK, detail: "defaults"},
		{name: "valid", content: `{"depsDisplay":"all","theme":"light","depsBackupLimit":5,"distributionSource":"https://go.dev/dl/","upgradeNotice":"off"}`, verdict: VerdictOK},
		{name: "partial file uses defaults for missing fields", content: `{"theme":"light"}`, verdict: VerdictOK},
		{name: "invalid JSON", content: `{"theme":`, verdict: VerdictWarn, detail: "not valid JSON", hint: "settings.json"},
		{name: "invalid theme", content: `{"theme":"neon"}`, verdict: VerdictWarn, detail: "theme", hint: "settings.json"},
		{name: "invalid limit", content: `{"depsBackupLimit":0}`, verdict: VerdictWarn, detail: "depsBackupLimit", hint: "settings.json"},
		{name: "invalid source", content: `{"distributionSource":"ftp://x"}`, verdict: VerdictWarn, detail: "distributionSource", hint: "settings.json"},
		{name: "invalid upgrade notice", content: `{"upgradeNotice":"maybe"}`, verdict: VerdictWarn, detail: "upgradeNotice", hint: "settings.json"},
		{name: "two invalid fields", content: `{"depsDisplay":"some","theme":"neon"}`, verdict: VerdictWarn, detail: "depsDisplay, theme", hint: "settings.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.healthy(t)
			if tc.content != "" {
				f.writeRoot(t, "settings.json", tc.content)
			}
			c := checkByName(t, run(t, f), CheckSettings)
			assertCheck(t, c, tc.verdict, tc.detail, tc.hint)
		})
	}
}

func TestChecksRunIndependently(t *testing.T) {
	f := newFixture(t)
	// Nothing exists at all: every Check must still report.
	r := run(t, f)
	if len(r.Checks) != 9 {
		t.Fatalf("got %d checks, want 9: %v", len(r.Checks), names(r))
	}
	if !r.Failed() {
		t.Fatal("Failed() = false with missing root")
	}
	for _, c := range r.Checks {
		if c.Detail == "" {
			t.Errorf("%s: empty detail", c.Name)
		}
	}
}

func assertCheck(t *testing.T, c Check, verdict Verdict, detail, hint string) {
	t.Helper()
	if c.Verdict != verdict {
		t.Fatalf("%s: verdict = %s, want %s (detail %q, hint %q)", c.Name, c.Verdict, verdict, c.Detail, c.Hint)
	}
	if detail != "" && !strings.Contains(c.Detail, detail) {
		t.Fatalf("%s: detail %q does not contain %q", c.Name, c.Detail, detail)
	}
	if hint != "" && !strings.Contains(c.Hint, hint) {
		t.Fatalf("%s: hint %q does not contain %q", c.Name, c.Hint, hint)
	}
	if verdict == VerdictOK && c.Hint != "" {
		t.Fatalf("%s: ok verdict carries hint %q", c.Name, c.Hint)
	}
	if verdict != VerdictOK && c.Hint == "" {
		t.Fatalf("%s: %s verdict without hint (detail %q)", c.Name, verdict, c.Detail)
	}
}
