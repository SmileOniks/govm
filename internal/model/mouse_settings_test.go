package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/application"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/loader"
	"github.com/SmileOniks/govm/internal/upgrade"
	"github.com/SmileOniks/govm/internal/utils"
	"github.com/charmbracelet/x/ansi"
)

// Locate a value on the actual rendered settings row, rather than looking for
// a bare number or "Current" elsewhere on the screen. Coordinates still come
// exclusively from visible text and terminal-cell widths, never mouse targets.
func mouseSettingValue(t *testing.T, m Model, rowLabel, value string) (Model, tea.Cmd) {
	t.Helper()
	v := m.View()
	_, y := mouseText(t, v, rowLabel)
	line := ansi.Strip(strings.Split(v.Content, "\n")[y])
	labelAt := strings.Index(line, rowLabel)
	valueAt := strings.Index(line[labelAt+len(rowLabel):], value)
	if valueAt < 0 {
		t.Fatalf("value %q absent from settings row %q", value, line)
	}
	x := ansi.StringWidth(line[:labelAt+len(rowLabel)+valueAt])
	return mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

type mouseSettingsRecorder struct {
	*memorySettingsStore
	saves int
}

func (s *mouseSettingsRecorder) Save(values config.Settings) error {
	s.saves++
	return s.memorySettingsStore.Save(values)
}

func TestMouseSettings(t *testing.T) {
	t.Run("row selection and wheel never save", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		store := &mouseSettingsRecorder{memorySettingsStore: settingsStore(m)}
		m.settings.store = store
		before := store.values
		for _, row := range []struct {
			label string
			kind  settingsRowKind
		}{
			{label: "Theme:", kind: settingRowTheme},
			{label: "Deps backups:", kind: settingRowDepsBackups},
			{label: "Distribution source:", kind: settingRowDistributionSource},
			{label: "Upgrade notice:", kind: settingRowUpgradeNotice},
			{label: "Deps display:", kind: settingRowDepsDisplay},
		} {
			m = mouseClickRun(t, m, row.label)
			activated := m.settings.textInputActive() || m.settings.values != before
			saved := store.saves != 0 || store.values != before
			if m.settings.cursor != row.kind || activated || saved {
				t.Fatalf("row %q activated or saved: cursor=%v values=%+v", row.label, m.settings.cursor, m.settings.values)
			}
		}
		m = mouseWheel(t, m, "Deps display:", -1)
		if m.settings.cursor != settingRowUpgradeNotice {
			t.Fatalf("Settings wheel did not wrap: cursor=%v", m.settings.cursor)
		}
		m = mouseWheel(t, m, "Theme:", 1)
		wrongCursor := m.settings.cursor != settingRowDepsDisplay
		saved := store.saves != 0 || store.values != before
		if wrongCursor || saved || m.settings.values != before {
			t.Fatalf("wheel changed settings instead of selection: cursor=%v values=%+v", m.settings.cursor, m.settings.values)
		}
	})

	t.Run("theme value applies runtime theme and restores it", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		initialTheme := m.theme
		for _, want := range []config.ThemeName{config.ThemeLight, config.ThemeCurrent} {
			var cmd tea.Cmd
			m, cmd = mouseSettingValue(t, m, "Theme:", themeLabel(m.settings.values.Theme))
			m = mouseRunCmd(t, m, cmd)
			if m.settings.cursor != settingRowTheme || m.settings.values.Theme != want || settingsStore(m).values.Theme != want {
				t.Fatalf("theme value did not activate clicked row: cursor=%v values=%+v saved=%+v",
					m.settings.cursor, m.settings.values, settingsStore(m).values)
			}
			if want == config.ThemeLight && reflect.DeepEqual(m.theme, initialTheme) {
				t.Fatal("saved Light theme did not reach runtime rendering")
			}
			if want == config.ThemeCurrent && !reflect.DeepEqual(m.theme, initialTheme) {
				t.Fatal("returning to Current did not restore runtime theme")
			}
			assertVersionViewsConsistent(t, m)
		}
	})

	t.Run("display value updates dependency projection", func(t *testing.T) {
		m := mouseSized(t, loadDeps(t, newTestModel(t), marksList()), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		for _, want := range []config.DepsDisplayMode{config.DepsDisplayAll, config.DepsDisplayDirect} {
			var cmd tea.Cmd
			m, cmd = mouseSettingValue(t, m, "Deps display:", depsDisplayLabel(m.settings.values.DepsDisplay))
			m = mouseRunCmd(t, m, cmd)
			if m.settings.values.DepsDisplay != want || settingsStore(m).values.DepsDisplay != want {
				t.Fatalf("display = %v saved=%v want=%v", m.settings.values.DepsDisplay, settingsStore(m).values.DepsDisplay, want)
			}
			found := false
			for _, path := range m.deps.rowPaths {
				found = found || path == "example.com/hidden"
			}
			if found != (want == config.DepsDisplayAll) {
				t.Fatalf("display %v published wrong module identities: %v", want, m.deps.rowPaths)
			}
		}
	})

	t.Run("upgrade notice value saves and applies visibility effects", func(t *testing.T) {
		checker := &stubUpgradeChecker{notice: upgrade.Notice{Latest: "v0.2.5"}, available: true}
		m := mouseSized(t, newUpgradeTestModel(t, checker, config.UpgradeNoticeOff), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		var cmd tea.Cmd
		m, cmd = mouseSettingValue(t, m, "Upgrade notice:", "Off")
		m = mouseRunCmd(t, m, cmd)
		if m.UpgradeNotice() != "v0.2.5" || checker.calls != 1 {
			t.Fatalf("enabling notice did not run the bound checker: notice=%q calls=%d",
				m.UpgradeNotice(), checker.calls)
		}
		m, cmd = mouseSettingValue(t, m, "Upgrade notice:", "On")
		m = mouseRunCmd(t, m, cmd)
		if m.UpgradeNotice() != "" || settingsStore(m).values.UpgradeNotice != config.UpgradeNoticeOff {
			t.Fatal("disabling notice did not save and hide the existing notice")
		}
		m, cmd = mouseSettingValue(t, m, "Upgrade notice:", "Off")
		m = mouseRunCmd(t, m, cmd)
		if checker.calls != 1 || settingsStore(m).values.UpgradeNotice != config.UpgradeNoticeOn {
			t.Fatal("re-enabling notice repeated the session check or failed to save")
		}
	})

	for _, tc := range []struct {
		name        string
		start, want int
		label       string
	}{
		{name: "minus wraps minimum", start: config.MinDepsBackupLimit, want: config.MaxDepsBackupLimit, label: "[ − ]"},
		{name: "plus wraps maximum", start: config.MaxDepsBackupLimit, want: config.MinDepsBackupLimit, label: "[ + ]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			m.settings.values.DepsBackupLimit = tc.start
			settingsStore(m).values.DepsBackupLimit = tc.start
			m = mouseSized(t, m, 100, 36)
			m = mouseClickRun(t, m, "Settings")
			m = mouseClickRun(t, m, tc.label)
			wrongCursor := m.settings.cursor != settingRowDepsBackups || m.settings.textInputActive()
			wrongValues := m.settings.values.DepsBackupLimit != tc.want || settingsStore(m).values.DepsBackupLimit != tc.want
			if wrongCursor || wrongValues || m.deps.backupLimit != tc.want {
				t.Fatalf("step outcome: cursor=%v values=%+v saved=%+v executor limit=%d",
					m.settings.cursor, m.settings.values, settingsStore(m).values, m.deps.backupLimit)
			}
		})
	}

	for _, tc := range []struct {
		name, input string
		saveErr     error
		want        int
		open        bool
	}{
		{name: "save valid number", input: "25", want: 25},
		{name: "invalid empty", input: "", want: 10, open: true},
		{name: "invalid text", input: "abc", want: 10, open: true},
		{name: "below minimum", input: "0", want: 10, open: true},
		{name: "above maximum", input: "101", want: 10, open: true},
		{name: "disk full", input: "25", saveErr: errors.New("disk full"), want: 10, open: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mouseSized(t, newTestModel(t), 100, 36)
			m = mouseClickRun(t, m, "Settings")
			m, _ = mouseSettingValue(t, m, "Deps backups:", "10")
			if !m.settings.editingDepsBackupLimit {
				t.Fatal("backup value did not open editor")
			}
			settingsStore(m).err = tc.saveErr
			m.settings.depsBackupLimitInput.SetValue(tc.input)
			m = mouseClickRun(t, m, "Save enter")
			wrongValues := m.settings.values.DepsBackupLimit != tc.want || settingsStore(m).values.DepsBackupLimit != tc.want
			wrongEditor := m.settings.editingDepsBackupLimit != tc.open
			if wrongEditor || wrongValues || m.deps.backupLimit != tc.want {
				t.Fatalf("save outcome: open=%v values=%+v saved=%+v dependency limit=%d",
					m.settings.editingDepsBackupLimit, m.settings.values, settingsStore(m).values, m.deps.backupLimit)
			}
			if tc.open {
				if m.settings.depsBackupLimitInputErr == "" {
					t.Fatal("rejected save did not display a validation/storage error")
				}
				m = mouseClickRun(t, m, "Cancel esc")
				if m.settings.editingDepsBackupLimit || m.settings.values.DepsBackupLimit != 10 {
					t.Fatal("cancel did not discard the failed edit")
				}
			}
		})
	}

	t.Run("Cancel discards valid unsaved limit", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		m, _ = mouseSettingValue(t, m, "Deps backups:", "10")
		m.settings.depsBackupLimitInput.SetValue("25")
		m = mouseClickRun(t, m, "Cancel esc")
		wrongValues := m.settings.values.DepsBackupLimit != 10 || settingsStore(m).values.DepsBackupLimit != 10
		if m.settings.textInputActive() || wrongValues {
			t.Fatal("Cancel saved or retained the pending limit editor")
		}
	})

	t.Run("source checking disables Check and Reset and Cancel rejects late result", func(t *testing.T) {
		m := mouseSized(t, newTestModel(t), 100, 36)
		m = mouseClickRun(t, m, "Settings")
		m, _ = mouseSettingValue(t, m, "Distribution source:", "https://go.dev/dl/")
		oldSettings := m.settings.values
		calls := 0
		m = m.BindVersionOperations(VersionOperations{DistributionSource: func(
			_ context.Context, source string,
		) (application.DistributionSourceResult, error) {
			calls++
			return application.DistributionSourceResult{
				Source:  source,
				Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{{Version: "1.99.0"}}},
			}, nil
		}})
		m.settings.distributionSourceInput.SetValue("https://mirror.example/dl/")
		var pending tea.Cmd
		m, pending = mouseClick(t, m, "Check and save enter")
		if !m.settings.checkingDistributionSource || m.settings.distributionSourceRequestID == 0 || pending == nil {
			t.Fatal("Check did not enter correlated source-check state")
		}
		requestID := m.settings.distributionSourceRequestID
		for _, label := range []string{"Check and save enter", "Reset to official r"} {
			var cmd tea.Cmd
			m, cmd = mouseClick(t, m, label)
			wrongRequest := m.settings.distributionSourceRequestID != requestID || !m.settings.checkingDistributionSource
			wrongInput := m.settings.distributionSourceInput.Value() != "https://mirror.example/dl/"
			if cmd != nil || wrongRequest || wrongInput {
				t.Fatalf("disabled %q changed pending check", label)
			}
		}
		// Produce the successful result, but deliver it only after Cancel.
		late := installedTestCommandResult(t, pending)
		m = mouseClickRun(t, m, "Cancel esc")
		editorActive := m.settings.textInputActive() || m.settings.checkingDistributionSource
		requestActive := m.settings.distributionSourceRequestID != 0 ||
			m.projection.operationPhase() != catalogOperationPhaseIdle
		if editorActive || requestActive {
			t.Fatalf("Cancel left the request active: settings=%+v phase=%v", m.settings, m.projection.operationPhase())
		}
		m = feed(t, m, late)
		if calls != 1 || m.settings.values != oldSettings || settingsStore(m).values != oldSettings {
			t.Fatalf("late result changed canceled source: calls=%d values=%+v saved=%+v",
				calls, m.settings.values, settingsStore(m).values)
		}
		if _, found := m.projection.lookup("1.99.0"); found {
			t.Fatal("late source result replaced the catalog after cancellation")
		}
		// Teardown must release the catalog operation as well as close input.
		m, _ = mouseSettingValue(t, m, "Distribution source:", "https://go.dev/dl/")
		m.settings.distributionSourceInput.SetValue("https://second.example/dl/")
		m = mouseClickRun(t, m, "Check and save enter")
		wrongSource := m.settings.values.DistributionSource != "https://second.example/dl/"
		if m.settings.textInputActive() || wrongSource || calls != 2 {
			t.Fatal("canceled source check prevented a subsequent successful check")
		}
	})

	for _, tc := range []struct {
		name, label, input, wantSource string
		err                            error
		open                           bool
	}{
		{
			name: "check saves operation source and catalog", label: "Check and save enter",
			input: "https://mirror.example/dl", wantSource: "https://mirror.example/dl/",
		},
		{
			name: "Reset checks official source", label: "Reset to official r",
			input: "https://mirror.example/dl", wantSource: config.DefaultDistributionSource,
		},
		{
			name: "invalid source remains open", label: "Check and save enter",
			input: "", wantSource: config.DefaultDistributionSource, open: true,
		},
		{
			name: "source failure remains open", label: "Check and save enter",
			input: "https://mirror.example/dl", wantSource: config.DefaultDistributionSource,
			err: errors.New("catalog unavailable"), open: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mouseSized(t, newTestModel(t), 100, 36)
			m = mouseClickRun(t, m, "Settings")
			m, _ = mouseSettingValue(t, m, "Distribution source:", "https://go.dev/dl/")
			calls := 0
			m = m.BindVersionOperations(VersionOperations{DistributionSource: func(
				_ context.Context, source string,
			) (application.DistributionSourceResult, error) {
				calls++
				if tc.err != nil {
					return application.DistributionSourceResult{}, tc.err
				}
				if source != tc.wantSource {
					t.Fatalf("operation source = %q, want %q", source, tc.wantSource)
				}
				return application.DistributionSourceResult{
					Source: source,
					Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{
						{Version: "1.26.0", Filename: "go1.26.0.darwin-arm64.tar.gz"},
					}},
				}, nil
			}})
			m.settings.distributionSourceInput.SetValue(tc.input)
			m = mouseClickRun(t, m, tc.label)
			wantCalls := 1
			if tc.input == "" {
				wantCalls = 0
			}
			wrongEditor := m.settings.editingDistributionSource != tc.open || m.settings.checkingDistributionSource
			wrongSource := m.settings.values.DistributionSource != tc.wantSource
			if calls != wantCalls || wrongEditor || wrongSource {
				t.Fatalf("source outcome: calls=%d open=%v checking=%v source=%q",
					calls, m.settings.editingDistributionSource,
					m.settings.checkingDistributionSource, m.settings.values.DistributionSource)
			}
			if tc.open {
				if m.settings.distributionSourceInputErr == "" {
					t.Fatal("source rejection has no visible error")
				}
				if _, found := m.projection.lookup("1.26.0"); found {
					t.Fatal("failed source change applied its catalog")
				}
			} else if _, found := m.projection.lookup("1.26.0"); !found {
				t.Fatal("successful source change did not publish the operation catalog")
			}
		})
	}
}

