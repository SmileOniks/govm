package model

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/upgrade"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

// stubUpgradeChecker counts calls and answers with a fixed outcome.
type stubUpgradeChecker struct {
	notice    upgrade.Notice
	available bool
	err       error
	calls     int
}

func (s *stubUpgradeChecker) check(context.Context) (upgrade.Notice, bool, error) {
	s.calls++
	return s.notice, s.available, s.err
}

func newUpgradeTestModel(t *testing.T, checker *stubUpgradeChecker, mode config.UpgradeNoticeMode) Model {
	t.Helper()
	m := newTestModel(t)
	m.settings.values.UpgradeNotice = mode
	if checker != nil {
		m = m.BindVersionOperations(VersionOperations{CheckUpgrade: checker.check})
	}
	return m
}

func withGovmVersion(t *testing.T, version string) {
	t.Helper()
	prev := utils.Version
	utils.Version = version
	t.Cleanup(func() { utils.Version = prev })
}

func TestUpgradeNoticeShownInHeaderWhenNewerReleaseArrives(t *testing.T) {
	withGovmVersion(t, "v0.2.4")
	m := newUpgradeTestModel(t, nil, config.UpgradeNoticeOn)

	updated, cmd := m.Update(upgradeCheckedMsg{Notice: upgrade.Notice{Latest: "v0.2.5"}, Available: true})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no follow-up command, got %v", cmd)
	}
	if got := m.UpgradeNotice(); got != "v0.2.5" {
		t.Fatalf("UpgradeNotice() = %q, want v0.2.5", got)
	}

	view := stripANSI(m.View().Content)
	for _, want := range []string{"GoVM", "Go Version Manager v0.2.4", "↑ v0.2.5 available"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestUpgradeNoticeSilentOnErrorAndWhenUpToDate(t *testing.T) {
	withGovmVersion(t, "v0.2.4")
	for name, msg := range map[string]upgradeCheckedMsg{
		"error":      {Err: errors.New("api rate limit exceeded")},
		"up to date": {Available: false},
	} {
		t.Run(name, func(t *testing.T) {
			m := newUpgradeTestModel(t, nil, config.UpgradeNoticeOn)
			statusBefore := m.Status.Text()

			updated, _ := m.Update(msg)
			m = updated.(Model)

			if m.UpgradeNotice() != "" {
				t.Fatalf("UpgradeNotice() = %q, want empty", m.UpgradeNotice())
			}
			if m.Status.Text() != statusBefore {
				t.Fatalf("status changed to %q; the check must stay silent", m.Status.Text())
			}
			if strings.Contains(stripANSI(m.View().Content), "available") {
				t.Fatalf("view shows a notice:\n%s", m.View().Content)
			}
		})
	}
}

func TestUpgradeNoticeDiscardedWhenSettingIsOffAtArrival(t *testing.T) {
	m := newUpgradeTestModel(t, nil, config.UpgradeNoticeOff)
	m.upgradeCheck = upgradeCheckInFlight

	updated, _ := m.Update(upgradeCheckedMsg{Notice: upgrade.Notice{Latest: "v0.2.5"}, Available: true})
	m = updated.(Model)

	if m.UpgradeNotice() != "" {
		t.Fatalf("UpgradeNotice() = %q, want empty while the setting is off", m.UpgradeNotice())
	}
	if m.upgradeCheck != upgradeCheckDone {
		t.Fatalf("upgradeCheck = %v, want done: the lookup is not repeated later", m.upgradeCheck)
	}
}

func TestInitialUpgradeCheckRespectsSettingAndBinding(t *testing.T) {
	checker := &stubUpgradeChecker{}
	cases := []struct {
		name    string
		checker *stubUpgradeChecker
		mode    config.UpgradeNoticeMode
		want    bool
	}{
		{name: "on and bound", checker: checker, mode: config.UpgradeNoticeOn, want: true},
		{name: "off and bound", checker: checker, mode: config.UpgradeNoticeOff, want: false},
		{name: "on and unbound", checker: nil, mode: config.UpgradeNoticeOn, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newUpgradeTestModel(t, tc.checker, tc.mode)
			cmd := m.initialUpgradeCheckCmd()
			if (cmd != nil) != tc.want {
				t.Fatalf("initialUpgradeCheckCmd() != nil = %v, want %v", cmd != nil, tc.want)
			}
			if cmd == nil {
				return
			}
			if _, ok := cmd().(upgradeCheckStartMsg); !ok {
				t.Fatalf("initial command produced %T, want upgradeCheckStartMsg", cmd())
			}
		})
	}
}

func TestUpgradeCheckRunsOncePerSession(t *testing.T) {
	checker := &stubUpgradeChecker{notice: upgrade.Notice{Latest: "v0.2.5"}, available: true}
	m := newUpgradeTestModel(t, checker, config.UpgradeNoticeOn)

	updated, cmd := m.Update(upgradeCheckStartMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected the start message to issue the lookup command")
	}
	if m.upgradeCheck != upgradeCheckInFlight {
		t.Fatalf("upgradeCheck = %v, want in flight", m.upgradeCheck)
	}

	msg, ok := cmd().(upgradeCheckedMsg)
	if !ok {
		t.Fatalf("lookup command produced %T, want upgradeCheckedMsg", cmd())
	}
	if checker.calls != 1 {
		t.Fatalf("checker called %d times, want 1", checker.calls)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.UpgradeNotice() != "v0.2.5" {
		t.Fatalf("UpgradeNotice() = %q, want v0.2.5", m.UpgradeNotice())
	}

	// A second start message and a Settings off/on flip must not
	// consult the source again.
	updated, cmd = m.Update(upgradeCheckStartMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("second start message issued another lookup")
	}
	m = focusSetting(t, m, settingRowUpgradeNotice)
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEnter}} {
		updated, cmd = m.Update(key)
		m = updated.(Model)
		if cmd != nil {
			t.Fatal("toggling the setting issued another lookup")
		}
	}
	if checker.calls != 1 {
		t.Fatalf("checker called %d times after toggles, want 1", checker.calls)
	}
	if m.UpgradeNotice() != "" {
		t.Fatalf("UpgradeNotice() = %q after off/on flip, want empty: the lookup is not repeated", m.UpgradeNotice())
	}
}

