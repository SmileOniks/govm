package model

import (
	"charm.land/bubbles/v2/table"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/SmileOniks/govm/internal/prune"
)

// Checkboxes distinguish the marked update set from the cursor highlight.
// Whether a marked module actually moves is decided by the update plan.
const (
	markFilled = "[●] "
	markEmpty  = "[○] "
	markWidth  = 3
)

// updateDependencyTable re-renders the table from the dependency list
// and the display setting. It is a pure projection: it never changes
// the list or the Marks.
func (s *depsTab) updateDependencyTable() {
	rows := make([]table.Row, 0, len(s.dependencies))
	paths := make([]string, 0, len(s.dependencies))
	for _, d := range s.dependencies {
		if s.display == config.DepsDisplayDirect && d.Indirect {
			continue
		}
		rows = append(rows, table.Row{markPrefix(*s, d) + d.Path, d.Version, d.Latest, dependencyStatus(d)})
		paths = append(paths, d.Path)
	}
	s.table.SetRows(rows)
	s.rowPaths = paths
}

func markPrefix(state depsTab, d deps.ModuleDependency) string {
	if state.marked(d.Path) {
		return markFilled
	}
	return markEmpty
}

// dependencyStatus returns a short status string for a module dependency
// describing its update state. Priority order is intentional:
// error > deprecated > indirect update > update avail > indirect > current.
func dependencyStatus(d deps.ModuleDependency) string {
	switch {
	case d.Error != "":
		return "error"
	case d.Deprecated != "":
		return "deprecated"
	case d.Indirect && d.Latest != "" && d.Latest != d.Version:
		return "indirect update"
	case d.Latest != "" && d.Latest != d.Version:
		return "update avail"
	case d.Indirect:
		return "indirect"
	default:
		return "current"
	}
}

func installedTableColumns(width int) []table.Column {
	versionWidth, sizeWidth, statusWidth, minPathWidth := 10, 10, 10, 18

	pathWidth := width - versionWidth - sizeWidth - statusWidth - 8
	if pathWidth < minPathWidth {
		pathWidth = minPathWidth
	}

	return []table.Column{
		{Title: "Version", Width: versionWidth},
		{Title: "Path", Width: pathWidth},
		{Title: "Size", Width: sizeWidth},
		{Title: "Status", Width: statusWidth},
	}
}

func formatDiskUsage(bytes int64) string {
	return prune.FormatBytes(bytes)
}

func dependencyTableColumns(width int) []table.Column {
	pathWidth, versionWidth, latestWidth, statusWidth, minPathWidth := 0, 9, 9, 10, 10

	used := versionWidth + latestWidth + statusWidth + 12
	pathWidth = width - used
	if pathWidth < minPathWidth {
		pathWidth = minPathWidth
	}

	return []table.Column{
		{Title: "Dependency", Width: pathWidth},
		{Title: "Current", Width: versionWidth},
		{Title: "Latest", Width: latestWidth},
		{Title: "Status", Width: statusWidth},
	}
}
