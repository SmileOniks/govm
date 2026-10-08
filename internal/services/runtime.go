package services

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/SmileOniks/govm/internal/adapter/github"
	"github.com/SmileOniks/govm/internal/adapter/godev"
	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/loader"
	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/state"
	"github.com/SmileOniks/govm/internal/upgrade"
	"github.com/SmileOniks/govm/internal/utils"
)

// Runtime is the composition root for all govm services. It wires
// together the loader, lifecycle, and install services with their
// production dependencies. main.go creates exactly one Runtime and
// passes it to CLI and TUI adapters.
type Runtime struct {
	Loader    *Loader
	Registry  local.Registry
	Lifecycle *lifecycle.Service
	Install   *install.Service
	Prune     *prune.Service
	Paths     *paths.Resolver
	// Upgrade compares the running govm against the Latest release on
	// GitHub. It shares the HTTP client with the catalog loader.
	Upgrade *upgrade.Checker
}

// Loader wraps loader orchestration with production dependencies.
// Separated into its own type so it can carry method receivers
// for TUI/CLI convenience.
type Loader struct {
	mu         sync.RWMutex
	deps       loader.Dependencies
	httpClient utils.Doer
}

// Load is the production entry point for loading the version catalog.
func (l *Loader) Load(ctx context.Context) (loader.VersionCatalog, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err := validateDistributionSource(l.deps.DistributionSource); err != nil {
		return loader.VersionCatalog{}, err
	}
	return loader.LoadVersionCatalog(ctx, l.deps)
}

// LoadVersions returns only the catalog entries. It is the narrow seam
// the CLI and TUI adapters bind to, so they do not depend on the
// composition root.
func (l *Loader) LoadVersions(ctx context.Context) ([]utils.GoVersion, error) {
	catalog, err := l.Load(ctx)
	if err != nil {
		return nil, err
	}
	return catalog.Versions, nil
}

func (l *Loader) LoadWithSource(ctx context.Context, source string) (loader.VersionCatalog, error) {
	normalized, err := config.ValidateDistributionSource(source)
	if err != nil {
		return loader.VersionCatalog{}, err
	}

	l.mu.RLock()
	deps := l.deps
	httpClient := l.httpClient
	l.mu.RUnlock()
	deps.DistributionSource = normalized
	deps.ReleaseSource = godev.NewClientForSource(httpClient, normalized)
	return loader.LoadVersionCatalog(ctx, deps)
}

func (l *Loader) SetDistributionSource(source string) error {
	normalized, err := config.ValidateDistributionSource(source)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.deps.DistributionSource = normalized
	l.deps.ReleaseSource = godev.NewClientForSource(l.httpClient, normalized)
	return nil
}

func validateDistributionSource(source string) error {
	if source == "" {
		return nil
	}
	_, err := config.ValidateDistributionSource(source)
	return err
}

// NewRuntime constructs the production composition root with all
// services wired to their real dependencies.
func NewRuntime(settings config.Settings) (*Runtime, error) {
	resolver := paths.New()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	source := config.Normalize(settings).DistributionSource
	releaseSource := godev.NewClientForSource(httpClient, source)
	localRegistry := local.NewRegistry(resolver)
	platform := loader.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}

	loaderDeps := loader.Dependencies{
		ReleaseSource:      releaseSource,
		LocalRegistry:      localRegistry,
		Platform:           platform,
		DistributionSource: source,
	}

	coordinator := state.NewCoordinator(resolver)
	lifecycleSvc, err := lifecycle.New(resolver, coordinator)
	if err != nil {
		return nil, fmt.Errorf("initialize lifecycle service: %w", err)
	}

	govmVersion := utils.GetVersion()
	latestRelease := github.NewClient(httpClient, github.DefaultAPIBaseURL, utils.Repository, "govm/"+govmVersion)
	upgradeChecker := upgrade.NewChecker(latestRelease, govmVersion)

	installSvc := install.New(resolver, coordinator)
	pruneSvc, err := prune.New(resolver, coordinator, lifecycleSvc)
	if err != nil {
		return nil, fmt.Errorf("initialize prune service: %w", err)
	}

	return &Runtime{
		Loader:    &Loader{deps: loaderDeps, httpClient: httpClient},
		Registry:  localRegistry,
		Lifecycle: lifecycleSvc,
		Install:   installSvc,
		Prune:     pruneSvc,
		Paths:     resolver,
		Upgrade:   upgradeChecker,
	}, nil
}
