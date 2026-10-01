package model

import (
	"errors"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

// This file is the single entry of the Settings tab module (see
// ADR-0004). The Model routes keys to update while the Input context
// is the Settings tab or one of its text inputs, routes the tab's own
// results to it, and applies the settingsStatus effect the tab
// returns. The module never touches the Model.

// settingsStatusScope says what the Model should do with the status
// part of a settingsStatus.
type settingsStatusScope int

const (
	// settingsStatusUntouched (the zero value) leaves the status line
	// as it is.
	settingsStatusUntouched settingsStatusScope = iota
	// settingsStatusTab sets a tab-scoped message.
	settingsStatusTab
	// settingsStatusGlobal sets a global message.
	settingsStatusGlobal
)

// settingsStatus is the effect a Settings tab operation returns: the
// Model applies it, never interpreting why the tab produced it. It is
// a value so the tab can be tested without a StatusLine. Besides the
// status line, the effect carries the rare notifications the Model
// must act on: that values were saved (every consumer of the settings
// must be told), that the theme flipped, that a distribution source
// check should start, failed, or produced a catalog to accept, and
// that the upgrade notice was switched on.
type settingsStatus struct {
	scope settingsStatusScope
	text  string
	kind  string

	// valuesChanged reports that values were saved and every consumer
	// of the settings must be told.
	valuesChanged bool
	// themeChanged reports that Values.Theme flipped and the Model
	// must rebuild its theme snapshot and propagate it.
	themeChanged bool
	// upgradeNoticeOn reports that the Upgrade notice setting was
	// switched on and the Model should start its check;
	// upgradeNoticeOff that it was switched off and a showing notice
	// must be hidden at once.
	upgradeNoticeOn  bool
	upgradeNoticeOff bool

	// beginSourceCheck asks the Model to start validating source
	// against the catalog projection; the Model answers the tab with
	// a sourceCheckStartedMsg carrying the request ID.
	beginSourceCheck bool
	source           string
	// failSourceCheck asks the Model to fail the catalog load of the
	// check identified by requestID (err says why).
	failSourceCheck bool
	requestID       uint64
	err             error
	// acceptCatalog asks the Model to accept versions as the catalog
	// of the check identified by requestID.
	acceptCatalog bool
	versions      []utils.GoVersion
}

func settingsTabStatus(text, kind string) settingsStatus {
	return settingsStatus{scope: settingsStatusTab, text: text, kind: kind}
}

// sourceCheckStartedMsg tells the tab the request ID of the catalog
// load its distribution-source check runs under.
type sourceCheckStartedMsg struct {
	requestID uint64
}

// sourceCheckRejectedMsg tells the tab its source check could not
// start (another operation active) or its catalog was rejected.
type sourceCheckRejectedMsg struct {
	reason string
}

// update is the single entry for keys and messages. Keys arrive only
// while the tab or one of its text inputs owns the keyboard (the
// Model keeps ctrl+c, q and tab navigation for itself); every other
// message is one of the tab's own results or a cursor-blink tick for
// the focused input.
func (s *settingsTab) update(msg tea.Msg) (tea.Cmd, settingsStatus) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return s.handleKey(msg)
	case sourceCheckStartedMsg:
		s.distributionSourceRequestID = msg.requestID
		return nil, settingsStatus{}
	case sourceCheckRejectedMsg:
		s.checkingDistributionSource = false
		s.distributionSourceRequestID = 0
		if s.editingDistributionSource {
			s.distributionSourceInputErr = msg.reason
		}
		return nil, settingsStatus{}
	case distributionSourceValidatedMsg:
		return s.handleSourceValidated(msg)
	default:
		var cmd tea.Cmd
		if s.editingDistributionSource {
			s.distributionSourceInput, cmd = s.distributionSourceInput.Update(msg)
		} else if s.editingDepsBackupLimit {
			s.depsBackupLimitInput, cmd = s.depsBackupLimitInput.Update(msg)
		}
		return cmd, settingsStatus{}
	}
}

// handleKey maps a key to the tab's own action.
func (s *settingsTab) handleKey(msg tea.KeyPressMsg) (tea.Cmd, settingsStatus) {
	if s.editingDistributionSource {
		return s.handleSourceInputKey(msg)
	}
	if s.editingDepsBackupLimit {
		return s.handleLimitInputKey(msg)
	}
	switch msg.String() {
	case "up", "k":
		s.moveUp()
	case "down", "j":
		s.moveDown()
	case "enter", "space":
		return s.editRow()
	case "left", "h":
		return s.stepRow(-1)
	case "right", "l":
		return s.stepRow(1)
	}
	return nil, settingsStatus{}
}

func (s *settingsTab) moveUp() {
	if s.cursor == 0 {
		s.cursor = settingRowCount - 1
		return
	}
	s.cursor--
}

func (s *settingsTab) moveDown() {
	s.cursor = (s.cursor + 1) % settingRowCount
}

