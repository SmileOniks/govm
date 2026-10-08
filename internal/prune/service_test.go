package prune

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/state"
)

type testFixture struct {
	service   *Service
	root      string
	versions  string
	downloads string
	active    string
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	home := t.TempDir()
	resolver := &paths.Resolver{HomeDir: func() (string, error) { return home, nil }}
	root := filepath.Join(home, ".govm")
	versions := filepath.Join(root, "versions")
	downloads := filepath.Join(root, "downloads")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		t.Fatal(err)
	}
	coordinator := state.NewCoordinator(resolver)
	lifecycleService, err := lifecycle.New(resolver, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(resolver, coordinator, lifecycleService)
	if err != nil {
		t.Fatal(err)
	}
	return &testFixture{
		service: service, root: root, versions: versions,
		downloads: downloads, active: filepath.Join(root, "active_version"),
	}
}

func (f *testFixture) install(t *testing.T, canonical string) string {
	t.Helper()
	bin := filepath.Join(f.versions, "go"+canonical, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("go"), 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(bin)
}

func TestPruneRemovesOnlySafeInactiveCandidates(t *testing.T) {
	f := newTestFixture(t)
	active := f.install(t, "1.26.1")
	inactive := f.install(t, "1.25.0")
	if err := os.WriteFile(f.active, []byte("1.26.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(f.versions, "go-not-a-version")
	if err := os.Mkdir(malformed, 0o700); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(f.versions, ".install-orphan")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	download := filepath.Join(f.downloads, ".govm-install-one.part")
	if err := os.WriteFile(download, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(f.downloads, ".govm-install-linked.part")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Prune(t.Context())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if len(result.Removed) != 2 {
		t.Fatalf("removed = %#v, want inactive version and download", result.Removed)
	}
	for _, path := range []string{active, malformed, staging, linked, outside} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preserved path %q: %v", path, err)
		}
	}
	if _, err := os.Lstat(inactive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive version remains: %v", err)
	}
	if _, err := os.Lstat(download); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary download remains: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("warnings = nil, want unknown-object warnings")
	}
}

func TestPruneFailsClosedForMissingActiveMarkerButCleansDownloads(t *testing.T) {
	f := newTestFixture(t)
	versionDir := f.install(t, "1.25.0")
	download := filepath.Join(f.downloads, ".govm-install-one.part")
	if err := os.WriteFile(download, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Prune(t.Context())
	if err == nil {
		t.Fatal("Prune() error = nil, want missing-marker error")
	}
	if len(result.Removed) != 1 || result.Removed[0].Kind != CandidateDownload {
		t.Fatalf("removed = %#v, want only download", result.Removed)
	}
	if _, err := os.Stat(versionDir); err != nil {
		t.Fatalf("version deleted with missing active marker: %v", err)
	}
	foundActiveWarning := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning.Error(), "active version marker is missing") {
			foundActiveWarning = true
		}
	}
	if !foundActiveWarning {
		t.Fatalf("warnings = %#v, want missing-marker warning", result.Warnings)
	}
}

func TestPruneFailsClosedForUnknownActiveVersion(t *testing.T) {
	f := newTestFixture(t)
	versionDir := f.install(t, "1.25.0")
	if err := os.WriteFile(f.active, []byte("1.99.0"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Prune(t.Context())
	if err == nil {
		t.Fatal("Prune() error = nil, want unknown-active error")
	}
	if _, statErr := os.Stat(versionDir); statErr != nil {
		t.Fatalf("version deleted with unknown active marker: %v", statErr)
	}
}

func TestDiskUsageCountsHardLinksOnceAndIgnoresSymlinks(t *testing.T) {
	f := newTestFixture(t)
	versionDir := f.install(t, "1.25.0")
	first := filepath.Join(versionDir, "bin", "first")
	if err := os.WriteFile(first, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, filepath.Join(versionDir, "bin", "second")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(versionDir, "bin", "linked")); err != nil {
		t.Fatal(err)
	}
	download := filepath.Join(f.downloads, ".govm-install-size.part")
	if err := os.WriteFile(download, []byte("1234567"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := f.service.DiskUsage(t.Context())
	if err != nil {
		t.Fatalf("DiskUsage() error = %v", err)
	}
	if summary.InstalledBytes != 7 {
		t.Fatalf("installed bytes = %d, want 7", summary.InstalledBytes)
	}
	if summary.DownloadBytes != 7 {
		t.Fatalf("download bytes = %d, want 7", summary.DownloadBytes)
	}
}
