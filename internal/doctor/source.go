package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SmileOniks/govm/internal/adapter/godev"
	"github.com/SmileOniks/govm/internal/config"
)

// defaultSourceTimeout bounds the release-catalog probe so an
// unreachable mirror cannot stall the whole report.
const defaultSourceTimeout = 5 * time.Second

// resolveSource returns the distribution source the doctor probes:
// the injected override, else the (normalised) settings.json value,
// else the go.dev default. settings.json problems are reported by
// checkSettings, so this never fails.
func resolveSource(deps Deps, settingsFile string) string {
	if deps.Source != "" {
		return deps.Source
	}
	settings := config.DefaultSettings()
	if data, err := deps.FS.ReadFile(settingsFile); err == nil {
		// Invalid JSON leaves the defaults in place.
		_ = json.Unmarshal(data, &settings)
	}
	return config.Normalize(settings).DistributionSource
}

func checkSource(ctx context.Context, e *env) Check {
	const name = CheckSource
	if e.deps.Offline {
		return Check{
			Name:    name,
			Verdict: VerdictOK,
			Detail:  fmt.Sprintf("source %s skipped (--offline)", e.source),
		}
	}
	warn := func(reason string) Check {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("source %s unreachable: %s", e.source, reason),
			Hint:    "check network or run with --offline",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, e.deps.SourceTimeout)
	defer cancel()
	releases, err := godev.NewClientForSource(e.deps.HTTPClient, e.source).FetchReleases(ctx)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return warn(fmt.Sprintf("timeout after %v", e.deps.SourceTimeout))
	case err != nil:
		return warn(err.Error())
	case len(releases) == 0:
		return warn("empty release list")
	}
	return Check{
		Name:    name,
		Verdict: VerdictOK,
		Detail:  fmt.Sprintf("source %s reachable (%d releases)", e.source, len(releases)),
	}
}
