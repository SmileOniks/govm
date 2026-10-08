package model

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/prune"
)

// Wall-clock budgets granted to a single prune operation. Preview and
// disk usage only walk the managed directories, while a prune waits for
// the shared mutation lock and then removes toolchains.
const (
	prunePreviewTimeout = 30 * time.Second
	pruneTimeout        = 30 * time.Minute
	diskUsageTimeout    = 30 * time.Second
)

type previewPruneFunc func(context.Context) (prune.Result, error)
type pruneFunc func(context.Context) (prune.Result, error)
type diskUsageFunc func(context.Context) (prune.Summary, error)

type prunePreviewMsg struct {
	RequestID uint64
	Result    prune.Result
	Err       error
}

type pruneDoneMsg struct {
	RequestID uint64
	Result    prune.Result
	Err       error
}

type diskUsageMsg struct {
	Summary prune.Summary
	Err     error
}

func (s *installedTab) previewPruneCmd(requestID uint64) tea.Cmd {
	preview := s.previewPrune
	return func() tea.Msg {
		if preview == nil {
			return prunePreviewMsg{RequestID: requestID, Err: errors.New("no prune service configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), prunePreviewTimeout)
		defer cancel()
		result, err := preview(ctx)
		return prunePreviewMsg{RequestID: requestID, Result: result, Err: err}
	}
}

func (s *installedTab) pruneCmd(requestID uint64) tea.Cmd {
	run := s.runPrune
	return func() tea.Msg {
		if run == nil {
			return pruneDoneMsg{RequestID: requestID, Err: errors.New("no prune service configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), pruneTimeout)
		defer cancel()
		result, err := run(ctx)
		return pruneDoneMsg{RequestID: requestID, Result: result, Err: err}
	}
}

func (s *installedTab) diskUsageCmd() tea.Cmd {
	usage := s.diskUsage
	if usage == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diskUsageTimeout)
		defer cancel()
		summary, err := usage(ctx)
		return diskUsageMsg{Summary: summary, Err: err}
	}
}
