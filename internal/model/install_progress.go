package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/charmbracelet/x/ansi"
)

const installProgressUpdateInterval = 100 * time.Millisecond

type installProgressMsg struct {
	operationID uint64
	progress    install.Progress
	session     *installProgressSession
}

type installProgressPollMsg struct {
	session *installProgressSession
}

type installProgressMailbox struct {
	mu       sync.Mutex
	latest   install.Progress
	notify   chan struct{}
	notified bool
}

func newInstallProgressMailbox() *installProgressMailbox {
	return &installProgressMailbox{
		notify: make(chan struct{}, 1),
	}
}

func (m *installProgressMailbox) Report(progress install.Progress) {
	m.mu.Lock()
	m.latest = progress
	if !m.notified {
		m.notified = true
		m.notify <- struct{}{}
	}
	m.mu.Unlock()
}

type installProgressSession struct {
	operationID uint64
	request     install.Request
	install     installProgressFunc
	outcomes    chan tea.Msg
	mailbox     *installProgressMailbox
}

func (s *installProgressSession) start() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
		defer cancel()

		result, err := s.install(ctx, s.request, s.mailbox)
		if err != nil {
			s.outcomes <- installFailureMsg{
				OperationID: s.operationID,
				Version:     s.request.Version,
				Err:         err,
			}
			return
		}
		s.outcomes <- installSuccessMsg{
			OperationID: s.operationID,
			Version:     result.Version,
			Path:        result.Path,
			Warnings:    result.Warnings,
		}
	}()
}

func (s *installProgressSession) wait(initial bool) tea.Cmd {
	return func() tea.Msg {
		if initial {
			select {
			case msg := <-s.outcomes:
				return msg
			case <-s.mailbox.notify:
				return installProgressMsg{
					operationID: s.operationID,
					progress:    s.mailbox.snapshot(),
					session:     s,
				}
			}
		}

		timer := time.NewTimer(installProgressUpdateInterval)
		defer timer.Stop()
		select {
		case msg := <-s.outcomes:
			return msg
		case <-timer.C:
			select {
			case msg := <-s.outcomes:
				return msg
			case <-s.mailbox.notify:
				return installProgressMsg{
					operationID: s.operationID,
					progress:    s.mailbox.snapshot(),
					session:     s,
				}
			default:
				return installProgressPollMsg{session: s}
			}
		}
	}
}

func (m *installProgressMailbox) snapshot() install.Progress {
	m.mu.Lock()
	progress := m.latest
	m.notified = false
	m.mu.Unlock()
	return progress
}

func (a *catalogProjectionAdapter) installProgressVersionCmd(operationID uint64, v install.Request) tea.Cmd {
	installFn := a.installWithProgress
	fallbackInstall := a.installGo
	if installFn == nil {
		installFn = func(
			ctx context.Context,
			request install.Request,
			_ install.ProgressReporter,
		) (install.Result, error) {
			if fallbackInstall == nil {
				return install.Result{}, fmt.Errorf("no installer configured")
			}
			return fallbackInstall(ctx, request)
		}
	}
	session := &installProgressSession{
		operationID: operationID,
		request:     v,
		install:     installFn,
		outcomes:    make(chan tea.Msg, 1),
		mailbox:     newInstallProgressMailbox(),
	}
	return func() tea.Msg {
		session.start()
		return session.wait(true)()
	}
}

func (a *catalogProjectionAdapter) handleInstallProgress(msg installProgressMsg) tea.Cmd {
	if msg.session == nil || !a.applyProgress(msg.operationID, msg.progress) {
		return nil
	}
	return msg.session.wait(false)
}

func (a *catalogProjectionAdapter) handleInstallProgressPoll(msg installProgressPollMsg) tea.Cmd {
	if msg.session == nil || !a.isActiveOperation(msg.session.operationID) {
		return nil
	}
	return msg.session.wait(false)
}

func (m Model) installStageStatus(progress install.Progress, width int) string {
	activity := fmt.Sprintf("%s Go %s", installStageLabel(progress.Stage), progress.Version)
	if progress.Stage == install.StageDownload {
		return m.renderDownloadStatus(progress, width)
	}
	return fmt.Sprintf("%s %s", m.Spinner.View(), activity)
}

func installStageLabel(stage install.Stage) string {
	switch stage {
	case install.StageValidate, install.StageLock, install.StagePrepare:
		return "Preparing"
	case install.StageDownload:
		return "Downloading"
	case install.StageIntegrity:
		return "Checking integrity"
	case install.StageExtract:
		return "Extracting"
	case install.StageVerify:
		return "Verifying"
	case install.StageCommit, install.StageCleanup:
		return "Finishing"
	default:
		return "Installing"
	}
}

func (m Model) renderDownloadStatus(progress install.Progress, width int) string {
	prefix := fmt.Sprintf("Downloading Go %s", progress.Version)
	available := maxInt(1, width-lipgloss.Width("• "))
	bytesText := fmt.Sprintf(
		"(%s / %s)",
		formatInstallBytes(progress.BytesReceived),
		formatInstallBytes(progress.BytesTotal),
	)
	ratio := 0.0
	if progress.BytesTotal > 0 {
		ratio = float64(progress.BytesReceived) / float64(progress.BytesTotal)
	}
	if progress.BytesTotal <= 0 {
		return fmt.Sprintf(
			"%s %s %s",
			m.Spinner.View(),
			prefix,
			formatInstallBytes(progress.BytesReceived),
		)
	}

	barWidth := installBarWidth(available - lipgloss.Width(prefix) - lipgloss.Width(bytesText) - 2)
	full := fmt.Sprintf("%s %s %s", prefix, renderInstallProgressBar(m.theme, barWidth, ratio), bytesText)
	if ansi.StringWidth(full) <= available {
		return full
	}

	barWidth = installBarWidth(available - lipgloss.Width(prefix) - 2)
	withoutBytes := fmt.Sprintf("%s %s", prefix, renderInstallProgressBar(m.theme, barWidth, ratio))
	if ansi.StringWidth(withoutBytes) <= available {
		return withoutBytes
	}

	barWidth = maxInt(1, available-lipgloss.Width(prefix)-1)
	return fmt.Sprintf("%s %s", prefix, renderInstallProgressBar(m.theme, barWidth, ratio))
}

func formatInstallBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	const unit = 1024
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	scaled := float64(value)
	for _, label := range units {
		scaled /= unit
		if scaled < unit || label == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", scaled, label)
		}
	}
	return fmt.Sprintf("%.1f TiB", scaled)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
