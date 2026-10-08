package styles

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/config"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plainText(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func TestItemTitleReturnsPreRenderedString(t *testing.T) {
	t.Parallel()

	theme := NewTheme(config.ThemeCurrent)
	rendered := RenderItemTitle(theme, "1.24.4", true, true)
	item := Item{Name: "1.24.4", RenderedTitle: rendered}

	if got := item.Title(); got != rendered {
		t.Fatalf("Title() = %q, want pre-rendered %q", got, rendered)
	}

	plain := plainText(item.Title())
	for _, want := range []string{"1.24.4", "active", "installed"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected title %q to contain %q", plain, want)
		}
	}
	if strings.Contains(plain, "(active)") || strings.Contains(plain, "(installed)") {
		t.Fatalf("expected modern badge text without legacy parentheses, got %q", plain)
	}
}

// TestItemTitleIgnoresSubsequentThemeMutations is the verification test
// for the pre-render contract: once an Item is built, Title() must not
// change if the caller later renders a different theme. The mutation
// case it guards against is someone re-introducing a live
// lipgloss.Style.Render call inside Title() that reads current theme
// state — that would make Title() depend on caller order again.
func TestItemTitleIgnoresSubsequentThemeMutations(t *testing.T) {
	t.Parallel()

	buildTheme := NewTheme(config.ThemeCurrent)
	item := Item{
		Name:          "1.24.4",
		RenderedTitle: RenderItemTitle(buildTheme, "1.24.4", true, true),
	}
	before := item.Title()

	// A different theme rendered afterwards must not retroactively
	// affect the previously built item.
	NewTheme(config.ThemeLight)

	if got := item.Title(); got != before {
		t.Fatalf("Title() changed after building a different theme: got %q, want %q", got, before)
	}
}

func TestItemDescriptionReturnsSecondaryText(t *testing.T) {
	t.Parallel()

	item := Item{DescriptionText: "go1.24.4.darwin-arm64.tar.gz"}

	if got := item.Description(); got != "go1.24.4.darwin-arm64.tar.gz" {
		t.Fatalf("expected description to preserve secondary text, got %q", got)
	}
}

// TestNewThemeBuildsFromPalette verifies that the immutable Theme value
// produced by NewTheme carries the expected palette colours and that
// light/current themes actually differ. Pure value test, safe under
// t.Parallel.
func TestNewThemeBuildsFromPalette(t *testing.T) {
	t.Parallel()

	current := NewTheme(config.ThemeCurrent)
	light := NewTheme(config.ThemeLight)

	for _, tc := range []struct {
		name    string
		theme   Theme
		primary string
		text    string
		muted   string
	}{
		{"current", current, "#7C3AED", "#E5E7EB", "#6B7280"},
		{"light", light, "#6D28D9", "#111827", "#4B5563"},
	} {
		if got, want := tc.theme.Primary, lipgloss.Color(tc.primary); got != want {
			t.Errorf("%s theme Primary = %v, want %v", tc.name, got, want)
		}
		if got, want := tc.theme.Text, lipgloss.Color(tc.text); got != want {
			t.Errorf("%s theme Text = %v, want %v", tc.name, got, want)
		}
		if got, want := tc.theme.Muted, lipgloss.Color(tc.muted); got != want {
			t.Errorf("%s theme Muted = %v, want %v", tc.name, got, want)
		}
	}

	currentOutput := current.TitleStyle.Render("GoVM")
	lightOutput := light.TitleStyle.Render("GoVM")
	if currentOutput == lightOutput {
		t.Fatalf("expected current and light TitleStyle output to differ")
	}

	// Minimum-viewport colors are theme-independent today; assert the
	// contract explicitly so a future change is caught here rather than
	// in a viewport snapshot diff.
	if got, want := current.MinimumViewportBackground, lipgloss.Color("#111827"); got != want {
		t.Fatalf("MinimumViewportBackground = %v, want %v", got, want)
	}
	if got, want := current.MinimumViewportText, lipgloss.Color("#F9FAFB"); got != want {
		t.Fatalf("MinimumViewportText = %v, want %v", got, want)
	}

	// InstalledBadgeStyle foreground was the regression that motivated
	// the original ApplyTheme test; keep the assertion as a value check.
	if got, want := current.InstalledBadgeStyle.GetForeground(), lipgloss.Color("#F8FAFC"); got != want {
		t.Fatalf("current InstalledBadgeStyle foreground = %v, want %v", got, want)
	}
}

// TestNewThemeFallsBackToCurrentForUnknownTheme pins NewTheme's
// silent-fallback contract without touching global state.
func TestNewThemeFallsBackToCurrentForUnknownTheme(t *testing.T) {
	t.Parallel()

	current := NewTheme(config.ThemeCurrent)
	unknown := NewTheme(config.ThemeName("does-not-exist"))

	if unknown.Primary != current.Primary {
		t.Fatalf("unknown theme Primary = %v, want fallback %v", unknown.Primary, current.Primary)
	}
	if unknown.TitleStyle.Render("x") != current.TitleStyle.Render("x") {
		t.Fatalf("unknown theme did not fall back to current rendering")
	}
}

// TestNewThemeIsPure proves NewTheme is safe to call concurrently; it
// must pass under -race.
func TestNewThemeIsPure(t *testing.T) {
	t.Parallel()

	done := make(chan Theme, 2)
	go func() { done <- NewTheme(config.ThemeCurrent) }()
	go func() { done <- NewTheme(config.ThemeLight) }()

	first := <-done
	second := <-done

	baseline := map[config.ThemeName]Theme{
		config.ThemeCurrent: NewTheme(config.ThemeCurrent),
		config.ThemeLight:   NewTheme(config.ThemeLight),
	}
	if first.Primary != baseline[config.ThemeCurrent].Primary && first.Primary != baseline[config.ThemeLight].Primary {
		t.Fatalf("concurrent first Primary %v matched neither baseline", first.Primary)
	}
	if second.Primary != baseline[config.ThemeCurrent].Primary && second.Primary != baseline[config.ThemeLight].Primary {
		t.Fatalf("concurrent second Primary %v matched neither baseline", second.Primary)
	}
	if first.Primary == second.Primary {
		t.Fatalf("expected concurrent results to come from distinct themes; both Primary = %v", first.Primary)
	}
}