func TestSettingsToggleUpgradeNoticeOffHidesNoticeAndSaves(t *testing.T) {
	m := newUpgradeTestModel(t, nil, config.UpgradeNoticeOn)
	m.upgradeNotice = "v0.2.5"
	m = focusSetting(t, m, settingRowUpgradeNotice)

	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyLeft}, {Code: 'l'}, {Code: tea.KeySpace}} {
		m.settings.values.UpgradeNotice = config.UpgradeNoticeOn
		m.upgradeNotice = "v0.2.5"
		updated, _ := m.Update(key)
		m = updated.(Model)
		if m.settings.values.UpgradeNotice != config.UpgradeNoticeOff {
			t.Fatalf("after %q UpgradeNotice setting = %q, want off", key.String(), m.settings.values.UpgradeNotice)
		}
		if m.UpgradeNotice() != "" {
			t.Fatalf("after %q the notice is still showing: %q", key.String(), m.UpgradeNotice())
		}
	}

	if saved := settingsStore(m).values.UpgradeNotice; saved != config.UpgradeNoticeOff {
		t.Fatalf("saved upgradeNotice = %q, want off", saved)
	}
	if !strings.Contains(stripANSI(m.View().Content), "Upgrade notice: Off") {
		t.Fatalf("settings view does not show the row as Off:\n%s", stripANSI(m.View().Content))
	}
}

func TestSettingsToggleUpgradeNoticeOnStartsSingleCheck(t *testing.T) {
	checker := &stubUpgradeChecker{notice: upgrade.Notice{Latest: "v0.2.5"}, available: true}
	m := newUpgradeTestModel(t, checker, config.UpgradeNoticeOff)
	if m.initialUpgradeCheckCmd() != nil {
		t.Fatal("initial check must not run while the setting is off")
	}
	m = focusSetting(t, m, settingRowUpgradeNotice)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.settings.values.UpgradeNotice != config.UpgradeNoticeOn {
		t.Fatalf("UpgradeNotice setting = %q, want on", m.settings.values.UpgradeNotice)
	}
	if cmd == nil {
		t.Fatal("switching the setting on must start the session's check")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.UpgradeNotice() != "v0.2.5" {
		t.Fatalf("UpgradeNotice() = %q, want v0.2.5", m.UpgradeNotice())
	}
	if checker.calls != 1 {
		t.Fatalf("checker called %d times, want 1", checker.calls)
	}
}

func TestSettingsCursorReachesUpgradeNoticeRow(t *testing.T) {
	m := newTestModel(t)
	m = focusSetting(t, m, settingRowDistributionSource)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.settings.cursor != settingRowUpgradeNotice {
		t.Fatalf("cursor = %d, want %d", m.settings.cursor, settingRowUpgradeNotice)
	}
	if !strings.Contains(stripANSI(m.View().Content), "> Upgrade notice: On") {
		t.Fatalf("settings view does not highlight the Upgrade notice row:\n%s", stripANSI(m.View().Content))
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.settings.cursor != settingRowDepsDisplay {
		t.Fatalf("cursor = %d after wrapping, want %d", m.settings.cursor, settingRowDepsDisplay)
	}
}

func TestRenderHeaderDropsPrefixBeforeNotice(t *testing.T) {
	t.Parallel()
	theme := testTheme()
	const pseudo = "v0.2.5-0.20260912123456-abcdef123456"

	full := stripANSI(renderHeader(theme, 120, "0.2.4", "v0.2.5"))
	for _, want := range []string{"GoVM", "Go Version Manager 0.2.4", "↑ v0.2.5 available"} {
		if !strings.Contains(full, want) {
			t.Fatalf("wide header lacks %q:\n%s", want, full)
		}
	}

	narrow := stripANSI(renderHeader(theme, 64, pseudo, "v0.2.5"))
	if strings.Contains(narrow, "Go Version Manager") {
		t.Fatalf("64-column header kept the prefix over the notice:\n%s", narrow)
	}
	for _, want := range []string{pseudo, "↑ v0.2.5 available"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("64-column header lacks %q:\n%s", want, narrow)
		}
	}

	tight := stripANSI(renderHeader(theme, 48, pseudo, "v0.2.5"))
	if strings.Contains(tight, "available") {
		t.Fatalf("48-column header kept the notice over the version:\n%s", tight)
	}
	if !strings.Contains(tight, pseudo) {
		t.Fatalf("48-column header truncated the version:\n%s", tight)
	}

	plain := stripANSI(renderHeader(theme, 64, "0.2.4", ""))
	if !strings.Contains(plain, "Go Version Manager 0.2.4") || strings.Contains(plain, "available") {
		t.Fatalf("header without notice changed:\n%s", plain)
	}
}

func TestCheckUpgradeCmdWithoutCheckerIsSilentError(t *testing.T) {
	t.Parallel()
	msg, ok := CheckUpgradeCmd(nil)().(upgradeCheckedMsg)
	if !ok {
		t.Fatalf("CheckUpgradeCmd(nil) produced %T", msg)
	}
	if msg.Err == nil || msg.Available {
		t.Fatalf("CheckUpgradeCmd(nil) = %+v, want an error and no notice", msg)
	}
}
