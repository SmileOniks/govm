package model

import (
	"errors"
	"strconv"

	"charm.land/bubbles/v2/textinput"
	"github.com/smileoniks-ctrl/govm/internal/config"
)

// settingsRowKind identifies a settings row by the setting it holds,
// never by its position on screen (ADR-0004): the display order in
// settingRows and the dispatch in settings_tab.go both switch on the
// kind, so adding a row means adding one kind, not renumbering.
type settingsRowKind int

const (
	settingRowDepsDisplay settingsRowKind = iota
	settingRowTheme
	settingRowDepsBackups
	settingRowDistributionSource
	settingRowUpgradeNotice
	settingRowCount
)

// settingRows is the display order of the rows.
var settingRows = [settingRowCount]settingsRowKind{
	settingRowDepsDisplay,
	settingRowTheme,
	settingRowDepsBackups,
	settingRowDistributionSource,
	settingRowUpgradeNotice,
}

// settingsTab is the Settings tab module: everything the tab owns
// behind the single update entry in settings_tab.go, mirroring
// depsTab. Every field is private to the module; the Model and the
// tests reach it only through its methods.
type settingsTab struct {
	values config.Settings
	cursor settingsRowKind
	// store is the seam every change is saved through, bound from
	// main (a file store) or from tests (an in-memory store).
	store config.Store
	// The two Settings text inputs; the editing flags double as the
	// dialog selectors, at most one set at a time.
	depsBackupLimitInput        textinput.Model
	depsBackupLimitInputErr     string
	editingDepsBackupLimit      bool
	distributionSourceInput     textinput.Model
	distributionSourceInputErr  string
	editingDistributionSource   bool
	checkingDistributionSource  bool
	distributionSourceRequestID uint64
	// lastSaveErr remembers the outcome of the most recent save so a
	// row activation can report it without a second write.
	lastSaveErr error
}

// newSettingsTab builds the tab over the already-loaded settings and
// the store every change is saved through. Normalization happens
// once, here, on the way in.
func newSettingsTab(settings config.Settings, store config.Store) settingsTab {
	tab := settingsTab{
		values: config.Normalize(settings),
		store:  store,
	}
	tab.depsBackupLimitInput = newDepsBackupLimitInput(tab.values.Theme)
	tab.distributionSourceInput = newDistributionSourceInput(tab.values.Theme)
	return tab
}

// Values returns the current settings values (always normalized).
func (s settingsTab) Values() config.Settings { return s.values }

// textInputActive reports whether one of the Settings text inputs has
// focus; the Model's Input context resolver reads it.
func (s settingsTab) textInputActive() bool {
	return s.editingDistributionSource || s.editingDepsBackupLimit
}

// editingSource reports which input is focused, for the hint bar.
func (s settingsTab) editingSource() bool { return s.editingDistributionSource }

// applyTheme restyles the inputs after a runtime theme change.
func (s *settingsTab) applyTheme(theme config.ThemeName) {
	s.depsBackupLimitInput.SetStyles(depsBackupLimitInputStyles(theme))
	s.distributionSourceInput.SetStyles(distributionSourceInputStyles(theme))
}

func newDepsBackupLimitInput(theme config.ThemeName) textinput.Model {
	input := textinput.New()
	input.Prompt = "Limit: "
	input.CharLimit = len(strconv.Itoa(config.MaxDepsBackupLimit))
	input.Validate = validateDepsBackupLimitInput
	input.SetStyles(depsBackupLimitInputStyles(theme))
	return input
}

func depsBackupLimitInputStyles(theme config.ThemeName) textinput.Styles {
	if theme == config.ThemeLight {
		return textinput.DefaultLightStyles()
	}
	return textinput.DefaultDarkStyles()
}

func newDistributionSourceInput(theme config.ThemeName) textinput.Model {
	input := textinput.New()
	input.Prompt = "Source: "
	input.CharLimit = 2048
	input.Validate = validateDistributionSourceInput
	input.SetStyles(distributionSourceInputStyles(theme))
	return input
}

func distributionSourceInputStyles(theme config.ThemeName) textinput.Styles {
	return depsBackupLimitInputStyles(theme)
}

func validateDepsBackupLimitInput(value string) error {
	if value == "" {
		return errors.New("Enter a whole number.")
	}

	limit, err := strconv.Atoi(value)
	if err != nil {
		return errors.New("Enter a whole number.")
	}
	return config.ValidateDepsBackupLimit(limit)
}

func validateDistributionSourceInput(value string) error {
	_, err := config.ValidateDistributionSource(value)
	return err
}
