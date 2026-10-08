package loader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/paths"
)

type fakeReleaseSource struct {
	releases []Release
	err      error
}

func (f *fakeReleaseSource) FetchReleases(ctx context.Context) ([]Release, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.releases, nil
}

// fakeRegistry stands in for the local toolchain registry. The registry
// owns the effective-active fallback, so the loader only sees whatever
// version it reports.
type fakeRegistry struct {
	toolchains []local.Toolchain
	listErr    error
	active     string
	activeErr  error
}

func (f *fakeRegistry) List(context.Context) ([]local.Toolchain, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.toolchains, nil
}

func (f *fakeRegistry) Find(context.Context, string) (local.Toolchain, error) {
	return local.Toolchain{}, local.ErrNotFound
}

func (f *fakeRegistry) Active(context.Context) (string, error) {
	if f.activeErr != nil {
		return "", f.activeErr
	}
	return f.active, nil
}

func TestLoadVersionCatalog_Success(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			releases: []Release{
				{
					Version: "1.22.0",
					Stable:  true,
					Files: []FileEntry{
						{Filename: "go1.22.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "abc123", Size: 1024},
					},
				},
				{
					Version: "1.21.5",
					Stable:  true,
					Files: []FileEntry{
						{Filename: "go1.21.5.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "def456", Size: 2048},
					},
				},
			},
		},
		LocalRegistry: &fakeRegistry{
			toolchains: []local.Toolchain{
				{Version: "1.21.5", Path: "/home/user/.govm/versions/go1.21.5"},
			},
			active: "1.21.5",
		},
		Platform: Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	catalog, err := LoadVersionCatalog(ctx, deps)
	if err != nil {
		t.Fatalf("LoadVersionCatalog failed: %v", err)
	}

	if len(catalog.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(catalog.Versions))
	}

	if catalog.Versions[0].Version != "1.22.0" {
		t.Errorf("expected first version 1.22.0, got %s", catalog.Versions[0].Version)
	}
	if catalog.Versions[0].Installed {
		t.Errorf("expected 1.22.0 not installed")
	}

	if catalog.Versions[1].Version != "1.21.5" {
		t.Errorf("expected second version 1.21.5, got %s", catalog.Versions[1].Version)
	}
	if !catalog.Versions[1].Installed {
		t.Errorf("expected 1.21.5 installed")
	}
	if !catalog.Versions[1].Active {
		t.Errorf("expected 1.21.5 active")
	}

	if catalog.ActiveVersion != "1.21.5" {
		t.Errorf("expected active version 1.21.5, got %s", catalog.ActiveVersion)
	}
}

// TestLoadVersionCatalog_AdoptsEffectiveActiveVersion pins that the
// loader reports whatever the registry considers active, including a
// version that is not installed under govm. The marker-or-PATH rule
// itself belongs to the registry.
func TestLoadVersionCatalog_AdoptsEffectiveActiveVersion(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			releases: []Release{
				{Version: "1.22.0", Stable: true, Files: []FileEntry{{Filename: "go1.22.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"}}},
			},
		},
		LocalRegistry: &fakeRegistry{active: "1.20.3"},
		Platform:      Platform{OS: "linux", Arch: "amd64"},
	}

	catalog, err := LoadVersionCatalog(context.Background(), deps)
	if err != nil {
		t.Fatalf("LoadVersionCatalog failed: %v", err)
	}

	if catalog.ActiveVersion != "1.20.3" {
		t.Errorf("expected active version 1.20.3, got %s", catalog.ActiveVersion)
	}
}

func TestLoadVersionCatalog_RequiresRegistry(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{},
		Platform:      Platform{OS: "linux", Arch: "amd64"},
	}

	if _, err := LoadVersionCatalog(context.Background(), deps); err == nil {
		t.Fatal("expected an error when no local registry is wired")
	}
}

func TestLoadVersionCatalog_ReleaseSourceError(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			err: errors.New("network timeout"),
		},
		LocalRegistry: &fakeRegistry{active: "1.21.0"},
		Platform:      Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	_, err := LoadVersionCatalog(ctx, deps)
	if err == nil {
		t.Fatal("expected error from release source, got nil")
	}
	if !errors.Is(err, errors.New("network timeout")) && err.Error() != "fetch releases: network timeout" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadVersionCatalog_RegistryListError(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			releases: []Release{},
		},
		LocalRegistry: &fakeRegistry{
			listErr: errors.New("permission denied"),
			active:  "1.21.0",
		},
		Platform: Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	_, err := LoadVersionCatalog(ctx, deps)
	if err == nil {
		t.Fatal("expected error from local versions, got nil")
	}
	if err.Error() != "scan installed: permission denied" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadVersionCatalog_ActiveVersionError(t *testing.T) {
	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			releases: []Release{},
		},
		LocalRegistry: &fakeRegistry{
			activeErr: errors.New("corrupted file"),
		},
		Platform: Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	_, err := LoadVersionCatalog(ctx, deps)
	if err == nil {
		t.Fatal("expected error from active version, got nil")
	}
	if err.Error() != "read active version: corrupted file" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadVersionCatalog_WithRealLocalAdapters(t *testing.T) {
	tmpDir := t.TempDir()
	versionsDir := filepath.Join(tmpDir, ".govm", "versions")
	activeFile := filepath.Join(tmpDir, ".govm", "active_version")

	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatalf("failed to create versions dir: %v", err)
	}

	version1Dir := filepath.Join(versionsDir, "go1.21.5")
	binDir := filepath.Join(version1Dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}
	goBin := filepath.Join(binDir, "go")
	if err := os.WriteFile(goBin, []byte("fake go binary"), 0755); err != nil {
		t.Fatalf("failed to create go binary: %v", err)
	}

	if err := os.WriteFile(activeFile, []byte("1.21.5"), 0644); err != nil {
		t.Fatalf("failed to write active version: %v", err)
	}

	resolver := &paths.Resolver{
		HomeDir: func() (string, error) {
			return tmpDir, nil
		},
	}

	deps := Dependencies{
		ReleaseSource: &fakeReleaseSource{
			releases: []Release{
				{
					Version: "1.22.0",
					Stable:  true,
					Files: []FileEntry{
						{Filename: "go1.22.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "abc123", Size: 1024},
					},
				},
				{
					Version: "1.21.5",
					Stable:  true,
					Files: []FileEntry{
						{Filename: "go1.21.5.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", SHA256: "def456", Size: 2048},
					},
				},
			},
		},
		LocalRegistry: local.NewRegistry(resolver),
		Platform:      Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	catalog, err := LoadVersionCatalog(ctx, deps)
	if err != nil {
		t.Fatalf("LoadVersionCatalog with real adapters failed: %v", err)
	}

	if len(catalog.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(catalog.Versions))
	}

	var installed, active int
	for _, v := range catalog.Versions {
		if v.Installed {
			installed++
		}
		if v.Active {
			active++
		}
	}

	if installed != 1 {
		t.Errorf("expected 1 installed version, got %d", installed)
	}

	if active != 1 {
		t.Errorf("expected 1 active version, got %d", active)
	}

	if catalog.ActiveVersion != "1.21.5" {
		t.Errorf("expected active version 1.21.5, got %s", catalog.ActiveVersion)
	}

	for _, v := range catalog.Versions {
		if v.Version == "1.21.5" {
			if !v.Installed {
				t.Errorf("expected 1.21.5 to be installed")
			}
			if !v.Active {
				t.Errorf("expected 1.21.5 to be active")
			}
			if v.Path != version1Dir {
				t.Errorf("expected path %s, got %s", version1Dir, v.Path)
			}
		}
	}
}
