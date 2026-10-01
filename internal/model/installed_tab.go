package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

type prunePhase int

const (
	prunePhaseIdle prunePhase = iota
	prunePhasePreviewing
	prunePhaseConfirming
	prunePhaseRunning
)

// installedTab owns its interactions and disk summary, not the catalog projection.
// A running prune survives leaving the tab; unconfirmed flows do not.
type installedTab struct {
	phase         prunePhase
	plan          prune.Result
	choiceYes     bool
	nextPruneID   uint64
	pruneID       uint64
	deleteVersion string
	summary       prune.Summary
	previewPrune  previewPruneFunc
	runPrune      pruneFunc
	diskUsage     diskUsageFunc
}

type installedKeyMsg struct {
	key             tea.KeyPressMsg
	selectedVersion string
	canStartPrune   bool
}

type installedDeleteConfirmationMsg struct {
	version string
	clear   bool
}

type installedLeaveMsg struct{}

type installedStatusScope uint8

const (
	installedStatusUntouched installedStatusScope = iota
	installedStatusTab
	installedStatusGlobal
)

type installedStatus struct {
	scope      installedStatusScope
	text       string
	kind       string
	catalogMsg tea.Msg
	navigate   bool
	tableKey   tea.KeyPressMsg
}

func (s *installedTab) bindOperations(operations VersionOperations) {
	s.previewPrune = operations.PreviewPrune
	s.runPrune = operations.Prune
	s.diskUsage = operations.DiskUsage
}

func (s *installedTab) pruneBusy() bool { return s.phase != prunePhaseIdle }

func (s *installedTab) inputContext() inputContext {
	if s.phase == prunePhaseConfirming {
		return inputPruneConfirm
	}
	if s.deleteVersion != "" {
		return inputDeleteConfirm
	}
	return inputTab
}

func isInstalledMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case prunePreviewMsg, pruneDoneMsg, diskUsageMsg:
		return true
	}
	return false
}

func (s *installedTab) update(msg tea.Msg) (tea.Cmd, installedStatus) {
	switch msg := msg.(type) {
	case installedKeyMsg:
		return s.handleKey(msg)
	case installedDeleteConfirmationMsg:
		if msg.clear {
			s.deleteVersion = ""
		}
		if msg.version != "" {
			s.deleteVersion = msg.version
		}
	case installedLeaveMsg:
		s.deleteVersion = ""
		if s.phase != prunePhaseRunning {
			s.resetPrune()
		}
	case prunePreviewMsg:
		if msg.RequestID == 0 || msg.RequestID != s.pruneID || s.phase != prunePhasePreviewing {
			return nil, installedStatus{}
		}
		if len(msg.Result.Candidates) == 0 {
			s.resetPrune()
			if msg.Err != nil {
				return nil, installedStatus{scope: installedStatusTab, text: fmt.Sprintf("Prune unavailable: %v", msg.Err), kind: "error"}
			}
			return nil, installedStatus{scope: installedStatusTab, text: "Nothing to prune.", kind: "info"}
		}
		s.phase = prunePhaseConfirming
		s.plan = msg.Result
		s.choiceYes = true
		text := "Review the prune plan and press Y to confirm."
		if msg.Err != nil {
			text = fmt.Sprintf("Prune has warnings: %v", msg.Err)
		}
		return nil, installedStatus{scope: installedStatusTab, text: text, kind: "warning"}
	case pruneDoneMsg:
		if msg.RequestID == 0 || msg.RequestID != s.pruneID || s.phase != prunePhaseRunning {
			return nil, installedStatus{}
		}
		s.resetPrune()
		status := installedStatus{
			scope:      installedStatusGlobal,
			text:       fmt.Sprintf("Pruned %d object(s), freed %s.", len(msg.Result.Removed), formatDiskUsage(pruneResultBytes(msg.Result))),
			kind:       "success",
			catalogMsg: catalogRefreshMsg{},
		}
		if msg.Err != nil {
			status.text = fmt.Sprintf("Prune completed with warnings: %v", msg.Err)
			status.kind = "warning"
		}
		return s.diskUsageCmd(), status
	case diskUsageMsg:
		s.summary = msg.Summary
		status := installedStatus{catalogMsg: catalogDiskUsageMsg{sizes: s.summary.VersionBytes}}
		if msg.Err != nil {
			status.scope, status.kind = installedStatusTab, "warning"
			status.text = fmt.Sprintf("Disk usage unavailable: %v", msg.Err)
		} else if len(s.summary.Warnings) > 0 {
			status.scope, status.kind = installedStatusTab, "warning"
			status.text = "Disk usage is approximate; some files could not be inspected."
		}
		return nil, status
	case catalogLoadedMsg:
		return nil, installedStatus{catalogMsg: catalogDiskUsageMsg{sizes: s.summary.VersionBytes}}
	}
	return nil, installedStatus{}
}

