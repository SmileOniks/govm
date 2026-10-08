package loader

import (
	"errors"
	"fmt"

	"github.com/SmileOniks/govm/internal/utils"
)

func ValidateCatalogVersions(versions []utils.GoVersion) error {
	seen := make(map[string]struct{}, len(versions))
	activeCount := 0
	for i, version := range versions {
		if version.Version == "" {
			return fmt.Errorf("empty version id at index %d", i)
		}
		if _, exists := seen[version.Version]; exists {
			return fmt.Errorf("duplicate version id %q", version.Version)
		}
		seen[version.Version] = struct{}{}
		if version.Active {
			activeCount++
		}
		if version.Installed && version.Path == "" {
			return fmt.Errorf("installed version %q requires non-empty path", version.Version)
		}
		if !version.Installed && version.Path != "" {
			return fmt.Errorf("uninstalled version %q must have empty path", version.Version)
		}
	}
	if activeCount > 1 {
		return fmt.Errorf("at most one active version allowed, got %d", activeCount)
	}
	return nil
}

func ValidatePlatformCatalog(catalog VersionCatalog) error {
	if catalog.Platform.OS == "" || catalog.Platform.Arch == "" {
		return errors.New("catalog platform is required")
	}
	if err := ValidateCatalogVersions(catalog.Versions); err != nil {
		return fmt.Errorf("validate catalog versions: %w", err)
	}
	for _, version := range catalog.Versions {
		if version.Filename != "" {
			return nil
		}
	}
	return errors.New("catalog has no archive for the current platform")
}
