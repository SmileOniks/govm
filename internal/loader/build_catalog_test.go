package loader

import (
	"testing"

	"github.com/SmileOniks/govm/internal/config"
)

// These cases came from internal/utils, which held a second copy of the
// catalog merge that hardcoded the official distribution source. They
// pin the merge itself: platform selection, installed/active flags,
// integrity metadata and archive URL construction.

func releasesForCatalog() []Release {
	return []Release{
		{
			Version: "1.22.0",
			Stable:  true,
			Files: []FileEntry{
				{Filename: "go1.22.0.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64"},
				{Filename: "go1.22.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"},
			},
		},
		{
			Version: "1.21.5",
			Stable:  true,
			Files: []FileEntry{
				{Filename: "go1.21.5.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64"},
			},
		},
		{
			Version: "1.21.0",
			Stable:  false,
			Files: []FileEntry{
				// darwin/arm64 build exists for an unstable release so we
				// can assert the Stable flag is propagated end-to-end.
				{Filename: "go1.21.0.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64"},
			},
		},
	}
}

func TestBuildVersionCatalog_SortsHighestVersionFirst(t *testing.T) {
	releases := []Release{
		{Version: "1.20.0", Stable: true, Files: []FileEntry{{Filename: "go1.20.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"}}},
		{Version: "1.22.1", Stable: true, Files: []FileEntry{{Filename: "go1.22.1.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"}}},
		{Version: "1.21.5", Stable: true, Files: []FileEntry{{Filename: "go1.21.5.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"}}},
	}

	versions := buildVersionCatalogWithSource(releases, "linux", "amd64", nil, "", "")

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	for i, want := range []string{"1.22.1", "1.21.5", "1.20.0"} {
		if versions[i].Version != want {
			t.Errorf("versions[%d] = %s, want %s", i, versions[i].Version, want)
		}
	}
}

// TestBuildVersionCatalog_SortIsStableForEquivalentVersions pins that
// releases the comparator treats as equal keep their go.dev order, so
// the Available list does not reshuffle between refreshes.
func TestBuildVersionCatalog_SortIsStableForEquivalentVersions(t *testing.T) {
	releases := []Release{
		{Version: "1.23.0", Files: []FileEntry{{Filename: "first-1.23.0", OS: "linux", Arch: "amd64"}}},
		{Version: "1.22", Files: []FileEntry{{Filename: "first-1.22", OS: "linux", Arch: "amd64"}}},
		{Version: "1.22.0.0", Files: []FileEntry{{Filename: "equivalent-1.22", OS: "linux", Arch: "amd64"}}},
		{Version: "1.22.0", Files: []FileEntry{{Filename: "second-1.22", OS: "linux", Arch: "amd64"}}},
		{Version: "1.21.9", Files: []FileEntry{{Filename: "1.21.9", OS: "linux", Arch: "amd64"}}},
	}

	got := buildVersionCatalogWithSource(releases, "linux", "amd64", nil, "", "")

	want := []string{"first-1.23.0", "first-1.22", "equivalent-1.22", "second-1.22", "1.21.9"}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d", len(want), len(got))
	}
	for i, filename := range want {
		if got[i].Filename != filename {
			t.Fatalf("entry[%d] = %q, want %q (comparator-equal order must survive)", i, got[i].Filename, filename)
		}
	}
}

func TestBuildVersionCatalog_UsesMirrorArchiveURL(t *testing.T) {
	releases := []Release{{
		Version: "1.22.1",
		Files: []FileEntry{{
			Filename: "go1.22.1.linux-amd64.tar.gz",
			OS:       "linux",
			Arch:     "amd64",
		}},
	}}

	versions := buildVersionCatalogWithSource(
		releases,
		"linux",
		"amd64",
		nil,
		"",
		"https://mirror.example/go/",
	)
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if got := versions[0].URL; got != "https://mirror.example/go/go1.22.1.linux-amd64.tar.gz" {
		t.Fatalf("archive URL = %q, want mirror URL", got)
	}
}

func TestBuildVersionCatalog_SelectsMatchingOSArch(t *testing.T) {
	got := buildVersionCatalogWithSource(releasesForCatalog(), "darwin", "arm64", nil, "", "")

	if len(got) != 3 {
		t.Fatalf("expected 3 entries (darwin/arm64 only), got %d: %+v", len(got), got)
	}
	wantVersions := []string{"1.22.0", "1.21.5", "1.21.0"}
	wantStable := []bool{true, true, false}
	for i, w := range wantVersions {
		if got[i].Version != w {
			t.Errorf("got[%d].Version = %q, want %q", i, got[i].Version, w)
		}
		if got[i].Stable != wantStable[i] {
			t.Errorf("got[%d].Stable = %v, want %v", i, got[i].Stable, wantStable[i])
		}
	}
	if got[0].Filename != "go1.22.0.darwin-arm64.tar.gz" {
		t.Errorf("got[0].Filename = %q, want darwin/arm64 file", got[0].Filename)
	}
}

