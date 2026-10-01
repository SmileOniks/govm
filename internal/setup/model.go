package setup

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/paths"
)

type Model struct {
	width        int
	height       int
	shimPath     string
	instructions viewport.Model
	footer       string
	footerWidth  int
}

type continueMsg struct{}

type scrollMsg struct {
	delta int
}

type shimDirResolver interface {
	ShimDir() (string, error)
}

func New() (Model, error) {
	return newWithResolver(paths.New())
}

func newWithResolver(resolver shimDirResolver) (Model, error) {
	shimPath, err := resolver.ShimDir()
	if err != nil {
		return Model{}, err
	}

	m := Model{
		shimPath: shimPath,
		width:    80,
		height:   24,
		instructions: viewport.New(
			viewport.WithWidth(80),
			viewport.WithHeight(22),
		),
	}
	m.instructions.FillHeight = true
	m.instructions.SoftWrap = true
	m.instructions.MouseWheelEnabled = false
	m.rebuildViewport()
	return m, nil
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		// Only semantic messages from the displayed frame may act on setup.
		return m, nil
	case continueMsg:
		return m, tea.Quit
	case scrollMsg:
		if msg.delta < 0 {
			m.instructions.ScrollUp(1)
		} else {
			m.instructions.ScrollDown(1)
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter", "space":
			return m, tea.Quit
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		if m.width != msg.Width || m.height != msg.Height {
			m.width = max(0, msg.Width)
			m.height = max(0, msg.Height)
			m.rebuildViewport()
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.width <= 0 || m.height <= 0 {
		return v
	}

	instructionsHeight := m.instructions.Height()
	content := m.footer
	if instructionsHeight > 0 {
		content = m.instructions.View() + "\n" + content
	}
	v.Content = content
	v.MouseMode = tea.MouseModeCellMotion

	// Capture only immutable coordinates from this displayed frame.
	width := m.width
	footerX := (width - m.footerWidth) / 2
	footerY := m.height - 1
	footerWidth := m.footerWidth
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		if mouse.Mod != 0 || mouse.X < 0 || mouse.X >= width {
			return nil
		}
		switch msg := msg.(type) {
		case tea.MouseClickMsg:
			onFooter := mouse.Y == footerY && mouse.X >= footerX && mouse.X < footerX+footerWidth
			if msg.Button == tea.MouseLeft && onFooter {
				return func() tea.Msg { return continueMsg{} }
			}
		case tea.MouseWheelMsg:
			if mouse.Y < 0 || mouse.Y >= instructionsHeight {
				return nil
			}
			switch msg.Button {
			case tea.MouseWheelUp:
				return func() tea.Msg { return scrollMsg{delta: -1} }
			case tea.MouseWheelDown:
				return func() tea.Msg { return scrollMsg{delta: 1} }
			}
		}
		return nil
	}
	return v
}

func (m *Model) rebuildViewport() {
	m.instructions.SetWidth(m.width)
	if m.width <= 0 || m.height <= 0 {
		m.instructions.SetHeight(0)
		m.footer = ""
		m.footerWidth = 0
		return
	}

	button := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Render(ansi.Truncate("[Enter continue]", m.width, ""))
	m.footerWidth = ansi.StringWidth(button)
	m.footer = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, button)
	if m.height > 1 {
		m.footer = strings.Repeat(" ", m.width) + "\n" + m.footer
	}
	footerHeight := lipgloss.Height(m.footer)
	m.instructions.SetHeight(max(0, m.height-footerHeight))
	m.instructions.SetContent(m.renderInstructions())
	m.instructions.SetYOffset(m.instructions.YOffset())
}

func (m Model) renderInstructions() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#3c71a8")).
		MarginBottom(1).
		Width(min(m.width, lipgloss.Width("GoVM First-Time Setup"))).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("#3c71a8")).
		PaddingBottom(1)

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3c71a8")).
		Padding(1, 2).
		Width(max(1, min(m.width-4, 80)))

	highlightStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3c71a8")).
		Bold(true)

	title := titleStyle.Render("GoVM First-Time Setup")

	var setupInstructions string
	if runtime.GOOS == "windows" {
		setupInstructions = fmt.Sprintf(`To use GoVM, you need to add this directory to your PATH:

%s

You can do this by running this command in Command Prompt:

%s

After adding to PATH, restart your terminal.`,
			highlightStyle.Render(m.shimPath),
			highlightStyle.Render(fmt.Sprintf("setx PATH \"%%PATH%%;%s\"", m.shimPath)))
	} else {
		shellConfigFile := "~/.bashrc"

		if strings.Contains(os.Getenv("SHELL"), "zsh") {
			shellConfigFile = "~/.zshrc"
		}

		setupInstructions = fmt.Sprintf(`To use GoVM, you need to add this directory to your PATH:

%s

Option 1: Run this command to add it automatically:

%s

Option 2: Or manually add this line to your %s:

%s

After adding to PATH, restart your terminal or run:

%s`,
			highlightStyle.Render(m.shimPath),
			highlightStyle.Render(fmt.Sprintf("echo 'export PATH=\"$HOME/.govm/shim:$PATH\"' >> %s", shellConfigFile)),
			shellConfigFile,
			highlightStyle.Render(fmt.Sprintf("export PATH=\"$HOME/.govm/shim:$PATH\"")),
			highlightStyle.Render(fmt.Sprintf("source %s", shellConfigFile)))
	}

	box := boxStyle.Render(setupInstructions)

	paddingTop := max(0, (m.instructions.Height()-lipgloss.Height(title)-lipgloss.Height(box))/2)
	return strings.Repeat("\n", paddingTop) + lipgloss.PlaceHorizontal(
		m.width,
		lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, title, box),
	)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