// editRow acts on the row under the cursor with enter or space: the
// editor rows open their editor, the two-variant rows flip.
func (s *settingsTab) editRow() (tea.Cmd, settingsStatus) {
	switch s.cursor {
	case settingRowDepsBackups:
		return s.openDepsBackupLimitInput(), settingsStatus{}
	case settingRowDistributionSource:
		return s.openDistributionSourceInput(), settingsStatus{}
	default:
		s.toggleRow()
		return nil, s.savedEffect()
	}
}

// stepRow steps the row under the cursor with left/right: the limit
// steps by delta (wrapping), the source row opens its editor, the
// two-variant rows flip.
func (s *settingsTab) stepRow(delta int) (tea.Cmd, settingsStatus) {
	switch s.cursor {
	case settingRowDepsBackups:
		s.adjustDepsBackupLimit(delta)
		return nil, s.savedEffect()
	case settingRowDistributionSource:
		return s.openDistributionSourceInput(), settingsStatus{}
	default:
		s.toggleRow()
		return nil, s.savedEffect()
	}
}

// savedEffect renders the outcome of the most recent save together
// with the notifications the changed rows imply.
func (s *settingsTab) savedEffect() settingsStatus {
	effect := settingsStatus{valuesChanged: true}
	switch s.cursor {
	case settingRowTheme:
		effect.themeChanged = true
	case settingRowUpgradeNotice:
		if s.values.UpgradeNotice == config.UpgradeNoticeOn {
			effect.upgradeNoticeOn = true
		} else {
			effect.upgradeNoticeOff = true
		}
	}
	if s.lastSaveErr != nil {
		return settingsTabStatus(fmt.Sprintf("Failed to save settings: %v", s.lastSaveErr), "error")
	}
	effect.scope = settingsStatusTab
	effect.text = "Settings saved."
	effect.kind = "info"
	return effect
}

// toggleRow flips the two-variant row under the cursor and persists
// the new values.
func (s *settingsTab) toggleRow() {
	switch s.cursor {
	case settingRowDepsDisplay:
		if s.values.DepsDisplay == config.DepsDisplayDirect {
			s.values.DepsDisplay = config.DepsDisplayAll
		} else {
			s.values.DepsDisplay = config.DepsDisplayDirect
		}
	case settingRowTheme:
		if s.values.Theme == config.ThemeCurrent {
			s.values.Theme = config.ThemeLight
		} else {
			s.values.Theme = config.ThemeCurrent
		}
	case settingRowUpgradeNotice:
		if s.values.UpgradeNotice == config.UpgradeNoticeOn {
			s.values.UpgradeNotice = config.UpgradeNoticeOff
		} else {
			s.values.UpgradeNotice = config.UpgradeNoticeOn
		}
	}
	s.save()
}

// adjustDepsBackupLimit steps the limit by delta with wrap-around and
// persists.
func (s *settingsTab) adjustDepsBackupLimit(delta int) {
	limit := s.values.DepsBackupLimit + delta
	if limit < config.MinDepsBackupLimit {
		limit = config.MaxDepsBackupLimit
	} else if limit > config.MaxDepsBackupLimit {
		limit = config.MinDepsBackupLimit
	}
	s.values.DepsBackupLimit = limit
	s.save()
}

// save persists the current values through the store and remembers
// the outcome for savedEffect.
func (s *settingsTab) save() {
	s.lastSaveErr = s.store.Save(s.values)
}

// openDepsBackupLimitInput opens the limit editor seeded with the
// current value.
func (s *settingsTab) openDepsBackupLimitInput() tea.Cmd {
	s.depsBackupLimitInputErr = ""
	s.depsBackupLimitInput.SetValue(strconv.Itoa(s.values.DepsBackupLimit))
	s.depsBackupLimitInput.CursorEnd()
	s.editingDepsBackupLimit = true
	return s.depsBackupLimitInput.Focus()
}

// closeDepsBackupLimitInput closes the limit editor.
func (s *settingsTab) closeDepsBackupLimitInput() {
	s.depsBackupLimitInputErr = ""
	s.depsBackupLimitInput.Blur()
	s.editingDepsBackupLimit = false
}

// openDistributionSourceInput opens the source editor seeded with the
// current value.
func (s *settingsTab) openDistributionSourceInput() tea.Cmd {
	s.distributionSourceInputErr = ""
	s.distributionSourceInput.SetValue(s.values.DistributionSource)
	s.distributionSourceInput.CursorEnd()
	s.editingDistributionSource = true
	return s.distributionSourceInput.Focus()
}

// closeDistributionSourceInput closes the source editor and forgets
// any pending check.
func (s *settingsTab) closeDistributionSourceInput() {
	s.distributionSourceInputErr = ""
	s.distributionSourceInput.Blur()
	s.editingDistributionSource = false
	s.checkingDistributionSource = false
	s.distributionSourceRequestID = 0
}