// TestBuildVersionCatalog_EmptySourceUsesOfficialArchiveURL pins the
// default half of the distribution source rule: an unset source resolves
// to the official one rather than producing a relative URL.
func TestBuildVersionCatalog_EmptySourceUsesOfficialArchiveURL(t *testing.T) {
	got := buildVersionCatalogWithSource(releasesForCatalog(), "darwin", "arm64", nil, "", "")
	if len(got) == 0 {
		t.Fatal("expected at least one entry")
	}
	want := config.DefaultDistributionSource + "go1.22.0.darwin-arm64.tar.gz"
	if got[0].URL != want {
		t.Errorf("got[0].URL = %q, want %q", got[0].URL, want)
	}
}

func TestBuildVersionCatalog_SkipsReleasesWithoutMatchingOSArch(t *testing.T) {
	releases := []Release{
		{
			Version: "1.22.0",
			Stable:  true,
			Files: []FileEntry{
				{Filename: "go1.22.0.linux-amd64.tar.gz", OS: "linux", Arch: "amd64"},
			},
		},
	}
	got := buildVersionCatalogWithSource(releases, "darwin", "arm64", nil, "", "")
	if len(got) != 0 {
		t.Errorf("linux-only release should be skipped on darwin, got %v", got)
	}
}

func TestBuildVersionCatalog_MarksInstalledAndActive(t *testing.T) {
	installed := map[string]string{
		"1.22.0": "/home/u/.govm/versions/go1.22.0",
	}
	got := buildVersionCatalogWithSource(releasesForCatalog(), "darwin", "arm64", installed, "1.21.5", "")

	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if !got[0].Installed {
		t.Errorf("1.22.0 should be Installed")
	}
	if got[0].Path != "/home/u/.govm/versions/go1.22.0" {
		t.Errorf("1.22.0 Path = %q", got[0].Path)
	}
	if got[0].Active {
		t.Errorf("1.22.0 should not be Active (active=1.21.5)")
	}
	if got[1].Installed {
		t.Errorf("1.21.5 should not be Installed")
	}
	if got[1].Path != "" {
		t.Errorf("1.21.5 Path should be empty, got %q", got[1].Path)
	}
	if !got[1].Active {
		t.Errorf("1.21.5 should be Active")
	}
	if got[2].Installed || got[2].Active {
		t.Errorf("1.21.0 should be neither installed nor active: %+v", got[2])
	}
}

func TestBuildVersionCatalog_EmptyInputsYieldEmptyResult(t *testing.T) {
	got := buildVersionCatalogWithSource(nil, "darwin", "arm64", nil, "", "")
	if got != nil {
		t.Errorf("nil releases should yield nil, got %v", got)
	}

	got = buildVersionCatalogWithSource([]Release{}, "darwin", "arm64", nil, "", "")
	if len(got) != 0 {
		t.Errorf("empty releases should yield empty slice, got %v", got)
	}
}

func TestBuildVersionCatalog_FirstMatchingFileWins(t *testing.T) {
	releases := []Release{
		{
			Version: "1.22.0",
			Stable:  true,
			Files: []FileEntry{
				{Filename: "go1.22.0.darwin-arm64.first.tar.gz", OS: "darwin", Arch: "arm64"},
				{Filename: "go1.22.0.darwin-arm64.second.tar.gz", OS: "darwin", Arch: "arm64"},
			},
		},
	}
	got := buildVersionCatalogWithSource(releases, "darwin", "arm64", nil, "", "")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Filename != "go1.22.0.darwin-arm64.first.tar.gz" {
		t.Errorf("first matching file should win, got %q", got[0].Filename)
	}
}

func TestBuildVersionCatalog_NoOSArchMatchYieldsNothing(t *testing.T) {
	got := buildVersionCatalogWithSource(releasesForCatalog(), "windows", "arm64", nil, "", "")
	if len(got) != 0 {
		t.Errorf("windows/arm64 has no matching files, expected empty, got %v", got)
	}
}

// TestBuildVersionCatalog_PropagatesMetadata verifies that the go.dev
// archive checksum (sha256) and size are propagated end-to-end into the
// GoVersion handed to the install core, and that a non-archive file
// never wins the platform match.
func TestBuildVersionCatalog_PropagatesMetadata(t *testing.T) {
	releases := []Release{
		{
			Version: "1.22.0",
			Stable:  true,
			Files: []FileEntry{
				{
					Filename: "go1.22.0.darwin-arm64.pkg",
					OS:       "darwin",
					Arch:     "arm64",
					Kind:     "installer",
				},
				{
					Filename: "go1.22.0.darwin-arm64.tar.gz",
					OS:       "darwin",
					Arch:     "arm64",
					Kind:     "archive",
					SHA256:   "deadbeefcafebabe",
					Size:     123456,
				},
			},
		},
	}
	got := buildVersionCatalogWithSource(releases, "darwin", "arm64", nil, "", "")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(got), got)
	}
	if got[0].Filename != "go1.22.0.darwin-arm64.tar.gz" {
		t.Errorf("Filename = %q, want the archive file", got[0].Filename)
	}
	if got[0].SHA256 != "deadbeefcafebabe" {
		t.Errorf("SHA256 = %q, want deadbeefcafebabe", got[0].SHA256)
	}
	if got[0].Size != 123456 {
		t.Errorf("Size = %d, want 123456", got[0].Size)
	}
}
