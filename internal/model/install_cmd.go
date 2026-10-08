package model

// This file owns the Bubbletea adapter for the install core.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/utils"
)

// installTimeout is the maximum wall-clock budget granted to a single
// install run. A slow network or a large archive should not hang the
// TUI indefinitely.
const installTimeout = 30 * time.Minute

// installFunc is the contract between the TUI and the install core.
// It is satisfied by a bound install.Service method
// ((*Service).Install), and by fakes injected in tests.
type installFunc func(context.Context, install.Request) (install.Result, error)

type installProgressFunc func(
	context.Context,
	install.Request,
	install.ProgressReporter,
) (install.Result, error)

func buildInstallRequest(v utils.GoVersion) install.Request {
	return install.Request{
		Version:  v.Version,
		Filename: v.Filename,
		URL:      v.URL,
		SHA256:   v.SHA256,
		Size:     v.Size,
	}
}

// installSuccessMsg carries the operation ID and whole install.Result for a
// completed installation, including any non-fatal warnings the core surfaced.
type installSuccessMsg struct {
	OperationID uint64
	Version     string
	Path        string
	Warnings    []install.Warning
}

// installFailureMsg carries the requested version and the typed error
// that terminated the installation (typically an *install.Error whose
// Error() reports the failing stage).
type installFailureMsg struct {
	OperationID uint64
	Version     string
	Err         error
}

// installSuccessStatus renders the status text and kind for a completed
// installation. With no warnings it preserves the historical success
// text ("Successfully installed Go <version>"); with warnings it
// surfaces them as a single global warning status so the user is
// alerted that the toolchain installed but something needs attention
// (e.g. an unavailable checksum or a cleanup failure). The direct
// completion path and the reconciliation path both use this helper so
// the wording cannot drift between them.
func installSuccessStatus(version string, warnings []install.Warning) (text, kind string) {
	if len(warnings) == 0 {
		return fmt.Sprintf("Successfully installed Go %s", version), "success"
	}
	return fmt.Sprintf("Installed Go %s with warnings: %s", version, joinInstallWarnings(warnings)), "warning"
}

// joinInstallWarnings renders a warning slice into a single semicolon
// separated string for status display.
func joinInstallWarnings(warnings []install.Warning) string {
	parts := make([]string, len(warnings))
	for i, w := range warnings {
		parts[i] = w.Error()
	}
	return strings.Join(parts, "; ")
}
