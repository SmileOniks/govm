package utils

import (
	"fmt"
	"runtime/debug"
	"time"
)

// This file reports the version of the govm binary itself, distinct
// from the versions of Go that govm manages (see go_versions.go).

var Version = "dev"

// Repository is the GitHub "owner/name" whose releases page publishes
// govm itself. The TUI's Upgrade notice compares the running version
// against the Latest release of this repository.
const Repository = "SmileOniks/govm"

func GetVersion() string {
	// Version injected at release time via ldflags (GoReleaser).
	if Version != "dev" {
		return Version
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	// go install records the module version in build info.
	if bi.Main.Version != "(devel)" && bi.Main.Version != "" {
		return bi.Main.Version
	}

	// Fall back to VCS metadata from a source checkout.
	var vcsRevision string
	var vcsTime time.Time

	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			vcsRevision = setting.Value
		case "vcs.time":
			vcsTime, _ = time.Parse(time.RFC3339, setting.Value)
		case "vcs.tag":
			if setting.Value != "" {
				return setting.Value
			}
		}
	}

	if vcsRevision != "" {
		return fmt.Sprintf("%s (%s)", vcsRevision[:8], vcsTime.Format("2006-01-02"))
	}

	return "dev"
}
