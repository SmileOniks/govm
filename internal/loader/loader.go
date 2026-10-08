package loader

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/utils"
)

// LoadVersionCatalog orchestrates the complete version-loading flow:
// fetch go.dev releases, scan installed versions, determine active
// version (with exec fallback), and merge everything into a single
// domain result. All I/O dependencies are passed explicitly so the
// orchestration logic is independently testable.
//
// The function fails fast on any critical dependency error. Callers
// control timeout and cancellation through ctx.
func LoadVersionCatalog(ctx context.Context, deps Dependencies) (VersionCatalog, error) {
	releases, err := deps.ReleaseSource.FetchReleases(ctx)
	if err != nil {
		return VersionCatalog{}, fmt.Errorf("fetch releases: %w", err)
	}

	if deps.LocalRegistry == nil {
		return VersionCatalog{}, errors.New("local toolchain registry is nil")
	}

	toolchains, err := deps.LocalRegistry.List(ctx)
	if err != nil {
		return VersionCatalog{}, fmt.Errorf("scan installed: %w", err)
	}
	installed := make(map[string]string, len(toolchains))
	for _, toolchain := range toolchains {
		installed[toolchain.Version] = toolchain.Path
	}

	activeVersion, err := deps.LocalRegistry.Active(ctx)
	if err != nil {
		return VersionCatalog{}, fmt.Errorf("read active version: %w", err)
	}

	versions := buildVersionCatalogWithSource(
		releases,
		deps.Platform.OS,
		deps.Platform.Arch,
		installed,
		activeVersion,
		deps.DistributionSource,
	)

	return VersionCatalog{
		Versions:      versions,
		ActiveVersion: activeVersion,
		Platform:      deps.Platform,
	}, nil
}

// buildVersionCatalogWithSource merges go.dev releases with the local
// view of installed and active versions. For each release, the first
// archive matching the target OS/Arch wins, and its URL is resolved
// against source. The returned slice is sorted highest-version-first.
//
// An empty source resolves to the official distribution source.
func buildVersionCatalogWithSource(
	releases []Release,
	targetOS, arch string,
	installed map[string]string,
	activeVersion, source string,
) []utils.GoVersion {
	var versions []utils.GoVersion
	for _, release := range releases {
		for _, file := range release.Files {
			if file.OS != targetOS || file.Arch != arch || (file.Kind != "" && file.Kind != "archive") {
				continue
			}
			v := utils.GoVersion{
				Version:  release.Version,
				Filename: file.Filename,
				URL:      archiveURL(source, file.Filename),
				SHA256:   file.SHA256,
				Size:     file.Size,
				Stable:   release.Stable,
			}
			if path, ok := installed[release.Version]; ok {
				v.Installed = true
				v.Path = path
			}
			if activeVersion == release.Version {
				v.Active = true
			}
			versions = append(versions, v)
			break
		}
	}
	sortGoVersionRecordsDesc(versions)
	return versions
}

func archiveURL(source, filename string) string {
	if source == "" {
		source = config.DefaultDistributionSource
	}
	return strings.TrimRight(source, "/") + "/" + filename
}

// sortGoVersionRecordsDesc orders records highest-version-first. The
// sort is stable so releases the comparator treats as equal keep their
// go.dev order.
func sortGoVersionRecordsDesc(records []utils.GoVersion) {
	sort.SliceStable(records, func(i, j int) bool {
		return utils.CompareGoVersions(records[i].Version, records[j].Version) > 0
	})
}
