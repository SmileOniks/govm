package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/paths"
	"github.com/SmileOniks/govm/internal/prune"
	"github.com/SmileOniks/govm/internal/state"
)

// diskUsage builds the production DiskUsage seam over resolver. The
// prune service is constructed lazily and only its read-only
// DiskUsage is called: no state lock, no recovery, no writes.
func diskUsage(resolver *paths.Resolver) func(context.Context) (prune.Summary, error) {
	return func(ctx context.Context) (prune.Summary, error) {
		coordinator := state.NewCoordinator(resolver)
		lifecycleSvc, err := lifecycle.New(resolver, coordinator)
		if err != nil {
			return prune.Summary{}, fmt.Errorf("initialize lifecycle service: %w", err)
		}
		pruneSvc, err := prune.New(resolver, coordinator, lifecycleSvc)
		if err != nil {
			return prune.Summary{}, fmt.Errorf("initialize prune service: %w", err)
		}
		return pruneSvc.DiskUsage(ctx)
	}
}

func checkDisk(ctx context.Context, e *env) Check {
	const name = CheckDisk
	if e.layoutErr != nil {
		return layoutFailure(name, e.layoutErr)
	}
	summary, err := e.deps.DiskUsage(ctx)
	if err != nil {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  fmt.Sprintf("disk usage unavailable: %v", err),
			Hint:    fmt.Sprintf("check permissions on %s", e.root),
		}
	}
	// Every valid toolchain other than the active one is inactive;
	// with no valid active version that is all of them.
	inactive := 0
	for v := range summary.VersionBytes {
		if v != e.active {
			inactive++
		}
	}
	versions := fmt.Sprintf("%d versions", len(summary.VersionBytes))
	if len(summary.VersionBytes) == 1 {
		versions = "1 version"
	}
	size := prune.FormatBytes(summary.InstalledBytes)
	if inactive > 0 {
		size += fmt.Sprintf(", %d inactive", inactive)
	}
	detail := fmt.Sprintf("disk: %s (%s), downloads %s", versions, size, prune.FormatBytes(summary.DownloadBytes))
	if len(summary.Warnings) > 0 {
		parts := make([]string, 0, len(summary.Warnings))
		for _, w := range summary.Warnings {
			parts = append(parts, w.Error())
		}
		detail += "; warnings: " + strings.Join(parts, "; ")
	}
	if inactive > 0 || summary.DownloadBytes > 0 {
		return Check{
			Name:    name,
			Verdict: VerdictWarn,
			Detail:  detail,
			Hint:    "run `govm prune` to remove inactive versions and downloads",
		}
	}
	return Check{Name: name, Verdict: VerdictOK, Detail: detail}
}
