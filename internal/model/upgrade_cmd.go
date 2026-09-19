package model

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/upgrade"
)

// upgradeCheckTimeout bounds the Latest release lookup. The Upgrade
// notice is a bonus, so it gets a shorter budget than the catalog.
const upgradeCheckTimeout = 5 * time.Second

// checkUpgradeFunc is the narrow seam the TUI binds to for the Upgrade
// notice. It reports the Latest release when it is newer than the
// running binary; tests supply a stub without wiring services.
type checkUpgradeFunc func(context.Context) (upgrade.Notice, bool, error)

// upgradeCheckPhase tracks the once-per-session rule: the Latest
// release is looked up at most once, no matter how often the Settings
// toggle flips afterwards.
type upgradeCheckPhase int

const (
	upgradeCheckIdle upgradeCheckPhase = iota
	upgradeCheckInFlight
	upgradeCheckDone
)

// upgradeCheckStartMsg is emitted by Init so the check starts through
// the mutating startUpgradeCheck path; Init itself has a value receiver
// and cannot record that the check has been issued.
type upgradeCheckStartMsg struct{}

// upgradeCheckedMsg carries the outcome of the Latest release lookup.
// Err and a false Available are both silent: nothing is shown and
// nothing is logged.
type upgradeCheckedMsg struct {
	Notice    upgrade.Notice
	Available bool
	Err       error
}

// CheckUpgradeCmd wraps the synchronous check in a tea.Cmd with its
// own timeout.
func CheckUpgradeCmd(check checkUpgradeFunc) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), upgradeCheckTimeout)
		defer cancel()

		if check == nil {
			return upgradeCheckedMsg{Err: errors.New("no upgrade checker configured")}
		}
		notice, available, err := check(ctx)
		return upgradeCheckedMsg{Notice: notice, Available: available, Err: err}
	}
}

// upgradeNoticeEnabled reports the current value of the Settings
// toggle after normalisation.
func (m Model) upgradeNoticeEnabled() bool {
	return m.settings.values.UpgradeNotice == config.UpgradeNoticeOn
}

// initialUpgradeCheckCmd returns the message that kicks off the
// session's single check, or nil when the toggle is off or no checker
// is bound.
func (m Model) initialUpgradeCheckCmd() tea.Cmd {
	if m.checkUpgrade == nil || !m.upgradeNoticeEnabled() {
		return nil
	}
	return func() tea.Msg { return upgradeCheckStartMsg{} }
}

// startUpgradeCheck issues the Latest release lookup if it has not
// been issued in this session. It returns nil otherwise.
func (m *Model) startUpgradeCheck() tea.Cmd {
	if m.checkUpgrade == nil || m.upgradeCheck != upgradeCheckIdle || !m.upgradeNoticeEnabled() {
		return nil
	}
	m.upgradeCheck = upgradeCheckInFlight
	return CheckUpgradeCmd(m.checkUpgrade)
}

// handleUpgradeChecked records the outcome. The toggle value at
// arrival time decides: a notice that lands after the user switched
// the setting off is discarded, and it is not re-fetched later.
func (m *Model) handleUpgradeChecked(msg upgradeCheckedMsg) {
	m.upgradeCheck = upgradeCheckDone
	if msg.Err != nil || !msg.Available || !m.upgradeNoticeEnabled() {
		return
	}
	m.upgradeNotice = msg.Notice.Latest
}

// UpgradeNotice returns the Latest release shown in the header, or ""
// when no notice is showing.
func (m Model) UpgradeNotice() string {
	return m.upgradeNotice
}
