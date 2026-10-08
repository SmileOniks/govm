package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/utils"
)

var ErrNotFound = errors.New("toolchain not found")

type Toolchain struct {
	Version string
	Path    string
}

type Registry interface {
	List(context.Context) ([]Toolchain, error)
	Find(context.Context, string) (Toolchain, error)
	Active(context.Context) (string, error)
}

type FilesystemRegistry struct {
	resolver *paths.Resolver
	// currentGoVersion reports the version of the go executable found on
	// PATH. It is a field so tests can exercise the effective-active
	// fallback without depending on the go toolchain of the test host.
	currentGoVersion func() string
}

func NewRegistry(resolver *paths.Resolver) *FilesystemRegistry {
	return &FilesystemRegistry{
		resolver:         resolver,
		currentGoVersion: utils.GetCurrentGoVersion,
	}
}

func (r *FilesystemRegistry) List(ctx context.Context) ([]Toolchain, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	versionsDir, err := r.resolver.VersionsDir()
	if err != nil {
		return nil, err
	}

	installed, err := utils.ScanInstalledVersions(versionsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Toolchain{}, nil
	}
	if err != nil {
		return nil, err
	}

	versions := make([]string, 0, len(installed))
	for version := range installed {
		versions = append(versions, version)
	}
	utils.SortGoVersionsDesc(versions)

	result := make([]Toolchain, 0, len(versions))
	for _, version := range versions {
		result = append(result, Toolchain{
			Version: version,
			Path:    installed[version],
		})
	}
	return result, nil
}

func (r *FilesystemRegistry) Find(ctx context.Context, query string) (Toolchain, error) {
	toolchains, err := r.List(ctx)
	if err != nil {
		return Toolchain{}, err
	}

	versions := make([]string, 0, len(toolchains))
	byVersion := make(map[string]Toolchain, len(toolchains))
	for _, toolchain := range toolchains {
		versions = append(versions, toolchain.Version)
		byVersion[toolchain.Version] = toolchain
	}

	matched, ok := utils.FindLatestGoVersion(versions, query)
	if !ok {
		return Toolchain{}, fmt.Errorf("%w: %q", ErrNotFound, query)
	}
	return byVersion[matched], nil
}

func (r *FilesystemRegistry) Active(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	activePath, err := r.resolver.ActiveVersionFile()
	if err != nil {
		return "", err
	}
	activeVersion, err := utils.ReadActiveVersion(activePath)
	if err != nil {
		return "", err
	}
	if activeVersion != "" {
		return activeVersion, nil
	}
	if r.currentGoVersion == nil {
		return utils.GetCurrentGoVersion(), nil
	}
	return r.currentGoVersion(), nil
}
