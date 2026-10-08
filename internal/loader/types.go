package loader

import (
	"context"

	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/utils"
)

// VersionCatalog is the complete domain result of loading available
// and installed Go versions. It joins the go.dev release catalog with
// the local view of installed/active versions.
type VersionCatalog struct {
	Versions      []utils.GoVersion
	ActiveVersion string
	Platform      Platform
}

// Platform identifies the OS and architecture used to filter releases.
type Platform struct {
	OS   string
	Arch string
}

// Dependencies bundles the external I/O adapters LoadVersionCatalog
// needs. Each dependency is substitutable for testing.
type Dependencies struct {
	ReleaseSource      ReleaseSource
	LocalRegistry      local.Registry
	Platform           Platform
	DistributionSource string
}

// ReleaseSource fetches the go.dev release catalog.
type ReleaseSource interface {
	FetchReleases(ctx context.Context) ([]Release, error)
}

// Release mirrors a single go.dev release entry. The Version field
// is normalised: the "go" prefix is stripped at the adapter boundary.
type Release struct {
	Version string
	Stable  bool
	Files   []FileEntry
}

// FileEntry mirrors a single file within a go.dev release.
type FileEntry struct {
	Filename string
	OS       string
	Arch     string
	Kind     string
	SHA256   string
	Size     int64
}
