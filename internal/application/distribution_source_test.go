package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/loader"
	"github.com/SmileOniks/govm/internal/utils"
)

type fakeSettingsStore struct {
	settings config.Settings
	saveErr  error
	calls    *[]string
	saves    []config.Settings
}

func (f *fakeSettingsStore) Load(string) (config.Settings, error) {
	*f.calls = append(*f.calls, "load-settings")
	return f.settings, nil
}

func (f *fakeSettingsStore) Save(_ string, settings config.Settings) error {
	*f.calls = append(*f.calls, "save:"+settings.DistributionSource)
	f.saves = append(f.saves, settings)
	if f.saveErr != nil {
		err := f.saveErr
		f.saveErr = nil
		return err
	}
	f.settings = settings
	return nil
}

type fakeCatalogLoader struct {
	candidate  loader.VersionCatalog
	refreshed  loader.VersionCatalog
	loadErr    error
	refreshErr error
	calls      *[]string
}

func (f *fakeCatalogLoader) LoadWithSource(_ context.Context, source string) (loader.VersionCatalog, error) {
	*f.calls = append(*f.calls, "validate:"+source)
	return f.candidate, f.loadErr
}

func (f *fakeCatalogLoader) Load(context.Context) (loader.VersionCatalog, error) {
	*f.calls = append(*f.calls, "refresh")
	return f.refreshed, f.refreshErr
}

type fakeSourceActivator struct {
	source string
	err    error
	calls  *[]string
}

func (f *fakeSourceActivator) SetDistributionSource(source string) error {
	*f.calls = append(*f.calls, "activate:"+source)
	if f.err != nil {
		err := f.err
		f.err = nil
		return err
	}
	f.source = source
	return nil
}

func TestDistributionSourceOperationChangesSourceTransactionally(t *testing.T) {
	calls := []string{}
	oldSource := config.DefaultDistributionSource
	newSource := "https://mirror.example/dl/"
	verified := catalogWithArchive("candidate.tar.gz")
	refreshed := catalogWithArchive("refreshed.tar.gz")
	store := &fakeSettingsStore{settings: settingsWithSource(oldSource), calls: &calls}
	catalog := &fakeCatalogLoader{candidate: verified, refreshed: refreshed, calls: &calls}
	activator := &fakeSourceActivator{source: oldSource, calls: &calls}
	operation := NewDistributionSourceOperation("settings.json", store, catalog, activator)

	result, err := operation.Change(context.Background(), " https://mirror.example/dl ")
	if err != nil {
		t.Fatalf("Change() error = %v", err)
	}
	if result.Source != newSource {
		t.Fatalf("source = %q, want %q", result.Source, newSource)
	}
	if !reflect.DeepEqual(result.Catalog, refreshed) {
		t.Fatalf("catalog = %#v, want refreshed catalog %#v", result.Catalog, refreshed)
	}
	wantCalls := []string{
		"validate:" + newSource,
		"load-settings",
		"save:" + newSource,
		"activate:" + newSource,
		"refresh",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
	if store.settings.DistributionSource != newSource || activator.source != newSource {
		t.Fatalf("source not committed: config=%q runtime=%q", store.settings.DistributionSource, activator.source)
	}
}

func TestDistributionSourceOperationDoesNotMutateBeforeValidation(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		loadErr   error
		catalog   loader.VersionCatalog
	}{
		{name: "invalid source", candidate: "http://mirror.example/dl"},
		{name: "catalog failure", candidate: "https://mirror.example/dl", loadErr: errors.New("offline")},
		{name: "missing platform archive", candidate: "https://mirror.example/dl", catalog: loader.VersionCatalog{}},
		{
			name:      "catalog without platform identity",
			candidate: "https://mirror.example/dl",
			catalog: loader.VersionCatalog{
				Versions: []utils.GoVersion{{Version: "1.26.0", Filename: "archive.tar.gz"}},
			},
		},
		{
			name:      "invalid catalog projection",
			candidate: "https://mirror.example/dl",
			catalog: loader.VersionCatalog{
				Versions: []utils.GoVersion{
					{Version: "1.26.0", Filename: "one.tar.gz"},
					{Version: "1.26.0", Filename: "two.tar.gz"},
				},
				Platform: loader.Platform{OS: "darwin", Arch: "arm64"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			store := &fakeSettingsStore{settings: settingsWithSource(config.DefaultDistributionSource), calls: &calls}
			catalog := &fakeCatalogLoader{candidate: tt.catalog, loadErr: tt.loadErr, calls: &calls}
			activator := &fakeSourceActivator{source: config.DefaultDistributionSource, calls: &calls}
			operation := NewDistributionSourceOperation("settings.json", store, catalog, activator)

			if _, err := operation.Change(context.Background(), tt.candidate); err == nil {
				t.Fatal("Change() error = nil")
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "save:") || strings.HasPrefix(call, "activate:") {
					t.Fatalf("premature mutation call %q in %#v", call, calls)
				}
			}
		})
	}
}

func TestDistributionSourceOperationRollsBackMutationFailures(t *testing.T) {
	tests := []struct {
		name        string
		saveErr     error
		activateErr error
		refreshErr  error
	}{
		{name: "persistence", saveErr: errors.New("disk full")},
		{name: "activation", activateErr: errors.New("activation failed")},
		{name: "refresh", refreshErr: errors.New("refresh failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			oldSource := config.DefaultDistributionSource
			store := &fakeSettingsStore{
				settings: settingsWithSource(oldSource),
				saveErr:  tt.saveErr,
				calls:    &calls,
			}
			catalog := &fakeCatalogLoader{
				candidate:  catalogWithArchive("candidate.tar.gz"),
				refreshed:  catalogWithArchive("refreshed.tar.gz"),
				refreshErr: tt.refreshErr,
				calls:      &calls,
			}
			activator := &fakeSourceActivator{source: oldSource, err: tt.activateErr, calls: &calls}
			operation := NewDistributionSourceOperation("settings.json", store, catalog, activator)

			if _, err := operation.Change(context.Background(), "https://mirror.example/dl"); err == nil {
				t.Fatal("Change() error = nil")
			}
			if store.settings.DistributionSource != oldSource {
				t.Fatalf("config source = %q, want rollback to %q", store.settings.DistributionSource, oldSource)
			}
			if activator.source != oldSource {
				t.Fatalf("runtime source = %q, want rollback to %q", activator.source, oldSource)
			}
			if len(store.saves) < 2 {
				t.Fatalf("save calls = %d, want commit and rollback", len(store.saves))
			}
		})
	}
}

func settingsWithSource(source string) config.Settings {
	settings := config.DefaultSettings()
	settings.DistributionSource = source
	return settings
}

func catalogWithArchive(filename string) loader.VersionCatalog {
	return loader.VersionCatalog{
		Versions: []utils.GoVersion{{Version: "1.26.0", Filename: filename}},
		Platform: loader.Platform{OS: "darwin", Arch: "arm64"},
	}
}
