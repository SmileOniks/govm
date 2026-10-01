package model

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// testTheme returns the canonical theme used by model tests. It is a
// pure value (no global state), so tests can run in parallel and can
// pass the same theme to both Model.New and depsDialog.Render.
func testTheme() styles.Theme {
	return styles.NewTheme(config.ThemeCurrent)
}

// seedVersions replaces the model's version catalog with the given
// versions through the invariant-preserving replaceVersions path. It
// fails the test on error. The returned tea.Cmd is deliberately not
// executed: seeding is a synchronous state transition and executing
// the command could trigger network or async side effects that break
// test determinism. Tests that need to observe command behaviour
// dispatch the originating msg through Update instead.
//
// This is the single entry point for populating versions in tests —
// never mutate catalog slices or widget items directly.
func seedVersions(t testing.TB, m *Model, versions []utils.GoVersion) {
	t.Helper()
	outcome := catalogProjectionOutcome{}
	if m.projection.initialLoad.ID != 0 && m.projection.operationPhase() == catalogOperationPhaseLoading {
		outcome = m.projection.acceptLoad(m.projection.initialLoad.ID, versions)
		m.projection.initialLoad = catalogLoadRequest{}
	} else {
		outcome = m.projection.replaceSnapshot(versions)
	}
	if outcome.kind == catalogProjectionOutcomeRejected {
		t.Fatalf("replaceSnapshot(%+v): %v", versions, outcome.err)
	}
}

func replaceVersions(m *Model, versions []utils.GoVersion) (tea.Cmd, error) {
	outcome := m.projection.replaceSnapshot(versions)
	return outcome.cmd, outcome.err
}

func selectedListVersion(m Model) string {
	selected := m.projection.selectedAvailableItem()
	if selected == nil {
		return ""
	}
	return selected.Name
}

