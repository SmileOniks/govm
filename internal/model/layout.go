package model

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

type viewChrome struct {
	header   renderedSurface
	tabs     renderedSurface
	warning  renderedSurface
	summary  renderedSurface
	status   renderedSurface
	controls renderedSurface
}

func (m *Model) chrome(width int) viewChrome {
	t := m.theme
	chrome := viewChrome{
		header: renderedSurface{content: renderHeader(t, width, utils.GetVersion(), m.upgradeNotice)},
		tabs:   renderTabs(t, m.CurrentTab),
	}
	ctx := m.inputContext()
	if ctx == inputTab || ctx == inputFilter || ctx == inputDeleteConfirm {
		chrome.controls = renderHelpBar(t, *m, width)
	}
	if m.ShimPathWarning != "" {
		chrome.warning.content = renderStatus(t, "warning", m.ShimPathWarning, width)
	}
	if m.CurrentTab == InstalledTab {
		if summary := m.installed.summaryView(); summary != "" {
			chrome.summary.content = lipgloss.NewStyle().Width(width).Render(summary)
		}
	}
	if status, kind := m.composeStatus(); status != "" {
		chrome.status.content = renderStatus(t, kind, status, width)
	}
	return chrome
}

func (m *Model) relayout() {
	if !m.mouseWindowKnown && m.TermWidth <= 0 && m.TermHeight <= 0 {
		return
	}
	frameH, frameV := styles.FrameOverhead(m.Layout)
	width := max(1, m.TermWidth-frameH)
	chrome := m.chrome(width)
	chromeHeight := 0
	for _, fragment := range []renderedSurface{chrome.header, chrome.tabs, chrome.warning, chrome.summary, chrome.status, chrome.controls} {
		if fragment.content != "" {
			chromeHeight += lipgloss.Height(fragment.content)
		}
	}
	height := max(0, m.TermHeight-frameV-chromeHeight)
	filterHeight := 0
	if indicator := m.renderAppliedFilterLine(m.theme, width); indicator != "" {
		filterHeight = lipgloss.Height(indicator)
	}
	availableHeight := max(0, height-filterHeight)
	if m.Width == width && m.Height == height && m.projection.availableModel().Height() == availableHeight {
		return
	}
	m.Width, m.Height = width, height
	m.projection.resize(width, availableHeight, height)
	m.deps.resize(width, height)
}

func joinSurfaces(fragments ...renderedSurface) renderedSurface {
	var content strings.Builder
	contentSize, targetCount := 0, 0
	for _, fragment := range fragments {
		contentSize += len(fragment.content) + 1
		targetCount += len(fragment.targets)
	}
	content.Grow(contentSize)
	targets := make([]mouseTarget, 0, targetCount)
	y := 0
	for _, fragment := range fragments {
		if fragment.content == "" {
			continue
		}
		if y > 0 {
			content.WriteByte('\n')
		}
		content.WriteString(fragment.content)
		for _, target := range fragment.targets {
			target.rect.y += y
			targets = append(targets, target)
		}
		y += lipgloss.Height(fragment.content)
	}
	return renderedSurface{content: content.String(), targets: targets}
}

func contentCanvas(surface renderedSurface, width, height int) renderedSurface {
	surface = surface.translated(0, 0, cellRect{width: width, height: height})
	surface.content = renderContentCanvas(surface.content, width, height)
	return surface
}
