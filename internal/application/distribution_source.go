package application

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/loader"
)

type SettingsStore interface {
	Load(path string) (config.Settings, error)
	Save(path string, settings config.Settings) error
}

type CatalogLoader interface {
	Load(context.Context) (loader.VersionCatalog, error)
	LoadWithSource(context.Context, string) (loader.VersionCatalog, error)
}

type SourceActivator interface {
	SetDistributionSource(string) error
}

type DistributionSourceResult struct {
	Source  string
	Catalog loader.VersionCatalog
}

type ChangeError struct {
	Err             error
	SourcePreserved bool
}

func (e *ChangeError) Error() string {
	return e.Err.Error()
}

func (e *ChangeError) Unwrap() error {
	return e.Err
}

type DistributionSourceOperation struct {
	mu        sync.Mutex
	path      string
	store     SettingsStore
	catalog   CatalogLoader
	activator SourceActivator
}

func NewDistributionSourceOperation(
	path string,
	store SettingsStore,
	catalog CatalogLoader,
	activator SourceActivator,
) *DistributionSourceOperation {
	return &DistributionSourceOperation{
		path:      path,
		store:     store,
		catalog:   catalog,
		activator: activator,
	}
}

func (o *DistributionSourceOperation) Change(ctx context.Context, candidate string) (DistributionSourceResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	normalized, err := config.ValidateDistributionSource(candidate)
	if err != nil {
		return DistributionSourceResult{}, fmt.Errorf("validate distribution source: %w", err)
	}
	if o.catalog == nil {
		return DistributionSourceResult{}, errors.New("validate distribution source: no catalog loader configured")
	}
	if o.store == nil {
		return DistributionSourceResult{}, errors.New("save distribution source: no settings store configured")
	}

	catalog, err := o.catalog.LoadWithSource(ctx, normalized)
	if err != nil {
		return DistributionSourceResult{}, fmt.Errorf("load distribution catalog: %w", err)
	}
	if err := loader.ValidatePlatformCatalog(catalog); err != nil {
		return DistributionSourceResult{}, fmt.Errorf("validate distribution catalog: %w", err)
	}

	previous, err := o.store.Load(o.path)
	if err != nil {
		return DistributionSourceResult{}, fmt.Errorf("load current settings: %w", err)
	}
	previous = config.Normalize(previous)
	next := previous
	next.DistributionSource = normalized
	if err := o.store.Save(o.path, next); err != nil {
		return o.rollback(previous, fmt.Errorf("save distribution source: %w", err))
	}
	if o.activator == nil {
		return o.rollback(previous, errors.New("activate distribution source: no source activator configured"))
	}
	if err := o.activator.SetDistributionSource(normalized); err != nil {
		return o.rollback(previous, fmt.Errorf("activate distribution source: %w", err))
	}
	catalog, err = o.catalog.Load(ctx)
	if err != nil {
		return o.rollback(previous, fmt.Errorf("refresh distribution catalog: %w", err))
	}
	if err := loader.ValidatePlatformCatalog(catalog); err != nil {
		return o.rollback(previous, fmt.Errorf("validate refreshed distribution catalog: %w", err))
	}

	return DistributionSourceResult{Source: normalized, Catalog: catalog}, nil
}

func (o *DistributionSourceOperation) rollback(previous config.Settings, cause error) (DistributionSourceResult, error) {
	rollbackErr := o.store.Save(o.path, previous)
	if o.activator != nil {
		rollbackErr = errors.Join(rollbackErr, o.activator.SetDistributionSource(previous.DistributionSource))
	}
	if rollbackErr != nil {
		return DistributionSourceResult{}, &ChangeError{
			Err: errors.Join(cause, fmt.Errorf("rollback distribution source: %w", rollbackErr)),
		}
	}
	return DistributionSourceResult{}, &ChangeError{Err: cause, SourcePreserved: true}
}

type FileSettingsStore struct{}

func (FileSettingsStore) Load(path string) (config.Settings, error) {
	return config.Load(path)
}

func (FileSettingsStore) Save(path string, settings config.Settings) error {
	return config.Save(path, settings)
}
