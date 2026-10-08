package model

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"github.com/SmileOniks/govm/internal/loader"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

// versionCatalogErrorKind classifies a versionCatalogError into one of
// the three categories callers branch on: identity/lookup miss, bad
// input, or an illegal state transition.
type versionCatalogErrorKind int

const (
	errKindNotFound versionCatalogErrorKind = iota
	errKindInvalid
	errKindForbidden
)

// versionCatalogError is the private error type for every catalog
// mutation. errors.Is matches by kind, so callers test a category
// without coupling to the message text.
type versionCatalogError struct {
	kind    versionCatalogErrorKind
	version string
	reason  string
}

func (e versionCatalogError) Error() string {
	if e.version == "" {
		return e.reason
	}
	return fmt.Sprintf("version %q: %s", e.version, e.reason)
}

func (e versionCatalogError) Is(target error) bool {
	other, ok := target.(versionCatalogError)
	if !ok {
		return false
	}
	return e.kind == other.kind
}

// Sentinel errors for the three categories. errors.Is(returnedErr, X)
// holds whenever the returned error shares X's kind.
var (
	errCatalogNotFound  = versionCatalogError{kind: errKindNotFound, reason: "version not found"}
	errCatalogInvalid   = versionCatalogError{kind: errKindInvalid, reason: "invalid catalog input"}
	errCatalogForbidden = versionCatalogError{kind: errKindForbidden, reason: "forbidden state transition"}
)

func catalogError(kind versionCatalogErrorKind, version, format string, args ...any) versionCatalogError {
	return versionCatalogError{
		kind:    kind,
		version: version,
		reason:  fmt.Sprintf(format, args...),
	}
}

// versionProjection is the eager, render-ready snapshot of the two
// derived views of a catalog: the Available list items and the
// Installed table rows. Both are pre-rendered at commit time so the
// per-frame TUI hot path performs no lipgloss work.
type versionProjection struct {
	available []list.Item
	installed []table.Row
}

// versionCatalog is the private, copy-on-write core that owns the
// canonical Go version list, its identity index, and the eager
// render-ready projections.
//
// Every mutation builds a brand-new slice, index and projection set and
// swaps them in atomically: on failure the prior state is left entirely
// untouched, and any previously copied catalog value keeps observing
// its original (never mutated) storage. Storage is never edited in
// place, which is what makes the copy-on-write guarantee hold.
type versionCatalog struct {
	versions  []utils.GoVersion
	index     map[string]int
	theme     styles.Theme
	available []list.Item
	installed []table.Row
}

func newVersionCatalog(theme styles.Theme) versionCatalog {
	return versionCatalog{
		versions:  []utils.GoVersion{},
		index:     map[string]int{},
		theme:     theme,
		available: []list.Item{},
		installed: []table.Row{},
	}
}

// replace swaps the whole catalog. The input is defensively cloned, so
// later mutation of the caller's slice cannot leak into the catalog.
// Empty input is valid and clears the catalog. Empty version ids and
// duplicate ids are rejected as invalid input; any state that violates
// the catalog invariants is rejected. An identical input is a no-op and
// reports changed=false. Any rejection leaves the prior state intact.
func (c *versionCatalog) replace(vs []utils.GoVersion) (bool, error) {
	next := make([]utils.GoVersion, len(vs))
	copy(next, vs)

	if err := loader.ValidateCatalogVersions(next); err != nil {
		return false, catalogError(errKindInvalid, "", "%v", err)
	}
	if versionsEqual(c.versions, next) {
		return false, nil
	}
	c.commit(next)
	return true, nil
}

// markInstalled marks a version as installed at path. An empty path is
// invalid input. Re-marking the same version at the same path is a
// no-op (changed=false); a different non-empty path updates it.
func (c *versionCatalog) markInstalled(version, path string) (bool, error) {
	idx, ok := c.index[version]
	if !ok {
		return false, catalogError(errKindNotFound, version, "not found")
	}
	if path == "" {
		return false, catalogError(errKindInvalid, version, "markInstalled requires non-empty path")
	}
	cur := c.versions[idx]
	if cur.Installed && cur.Path == path {
		return false, nil
	}
	next := cloneVersions(c.versions)
	next[idx].Installed = true
	next[idx].Path = path
	if err := validateInvariants(next); err != nil {
		return false, err
	}
	c.commit(next)
	return true, nil
}

