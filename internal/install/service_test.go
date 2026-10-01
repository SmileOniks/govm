package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/smileoniks-ctrl/govm/internal/paths"
	"github.com/smileoniks-ctrl/govm/internal/state"
)

// Test doubles

type fakeDoer struct {
	respond func(*http.Request) (*http.Response, error)
}

func (f *fakeDoer) Do(r *http.Request) (*http.Response, error) { return f.respond(r) }

type contextBody struct {
	ctx context.Context
}

func (b *contextBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextBody) Close() error {
	return nil
}

func okResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func statusResponse(code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

func stubExtractor(_ context.Context, _ string, destination string) error {
	binDir := filepath.Join(destination, "go", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(binDir, binaryName()), []byte("fake-binary"), 0o700)
}

func versionOutput(version string) []byte {
	return []byte(fmt.Sprintf("go version go%s %s/%s\n", version, runtime.GOOS, runtime.GOARCH))
}

// Helpers

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func makeRequest(version string, mutators ...func(*Request)) Request {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	filename := "go" + version + "." + runtime.GOOS + "-" + runtime.GOARCH + "." + ext
	r := Request{
		Version:  version,
		Filename: filename,
		URL:      "https://go.dev/dl/" + filename,
	}
	for _, m := range mutators {
		m(&r)
	}
	return r
}

func with(r Request, fn func(*Request)) Request {
	fn(&r)
	return r
}

// newTestService returns a Service wired to a temp govm root with
// in-memory HTTP, a stub extractor that materialises a go tree, and a
// verifier returning the expected version output.
func newTestService(t *testing.T, version string) (*Service, string) {
	t.Helper()
	tmp := t.TempDir()
	s := &Service{
		resolver: &paths.Resolver{HomeDir: func() (string, error) { return tmp, nil }},
		doer: &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return okResponse([]byte("archive-content")), nil
		}},
		extract: stubExtractor,
		verify: func(context.Context, string) ([]byte, error) {
			return versionOutput(version), nil
		},
		rename:  os.Rename,
		cleanup: os.RemoveAll,
	}
	return s, tmp
}

func assertStage(t *testing.T, err error, want Stage) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", want)
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *install.Error, got %T: %v", err, err)
	}
	if se.Stage != want {
		t.Fatalf("expected stage %s, got %s (%v)", want, se.Stage, err)
	}
}

func versionsDirFor(tmp string) string { return filepath.Join(tmp, ".govm", "versions") }

func TestInstallHoldsMutationLockDuringDownload(t *testing.T) {
	s, _ := newTestService(t, "1.26.1")
	entered := make(chan struct{})
	release := make(chan struct{})
	s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return okResponse([]byte("archive-content")), nil
	}}

	done := make(chan error, 1)
	go func() {
		_, err := s.Install(t.Context(), makeRequest("1.26.1"))
		done <- err
	}()
	<-entered

	resolver := s.resolver
	lockPath, err := resolver.StateMutationLockFile()
	must(t, err)
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	must(t, err)
	if locked {
		_ = lock.Unlock()
		t.Fatal("install did not hold mutation lock during download")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Install() error = %v", err)
	}
}

// seedInstall writes a recognisable prior install (bin/go plus a
// marker) at the final version directory.
func seedInstall(t *testing.T, dir string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "bin", binaryName()), []byte("old-binary"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "OLD_MARKER"), []byte("old"), 0o600))
}

// Validation