func TestMouseSettingsButtonEdges(t *testing.T) {
	for _, theme := range []config.ThemeName{config.ThemeCurrent, config.ThemeLight} {
		for _, width := range []int{64, 130} {
			for _, row := range []struct {
				label string
				kind  settingsRowKind
			}{
				{label: "Deps display:", kind: settingRowDepsDisplay},
				{label: "Theme:", kind: settingRowTheme},
				{label: "Deps backups:", kind: settingRowDepsBackups},
				{label: "Distribution source:", kind: settingRowDistributionSource},
				{label: "Upgrade notice:", kind: settingRowUpgradeNotice},
			} {
				for _, edge := range []string{"left bracket", "left padding", "right padding", "right bracket"} {
					t.Run(fmt.Sprintf("%s/%d/%s/%s", theme, width, row.label, edge), func(t *testing.T) {
						m := newTestModel(t)
						m.settings.values.Theme = theme
						source := "https://example.com/" + strings.Repeat("界e\u0301", 30) + "/"
						m.settings.values.DistributionSource = source
						m.applyRuntimeTheme()
						m = mouseSized(t, m, width, 20)
						m, _ = mouseClick(t, m, "Settings")
						before := m.settings.values
						v := m.View()
						mouseGeometryBounds(t, v, width, 20)
						_, y := mouseText(t, v, row.label)
						line := ansi.Strip(strings.Split(v.Content, "\n")[y])
						left := strings.Index(line, "[ ")
						right := strings.Index(line, " ]")
						if left < 0 || right < 0 {
							t.Fatalf("setting value has no complete button: %q", line)
						}
						x := ansi.StringWidth(line[:left])
						switch edge {
						case "left padding":
							x++
						case "right padding":
							x = ansi.StringWidth(line[:right])
						case "right bracket":
							x = ansi.StringWidth(line[:right]) + 1
						}
						m, cmd := mouseAt(t, m, v, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
						m = mouseRunCmd(t, m, cmd)
						if m.settings.cursor != row.kind {
							t.Fatal("button did not select its setting")
						}
						switch row.kind {
						case settingRowDepsDisplay:
							if m.settings.values.DepsDisplay == before.DepsDisplay {
								t.Fatal("button edge did not toggle display mode")
							}
						case settingRowTheme:
							if m.settings.values.Theme == before.Theme {
								t.Fatal("button edge did not toggle theme")
							}
						case settingRowDepsBackups:
							if !m.settings.editingDepsBackupLimit || m.settings.values.DepsBackupLimit != before.DepsBackupLimit {
								t.Fatal("value button must open the editor, not step the limit")
							}
						case settingRowDistributionSource:
							if !m.settings.editingSource() || m.settings.distributionSourceInput.Value() != source {
								t.Fatal("clipped source button did not open the full source for editing")
							}
						case settingRowUpgradeNotice:
							if m.settings.values.UpgradeNotice == before.UpgradeNotice {
								t.Fatal("button edge did not toggle upgrade notice")
							}
						}
					})
				}
			}
		}
	}
}
