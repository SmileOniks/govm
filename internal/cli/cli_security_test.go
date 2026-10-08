package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SmileOniks/govm/internal/adapter/local"
	"github.com/SmileOniks/govm/internal/paths"
)

func TestRegistryFindRejectsTraversalQueries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	versionsDir := filepath.Join(home, ".govm", "versions")
	if err := os.MkdirAll(filepath.Join(versionsDir, "go1.24.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		"../../../..",
		"v../../../..",
		"go../../../..",
	} {
		t.Run(query, func(t *testing.T) {
			registry := local.NewRegistry(&paths.Resolver{HomeDir: func() (string, error) {
				return home, nil
			}})
			if _, err := registry.Find(context.Background(), query); err == nil {
				t.Fatalf("registry.Find(%q) resolved a traversal query", query)
			}
		})
	}
}