// handleLimitInputKey owns the keyboard while the limit editor is
// open. enter validates and saves; esc discards.
func (s *settingsTab) handleLimitInputKey(msg tea.KeyPressMsg) (tea.Cmd, settingsStatus) {
	switch msg.String() {
	case "esc":
		s.closeDepsBackupLimitInput()
		return nil, settingsStatus{}
	case "enter":
		if err := s.depsBackupLimitInput.Err; err != nil {
			s.depsBackupLimitInputErr = err.Error()
			return nil, settingsStatus{}
		}
		limit, err := strconv.Atoi(s.depsBackupLimitInput.Value())
		if err != nil {
			s.depsBackupLimitInputErr = "Enter a whole number."
			return nil, settingsStatus{}
		}
		if err := config.ValidateDepsBackupLimit(limit); err != nil {
			s.depsBackupLimitInputErr = err.Error()
			return nil, settingsStatus{}
		}
		values := s.values
		values.DepsBackupLimit = limit
		if err := s.store.Save(values); err != nil {
			s.depsBackupLimitInputErr = fmt.Sprintf("Failed to save settings: %v", err)
			return nil, settingsStatus{}
		}
		s.values = values
		s.closeDepsBackupLimitInput()
		return nil, settingsTabStatus("Settings saved.", "info").withValuesChanged()
	default:
		var cmd tea.Cmd
		s.depsBackupLimitInput, cmd = s.depsBackupLimitInput.Update(msg)
		s.depsBackupLimitInputErr = ""
		return cmd, settingsStatus{}
	}
}

// handleSourceInputKey owns the keyboard while the source editor is
// open. While a check is in flight only esc (cancel) is answered.
func (s *settingsTab) handleSourceInputKey(msg tea.KeyPressMsg) (tea.Cmd, settingsStatus) {
	if s.checkingDistributionSource {
		if msg.String() == "esc" {
			requestID := s.distributionSourceRequestID
			s.closeDistributionSourceInput()
			return nil, settingsStatus{
				failSourceCheck: true,
				requestID:       requestID,
				err:             errSourceCheckCanceled,
			}
		}
		return nil, settingsStatus{}
	}

	switch msg.String() {
	case "esc":
		s.closeDistributionSourceInput()
		return nil, settingsStatus{}
	case "r":
		s.distributionSourceInput.SetValue(config.DefaultDistributionSource)
		s.distributionSourceInputErr = ""
		return s.beginSourceCheck()
	case "enter":
		if err := s.distributionSourceInput.Err; err != nil {
			s.distributionSourceInputErr = err.Error()
			return nil, settingsStatus{}
		}
		return s.beginSourceCheck()
	default:
		var cmd tea.Cmd
		s.distributionSourceInput, cmd = s.distributionSourceInput.Update(msg)
		s.distributionSourceInputErr = ""
		return cmd, settingsStatus{}
	}
}

// errSourceCheckCanceled is the reason the Model fails the catalog
// load with when the user cancels a running source check.
var errSourceCheckCanceled = errors.New("distribution source check canceled")

// beginSourceCheck validates the typed source and asks the Model, via
// the effect, to start the distribution-source check against the
// catalog projection. The Model answers with sourceCheckStartedMsg.
func (s *settingsTab) beginSourceCheck() (tea.Cmd, settingsStatus) {
	source, err := config.ValidateDistributionSource(s.distributionSourceInput.Value())
	if err != nil {
		s.distributionSourceInputErr = err.Error()
		return nil, settingsStatus{}
	}
	s.checkingDistributionSource = true
	return nil, settingsStatus{
		beginSourceCheck: true,
		source:           source,
	}
}

// handleSourceValidated applies the result of the distribution-source
// operation the Model ran for this tab.
func (s *settingsTab) handleSourceValidated(msg distributionSourceValidatedMsg) (tea.Cmd, settingsStatus) {
	if !s.checkingDistributionSource ||
		msg.RequestID != s.distributionSourceRequestID {
		return nil, settingsStatus{}
	}
	if msg.Err != nil {
		s.checkingDistributionSource = false
		s.distributionSourceInputErr = msg.Err.Error()
		return nil, settingsStatus{
			failSourceCheck: true,
			requestID:       msg.RequestID,
			err:             msg.Err,
		}
	}

	s.values.DistributionSource = msg.Result.Source
	s.closeDistributionSourceInput()
	return nil, settingsStatus{
		acceptCatalog: true,
		requestID:     msg.RequestID,
		versions:      msg.Result.Catalog.Versions,
		valuesChanged: true,
	}
}

// withValuesChanged marks the effect as one whose saved values must
// reach the settings consumers.
func (e settingsStatus) withValuesChanged() settingsStatus {
	e.valuesChanged = true
	return e
}

// view renders the tab's content canvas.
func (s settingsTab) view(width int) renderedSurface { return renderSettingsView(s, width) }
