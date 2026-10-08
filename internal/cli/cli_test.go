package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/utils"
)

func catalogFixture() []utils.GoVersion {
	return []utils.GoVersion{
		{
			Version:  "1.22.0",
			Filename: "go1.22.0.darwin-arm64.tar.gz",
			URL:      "https://go.dev/dl/go1.22.0.darwin-arm64.tar.gz",
			SHA256:   "aaa",
			Size:     101,
			Stable:   true,
		},
		{Version: "1.21.5", Filename: "go1.21.5.darwin-arm64.tar.gz"},
		{Version: "1.21.0", Filename: "go1.21.0.darwin-arm64.tar.gz"},
	}
}

func staticCatalog(versions []utils.GoVersion) loadCatalogFunc {
	return func(context.Context) ([]utils.GoVersion, error) {
		return versions, nil
	}
}

func TestFindMatchingVersionReturnsWholeRecordOnExactMatch(t *testing.T) {
	matched, err := findMatchingVersion(staticCatalog(catalogFixture()), "1.22.0")
	if err != nil {
		t.Fatalf("findMatchingVersion failed: %v", err)
	}
	want := catalogFixture()[0]
	if matched != want {
		t.Fatalf("matched = %+v, want %+v", matched, want)
	}
}

func TestFindMatchingVersionStripsLeadingGo(t *testing.T) {
	matched, err := findMatchingVersion(staticCatalog(catalogFixture()), "go1.22.0")
	if err != nil {
		t.Fatalf("findMatchingVersion failed: %v", err)
	}
	if matched.Version != "1.22.0" {
		t.Fatalf("matched version = %q, want 1.22.0", matched.Version)
	}
}

func TestFindMatchingVersionPrefixSelectsHighest(t *testing.T) {
	matched, err := findMatchingVersion(staticCatalog(catalogFixture()), "1.21")
	if err != nil {
		t.Fatalf("findMatchingVersion failed: %v", err)
	}
	if matched.Version != "1.21.5" {
		t.Fatalf("matched version = %q, want 1.21.5", matched.Version)
	}
}

func TestFindMatchingVersionRejectsUnknownQuery(t *testing.T) {
	_, err := findMatchingVersion(staticCatalog(catalogFixture()), "1.19")
	if err == nil {
		t.Fatal("expected error for unmatched query")
	}
	if !strings.Contains(err.Error(), "no version matching '1.19' found") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindMatchingVersionWrapsLoadFailure(t *testing.T) {
	loadErr := errors.New("catalog unavailable")
	_, err := findMatchingVersion(func(context.Context) ([]utils.GoVersion, error) {
		return nil, loadErr
	}, "1.22.0")
	if !errors.Is(err, loadErr) {
		t.Fatalf("err = %v, want wrapped %v", err, loadErr)
	}
}

func TestFindMatchingVersionWithoutSeamFailsWithoutPanic(t *testing.T) {
	_, err := findMatchingVersion(nil, "1.22.0")
	if err == nil {
		t.Fatal("expected error when no catalog seam is bound")
	}
}

func TestFindMatchingVersionAppliesLoadDeadline(t *testing.T) {
	var deadlineSet bool
	_, err := findMatchingVersion(func(ctx context.Context) ([]utils.GoVersion, error) {
		_, deadlineSet = ctx.Deadline()
		return catalogFixture(), nil
	}, "1.22.0")
	if err != nil {
		t.Fatalf("findMatchingVersion failed: %v", err)
	}
	if !deadlineSet {
		t.Fatal("catalog load context carries no deadline")
	}
}