func TestInstall_Validation(t *testing.T) {
	base := func() Request { return makeRequest("1.22.0") }
	cases := []struct {
		name string
		req  Request
	}{
		{"empty version", with(base(), func(r *Request) { r.Version = "" })},
		{"malformed version leading go", with(base(), func(r *Request) { r.Version = "go1.22.0" })},
		{"malformed version letters", with(base(), func(r *Request) { r.Version = "abc" })},
		{"malformed version suffix", with(base(), func(r *Request) { r.Version = "1.22evil" })},
		{"filename mismatch", with(base(), func(r *Request) { r.Filename = "wrong.tar.gz" })},
		{"url non-https", with(base(), func(r *Request) {
			r.URL = "http://go.dev/dl/" + r.Filename
		})},
		{"url missing host", with(base(), func(r *Request) {
			r.URL = "https:///dl/" + r.Filename
		})},
		{"url path missing filename", with(base(), func(r *Request) {
			r.URL = "https://mirror.example/go/"
		})},
		{"url query", with(base(), func(r *Request) {
			r.URL = "https://mirror.example/go/" + r.Filename + "?token=secret"
		})},
		{"url basename mismatch", with(base(), func(r *Request) {
			r.URL = "https://go.dev/dl/other.tar.gz"
		})},
		{"url empty", with(base(), func(r *Request) { r.URL = "" })},
		{"sha wrong length", with(base(), func(r *Request) { r.SHA256 = "abc" })},
		{"sha non-hex", with(base(), func(r *Request) { r.SHA256 = strings.Repeat("z", 64) })},
		{"size negative", with(base(), func(r *Request) { r.Size = -1 })},
		{"size oversized", with(base(), func(r *Request) { r.Size = MaxDownloadSize + 1 })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newTestService(t, "1.22.0")
			_, err := s.Install(context.Background(), c.req)
			assertStage(t, err, StageValidate)
		})
	}
}

func TestInstall_ValidationAcceptsOfficialPrereleaseVersions(t *testing.T) {
	for _, version := range []string{"1.26beta1", "1.26rc2"} {
		t.Run(version, func(t *testing.T) {
			s, _ := newTestService(t, version)
			if _, err := s.Install(context.Background(), makeRequest(version)); err != nil {
				t.Fatalf("official prerelease version should be accepted: %v", err)
			}
		})
	}
}

func TestInstall_ValidationAcceptsCaseInsensitiveOfficialHost(t *testing.T) {
	s, _ := newTestService(t, "1.22.0")
	req := makeRequest("1.22.0")
	req.URL = "https://GO.DEV/dl/" + req.Filename

	if _, err := s.Install(context.Background(), req); err != nil {
		t.Fatalf("official host should be case-insensitive: %v", err)
	}
}

func TestInstall_ValidationAcceptsConfiguredMirrorURL(t *testing.T) {
	s, _ := newTestService(t, "1.22.0")
	req := makeRequest("1.22.0")
	req.URL = "https://mirror.example/golang/" + req.Filename

	if _, err := s.Install(context.Background(), req); err != nil {
		t.Fatalf("configured mirror URL should be accepted: %v", err)
	}
}

func TestProductionHTTPClientRedirectPolicy(t *testing.T) {
	client := newProductionHTTPClient()
	if client.Timeout != 30*time.Minute {
		t.Fatalf("client timeout = %v, want 30m", client.Timeout)
	}

	tests := []struct {
		name    string
		rawURL  string
		via     int
		wantErr bool
	}{
		{name: "go dev", rawURL: "https://go.dev/dl/file", wantErr: false},
		{name: "google download", rawURL: "https://dl.google.com/go/file", wantErr: false},
		{name: "uppercase official host", rawURL: "https://DL.GOOGLE.COM/go/file", wantErr: false},
		{name: "plaintext", rawURL: "http://dl.google.com/go/file", wantErr: true},
		{name: "mirror CDN host", rawURL: "https://example.com/go/file", wantErr: false},
		{name: "too many redirects", rawURL: "https://go.dev/dl/file", via: 10, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.rawURL, nil)
			must(t, err)
			via := make([]*http.Request, tt.via)
			err = client.CheckRedirect(req, via)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckRedirect() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

// Lock

func TestInstall_LockContention(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	root := filepath.Join(tmp, ".govm")
	must(t, os.MkdirAll(root, 0o700))
	lockPath := filepath.Join(root, "install.lock")

	holder := flock.New(lockPath)
	ok, err := holder.TryLock()
	if err != nil || !ok {
		t.Fatalf("setup lock: ok=%v err=%v", ok, err)
	}
	defer holder.Unlock()

	_, err = s.Install(context.Background(), makeRequest("1.22.0"))
	assertStage(t, err, StageLock)

	// The lock file must persist even after the failed attempt.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should persist: %v", err)
	}
}

func TestInstall_CancelledDownloadReleasesLock(t *testing.T) {
	s, _ := newTestService(t, "1.22.0")
	started := make(chan struct{})
	s.doer = &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		close(started)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       &contextBody{ctx: req.Context()},
		}, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := s.Install(ctx, makeRequest("1.22.0"))
		errCh <- err
	}()
	<-started
	cancel()
	assertStage(t, <-errCh, StageDownload)

	s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
		return okResponse([]byte("archive-content")), nil
	}}
	if _, err := s.Install(context.Background(), makeRequest("1.22.0")); err != nil {
		t.Fatalf("follow-up install should acquire released lock: %v", err)
	}
}

