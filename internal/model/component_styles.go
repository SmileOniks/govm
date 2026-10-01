package model

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

// tableStyles returns the table.Styles pair (header/selected/cell) for
// the given theme. It is called from Model.New and from
// applyRuntimeTheme when the theme changes.
func tableStyles(t styles.Theme) table.Styles {
	return table.Styles{
		Header:   t.TableHeaderStyle,
		Selected: t.TableSelectedStyle,
		Cell:     t.TableCellStyle,
	}
}

// listDefaultDelegate returns the bubbles/list delegate configured for
// the given theme. As with tableStyles, callers are Model.New and
// applyRuntimeTheme.
func listDefaultDelegate(t styles.Theme) catalogItemDelegate {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = t.TableSelectedStyle
	delegate.Styles.SelectedDesc = t.TableSelectedStyle
	delegate.Styles.NormalDesc = t.MutedStyle
	return catalogItemDelegate{
		DefaultDelegate: delegate,
		versionStyle:    t.ItemVersionStyle,
	}
}

// catalogItemDelegate renders the Available list rows. It reuses
// list.DefaultDelegate for Height, Spacing, Update and the style set,
// but owns Render: the default Render highlights filter matches with
// lipgloss.StyleRunes over Item.Title(), using rune indices that the
// fuzzy matcher computed against the plain FilterValue. Our title is
// pre-rendered with ANSI codes, so those indices land inside escape
// sequences and split them (rune 0 is ESC), leaking fragments such as
// "[1;38;2;229;231;235m" into the row. This Render highlights the
// plain Name instead and appends the pre-rendered badges afterwards.
type catalogItemDelegate struct {
	list.DefaultDelegate
	versionStyle lipgloss.Style
}

func (d catalogItemDelegate) headerHeight(m list.Model) int {
	if !m.ShowTitle() && !(m.ShowFilter() && m.FilteringEnabled()) {
		return 0
	}
	content := ""
	if m.ShowFilter() && m.FilterState() == list.Filtering {
		content = m.Styles.TitleBar.Render(m.FilterInput.View())
	} else if m.ShowTitle() {
		content = m.Styles.TitleBar.Render(m.Styles.Title.Render(m.Title))
	}
	return lipgloss.Height(content)
}

const listEllipsis = "…"

// Render mirrors list.DefaultDelegate.Render except for how a filtered
// row's title is produced (see the type comment).
func (d catalogItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(styles.Item)
	if !ok || m.Width() <= 0 {
		return
	}
	s := &d.Styles

	var (
		isSelected  = index == m.Index()
		filtering   = m.FilterState() == list.Filtering
		emptyFilter = filtering && m.FilterValue() == ""
		isFiltered  = filtering || m.FilterState() == list.FilterApplied
	)

	title := it.Title()
	if isFiltered && !emptyFilter {
		unmatched := d.versionStyle.Inline(true)
		matched := unmatched.Inherit(s.FilterMatch)
		title = lipgloss.StyleRunes(it.Name, m.MatchesForItem(index), matched, unmatched)
		if it.RenderedBadges != "" {
			title += " " + it.RenderedBadges
		}
	}

	textwidth := m.Width() - s.NormalTitle.GetPaddingLeft() - s.NormalTitle.GetPaddingRight()
	title = ansi.Truncate(title, textwidth, listEllipsis)

	desc := it.Description()
	if d.ShowDescription {
		var lines []string
		for i, line := range strings.Split(desc, "\n") {
			if i >= d.Height()-1 {
				break
			}
			lines = append(lines, ansi.Truncate(line, textwidth, listEllipsis))
		}
		desc = strings.Join(lines, "\n")
	}

	switch {
	case emptyFilter:
		title = s.DimmedTitle.Render(title)
		desc = s.DimmedDesc.Render(desc)
	case isSelected && !filtering:
		title = s.SelectedTitle.Render(title)
		desc = s.SelectedDesc.Render(desc)
	default:
		title = s.NormalTitle.Render(title)
		desc = s.NormalDesc.Render(desc)
	}

	if d.ShowDescription {
		fmt.Fprintf(w, "%s\n%s", title, desc) //nolint:errcheck
		return
	}
	fmt.Fprint(w, title) //nolint:errcheck
}
