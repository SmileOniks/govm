package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	coredeps "github.com/SmileOniks/govm/internal/deps"
)

func TestDepsTabArrowAndVimKeysMoveTableCursor(t *testing.T) {
	m := loadDeps(t, newTestModel(t), []coredeps.ModuleDependency{
		{Path: "example.com/first", Version: "v1.0.0"},
		{Path: "example.com/second", Version: "v1.0.0"},
	})

	for _, tt := range []struct {
		name string
		key  tea.KeyPressMsg
		want int
	}{
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}, want: 1},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}, want: 0},
		{name: "j", key: tea.KeyPressMsg{Code: 'j'}, want: 1},
		{name: "k", key: tea.KeyPressMsg{Code: 'k'}, want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updated, _ := m.Update(tt.key)
			m = updated.(Model)

			if got := m.deps.table.Cursor(); got != tt.want {
				t.Fatalf("expected deps table cursor to move to index %d, got %d", tt.want, got)
			}
		})
	}
}

func TestDepsTabDownDoesNotMoveTableCursorWhileConfirmingUpdate(t *testing.T) {
	m := openUpdateDialog(t, loadDeps(t, newTestModel(t), []coredeps.ModuleDependency{
		{Path: "example.com/first", Version: "v1.0.0", Latest: "v1.1.0"},
		{Path: "example.com/second", Version: "v1.0.0"},
	}))

	before := m.deps.table.Cursor()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)

	if got := m.deps.table.Cursor(); got != before {
		t.Fatalf("expected deps table cursor to remain at index %d while update dialog is open, got %d", before, got)
	}
}

func TestDepsTabGlobalUOpensUpdateConfirmationForDirectUpdate(t *testing.T) {
	m := loadDeps(t, newTestModel(t), []coredeps.ModuleDependency{
		{Path: "example.com/direct", Version: "v1.0.0", Latest: "v1.1.0"},
	})

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'u'})
	m = updated.(Model)
	updated, _ = m.Update(coredeps.CheckUpdatesDoneEvent{Dependencies: m.deps.dependencies})
	m = updated.(Model)

	if m.deps.dialog.kind != dialogUpdate {
		t.Fatal("expected fresh preflight to open the update confirmation")
	}
}