func setInstalledCursor(m *Model, cursor int) {
	for m.projection.installedModel().Cursor() < cursor {
		m.projection.updateInstalled(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	for m.projection.installedModel().Cursor() > cursor {
		m.projection.updateInstalled(tea.KeyPressMsg{Code: tea.KeyUp})
	}
}

func catalogRequestID(t *testing.T, cmd tea.Cmd) uint64 {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected catalog load command")
	}
	msg, ok := cmd().(catalogLoadFailedMsg)
	if !ok {
		t.Fatalf("catalog load command returned %T, want catalogLoadFailedMsg", msg)
	}
	return msg.RequestID
}

// assertVersionViewsConsistent verifies the postcondition that the
// Available Versions list and the Installed Versions table must be
// exact projections of the private catalog. It reads the catalog
// through the projection()/lookup() API rather than touching backing
// records, and asserts that every Available list item's fields and
// pre-rendered title match the source version, and that every Installed
// row matches its installed version.
//
// Used after msg-dispatch in update tests to catch any handler that
// mutates the catalog without keeping the derived views in sync.
func assertVersionViewsConsistent(t *testing.T, m Model) {
	t.Helper()

	proj := m.projection.projection()
	items := m.projection.availableModel().Items()
	if len(items) != len(proj.available) {
		t.Fatalf("list length = %d, want %d (catalog projection)", len(items), len(proj.available))
	}

	for i, it := range items {
		got, ok := it.(styles.Item)
		if !ok {
			t.Fatalf("list item %d is %T, want styles.Item", i, it)
		}
		want, ok := proj.available[i].(styles.Item)
		if !ok {
			t.Fatalf("catalog list item %d is %T, want styles.Item", i, proj.available[i])
		}
		if got != want {
			t.Errorf("list item %d = %+v, want %+v", i, got, want)
		}
		// Cross-check via lookup so the assertion does not rely on
		// exposed records but still verifies semantic correctness.
		v, found := m.projection.lookup(got.Name)
		if !found {
			t.Fatalf("list item %d Name %q not in catalog", i, got.Name)
		}
		if got.DescriptionText != v.DisplayDescription() {
			t.Errorf("list item %d DescriptionText = %q, want %q", i, got.DescriptionText, v.DisplayDescription())
		}
		if wantTitle := styles.RenderItemTitle(m.theme, v.Version, v.Installed, v.Active); got.RenderedTitle != wantTitle {
			t.Errorf("list item %d RenderedTitle = %q, want %q", i, got.RenderedTitle, wantTitle)
		}
	}

	rows := m.projection.installedModel().Rows()
	if len(rows) != len(proj.installed) {
		t.Fatalf("installed table rows = %d, want %d (catalog projection)", len(rows), len(proj.installed))
	}

	for i, row := range rows {
		wantRow := proj.installed[i]
		if len(row) != len(wantRow) {
			t.Fatalf("installed row %d length = %d, want %d", i, len(row), len(wantRow))
		}
		for j := range row {
			if row[j] != wantRow[j] {
				t.Errorf("installed row %d column %d = %q, want %q", i, j, row[j], wantRow[j])
			}
		}
		v, found := m.projection.lookup(row[0])
		if !found {
			t.Fatalf("installed row %d Version %q not in catalog", i, row[0])
		}
		if row[0] != v.Version {
			t.Errorf("installed row Version = %q, want %q", row[0], v.Version)
		}
		if row[1] != v.Path {
			t.Errorf("installed row Path = %q, want %q", row[1], v.Path)
		}
		if row[2] != formatDiskUsage(v.DiskUsage) {
			t.Errorf("installed row Size = %q, want %q", row[2], formatDiskUsage(v.DiskUsage))
		}
		wantStatus := ""
		if v.Active {
			wantStatus = "active"
		}
		if row[3] != wantStatus {
			t.Errorf("installed row Status = %q, want %q", row[3], wantStatus)
		}
	}
}

func newTestModel(t testing.TB) Model {
	t.Helper()

	home := t.TempDir()
	shim := filepath.Join(home, ".govm", "shim")
	if err := os.MkdirAll(shim, 0755); err != nil {
		t.Fatalf("create shim dir: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", shim)

	m := New(
		"",
		config.DefaultSettings(),
		newMemorySettingsStore(config.DefaultSettings()),
		"",
		testTheme(),
	)
	seedVersions(t, &m, []utils.GoVersion{{
		Version:   "1.24.4",
		Filename:  "go1.24.4.darwin-arm64.tar.gz",
		Installed: true,
		Active:    true,
		Path:      filepath.Join(home, ".govm", "versions", "go1.24.4"),
	}})
	m.Status.SetTab("Successfully installed Go 1.24.4", "success")
	m.Layout = styles.LayoutWide
	// A fake executor by default: no model test may reach the go
	// toolchain, and tests that drive the cycle rebind their own.
	fakeDepsExecutor{}.bind(&m)
	return m
}

// fakeDepsExecutor is the depsExecutor substitute for model tests.
// execute drives the update cycle; the standalone operations return
// zero values. bind installs it on a Model regardless of backup limit.
type fakeDepsExecutor struct {
	execute func(deps.Intent) (deps.Event, error)
}

// memorySettingsStore is the in-memory config.Store adapter for
// tests: it records the last saved values and can be told to fail.
type memorySettingsStore struct {
	values config.Settings
	err    error
}

func newMemorySettingsStore(values config.Settings) *memorySettingsStore {
	return &memorySettingsStore{values: config.Normalize(values)}
}

func (s *memorySettingsStore) Save(settings config.Settings) error {
	if s.err != nil {
		return s.err
	}
	s.values = config.Normalize(settings)
	return nil
}

func (f fakeDepsExecutor) Execute(intent deps.Intent) (deps.Event, error) {
	if f.execute != nil {
		return f.execute(intent)
	}
	return nil, nil
}

func (fakeDepsExecutor) List() ([]deps.ModuleDependency, error)         { return nil, nil }
func (fakeDepsExecutor) CheckUpdates() ([]deps.ModuleDependency, error) { return nil, nil }
func (fakeDepsExecutor) Backups() ([]deps.DependencyBackupInfo, error)  { return nil, nil }
func (fakeDepsExecutor) Restore(string) (deps.DependencyRestoreResult, error) {
	return deps.DependencyRestoreResult{}, nil
}

func (f fakeDepsExecutor) bind(m *Model) {
	*m = m.BindDeps(func(int) deps.API { return f })
}

// focusSetting drives the Settings tab to the given row by pressing
// the navigation keys, so tests reach a row the way the user does
// (ADR-0004: tests go through the tab's interface, not its fields).
func focusSetting(t testing.TB, m Model, row settingsRowKind) Model {
	t.Helper()
	if m.CurrentTab != SettingsTab {
		m.CurrentTab = SettingsTab
	}
	for range settingRowCount {
		if m.settings.cursor == row {
			return m
		}
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("setting row %d not found from cursor %d", row, m.settings.cursor)
	return m
}

// settingsStore returns the Model's in-memory settings store, the
// second config.Store adapter (production binds config.FileStore).
func settingsStore(m Model) *memorySettingsStore {
	return m.settings.store.(*memorySettingsStore)
}

// runCatalogTestCmd delivers a finite command chain, including refilter and
// disk-usage followups. Active progress waits must be driven explicitly.
func runCatalogTestCmd(t testing.TB, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
		return m
	case tea.BatchMsg:
		for _, child := range msg {
			m = runCatalogTestCmd(t, m, child)
		}
		return m
	default:
		updated, next := m.Update(msg)
		return runCatalogTestCmd(t, updated.(Model), next)
	}
}
