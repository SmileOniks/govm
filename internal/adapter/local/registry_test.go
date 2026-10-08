package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SmileOniks/govm/internal/paths"
)

func TestFilesystemRegistryListSortsToolchains(t *testing.T) {
	home := t.TempDir()
	versionsDir := filepath.Join(home, ".govm", "versions")
	for _, version := range []string{"1.21.5", "1.22.0", "1.20.9"} {
		goPath := filepath.Join(versionsDir, "go"+version, "bin", "go")
		if err := os.MkdirAll(filepath.Dir(goPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goPath, []byte("go"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	registry := NewRegistry(&paths.Resolver{HomeDir: func() (string, error) {
		return home, nil
	}})
	toolchains, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	got := []string{toolchains[0].Version, toolchains[1].Version, toolchains[2].Version}
	want := []string{"1.22.0", "1.21.5", "1.20.9"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("versions = %v, want %v", got, want)
		}
	}
}

// TestFilesystemRegistryActivePrefersMarker pins the first half of the
// effective-active rule: a recorded marker wins and the go executable on
// PATH is never consulted.
func TestFilesystemRegistryActivePrefersMarker(t *testing.T) {
	home := t.TempDir()
	activeFile := filepath.Join(home, ".govm", "active_version")
	if err := os.MkdirAll(filepath.Dir(activeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeFile, []byte("1.22.0"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(&paths.Resolver{HomeDir: func() (string, error) {
		return home, nil
	}})
	registry.currentGoVersion = func() string {
		t.Fatal("PATH lookup consulted despite a recorded marker")
		return ""
	}

	active, err := registry.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active != "1.22.0" {
		t.Fatalf("active = %q, want 1.22.0", active)
	}
}

// TestFilesystemRegistryActiveFallsBackToPath pins the second half: with
// no marker recorded, the version reported by the go executable on PATH
// becomes the effective active version.
func TestFilesystemRegistryActiveFallsBackToPath(t *testing.T) {
	home := t.TempDir()
	registry := NewRegistry(&paths.Resolver{HomeDir: func() (string, error) {
		return home, nil
	}})
	registry.currentGoVersion = func() string { return "1.20.3" }

	active, err := registry.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active != "1.20.3" {
		t.Fatalf("active = %q, want the PATH fallback 1.20.3", active)
	}
}

func TestFilesystemRegistryFindNormalizesAndMatchesPrefix(t *testing.T) {
	home := t.TempDir()
	goPath := filepath.Join(home, ".govm", "versions", "go1.22.4", "bin", "go")
	if err := os.MkdirAll(filepath.Dir(goPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, []byte("go"), 0o755); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(&paths.Resolver{HomeDir: func() (string, error) {
		return home, nil
	}})
	toolchain, err := registry.Find(context.Background(), "v1.22")
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Version != "1.22.4" {
		t.Fatalf("version = %q, want 1.22.4", toolchain.Version)
	}
}
