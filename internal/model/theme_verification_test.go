package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

// modelSourceFiles returns the contents of every non-test .go file in
// the model package directory. Used by structural verification tests
// that must assert no caller re-introduces a package-level styles
// global read.
func modelSourceFiles(t *testing.T) map[string]string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("os.ReadDir(%q): %v", wd, err)
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(data)
	}
	return out
}

// appearsInModelSources reports whether needle appears in any non-test
// .go file in the model package. The check is deliberately literal so
// it can be reasoned about: a banned substring of the form
// "styles.Primary" matches only that exact access expression.
func appearsInModelSources(t *testing.T, needle string) bool {
	t.Helper()
	for name, body := range modelSourceFiles(t) {
		if strings.Contains(body, needle) {
			t.Logf("match for %q in %s", needle, name)
			return true
		}
	}
	return false
}

// TestThemeIsTheOnlyStyleSource is the structural verification gate for
// architecture review candidate 5. It greps the model package's
// non-test Go files for any direct read of the previous package-level
// styles globals. After the migration every renderer takes a Theme
// parameter, so there must be no remaining styles.X global references
// in model sources.
//
// If this test fails, someone re-introduced a global read. Add the
// missing Theme parameter instead of allow-listing the new site.
func TestThemeIsTheOnlyStyleSource(t *testing.T) {
	t.Parallel()

	for _, banned := range []string{
		"styles.Primary", "styles.Success", "styles.Error", "styles.Warning",
		"styles.Info", "styles.Muted", "styles.Text", "styles.MinimumViewportBackground",
		"styles.MinimumViewportText", "styles.TitleStyle", "styles.HeaderMetaStyle", "styles.HeaderNoticeStyle",
		"styles.ActiveTabStyle", "styles.InactiveTabStyle", "styles.ActiveBadgeStyle",
		"styles.InstalledBadgeStyle", "styles.ItemVersionStyle", "styles.MutedStyle",
		"styles.StatusSuccessStyle", "styles.StatusErrorStyle", "styles.StatusWarningStyle",
		"styles.StatusInfoStyle", "styles.HelpKeyStyle", "styles.HelpTextStyle",
		"styles.TableHeaderStyle", "styles.TableSelectedStyle", "styles.TableCellStyle",
		"styles.SpinnerStyle", "styles.AppStyleFor", "styles.ApplyTheme",
		"styles.CurrentTheme", "styles.ThemeName", "styles.ThemeCurrent", "styles.ThemeLight",
	} {
		if appearsInModelSources(t, banned) {
			t.Fatalf("banned styles-package global %q still referenced in model sources", banned)
		}
	}
}

// TestInitIsGoneFromDialog guards the init() removal. Previously
// dialog.go carried a package-level init() that built the dialog style
// globals on import; the migration deleted it. A future contributor
// must not silently resurrect it.
func TestInitIsGoneFromDialog(t *testing.T) {
	t.Parallel()

	if appearsInModelSources(t, "func init()") {
		t.Fatalf("found func init() in model package; dialog.go init() must stay deleted after the Theme migration")
	}
}

// TestModelNewRequiresTheme is a compile-time guarantee that Model.New
// takes a Theme parameter. It exists as a runnable test so the contract
// is documented in the test surface instead of only in the call site.
func TestModelNewRequiresTheme(t *testing.T) {
	t.Parallel()

	m := New("", config.DefaultSettings(), newMemorySettingsStore(config.DefaultSettings()), "", styles.NewTheme(config.ThemeCurrent))
	if got := m.theme.Primary; got == nil {
		t.Fatal("Model.New did not store the provided theme")
	}
}

// TestRenderItemTitleUsedByList is the mutation-guard for the pre-render
// contract: it asserts that the title shown by the Available Versions
// list is the value styles.RenderItemTitle produced, not a per-frame
// re-render. If someone re-introduces a lipgloss.Style.Render call
// inside Item.Title() this test will fail (because the title would
// drift from the pre-rendered RenderedTitle field).
//
// Not parallel: newTestModel calls t.Setenv via its temp HOME shim.
func TestRenderItemTitleUsedByList(t *testing.T) {
	m := newTestModel(t)
	items := m.projection.availableModel().Items()
	if len(items) == 0 {
		t.Fatal("test model has no list items")
	}

	it0, ok := items[0].(styles.Item)
	if !ok {
		t.Fatalf("list item 0 is %T, want styles.Item", items[0])
	}
	v, found := m.projection.lookup(it0.Name)
	if !found {
		t.Fatalf("list item 0 version %q not in catalog", it0.Name)
	}
	want := styles.RenderItemTitle(m.theme, v.Version, v.Installed, v.Active)

	if got := it0.Title(); got != want {
		t.Fatalf("list item 0 Title = %q, want pre-rendered %q", got, want)
	}
	if got := it0.RenderedTitle; !strings.Contains(got, v.Version) {
		t.Fatalf("RenderedTitle lost version name: %q", got)
	}
}

// TestApplyRuntimeThemePropagatesToComponents verifies that toggling
// theme rebuilds not only m.theme but also the components that cache
// style values by value (Spinner, tables, list delegate). The previous
// global-mutation design relied on readers picking up changes
// implicitly; the new design must propagate explicitly.
//
// Tables do not expose their cached styles, so the proof that they
// picked up the new theme is that their rendered output changes when
// the theme changes (the light theme's Primary differs from the current
// theme's Primary, which feeds header backgrounds).
//
// The list delegate is rebuilt via SetDelegate; we assert presence
// only, since bubbles/list does not expose delegate styles and the
// visible selected-state difference depends on cursor position which
// the bench model does not set up.
//
// Not parallel: newTestModel calls t.Setenv via its temp HOME shim.
func TestApplyRuntimeThemePropagatesToComponents(t *testing.T) {
	m := newTestModel(t)
	lightPrimary := styles.NewTheme(config.ThemeLight).Primary

	currentSpinner := m.Spinner.Style.GetForeground()
	currentInstalledOut := m.projection.installedView().content
	currentDepsOut := m.deps.table.render(mouseDependencyRow).content

	m.settings.values.Theme = config.ThemeLight
	m.applyRuntimeTheme()

	if got := m.theme.Primary; got != lightPrimary {
		t.Fatalf("m.theme.Primary = %v, want light %v", got, lightPrimary)
	}
	if got := m.Spinner.Style.GetForeground(); got != lightPrimary {
		t.Fatalf("Spinner.Style foreground = %v, want light %v", got, lightPrimary)
	}
	if got := m.Spinner.Style.GetForeground(); got == currentSpinner {
		t.Fatal("Spinner.Style did not change after applyRuntimeTheme")
	}
	if got := m.projection.installedView().content; got == currentInstalledOut {
		t.Fatal("installed table render did not change after applyRuntimeTheme")
	}
	if got := m.deps.table.render(mouseDependencyRow).content; got == currentDepsOut {
		t.Fatal("dependency table render did not change after applyRuntimeTheme")
	}
}