// activate makes version the single active version. Activating a
// version that is not installed is a forbidden transition; activating
// the already-active version is a no-op. Any prior active version is
// cleared so at most one active version ever exists.
func (c *versionCatalog) activate(version string) (bool, error) {
	idx, ok := c.index[version]
	if !ok {
		return false, catalogError(errKindNotFound, version, "not found")
	}
	if !c.versions[idx].Installed {
		return false, catalogError(errKindForbidden, version, "activation requires an installed version")
	}
	if c.versions[idx].Active {
		return false, nil
	}
	next := cloneVersions(c.versions)
	for i := range next {
		next[i].Active = false
	}
	next[idx].Active = true
	if err := validateInvariants(next); err != nil {
		return false, err
	}
	c.commit(next)
	return true, nil
}

// markDeleted clears the installed state and path of a version. Deleting
// an active version is a forbidden transition; deleting an already
// uninstalled version is a no-op. The version remains in the catalog.
func (c *versionCatalog) markDeleted(version string) (bool, error) {
	idx, ok := c.index[version]
	if !ok {
		return false, catalogError(errKindNotFound, version, "not found")
	}
	if c.versions[idx].Active {
		return false, catalogError(errKindForbidden, version, "cannot delete an active version")
	}
	if !c.versions[idx].Installed {
		return false, nil
	}
	next := cloneVersions(c.versions)
	next[idx].Installed = false
	next[idx].Path = ""
	if err := validateInvariants(next); err != nil {
		return false, err
	}
	c.commit(next)
	return true, nil
}

func (c *versionCatalog) setDiskUsage(sizes map[string]int64) (bool, error) {
	if sizes == nil {
		return false, nil
	}
	next := cloneVersions(c.versions)
	changed := false
	for i := range next {
		if !next[i].Installed {
			continue
		}
		size, ok := sizes[next[i].Version]
		if !ok || next[i].DiskUsage == size {
			continue
		}
		next[i].DiskUsage = size
		changed = true
	}
	if !changed {
		return false, nil
	}
	c.commit(next)
	return true, nil
}

// lookup returns the stored record for version. The bool is false when
// the version is absent.
func (c *versionCatalog) lookup(version string) (utils.GoVersion, bool) {
	idx, ok := c.index[version]
	if !ok {
		return utils.GoVersion{}, false
	}
	return c.versions[idx], true
}

// setTheme adopts a new theme and rebuilds the cached Available item
// titles (the only projection that depends on styling). It always
// reports a rebuild.
func (c *versionCatalog) setTheme(theme styles.Theme) bool {
	c.theme = theme
	c.available = buildAvailable(theme, c.versions)
	return true
}

// projection returns defensive clones of the cached render-ready views.
// The Available slice is cloned at the outer level (items are immutable
// values); the Installed slice is cloned at the outer level and each
// row is cloned too, since table.Row is a mutable []string.
func (c *versionCatalog) projection() versionProjection {
	available := make([]list.Item, len(c.available))
	copy(available, c.available)

	installed := make([]table.Row, len(c.installed))
	for i, row := range c.installed {
		clone := make(table.Row, len(row))
		copy(clone, row)
		installed[i] = clone
	}
	return versionProjection{available: available, installed: installed}
}

// commit is the single atomic write path. It rebuilds the index and
// both projections from the new versions slice using the current theme,
// then swaps everything in. Prior storage is never mutated, so any
// older catalog copy keeps its original data.
func (c *versionCatalog) commit(versions []utils.GoVersion) {
	index := make(map[string]int, len(versions))
	for i, v := range versions {
		index[v.Version] = i
	}
	c.versions = versions
	c.index = index
	c.available = buildAvailable(c.theme, versions)
	c.installed = buildInstalled(versions)
}

func cloneVersions(vs []utils.GoVersion) []utils.GoVersion {
	out := make([]utils.GoVersion, len(vs))
	copy(out, vs)
	return out
}

func versionsEqual(a, b []utils.GoVersion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateInvariants(versions []utils.GoVersion) error {
	if err := loader.ValidateCatalogVersions(versions); err != nil {
		return catalogError(errKindInvalid, "", "%v", err)
	}
	return nil
}

func buildAvailable(theme styles.Theme, vs []utils.GoVersion) []list.Item {
	items := make([]list.Item, len(vs))
	for i, v := range vs {
		items[i] = styles.Item{
			Name:            v.Version,
			DescriptionText: v.DisplayDescription(),
			RenderedTitle:   styles.RenderItemTitle(theme, v.Version, v.Installed, v.Active),
			RenderedBadges:  styles.RenderItemBadges(theme, v.Installed, v.Active),
		}
	}
	return items
}

func buildInstalled(vs []utils.GoVersion) []table.Row {
	rows := make([]table.Row, 0, len(vs))
	for _, v := range vs {
		if !v.Installed {
			continue
		}
		status := ""
		if v.Active {
			status = "active"
		}
		rows = append(rows, table.Row{v.Version, v.Path, formatDiskUsage(v.DiskUsage), status})
	}
	return rows
}
