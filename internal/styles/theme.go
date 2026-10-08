package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/config"
)

// themePalette is the per-theme input to buildTheme. It carries only
// palette colours; every lipgloss.Style is derived from these in
// buildTheme so that there is a single place to add a new theme.
type themePalette struct {
	Primary                  color.Color
	Success                  color.Color
	Error                    color.Color
	Warning                  color.Color
	Info                     color.Color
	Muted                    color.Color
	Text                     color.Color
	OnPrimary                color.Color
	InstalledBadgeForeground color.Color
}

// themeRegistry is the package-private map of known theme names to
// palettes. config.ThemeName is the canonical type for theme identity
// (it is the value persisted in settings.json and validated by
// config.Normalize), so styles depends on config rather than the
// reverse.
var themeRegistry = map[config.ThemeName]themePalette{
	config.ThemeCurrent: {
		Primary:                  lipgloss.Color("#7C3AED"),
		Success:                  lipgloss.Color("#10B981"),
		Error:                    lipgloss.Color("#EF4444"),
		Warning:                  lipgloss.Color("#F59E0B"),
		Info:                     lipgloss.Color("#3B82F6"),
		Muted:                    lipgloss.Color("#6B7280"),
		Text:                     lipgloss.Color("#E5E7EB"),
		OnPrimary:                lipgloss.Color("#FFFFFF"),
		InstalledBadgeForeground: lipgloss.Color("#F8FAFC"),
	},
	config.ThemeLight: {
		Primary:                  lipgloss.Color("#6D28D9"),
		Success:                  lipgloss.Color("#047857"),
		Error:                    lipgloss.Color("#DC2626"),
		Warning:                  lipgloss.Color("#B45309"),
		Info:                     lipgloss.Color("#2563EB"),
		Muted:                    lipgloss.Color("#4B5563"),
		Text:                     lipgloss.Color("#111827"),
		OnPrimary:                lipgloss.Color("#FFFFFF"),
		InstalledBadgeForeground: lipgloss.Color("#F8FAFC"),
	},
}

// Theme is an immutable snapshot of every palette color and pre-built
// lipgloss.Style that the TUI renders. It is the single value that
// callers (Model.View, ConfirmDialog.Render, cli output) receive as a
// parameter, replacing the previous generation of package-level mutable
// style singletons.
//
// Theme is a flat struct on purpose: nested grouping (Palette/General/
// Dialog) would add indirection without the sub-groups ever being passed
// separately, and the uniform naming convention (every pre-built style
// ends in Style) keeps the field list greppable.
type Theme struct {
	// Palette colors.
	Primary                  color.Color
	Success                  color.Color
	Error                    color.Color
	Warning                  color.Color
	Info                     color.Color
	Muted                    color.Color
	Text                     color.Color
	OnPrimary                color.Color
	InstalledBadgeForeground color.Color

	// Minimum-viewport fallback colors (used when the terminal is too
	// small to render the normal layout). These are constant across all
	// themes today but live on Theme so renderers take a single value.
	MinimumViewportBackground color.Color
	MinimumViewportText       color.Color

	// General-purpose styles.
	TitleStyle          lipgloss.Style
	HeaderMetaStyle     lipgloss.Style
	HeaderNoticeStyle   lipgloss.Style
	ActiveTabStyle      lipgloss.Style
	InactiveTabStyle    lipgloss.Style
	ActiveBadgeStyle    lipgloss.Style
	InstalledBadgeStyle lipgloss.Style
	ItemVersionStyle    lipgloss.Style
	MutedStyle          lipgloss.Style
	StatusSuccessStyle  lipgloss.Style
	StatusErrorStyle    lipgloss.Style
	StatusWarningStyle  lipgloss.Style
	StatusInfoStyle     lipgloss.Style
	HelpKeyStyle        lipgloss.Style
	HelpTextStyle       lipgloss.Style
	TableHeaderStyle    lipgloss.Style
	TableSelectedStyle  lipgloss.Style
	TableCellStyle      lipgloss.Style
	SpinnerStyle        lipgloss.Style

	// Modal dialog styles (rendered via renderDialog / ConfirmDialog.Render).
	DialogTitleStyle    lipgloss.Style
	DialogWarningStyle  lipgloss.Style
	DialogBodyStyle     lipgloss.Style
	DialogMutedStyle    lipgloss.Style
	DialogActiveStyle   lipgloss.Style
	DialogInactiveStyle lipgloss.Style
	DialogBoxStyle      lipgloss.Style
	DialogErrorBoxStyle lipgloss.Style
}

