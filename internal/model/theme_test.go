package model

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/styles"
)

func TestSettingsDepsBackupLimitInputFollowsTheme(t *testing.T) {
	m := newTestModel(t)

	m.settings.values.Theme = config.ThemeLight
	m.applyRuntimeTheme()
	if got := m.settings.depsBackupLimitInput.Styles(); !reflect.DeepEqual(got, textinput.DefaultLightStyles()) {
		t.Fatal("expected backup limit input to use light theme styles")
	}

	m.settings.values.Theme = config.ThemeCurrent
	m.applyRuntimeTheme()
	if got := m.settings.depsBackupLimitInput.Styles(); !reflect.DeepEqual(got, textinput.DefaultDarkStyles()) {
		t.Fatal("expected backup limit input to use current theme styles")
	}
}

// TestSettingsToggleThemeChangesStateAndMessage replaces the previous
// version that asserted on global state via styles.CurrentTheme(). With
// theme now living on Model as a value, the assertion is that m.theme
// was rebuilt to match the new settings value, with no global state to
// reset in t.Cleanup.
func TestSettingsToggleThemeChangesStateAndMessage(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowTheme)
	wantLightPrimary := styles.NewTheme(config.ThemeLight).Primary

	updated, _ := m.Update(tea.KeyPressMsg{Code: ' '})
	m = updated.(Model)

	if m.settings.values.Theme != config.ThemeLight {
		t.Fatalf("expected theme light, got %q", m.settings.values.Theme)
	}
	if m.Status.Kind() == "error" || m.Status.Text() == "" {
		t.Fatalf("expected non-error message after theme save, got %q: %s", m.Status.Kind(), m.Status.Text())
	}
	if got := m.theme.Primary; got != wantLightPrimary {
		t.Fatalf("expected m.theme to be rebuilt to light; Primary = %v, want %v", got, wantLightPrimary)
	}
}

// TestApplyRuntimeThemeRebuildsDependencyDialogStyles pins the contract
// that previously broke silently (see docs/review/03-performance.md):
// after applyRuntimeTheme the active theme must flow into
// depsDialog.Render. Because Render now takes Theme as a parameter,
// the test also documents the new propagation path explicitly.
func TestApplyRuntimeThemeRebuildsDependencyDialogStyles(t *testing.T) {
	m := newTestModel(t)

	m.settings.values.Theme = config.ThemeCurrent
	m.applyRuntimeTheme()
	currentDialog := depsDialog{kind: dialogChecks, choiceYes: true}.render(m.theme, depsTab{}, viewportSize{Width: 64, Height: 20}).content

	m.settings.values.Theme = config.ThemeLight
	m.applyRuntimeTheme()
	lightDialog := depsDialog{kind: dialogChecks, choiceYes: true}.render(m.theme, depsTab{}, viewportSize{Width: 64, Height: 20}).content

	if lightDialog == currentDialog {
		t.Fatal("expected light theme to change dependency dialog output")
	}
}
