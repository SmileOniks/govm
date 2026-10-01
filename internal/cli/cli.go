package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

// installFunc mirrors install.(*Service).Install. It is parameterised so
// the CLI adapter can be exercised in tests without performing real
// network or disk work.
type installFunc func(ctx context.Context, req install.Request) (install.Result, error)

// installOutcome carries the service result and error together through a
// single buffered channel, so the adapter never needs separate done/err
// channels.
type installOutcome struct {
	result install.Result
	err    error
}

// installAdapter drives the animated spinner while a transactional
// installFunc runs. It is package-private and fully deterministic when
// constructed with a controllable tick channel and an injected install
// function.
type installAdapter struct {
	version  string
	request  install.Request
	install  installFunc
	out      io.Writer
	tick     <-chan time.Time // nil -> real ticker at spinRate
	spinRate time.Duration
}

func buildInstallRequest(v utils.GoVersion) install.Request {
	return install.Request{
		Version:  v.Version,
		Filename: v.Filename,
		URL:      v.URL,
		SHA256:   v.SHA256,
		Size:     v.Size,
	}
}

// newInstallAdapter wires the adapter for the public CLI path: output to
// stdout and a 100ms spinner cadence.
func newInstallAdapter(v utils.GoVersion, fn installFunc, out io.Writer) *installAdapter {
	return &installAdapter{
		version:  v.Version,
		request:  buildInstallRequest(v),
		install:  fn,
		out:      out,
		spinRate: 100 * time.Millisecond,
	}
}

// installSpinChars is the braille spinner animation shared by the CLI
// and TUI install flows.
var installSpinChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// run animates the spinner until the install func completes, then prints
// the success / warning / failure output to a.out.
//
// The install func runs in its own goroutine carrying ctx, so the service
// observes cancellation. On ctx cancellation the adapter does NOT abandon
// the worker: it disables the done case and keeps waiting for the service
// result (whose error carries the cancellation), which avoids stranding
// the goroutine and a resulting leak.
func (a *installAdapter) run(ctx context.Context) {
	if a.install == nil {
		a.finish(installOutcome{err: errors.New("no installer configured")})
		return
	}

	tick := a.tick
	if tick == nil {
		ticker := time.NewTicker(a.spinRate)
		defer ticker.Stop()
		tick = ticker.C
	}

	outcomeCh := make(chan installOutcome, 1)
	go func() {
		res, err := a.install(ctx, a.request)
		outcomeCh <- installOutcome{result: res, err: err}
	}()

	done := ctx.Done()
	idx := 0
	for {
		select {
		case o := <-outcomeCh:
			a.finish(o)
			return
		case <-tick:
			fmt.Fprintf(a.out, "\r%s Installing Go %s...", installSpinChars[idx], a.version)
			idx = (idx + 1) % len(installSpinChars)
		case <-done:
			// Deadline/cancellation reached. Do not return and strand the
			// worker: nil out done so this case stops firing, and keep
			// waiting for the service result, which reports the
			// cancellation as a phase-aware error.
			done = nil
		}
	}
}

// finish renders the terminal output for a completed install. The error
// path passes the typed install.Error through unchanged, so its
// phase-aware message and RecoveryPath remain visible.
func (a *installAdapter) finish(o installOutcome) {
	if o.err != nil {
		fmt.Fprintf(a.out, "\r❌ Installation failed: %v\n", o.err)
		return
	}
	fmt.Fprintf(a.out, "\r✅ Successfully installed Go %s\n", a.version)
	fmt.Fprintf(a.out, "👉 To activate this version, run: govm use %s\n", a.version)
	for _, w := range o.result.Warnings {
		fmt.Fprintf(a.out, "⚠️  %s\n", w)
	}
}

// findMatchingVersion looks up a Go version available on go.dev.
// It first checks for an exact match, then falls back to the highest
// version that starts with query (with or without a separating dot).
func findMatchingVersion(load loadCatalogFunc, version string) (utils.GoVersion, error) {
	if load == nil {
		return utils.GoVersion{}, errors.New("no loader configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	versions, err := load(ctx)
	if err != nil {
		return utils.GoVersion{}, fmt.Errorf("failed to load version catalog: %w", err)
	}

	query := utils.NormalizeGoVersionQuery(version)
	versionStrings := make([]string, len(versions))
	for i, v := range versions {
		versionStrings[i] = v.Version
	}
	matched, ok := utils.FindLatestGoVersion(versionStrings, query)
	if !ok {
		return utils.GoVersion{}, fmt.Errorf("no version matching '%s' found", version)
	}
	for _, v := range versions {
		if v.Version == matched {
			return v, nil
		}
	}
	return utils.GoVersion{}, fmt.Errorf("no version matching '%s' found", version)
}