// Download and integrity

func TestInstall_DownloadFailures(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return statusResponse(404), nil
		}}
		_, err := s.Install(context.Background(), makeRequest("1.22.0"))
		assertStage(t, err, StageDownload)
	})

	t.Run("size mismatch empty", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		req := makeRequest("1.22.0", func(r *Request) { r.Size = 100 })
		s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return okResponse(nil), nil
		}}
		_, err := s.Install(context.Background(), req)
		assertStage(t, err, StageDownload)
	})

	t.Run("empty archive without declared size", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return okResponse(nil), nil
		}}
		_, err := s.Install(context.Background(), makeRequest("1.22.0"))
		assertStage(t, err, StageDownload)
	})

	t.Run("size mismatch truncated", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		req := makeRequest("1.22.0", func(r *Request) { r.Size = 100 })
		s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return okResponse([]byte("short")), nil
		}}
		_, err := s.Install(context.Background(), req)
		assertStage(t, err, StageDownload)
	})

	t.Run("oversized declared rejected before download", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		called := false
		s.doer = &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			called = true
			return okResponse([]byte("x")), nil
		}}
		req := makeRequest("1.22.0", func(r *Request) { r.Size = MaxDownloadSize + 1 })
		_, err := s.Install(context.Background(), req)
		assertStage(t, err, StageValidate)
		if called {
			t.Fatalf("download must not be attempted for an oversized request")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		req := makeRequest("1.22.0", func(r *Request) {
			r.SHA256 = strings.Repeat("0", 64) // valid format, wrong digest
		})
		_, err := s.Install(context.Background(), req)
		assertStage(t, err, StageIntegrity)
	})

	t.Run("checksum missing produces warning", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		res, err := s.Install(context.Background(), makeRequest("1.22.0"))
		must(t, err)
		found := false
		for _, w := range res.Warnings {
			if w.Kind == WarningIntegrityUnavailable {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected integrity-unavailable warning, got %v", res.Warnings)
		}
	})

	t.Run("uppercase checksum accepted", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		body := []byte("archive-content")
		sum := sha256.Sum256(body)
		req := makeRequest("1.22.0", func(r *Request) {
			r.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
		})

		if _, err := s.Install(context.Background(), req); err != nil {
			t.Fatalf("uppercase checksum should be accepted: %v", err)
		}
	})
}

// Success

func TestInstall_Success_EmptyDestination(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	res, err := s.Install(context.Background(), makeRequest("1.22.0"))
	must(t, err)

	wantPath := filepath.Join(versionsDirFor(tmp), "go1.22.0")
	if res.Path != wantPath {
		t.Fatalf("path: got %q want %q", res.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "bin", binaryName())); err != nil {
		t.Fatalf("extracted binary missing after install: %v", err)
	}
	// No staging or part leftovers may remain.
	assertNoStagingLeftovers(t, versionsDirFor(tmp), filepath.Join(tmp, ".govm", "downloads"))
}

