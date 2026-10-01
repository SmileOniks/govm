package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

var errBoom = errors.New("boom")

func TestBuildInstallRequestMapsAllMetadata(t *testing.T) {
	v := utils.GoVersion{
		Version:  "1.22.3",
		Filename: "go1.22.3.darwin-arm64.tar.gz",
		URL:      "https://go.dev/dl/go1.22.3.darwin-arm64.tar.gz",
		SHA256:   "abc123deadbeef",
		Size:     71234567,
	}
	req := buildInstallRequest(v)

	if req.Version != v.Version {
		t.Errorf("Version = %q, want %q", req.Version, v.Version)
	}
	if req.Filename != v.Filename {
		t.Errorf("Filename = %q, want %q", req.Filename, v.Filename)
	}
	if req.URL != v.URL {
		t.Errorf("URL = %q, want %q", req.URL, v.URL)
	}
	if req.SHA256 != v.SHA256 {
		t.Errorf("SHA256 = %q, want %q", req.SHA256, v.SHA256)
	}
	if req.Size != v.Size {
		t.Errorf("Size = %d, want %d", req.Size, v.Size)
	}
}

// newTestAdapter builds an adapter whose install function and tick
// channel are fully controllable, so tests never touch the network, the
// disk, or real time. The returned tick channel is unbuffered so every
// sent tick is guaranteed to have been consumed (and rendered) before
// the next send, making the spinner assertions deterministic.
func newTestAdapter(version string, fn installFunc) (*installAdapter, *bytes.Buffer, chan time.Time) {
	var buf bytes.Buffer
	tick := make(chan time.Time)
	a := &installAdapter{
		version: version,
		request: install.Request{Version: version},
		install: fn,
		out:     &buf,
		tick:    tick,
	}
	return a, &buf, tick
}

func TestInstallAdapterSuccess(t *testing.T) {
	a, buf, _ := newTestAdapter("1.22.3", func(ctx context.Context, req install.Request) (install.Result, error) {
		return install.Result{Version: req.Version, Path: "/tmp/go1.22.3"}, nil
	})

	a.run(context.Background())

	out := buf.String()
	if !strings.Contains(out, "✅ Successfully installed Go 1.22.3") {
		t.Errorf("success message missing in %q", out)
	}
	if !strings.Contains(out, "👉 To activate this version, run: govm use 1.22.3") {
		t.Errorf("activation instruction missing in %q", out)
	}
	if strings.Contains(out, "⚠️") {
		t.Errorf("unexpected warning line in %q", out)
	}
}

func TestInstallAdapterWarnings(t *testing.T) {
	a, buf, _ := newTestAdapter("1.21.0", func(ctx context.Context, req install.Request) (install.Result, error) {
		return install.Result{
			Version: req.Version,
			Warnings: []install.Warning{
				{Kind: install.WarningCleanup, Err: nil},
				{Kind: install.WarningIntegrityUnavailable},
			},
		}, nil
	})

	a.run(context.Background())

	out := buf.String()
	if !strings.Contains(out, "✅ Successfully installed Go 1.21.0") {
		t.Errorf("success message missing in %q", out)
	}
	if got := strings.Count(out, "⚠️"); got != 2 {
		t.Errorf("want 2 warning lines, got %d in %q", got, out)
	}
	if !strings.Contains(out, "cleanup incomplete") {
		t.Errorf("cleanup warning text missing in %q", out)
	}
	if !strings.Contains(out, "archive checksum unavailable") {
		t.Errorf("integrity warning text missing in %q", out)
	}
}

func TestInstallAdapterTypedErrorWithRecoveryPath(t *testing.T) {
	wantErr := &install.Error{
		Stage:        install.StageExtract,
		Err:          errBoom,
		RecoveryPath: "/tmp/recovery/go1.22.3.bak",
	}
	a, buf, _ := newTestAdapter("1.22.3", func(ctx context.Context, req install.Request) (install.Result, error) {
		return install.Result{}, wantErr
	})

	a.run(context.Background())

	out := buf.String()
	if !strings.Contains(out, "❌ Installation failed") {
		t.Errorf("failure banner missing in %q", out)
	}
	if !strings.Contains(out, "extraction") { // install.StageExtract.String()
		t.Errorf("phase-aware stage missing in %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("underlying error text missing in %q", out)
	}
	if !strings.Contains(out, wantErr.RecoveryPath) {
		t.Errorf("recovery path missing in %q", out)
	}
}

func TestInstallAdapterSpinnerEmitsWhileBlocked(t *testing.T) {
	release := make(chan struct{})
	a, buf, tick := newTestAdapter("1.20.5", func(ctx context.Context, req install.Request) (install.Result, error) {
		<-release
		return install.Result{Version: req.Version}, nil
	})

	done := make(chan struct{})
	go func() { a.run(context.Background()); close(done) }()

	// While the worker is blocked, drive several ticks. Because tick is
	// unbuffered, each send completes only once run has received and
	// rendered it, so all frames are emitted before release.
	const frames = 5
	for i := 0; i < frames; i++ {
		tick <- time.Now()
	}
	close(release)
	<-done

	out := buf.String()
	if got := strings.Count(out, "Installing Go 1.20.5"); got < frames {
		t.Errorf("want >= %d spinner frames while blocked, got %d in %q", frames, got, out)
	}
	if !strings.Contains(out, "✅ Successfully installed Go 1.20.5") {
		t.Errorf("success message after release missing in %q", out)
	}
}

func TestInstallAdapterNilInstaller(t *testing.T) {
	a, buf, _ := newTestAdapter("1.22.3", nil)

	a.run(context.Background())

	if out := buf.String(); !strings.Contains(out, "no installer configured") {
		t.Fatalf("missing nil installer error in %q", out)
	}
}

func TestInstallAdapterWaitsForCancellationResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, buf, _ := newTestAdapter("1.22.3", func(ctx context.Context, _ install.Request) (install.Result, error) {
		<-ctx.Done()
		return install.Result{}, ctx.Err()
	})

	a.run(ctx)

	out := buf.String()
	if !strings.Contains(out, "Installation failed") || !strings.Contains(out, context.Canceled.Error()) {
		t.Fatalf("missing cancellation result in %q", out)
	}
}