func (s *installedTab) handleKey(msg installedKeyMsg) (tea.Cmd, installedStatus) {
	if s.phase == prunePhaseConfirming {
		choice, action := yesNoKeyAction(msg.key.String(), s.choiceYes)
		s.choiceYes = choice
		switch action {
		case dialogConfirm:
			s.phase = prunePhaseRunning
			return s.pruneCmd(s.pruneID), installedStatus{
				scope: installedStatusGlobal, text: "Pruning inactive Go versions...", kind: "info",
			}
		case dialogCancel:
			s.resetPrune()
			return nil, installedStatus{scope: installedStatusTab, text: "Prune operation canceled.", kind: "info"}
		}
		return nil, installedStatus{}
	}
	switch msg.key.String() {
	case "up", "down", "k", "j":
		return nil, installedStatus{navigate: true, tableKey: msg.key}
	case "u":
		if msg.selectedVersion != "" {
			return nil, installedStatus{catalogMsg: catalogActionMsg{
				kind: catalogActionActivate, version: msg.selectedVersion, tab: InstalledTab,
			}}
		}
	case "d":
		if msg.selectedVersion != "" && !s.pruneBusy() {
			return nil, installedStatus{catalogMsg: catalogActionMsg{
				kind: catalogActionRequestDelete, version: msg.selectedVersion, tab: InstalledTab,
			}}
		}
	case "r":
		return nil, installedStatus{catalogMsg: catalogRefreshMsg{manual: true}}
	case "y", "Y":
		if s.deleteVersion != "" {
			return nil, installedStatus{catalogMsg: catalogActionMsg{
				kind: catalogActionConfirmDelete, version: s.deleteVersion, tab: InstalledTab,
			}}
		}
	case "n", "N":
		if s.deleteVersion != "" {
			s.deleteVersion = ""
			return nil, installedStatus{scope: installedStatusTab, text: "Delete operation canceled.", kind: "info"}
		}
	case "p":
		if !msg.canStartPrune || s.pruneBusy() || s.deleteVersion != "" {
			return nil, installedStatus{}
		}
		if s.previewPrune == nil {
			return nil, installedStatus{scope: installedStatusTab, text: "Prune service is not configured.", kind: "error"}
		}
		s.nextPruneID++
		s.pruneID = s.nextPruneID
		s.phase = prunePhasePreviewing
		return s.previewPruneCmd(s.pruneID), installedStatus{
			scope: installedStatusTab, text: "Preparing prune plan...", kind: "info",
		}
	}
	return nil, installedStatus{}
}

func (s *installedTab) resetPrune() {
	s.phase = prunePhaseIdle
	s.plan = prune.Result{}
	s.choiceYes = false
	s.pruneID = 0
}

func (s *installedTab) summaryView() string {
	if s.diskUsage == nil {
		return ""
	}
	line := fmt.Sprintf("Installed: %s  Reclaimable: %s",
		prune.FormatBytes(s.summary.InstalledBytes), prune.FormatBytes(s.summary.ReclaimableBytes))
	if s.summary.DownloadBytes > 0 {
		line += fmt.Sprintf("  Interrupted: %s", prune.FormatBytes(s.summary.DownloadBytes))
	}
	return line
}

func (s *installedTab) dialogView(t styles.Theme, viewport viewportSize) string {
	result := s.plan
	lines := []string{
		t.DialogTitleStyle.Render(t.DialogWarningStyle.Render("⚠ Prune inactive Go versions?")),
		"",
		t.DialogBodyStyle.Render(fmt.Sprintf("Candidates: %d", len(result.Candidates))),
		t.DialogBodyStyle.Render(fmt.Sprintf("Reclaimable: %s", prune.FormatBytes(pruneCandidateBytes(result)))),
		"",
	}
	visible := result.Candidates
	extra := 0
	if len(visible) > maxDependencyListLines {
		extra = len(visible) - maxDependencyListLines
		visible = visible[:maxDependencyListLines]
	}
	for _, candidate := range visible {
		label := candidate.Version
		if label == "" {
			label = candidate.Path
		}
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("  %s  %s", label, prune.FormatBytes(candidate.Bytes))))
	}
	if extra > 0 {
		lines = append(lines, t.DialogBodyStyle.Render(fmt.Sprintf("  …and %d more", extra)))
	}
	lines = append(lines, "", renderYesNoButtons(t, s.choiceYes, "Yes", "No"))
	return renderDialog(t, lipgloss.JoinVertical(lipgloss.Left, lines...), false, viewport)
}

func pruneResultBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Removed {
		total += candidate.Bytes
	}
	return total
}

// The preview carries Candidates; Removed is filled in by execution.
func pruneCandidateBytes(result prune.Result) int64 {
	var total int64
	for _, candidate := range result.Candidates {
		total += candidate.Bytes
	}
	return total
}