func assertNoStagingLeftovers(t *testing.T, versionsDir, downloadsDir string) {
	t.Helper()
	if entries, err := os.ReadDir(versionsDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), stagingPrefix) {
				t.Fatalf("staging leftover %s", e.Name())
			}
		}
	}
	if entries, err := os.ReadDir(downloadsDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), partPrefix) {
				t.Fatalf("download leftover %s", e.Name())
			}
		}
	}
}

// Transactional commit

func TestInstall_ReplacementKeepsOldUntilVerified(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	finalDir := filepath.Join(versionsDirFor(tmp), "go1.22.0")
	seedInstall(t, finalDir)

	s.verify = func(_ context.Context, binary string) ([]byte, error) {
		// At verification time the old install must still be in place
		// and the new binary must already exist in staging.
		if _, err := os.Stat(filepath.Join(finalDir, "OLD_MARKER")); err != nil {
			t.Errorf("old install replaced before verification: %v", err)
		}
		if _, err := os.Stat(binary); err != nil {
			t.Errorf("new staging binary missing at verification: %v", err)
		}
		return versionOutput("1.22.0"), nil
	}

	res, err := s.Install(context.Background(), makeRequest("1.22.0"))
	must(t, err)

	if _, err := os.Stat(filepath.Join(finalDir, "OLD_MARKER")); !os.IsNotExist(err) {
		t.Fatalf("old marker should be gone after commit, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "bin", binaryName())); err != nil {
		t.Fatalf("new binary missing after commit: %v", err)
	}
}

func TestInstall_VerifyMismatch(t *testing.T) {
	t.Run("version mismatch", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		s.verify = func(context.Context, string) ([]byte, error) { return versionOutput("1.99.9"), nil }
		_, err := s.Install(context.Background(), makeRequest("1.22.0"))
		assertStage(t, err, StageVerify)
	})

	t.Run("platform mismatch", func(t *testing.T) {
		s, _ := newTestService(t, "1.22.0")
		wrongOS := "linux"
		if runtime.GOOS == "linux" {
			wrongOS = "darwin"
		}
		s.verify = func(context.Context, string) ([]byte, error) {
			return []byte(fmt.Sprintf("go version go1.22.0 %s/noarch\n", wrongOS)), nil
		}
		_, err := s.Install(context.Background(), makeRequest("1.22.0"))
		assertStage(t, err, StageVerify)
	})
}

func TestDefaultCommandRunner_DisablesToolchainAutoSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper requires a POSIX shell")
	}

	t.Setenv("GOTOOLCHAIN", "auto")
	binary := filepath.Join(t.TempDir(), "go")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$GOTOOLCHAIN" = "local" ]; then
	printf 'go version go1.26.0 %s/%s\n'
else
	printf 'go version go1.26.1 %s/%s\n'
