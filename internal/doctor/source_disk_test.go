package doctor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/prune"
)

const catalogJSON = `[{"version":"go1.27.1","stable":true,"files":[]}]`

// doerFunc adapts a function to utils.Doer.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// catalogDoer answers every request with a valid, non-empty catalog
// and never touches the network.
func catalogDoer() doerFunc {
	return func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(catalogJSON)),
			Request:    req,
		}, nil
	}
}

func TestCheckSource(t *testing.T) {
	t.Run("valid catalog is ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, catalogJSON)
		}))
		defer srv.Close()
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = srv.Client()
		f.deps.Source = srv.URL + "/dl/"
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictOK, "source "+srv.URL+"/dl/ reachable", "")
	})

	t.Run("server error is warn", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = srv.Client()
		f.deps.Source = srv.URL + "/dl/"
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictWarn, "source "+srv.URL+"/dl/ unreachable: fetch go.dev releases: unexpected status 500", "check network or run with --offline")
	})

	t.Run("empty catalog is warn", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = doerFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Request: req}, nil
		})
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictWarn, "empty release list", "check network or run with --offline")
	})

	t.Run("hanging server times out with warn", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer srv.Close()
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = srv.Client()
		f.deps.Source = srv.URL + "/dl/"
		f.deps.SourceTimeout = 50 * time.Millisecond
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictWarn, "timeout after 50ms", "check network or run with --offline")
	})

	t.Run("offline never issues a request", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.deps.Offline = true
		f.deps.HTTPClient = doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request issued in offline mode")
			return nil, errors.New("unreachable")
		})
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictOK, "skipped (--offline)", "")
	})

	t.Run("source defaults to settings.json then go.dev", func(t *testing.T) {
		var seen []string
		record := doerFunc(func(req *http.Request) (*http.Response, error) {
			seen = append(seen, req.URL.String())
			return catalogDoer()(req)
		})
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = record
		checkByName(t, run(t, f), CheckSource)

		f.writeRoot(t, "settings.json", `{"distributionSource":"https://mirror.example/golang"}`)
		c := checkByName(t, run(t, f), CheckSource)
		assertCheck(t, c, VerdictOK, "source https://mirror.example/golang/ reachable", "")

		if len(seen) != 2 {
			t.Fatalf("requests = %v, want 2", seen)
		}
		if !strings.HasPrefix(seen[0], config.DefaultDistributionSource) {
			t.Errorf("default request %q does not target %s", seen[0], config.DefaultDistributionSource)
		}
		if !strings.HasPrefix(seen[1], "https://mirror.example/golang/") {
			t.Errorf("settings request %q does not target the configured mirror", seen[1])
		}
	})

	t.Run("cancelled context is warn", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.deps.HTTPClient = doerFunc(func(req *http.Request) (*http.Response, error) {
			return nil, req.Context().Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := checkByName(t, Run(ctx, f.deps), CheckSource)
		assertCheck(t, c, VerdictWarn, "unreachable", "check network or run with --offline")
	})
}

func (f *fixture) writeDownload(t *testing.T, name string, size int) {
	t.Helper()
	dir := filepath.Join(f.root, "downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDisk(t *testing.T) {
	t.Run("single active version and no downloads is ok", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictOK, "disk: 1 version (", "")
		if !strings.Contains(c.Detail, "downloads 0 B") {
			t.Fatalf("detail %q lacks downloads size", c.Detail)
		}
	})

	t.Run("inactive version is warn", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.install(t, "1.26.0")
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictWarn, "disk: 2 versions (", "run `govm prune`")
		if !strings.Contains(c.Detail, "1 inactive") {
			t.Fatalf("detail %q does not name the inactive version count", c.Detail)
		}
	})

	t.Run("no active version counts every toolchain as inactive", func(t *testing.T) {
		f := newFixture(t)
		f.mkdirs(t)
		f.install(t, "1.27.1")
		f.install(t, "1.26.0")
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictWarn, "2 versions (", "run `govm prune`")
		if !strings.Contains(c.Detail, "2 inactive") {
			t.Fatalf("detail %q does not count both versions as inactive", c.Detail)
		}
	})

	t.Run("leftover download is warn", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.writeDownload(t, ".govm-install-abc.part", 2048)
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictWarn, "downloads 2.0 KiB", "run `govm prune`")
	})

	t.Run("scan warnings are appended without escalating", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		if err := os.WriteFile(filepath.Join(f.versions, "stray.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictOK, "warnings: ", "")
		if !strings.Contains(c.Detail, "stray.txt") {
			t.Fatalf("detail %q does not mention the stray object", c.Detail)
		}
	})

	t.Run("missing root reports zero usage", func(t *testing.T) {
		f := newFixture(t)
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictOK, "disk: 0 versions (0 B), downloads 0 B", "")
	})

	t.Run("disk usage error is warn", func(t *testing.T) {
		f := newFixture(t)
		f.healthy(t)
		f.deps.DiskUsage = func(context.Context) (prune.Summary, error) {
			return prune.Summary{}, errors.New("scan failed")
		}
		c := checkByName(t, run(t, f), CheckDisk)
		assertCheck(t, c, VerdictWarn, "disk usage unavailable: scan failed", "check permissions")
	})
}
