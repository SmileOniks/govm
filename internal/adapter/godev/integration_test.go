package godev_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SmileOniks/govm/internal/adapter/godev"
	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/loader"
)

type fakeRegistry struct {
	toolchains []local.Toolchain
	active     string
}

func (f *fakeRegistry) List(context.Context) ([]local.Toolchain, error) {
	return f.toolchains, nil
}

func (f *fakeRegistry) Find(context.Context, string) (local.Toolchain, error) {
	return local.Toolchain{}, local.ErrNotFound
}

func (f *fakeRegistry) Active(context.Context) (string, error) {
	return f.active, nil
}

func TestIntegration_GodevWithLoader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[
			{
				"version": "go1.22.0",
				"stable": true,
				"files": [
					{
						"filename": "go1.22.0.linux-amd64.tar.gz",
						"os": "linux",
						"arch": "amd64",
						"kind": "archive",
						"sha256": "abc123",
						"size": 102400
					}
				]
			},
			{
				"version": "go1.21.5",
				"stable": true,
				"files": [
					{
						"filename": "go1.21.5.linux-amd64.tar.gz",
						"os": "linux",
						"arch": "amd64",
						"kind": "archive",
						"sha256": "def456",
						"size": 98304
					}
				]
			}
		]`))
	}))
	defer server.Close()

	deps := loader.Dependencies{
		ReleaseSource: godev.NewClient(server.Client(), server.URL),
		LocalRegistry: &fakeRegistry{
			toolchains: []local.Toolchain{
				{Version: "1.21.5", Path: "/home/user/.govm/versions/go1.21.5"},
			},
			active: "1.21.5",
		},
		Platform: loader.Platform{OS: "linux", Arch: "amd64"},
	}

	ctx := context.Background()
	catalog, err := loader.LoadVersionCatalog(ctx, deps)
	if err != nil {
		t.Fatalf("LoadVersionCatalog with real godev adapter failed: %v", err)
	}

	if len(catalog.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(catalog.Versions))
	}

	if catalog.Versions[0].Version != "1.22.0" {
		t.Errorf("expected first version 1.22.0, got %s", catalog.Versions[0].Version)
	}
	if catalog.Versions[0].SHA256 != "abc123" {
		t.Errorf("expected SHA256 abc123, got %s", catalog.Versions[0].SHA256)
	}
	if catalog.Versions[0].Size != 102400 {
		t.Errorf("expected size 102400, got %d", catalog.Versions[0].Size)
	}

	if catalog.Versions[1].Version != "1.21.5" {
		t.Errorf("expected second version 1.21.5, got %s", catalog.Versions[1].Version)
	}
	if !catalog.Versions[1].Installed {
		t.Errorf("expected 1.21.5 to be installed")
	}
	if !catalog.Versions[1].Active {
		t.Errorf("expected 1.21.5 to be active")
	}
	if catalog.Versions[1].SHA256 != "def456" {
		t.Errorf("expected SHA256 def456, got %s", catalog.Versions[1].SHA256)
	}

	if catalog.ActiveVersion != "1.21.5" {
		t.Errorf("expected active version 1.21.5, got %s", catalog.ActiveVersion)
	}
}