fi
`, runtime.GOOS, runtime.GOARCH, runtime.GOOS, runtime.GOARCH)
	must(t, os.WriteFile(binary, []byte(script), 0o700))

	out, err := defaultCommandRunner(context.Background(), binary)
	must(t, err)
	if err := verifyVersionOutput(out, "1.26.0"); err != nil {
		t.Fatal(err)
	}
}

func TestInstall_SecondRenameFailureRollsBack(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	versionsDir := versionsDirFor(tmp)
	finalDir := filepath.Join(versionsDir, "go1.22.0")
	seedInstall(t, finalDir)

	calls := 0
	s.rename = func(old, new string) error {
		calls++
		if calls == 2 { //nolint:gocritic // intentional failure injection
			return errors.New("commit rename boom")
		}
		return os.Rename(old, new)
	}

	_, err := s.Install(context.Background(), makeRequest("1.22.0"))
	assertStage(t, err, StageCommit)

	// The old install must be restored exactly.
	if _, err := os.Stat(filepath.Join(finalDir, "OLD_MARKER")); err != nil {
		t.Fatalf("old install not restored after rollback: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(finalDir, "bin", binaryName())); err != nil ||
		string(data) != "old-binary" {
		t.Fatalf("old binary not restored: data=%q err=%v", string(data), err)
	}
	// No backup or recovery leftovers may remain.
	entries, _ := os.ReadDir(versionsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupPrefix) || strings.HasPrefix(e.Name(), recoveryPrefix) ||
			strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Fatalf("unexpected leftover %s", e.Name())
		}
	}
}

func TestInstall_RollbackFailurePreservesRecovery(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	versionsDir := versionsDirFor(tmp)
	finalDir := filepath.Join(versionsDir, "go1.22.0")
	seedInstall(t, finalDir)

	calls := 0
	s.rename = func(old, new string) error {
		calls++
		if calls == 2 || calls == 3 { // commit + rollback both fail //nolint:gocritic
			return errors.New("rename boom")
		}
		return os.Rename(old, new)
	}

	_, err := s.Install(context.Background(), makeRequest("1.22.0"))
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *install.Error, got %T: %v", err, err)
	}
	if se.Stage != StageCommit {
		t.Fatalf("expected StageCommit, got %s", se.Stage)
	}
	if se.RecoveryPath == "" {
		t.Fatalf("expected non-empty RecoveryPath, got %v", err)
	}
	if !strings.HasPrefix(filepath.Base(se.RecoveryPath), recoveryPrefix) {
		t.Fatalf("recovery path should use %s prefix, got %q", recoveryPrefix, se.RecoveryPath)
	}
	if _, err := os.Stat(se.RecoveryPath); err != nil {
		t.Fatalf("recovery backup not preserved at %s: %v", se.RecoveryPath, err)
	}
	if _, err := os.Stat(filepath.Join(se.RecoveryPath, "OLD_MARKER")); err != nil {
		t.Fatalf("old marker missing in recovery: %v", err)
	}
	// The final dir was moved aside and must no longer exist.
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("final dir should not exist, got %v", err)
	}
	// No stray backups; only the recovery tree and staging cleanup remain.
	entries, _ := os.ReadDir(versionsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupPrefix) {
			t.Fatalf("stray backup left behind: %s", e.Name())
		}
	}
}

func TestInstall_CleanupWarningAfterCommit(t *testing.T) {
	s, _ := newTestService(t, "1.22.0")
	s.cleanup = func(string) error { return errors.New("cleanup boom") }

	res, err := s.Install(context.Background(), makeRequest("1.22.0"))
	must(t, err)

	found := false
	for _, w := range res.Warnings {
		if w.Kind == WarningCleanup {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected cleanup warning, got %v", res.Warnings)
	}
}

func TestInstall_PostcommitCleanupFailurePreservesMarker(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	cleanupErr := errors.New("cleanup boom")
	s.cleanup = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), partPrefix) {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}

	res, err := s.Install(context.Background(), makeRequest("1.22.0"))
	must(t, err)
	if len(res.Warnings) == 0 {
		t.Fatal("Install() warnings are empty")
	}
	marker, present, err := state.NewMarkerStore(filepath.Join(tmp, ".govm")).Read()
	if err != nil || !present {
		t.Fatalf("preserved marker = %#v, present %t, error %v", marker, present, err)
	}
	if marker.Operation != state.OperationInstall || marker.Phase != "committing" {
		t.Fatalf("preserved marker = %#v", marker)
	}
}

func TestInstallRecoveryRejectsMarkerPathRoleMismatch(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	root := filepath.Join(tmp, ".govm")
	must(t, os.MkdirAll(root, 0o700))
	marker := state.Marker{
		SchemaVersion: state.SchemaVersion,
		Operation:     state.OperationInstall,
		Phase:         "prepared",
		Version:       "1.22.0",
		Artifacts: map[string]string{
			"staging":  "go1.22.0",
			"download": ".govm-install-test.part",
			"target":   ".install-victim",
		},
	}
	victim := filepath.Join(versionsDirFor(tmp), ".install-victim")
	must(t, os.MkdirAll(victim, 0o700))

	if _, err := s.RecoveryHandler().Recover(t.Context(), marker); err == nil {
		t.Fatal("Recover() error = nil for role-mismatched marker")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim changed by invalid recovery marker: %v", err)
	}
}

func TestInstallRejectsSymlinkVersionsDirectory(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	root := filepath.Join(tmp, ".govm")
	must(t, os.MkdirAll(root, 0o700))
	outside := t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(root, "versions")))

	_, err := s.Install(t.Context(), makeRequest("1.22.0"))
	assertStage(t, err, StagePrepare)
}

func TestInstallRejectsSymlinkPreparedBinaryBeforeCommit(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	outside := filepath.Join(t.TempDir(), "go")
	must(t, os.WriteFile(outside, []byte("outside"), 0o700))
	s.extract = func(_ context.Context, _ string, destination string) error {
		binDir := filepath.Join(destination, "go", "bin")
		if err := os.MkdirAll(binDir, 0o700); err != nil {
			return err
		}
		return os.Symlink(outside, filepath.Join(binDir, binaryName()))
	}
	s.verify = func(context.Context, string) ([]byte, error) {
		return versionOutput("1.22.0"), nil
	}

	_, err := s.Install(t.Context(), makeRequest("1.22.0"))
	assertStage(t, err, StageCommit)
	if _, statErr := os.Stat(filepath.Join(versionsDirFor(tmp), "go1.22.0")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsafe toolchain committed: %v", statErr)
	}
}

// Orphan cleanup

func TestInstall_OrphanCleanupPreservesRecovery(t *testing.T) {
	s, tmp := newTestService(t, "1.22.0")
	versionsDir := versionsDirFor(tmp)
	downloadsDir := filepath.Join(tmp, ".govm", "downloads")

	// Pre-place recognisable orphans from a hypothetical crashed run.
	must(t, os.MkdirAll(filepath.Join(versionsDir, ".install-old", "go"), 0o700))
	must(t, os.WriteFile(filepath.Join(versionsDir, ".install-old", "go", "x"), []byte("x"), 0o600))
	must(t, os.MkdirAll(filepath.Join(versionsDir, ".recovery-old", "bin"), 0o700))
	must(t, os.WriteFile(filepath.Join(versionsDir, ".recovery-old", "bin", binaryName()), []byte("rec"), 0o700))
	must(t, os.MkdirAll(filepath.Join(versionsDir, ".govm-backup-old"), 0o700))
	must(t, os.WriteFile(filepath.Join(versionsDir, ".govm-backup-old", "OLD_MARKER"), []byte("old"), 0o600))
	must(t, os.MkdirAll(downloadsDir, 0o700))
	must(t, os.WriteFile(filepath.Join(downloadsDir, ".govm-install-old.part"), []byte("partial"), 0o600))

	res, err := s.Install(context.Background(), makeRequest("1.22.0"))
	must(t, err)

	if _, err := os.Stat(filepath.Join(versionsDir, ".install-old")); !os.IsNotExist(err) {
		t.Fatalf("staging orphan should have been removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(downloadsDir, ".govm-install-old.part")); !os.IsNotExist(err) {
		t.Fatalf("download orphan should have been removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(versionsDir, ".recovery-old")); err != nil {
		t.Fatalf("recovery backup must be preserved, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(versionsDir, ".govm-backup-old")); !os.IsNotExist(err) {
		t.Fatalf("interrupted commit backup should be moved to recovery, got %v", err)
	}
	foundRecovery := false
	entries, err := os.ReadDir(versionsDir)
	must(t, err)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), recoveryPrefix) || entry.Name() == ".recovery-old" {
			continue
		}
		if _, err := os.Stat(filepath.Join(versionsDir, entry.Name(), "OLD_MARKER")); err == nil {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatal("interrupted commit backup was not preserved under a recovery path")
	}
	foundWarning := false
	for _, warning := range res.Warnings {
		if warning.Kind == WarningCleanup && strings.Contains(warning.Error(), "interrupted installation backup preserved") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected preserved-backup warning, got %v", res.Warnings)
	}
}
