package model

import (
	"strings"
	"testing"
)

// TestRegistryGlobalBindsHelpOnEveryContext asserts the discoverability
// contract: the "?" hint appears in every context where the overlay
// can open — all tabs, all dialogs, and the confirmations.
func TestRegistryGlobalBindsHelpOnEveryContext(t *testing.T) {
	global := globalKeyBindings()
	if !hasBinding(global, "?") {
		t.Fatal("global section must document the ? key")
	}
	dialogGlobal := dialogGlobalKeyBindings()
	if !hasBinding(dialogGlobal, "?") {
		t.Fatal("dialog global section must document the ? key")
	}
	if hasBinding(dialogGlobal, "tab") || hasBinding(dialogGlobal, "shift+tab") {
		t.Fatal("dialog global section must not document tab keys: they toggle the choice inside dialogs")
	}
}

func hasBinding(section helpSection, keys string) bool {
	for _, binding := range section.bindings {
		if binding.keys == keys {
			return true
		}
	}
	return false
}

// TestRegistryDialogSectionsCoverAllKinds walks every dialog kind and
// asserts the shared choice keys plus the restore-only extras.
func TestRegistryDialogSectionsCoverAllKinds(t *testing.T) {
	kinds := []depsDialogKind{dialogUpdate, dialogChecks, dialogRollback, dialogRestore}
	for _, kind := range kinds {
		section := dialogKeyBindings(depsDialog{kind: kind})
		if section.title == "" || len(section.bindings) == 0 {
			t.Fatalf("dialog kind %d: expected titled non-empty section", kind)
		}
		for _, keys := range []string{"←/→ h/l", "enter", "y", "n / esc"} {
			if !hasBinding(section, keys) {
				t.Errorf("dialog kind %d: section missing %q binding", kind, keys)
			}
		}
	}

	restore := dialogKeyBindings(depsDialog{kind: dialogRestore, choiceYes: true})
	if !hasBinding(restore, "↑/↓ k/j") {
		t.Error("restore section must document backup navigation")
	}
	for _, tt := range []struct {
		choiceYes bool
		want      string
	}{
		{choiceYes: true, want: "restore"},
		{choiceYes: false, want: "cancel"},
	} {
		section := dialogKeyBindings(depsDialog{kind: dialogRestore, choiceYes: tt.choiceYes})
		found := false
		for _, binding := range section.bindings {
			if binding.keys == "enter" {
				found = binding.desc == tt.want
			}
		}
		if !found {
			t.Errorf("restore enter desc with ChoiceYes=%v = %q, want %q", tt.choiceYes, tt.want, tt.want)
		}
	}
}

// TestRegistryEditingSectionsHaveNoGlobalKeys pins the rule that the
// settings text inputs document only their own keys: the overlay
// cannot open there, so global bindings must not leak into their bar.
func TestRegistryEditingSectionsHaveNoGlobalKeys(t *testing.T) {
	for _, editingSource := range []bool{false, true} {
		section := editingKeyBindings(editingSource)
		for _, binding := range section.bindings {
			for _, forbidden := range []string{"?", "q", "tab"} {
				if strings.Contains(binding.keys, forbidden) {
					t.Errorf("editing section (%v) must not document %q: the overlay cannot open while an input has focus", editingSource, forbidden)
				}
			}
		}
	}
}
