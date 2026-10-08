package model

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/deps"
	"github.com/SmileOniks/govm/internal/styles"
)

// Bubble Tea v2.0.10's renderer equality ignores OnMouse and retains its previous
// callback when only the frame revision changes. A standard OSC 8 close with an
// epoch ID changes Content without producing a cell or leaving a hyperlink open,
// so the renderer publishes the callback even when every visible cell is equal.
func mouseFrameContent(content string, revision uint64) string {
	var epoch [20]byte
	digits := strconv.AppendUint(epoch[:0], revision, 10)
	return content + "\x1b]8;id=govm-mouse-" + string(digits) + ";\x1b\\"
}

type cellRect struct{ x, y, width, height int }

func (r cellRect) contains(x, y int) bool {
	return r.width > 0 && r.height > 0 && x >= r.x && y >= r.y && x < r.x+r.width && y < r.y+r.height
}

func (r cellRect) intersect(other cellRect) cellRect {
	x, y := max(r.x, other.x), max(r.y, other.y)
	return cellRect{x: x, y: y, width: max(0, min(r.x+r.width, other.x+other.width)-x), height: max(0, min(r.y+r.height, other.y+other.height)-y)}
}

type renderedSurface struct {
	content string
	targets []mouseTarget
}
type mouseTarget struct {
	rect   cellRect
	action mouseAction
}
type mouseActionKind uint8

const (
	mouseKey mouseActionKind = iota
	mouseTab
	mouseAvailableRow
	mouseInstalledRow
	mouseDependencyRow
	mouseDependencyMark
	mouseSettingRow
	mouseSettingActivate
	mouseSettingStep
	mouseDialogLevel
	mouseDialogScope
	mouseBackupRow
	mouseScroll
)

type mouseAction struct {
	kind     mouseActionKind
	key      tea.KeyPressMsg
	index    int
	identity string
	path     string
	delta    int
	level    deps.UpdateLevel
	explicit bool
}

type mouseActionMsg struct {
	action     mouseAction
	revision   uint64
	tab        int
	context    inputContext
	dialogKind depsDialogKind
}

// mouseCallback owns only values from the displayed frame, never the live Model.
func mouseCallback(surface renderedSurface, frame mouseActionMsg) func(tea.MouseMsg) tea.Cmd {
	targets := surface.targets
	return func(msg tea.MouseMsg) tea.Cmd {
		captured := frame
		event := msg.Mouse()
		if event.Mod != 0 {
			return nil
		}
		var kind mouseActionKind
		switch msg.(type) {
		case tea.MouseClickMsg:
			if event.Button != tea.MouseLeft {
				return nil
			}
		case tea.MouseWheelMsg:
			kind = mouseScroll
			switch event.Button {
			case tea.MouseWheelUp:
				captured.action.delta = -1
			case tea.MouseWheelDown:
				captured.action.delta = 1
			default:
				return nil
			}
		default:
			return nil
		}
		for i := len(targets) - 1; i >= 0; i-- {
			target := targets[i]
			if !target.rect.contains(event.X, event.Y) {
				continue
			}
			if kind == mouseScroll {
				if target.action.kind != mouseScroll {
					continue
				}
				delta := captured.action.delta
				captured.action = target.action
				captured.action.delta = delta
			} else {
				if target.action.kind == mouseScroll {
					continue
				}
				captured.action = target.action
			}
			return func() tea.Msg { return captured }
		}
		return nil
	}
}