// NewTheme returns an immutable Theme value for the given name. It is a
// pure function: it does not mutate any package state and is safe to
// call concurrently. An unknown name silently falls back to
// config.ThemeCurrent, matching the validation already performed by
// config.Normalize.
func NewTheme(name config.ThemeName) Theme {
	palette, ok := themeRegistry[name]
	if !ok {
		palette = themeRegistry[config.ThemeCurrent]
	}
	return buildTheme(palette)
}

func buildTheme(palette themePalette) Theme {
	return Theme{
		Primary:                  palette.Primary,
		Success:                  palette.Success,
		Error:                    palette.Error,
		Warning:                  palette.Warning,
		Info:                     palette.Info,
		Muted:                    palette.Muted,
		Text:                     palette.Text,
		OnPrimary:                palette.OnPrimary,
		InstalledBadgeForeground: palette.InstalledBadgeForeground,

		// Minimum-viewport colors are not theme-dependent today; they
		// live on Theme so a single value carries every rendered color.
		MinimumViewportBackground: lipgloss.Color("#111827"),
		MinimumViewportText:       lipgloss.Color("#F9FAFB"),

		TitleStyle:          lipgloss.NewStyle().Bold(true).Foreground(palette.Text),
		HeaderMetaStyle:     lipgloss.NewStyle().Foreground(palette.Muted),
		HeaderNoticeStyle:   lipgloss.NewStyle().Foreground(palette.Success).Bold(true),
		ActiveTabStyle:      lipgloss.NewStyle().Foreground(palette.OnPrimary).Background(palette.Primary).Bold(true).Padding(0, 1),
		InactiveTabStyle:    lipgloss.NewStyle().Foreground(palette.Muted).Padding(0, 1),
		ActiveBadgeStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#052E16")).Background(palette.Success).Bold(true).Padding(0, 1),
		InstalledBadgeStyle: lipgloss.NewStyle().Foreground(palette.InstalledBadgeForeground).Background(palette.Primary).Padding(0, 1),
		ItemVersionStyle:    lipgloss.NewStyle().Foreground(palette.Text).Bold(true),
		MutedStyle:          lipgloss.NewStyle().Foreground(palette.Muted),
		StatusSuccessStyle:  lipgloss.NewStyle().Foreground(palette.Success).Bold(true),
		StatusErrorStyle:    lipgloss.NewStyle().Foreground(palette.Error).Bold(true),
		StatusWarningStyle:  lipgloss.NewStyle().Foreground(palette.Warning).Bold(true),
		StatusInfoStyle:     lipgloss.NewStyle().Foreground(palette.Info).Bold(true),
		HelpKeyStyle:        lipgloss.NewStyle().Foreground(palette.Primary).Bold(true),
		HelpTextStyle:       lipgloss.NewStyle().Foreground(palette.Muted),
		TableHeaderStyle:    lipgloss.NewStyle().Bold(true).Foreground(palette.OnPrimary).Background(palette.Primary).Padding(0, 1),
		TableSelectedStyle:  lipgloss.NewStyle().Foreground(palette.OnPrimary).Background(palette.Primary).Bold(true).Padding(0, 1),
		TableCellStyle:      lipgloss.NewStyle().Padding(0, 1),
		SpinnerStyle:        lipgloss.NewStyle().Foreground(palette.Primary),

		DialogTitleStyle:    lipgloss.NewStyle().Bold(true).Foreground(palette.Warning).Padding(0, 1),
		DialogWarningStyle:  lipgloss.NewStyle().Foreground(palette.Warning).Bold(true),
		DialogBodyStyle:     lipgloss.NewStyle().Padding(0, 1),
		DialogMutedStyle:    lipgloss.NewStyle().Foreground(palette.Muted).Padding(0, 1),
		DialogActiveStyle:   lipgloss.NewStyle().Foreground(palette.OnPrimary).Background(palette.Primary).Bold(true).Padding(0, 2),
		DialogInactiveStyle: lipgloss.NewStyle().Foreground(palette.Muted).Padding(0, 2),
		DialogBoxStyle:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(palette.Warning).Padding(1, 2).Width(64),
		DialogErrorBoxStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(palette.Error).Padding(1, 2).Width(64),
	}
}
