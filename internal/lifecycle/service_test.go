package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/state"
)

type fixture struct {
	service  *Service
	resolver *paths.Resolver
	root     string
	versions string
	shims    string
	active   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	resolver := &paths.Resolver{HomeDir: func() (string, error) { return home, nil }}
	root := filepath.Join(home, ".govm")
	versions := filepath.Join(root, "versions")
	shims := filepath.Join(root, "shim")
	active := filepath.Join(root, "active_version")
	if err := os.MkdirAll(versions, privateDirMode); err != nil {
		t.Fatal(err)
	}
	coordinator := state.NewCoordinator(resolver)
	service, err := New(resolver, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		service: service, resolver: resolver, root: root,
		versions: versions, shims: shims, active: active,
	}
}

func (f *fixture) install(t *testing.T, version string, binaries ...string) string {
	t.Helper()
	dir := filepath.Join(f.versions, "go"+version)
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, privateDirMode); err != nil {
		t.Fatal(err)
	}
	goName := "go"
	if f.service.fs.targetOS == "windows" {
		goName = "go.exe"
	}
	names := append([]string{goName}, binaries...)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestServiceActivateFirstAndRepeatRepairsCompleteShimSet(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.26.1", "gofmt")

	result, err := f.service.Activate(t.Context(), "1.26.1")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if result.Version != "1.26.1" || result.ShimDir != f.shims {
		t.Fatalf("Activate() result = %#v", result)
	}
	wantShims := []string{"go", "gofmt"}
	if runtime.GOOS == "windows" {
		wantShims = []string{"go.bat", "gofmt.bat"}
	}
	if !reflect.DeepEqual(result.Shims, wantShims) {
		t.Fatalf("Activate() shims = %#v, want %#v", result.Shims, wantShims)
	}
	if got := mustRead(t, f.active); got != "1.26.1" {
		t.Fatalf("active version = %q", got)
	}

	if err := os.WriteFile(filepath.Join(f.shims, "stale"), []byte("stale"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.shims, wantShims[0]), []byte("broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	repaired, err := f.service.Activate(t.Context(), "1.26.1")
	if err != nil {
		t.Fatalf("repeat Activate() error = %v", err)
	}
	if len(repaired.Warnings) != 0 {
		t.Fatalf("repeat warnings = %#v", repaired.Warnings)
	}
	if _, err := os.Lstat(filepath.Join(f.shims, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale shim remains: %v", err)
	}
	if got := mustRead(t, filepath.Join(f.shims, wantShims[0])); got == "broken" {
		t.Fatal("repeat activation did not repair shim")
	}
	if marker, present, err := state.NewMarkerStore(f.root).Read(); err != nil || present {
		t.Fatalf("marker after activation = %#v, present %t, error %v", marker, present, err)
	}
}

func TestServiceActivateRollbackWhenRecordReplaceFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.25.0")
	f.install(t, "1.26.1")
	if _, err := f.service.Activate(t.Context(), "1.25.0"); err != nil {
		t.Fatal(err)
	}
	oldShim := mustRead(t, filepath.Join(f.shims, hostShimName("go")))

	replaceErr := errors.New("replace failed")
	realReplace := f.service.fs.replace
	f.service.fs.replace = func(source, target string) error {
		if strings.HasPrefix(filepath.Base(source), activeTargetPrefix) {
			return replaceErr
		}
		return realReplace(source, target)
	}
	_, err := f.service.Activate(t.Context(), "1.26.1")
	if !errors.Is(err, replaceErr) {
		t.Fatalf("Activate() error = %v, want replace failure", err)
	}
	var phaseErr *Error
	if !errors.As(err, &phaseErr) || phaseErr.Phase != PhaseActiveRecordCommitting {
		t.Fatalf("Activate() error = %#v, want active record phase", err)
	}
	if got := mustRead(t, f.active); got != "1.25.0" {
		t.Fatalf("active version after rollback = %q", got)
	}
	if got := mustRead(t, filepath.Join(f.shims, hostShimName("go"))); got != oldShim {
		t.Fatal("old shim set was not restored")
	}
	if _, present, readErr := state.NewMarkerStore(f.root).Read(); readErr != nil || present {
		t.Fatalf("marker after rollback present = %t, error %v", present, readErr)
	}
}

func TestServiceActivateRejectsUnsafeInstalledLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, *fixture)
	}{
		{
			name: "version symlink",
			setup: func(t *testing.T, f *fixture) {
				outside := filepath.Join(t.TempDir(), "go1.26.1")
				if err := os.MkdirAll(filepath.Join(outside, "bin"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(f.versions, "go1.26.1")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bin symlink",
			setup: func(t *testing.T, f *fixture) {
				versionDir := filepath.Join(f.versions, "go1.26.1")
				if err := os.Mkdir(versionDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(versionDir, "bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "go binary symlink",
			setup: func(t *testing.T, f *fixture) {
				dir := filepath.Join(f.versions, "go1.26.1", "bin")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "go")
				if err := os.WriteFile(target, []byte("go"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, "go")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "versions symlink",
			setup: func(t *testing.T, f *fixture) {
				if err := os.Remove(f.versions); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), f.versions); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			tt.setup(t, f)
			if _, err := f.service.Activate(t.Context(), "1.26.1"); err == nil {
				t.Fatal("Activate() error = nil, want unsafe layout rejection")
			}
		})
	}
}

func TestServiceActivateSkipsNonRegularShimCandidates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	versionDir := f.install(t, "1.26.1", "gofmt")
	bin := filepath.Join(versionDir, "bin")
	if err := os.Mkdir(filepath.Join(bin, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(bin, "gofmt"), filepath.Join(bin, "linked")); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Activate(t.Context(), "1.26.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range result.Shims {
		if strings.Contains(name, "directory") || strings.Contains(name, "linked") {
			t.Fatalf("unsafe shim candidate included: %q", name)
		}
	}
}

func TestServiceActivateReturnsNotInstalledError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.Activate(t.Context(), "1.26.1")
	var notInstalled *NotInstalledError
	if !errors.As(err, &notInstalled) || notInstalled.Version != "1.26.1" {
		t.Fatalf("Activate() error = %v, want NotInstalledError", err)
	}
}

func TestServiceDeleteSuccessAndActiveProtection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.25.0")
	f.install(t, "1.26.1")
	if _, err := f.service.Activate(t.Context(), "1.26.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Delete(t.Context(), "1.26.1"); err == nil {
		t.Fatal("Delete(active) error = nil")
	} else {
		var activeErr *ActiveVersionError
		if !errors.As(err, &activeErr) {
			t.Fatalf("Delete(active) error = %v, want ActiveVersionError", err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.versions, "go1.26.1")); err != nil {
		t.Fatalf("active version was changed: %v", err)
	}

	result, err := f.service.Delete(t.Context(), "1.25.0")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if result.Version != "1.25.0" || len(result.Warnings) != 0 {
		t.Fatalf("Delete() result = %#v", result)
	}
	if _, err := os.Lstat(filepath.Join(f.versions, "go1.25.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted version remains: %v", err)
	}
}

func TestServiceDeleteFailsClosedForActiveRecordErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, *fixture)
	}{
		{
			name: "malformed",
			setup: func(t *testing.T, f *fixture) {
				if err := os.WriteFile(f.active, []byte("go1.25.0\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, f *fixture) {
				target := filepath.Join(t.TempDir(), "active")
				if err := os.WriteFile(target, []byte("1.25.0"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, f.active); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "read failure",
			setup: func(t *testing.T, f *fixture) {
				if err := os.WriteFile(f.active, []byte("1.25.0"), 0o600); err != nil {
					t.Fatal(err)
				}
				realRead := f.service.fs.readFile
				f.service.fs.readFile = func(path string) ([]byte, error) {
					if path == f.active {
						return nil, errors.New("read failed")
					}
					return realRead(path)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.install(t, "1.25.0")
			tt.setup(t, f)
			if _, err := f.service.Delete(t.Context(), "1.25.0"); err == nil {
				t.Fatal("Delete() error = nil")
			}
			if _, err := os.Stat(filepath.Join(f.versions, "go1.25.0")); err != nil {
				t.Fatalf("version changed despite active read failure: %v", err)
			}
		})
	}
}

func TestServiceDeleteCleanupFailureReturnsSuccessWarningAndPreservesMarker(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.install(t, "1.25.0")
	cleanupErr := errors.New("cleanup failed")
	realRemoveAll := f.service.fs.removeAll
	f.service.fs.removeAll = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), deletionPrefix) {
			return cleanupErr
		}
		return realRemoveAll(path)
	}

	result, err := f.service.Delete(t.Context(), "1.25.0")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(result.Warnings) != 1 || !errors.Is(result.Warnings[0], cleanupErr) {
		t.Fatalf("Delete() warnings = %#v", result.Warnings)
	}
	if _, err := os.Lstat(filepath.Join(f.versions, "go1.25.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still visible after committed delete: %v", err)
	}
	marker, present, err := state.NewMarkerStore(f.root).Read()
	if err != nil || !present || marker.Operation != state.OperationDelete {
		t.Fatalf("preserved marker = %#v, present %t, error %v", marker, present, err)
	}
}

func TestServiceRejectsNonCanonicalVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, input := range []string{"go1.26.1", "../1.26.1", "1.26.1\n"} {
		if _, err := f.service.Activate(t.Context(), input); err == nil {
			t.Fatalf("Activate(%q) error = nil", input)
		}
		if _, err := f.service.Delete(t.Context(), input); err == nil {
			t.Fatalf("Delete(%q) error = nil", input)
		}
	}
}

func TestRenderShimQuotesPathsAndWindowsNames(t *testing.T) {
	t.Parallel()
	name, content := renderShim("linux", "go", "/tmp/a'b/go")
	if name != "go" || string(content) != "#!/bin/sh\nexec '/tmp/a'\"'\"'b/go' \"$@\"\n" {
		t.Fatalf("Unix shim = %q, %q", name, content)
	}
	name, content = renderShim("windows", "foo.exe", `C:\Users\100% Done\foo.exe`)
	if name != "foo.bat" {
		t.Fatalf("Windows shim name = %q", name)
	}
	if got := string(content); !strings.Contains(got, `"C:\Users\100%% Done\foo.exe" %*`) ||
		!strings.Contains(got, "DisableDelayedExpansion") {
		t.Fatalf("Windows shim content = %q", got)
	}
}

func TestWindowsShimCandidatesRejectCaseInsensitiveCollisions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.service.fs.targetOS = "windows"
	dir := f.install(t, "1.26.1", "Foo.exe", "foo")
	_, err := f.service.shimCandidates(filepath.Join(dir, "bin"))
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("shimCandidates() error = %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hostShimName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.TrimSuffix(name, ".exe") + ".bat"
	}
	return name
}