func (m *Model) handleMouse(msg mouseActionMsg) (tea.Model, tea.Cmd) {
	if m.inMinimumViewport() {
		return m, nil
	}
	if msg.tab != m.CurrentTab || msg.context != m.inputContext() {
		return m, nil
	}
	if msg.action.kind == mouseScroll {
		if msg.context == inputDepsDialog && msg.dialogKind != m.deps.dialog.kind {
			return m, nil
		}
	} else if msg.revision != m.mouseRevision {
		return m, nil
	}
	switch msg.action.kind {
	case mouseAvailableRow:
		if msg.context != inputTab || m.CurrentTab != AvailableTab {
			return m, nil
		}
		visible := m.projection.list.VisibleItems()
		index := msg.action.index
		if index < 0 || index >= len(visible) {
			return m, nil
		}
		item, ok := visible[index].(styles.Item)
		if !ok || item.Name != msg.action.identity {
			return m, nil
		}
		if m.projection.selectAvailableVersion(msg.action.identity) {
			m.mouseRevision++
		}
	case mouseInstalledRow:
		if msg.context != inputTab || m.CurrentTab != InstalledTab {
			return m, nil
		}
		rows, index := m.projection.installedTable.Rows(), msg.action.index
		if index < 0 || index >= len(rows) || len(rows[index]) == 0 {
			return m, nil
		}
		if rows[index][0] != msg.action.identity {
			return m, nil
		}
		m.projection.installedTable.SetCursor(index)
		m.mouseRevision++
	case mouseDependencyRow, mouseDependencyMark:
		if msg.context != inputTab || m.CurrentTab != DepsTab {
			return m, nil
		}
		index := msg.action.index
		if index < 0 || index >= len(m.deps.rowPaths) {
			return m, nil
		}
		if m.deps.rowPaths[index] != msg.action.identity {
			return m, nil
		}
		m.deps.table.SetCursor(index)
		m.mouseRevision++
		if msg.action.kind == mouseDependencyMark {
			return m.dispatchKey(tea.KeyPressMsg{Code: tea.KeySpace})
		}
	case mouseSettingRow, mouseSettingActivate, mouseSettingStep:
		if msg.context != inputTab || m.CurrentTab != SettingsTab {
			return m, nil
		}
		index := msg.action.index
		if index < 0 || index >= len(settingRows) {
			return m, nil
		}
		m.settings.cursor = settingRows[index]
		m.mouseRevision++
		if msg.action.kind == mouseSettingActivate {
			return m.dispatchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		if msg.action.kind == mouseSettingStep {
			code := tea.KeyRight
			if msg.action.delta < 0 {
				code = tea.KeyLeft
			}
			return m.dispatchKey(tea.KeyPressMsg{Code: code})
		}
	case mouseDialogLevel:
		if msg.context != inputDepsDialog || m.deps.dialog.kind != dialogUpdate {
			return m, nil
		}
		if m.deps.dialog.level == msg.action.level {
			return m, nil
		}
		cmd, status := m.deps.selectDialogLevel(msg.action.level)
		m.applyDepsStatus(status)
		m.mouseRevision++
		return m, cmd
	case mouseDialogScope:
		if msg.context != inputDepsDialog || !m.deps.dialog.canToggleScope() {
			return m, nil
		}
		if m.deps.dialog.explicit == msg.action.explicit {
			return m, nil
		}
		cmd, status := m.deps.selectDialogScope(msg.action.explicit)
		m.applyDepsStatus(status)
		m.mouseRevision++
		return m, cmd
	case mouseBackupRow:
		if msg.context != inputDepsDialog || m.deps.dialog.kind != dialogRestore {
			return m, nil
		}
		index := msg.action.index
		if index < 0 || index >= len(m.deps.backups) {
			return m, nil
		}
		backup := m.deps.backups[index]
		if backup.Name != msg.action.identity || backup.Path != msg.action.path {
			return m, nil
		}
		m.deps.dialog.cursor = index
		m.mouseRevision++
	case mouseScroll:
		if msg.context == inputDepsDialog {
			if m.deps.dialog.kind != dialogRestore {
				return m, nil
			}
			m.deps.dialog.cursor = max(0, min(m.deps.dialog.cursor+msg.action.delta, len(m.deps.backups)-1))
		} else {
			if msg.context != inputTab {
				return m, nil
			}
			switch m.CurrentTab {
			case AvailableTab:
				m.projection.moveAvailableSelection(msg.action.delta)
			case InstalledTab:
				m.projection.installedTable.Move(msg.action.delta)
			case DepsTab:
				m.deps.table.Move(msg.action.delta)
			case SettingsTab:
				if msg.action.delta < 0 {
					m.settings.moveUp()
				} else {
					m.settings.moveDown()
				}
			default:
				return m, nil
			}
		}
		m.mouseRevision++
	case mouseKey:
		m.mouseRevision++
		return m.dispatchKey(msg.action.key)
	case mouseTab:
		ctx := m.inputContext()
		if ctx != inputTab && ctx != inputFilter && ctx != inputDeleteConfirm {
			return m, nil
		}
		if msg.action.index == m.CurrentTab || msg.action.index < 0 || msg.action.index >= tabCount {
			return m, nil
		}
		m.mouseRevision++
		return m.switchTab(msg.action.index)
	}
	return m, nil
}

// Surfaces own their target slices until View publishes the callback. Transform
// that owned buffer in place rather than copying it at each composition layer.
func (s renderedSurface) translated(x, y int, clip cellRect) renderedSurface {
	targets := s.targets[:0]
	for _, target := range s.targets {
		target.rect.x += x
		target.rect.y += y
		target.rect = target.rect.intersect(clip)
		if target.rect.width > 0 && target.rect.height > 0 {
			targets = append(targets, target)
		}
	}
	s.targets = targets
	return s
}
