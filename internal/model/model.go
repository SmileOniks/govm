package model

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/application"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/deps"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

type changeDistributionSourceFunc func(context.Context, string) (application.DistributionSourceResult, error)

// defaultConstructionWidth is the initial column budget passed to the
// table-column helpers when a real WindowSizeMsg has not arrived yet.
// It mirrors the fallback returned by Model.viewWidth.
const defaultConstructionWidth = 80

const (
	AvailableTab = iota
	InstalledTab
	DepsTab
	SettingsTab
	tabCount
)

type Model struct {
	projection catalogProjectionAdapter

	Spinner    spinner.Model
	CurrentTab int
	// Status owns the status triplet (text, kind, scope) as the
	// StatusLine value-type module. Reads go through Text/Kind/Scope;
	// mutations go through SetTab/SetGlobal/Clear/ClearTab.
	Status StatusLine
	// ShimPathWarning is the pre-rendered PATH warning captured before
	// launching the TUI, so View does not resolve PATH on every render.
	ShimPathWarning  string
	ConfirmingDelete bool
	DeleteVersion    string
	// HelpVisible reports whether the Help overlay (opened with "?")
	// is showing. While it is open every key except ?, esc, and
	// ctrl+c is swallowed, so no action fires underneath it.
	HelpVisible bool
	// Prune owns the prune flow (phase plus the plan awaiting
	// confirmation) as the PruneState value-type module.
	Prune      PruneState
	DiskUsage  prune.Summary
	Width      int
	Height     int
	TermWidth  int
	TermHeight int
	Layout     styles.LayoutMode

	// theme is the immutable rendering snapshot used by View and every
	// renderer. main.go builds it once from settings at startup and
	// applyRuntimeTheme rebuilds it when the user toggles themes in the
	// Settings tab. It is the only source of styling for the model —
	// the styles package no longer carries any package-level state.
	theme styles.Theme

	// Settings groups the Settings tab (see ADR-0004); use the entry
	// points in settings_tab.go.
	settings settingsTab
	deps     depsTab

	checkUpgrade        checkUpgradeFunc
	installGo           installFunc
	installWithProgress installProgressFunc
	activateGo          activateFunc
	deleteGo            deleteFunc
	previewPrune        previewPruneFunc
	runPrune            pruneFunc
	diskUsage           diskUsageFunc
	shimInPath          func() bool

	// upgradeCheck and upgradeNotice implement the Upgrade notice: the
	// session's single Latest release lookup and the tag it produced
	// (empty while nothing is shown). See upgrade_cmd.go.
	upgradeCheck  upgradeCheckPhase
	upgradeNotice string
}

type programModel struct {
	model          Model
	lastRefreshKey time.Time
}

const refreshKeyRepeatWindow = 750 * time.Millisecond

func NewProgramModel(model Model) tea.Model {
	return newProgramModel(model)
}

func FilterProgramMessage(current tea.Model, msg tea.Msg) tea.Msg {
	program, ok := current.(*programModel)
	if !ok {
		return msg
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || key.String() != "r" {
		return msg
	}
	now := time.Now()
	repeated := key.IsRepeat ||
		(!program.lastRefreshKey.IsZero() && now.Sub(program.lastRefreshKey) < refreshKeyRepeatWindow)
	program.lastRefreshKey = now
	if repeated || program.model.refreshInFlight() {
		// In a text-entry context r is ordinary input: the repeat
		// suppression must not eat the second r of a fast "rr".
		if program.model.inputContext().textEntry() {
			return msg
		}
		return nil
	}
	return msg
}

func newProgramModel(model Model) *programModel {
	return &programModel{model: model}
}

func (m *programModel) Init() tea.Cmd {
	return m.model.Init()
}

func (m *programModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.model.update(msg)
	return m, cmd
}

func (m *programModel) View() tea.View {
	return m.model.View()
}

// Theme returns the Model's current immutable theme snapshot. It is
// read-only access for tests and helpers that need to render outside
// of View(); production rendering goes through m.theme directly.
func (m Model) Theme() styles.Theme { return m.theme }

// New builds the top-level Model for the TUI. It owns the invariant
// setup that every caller needs: the spinner, the installed-versions
// table (columns + height + styles), the version list and its
// delegate, the Deps tab and the Settings tab. The settings store is
// bound here, once: a Model without it cannot exist, so there is no
// unbound branch to defend against.
//
// The theme parameter is the immutable styles.Theme value built by the
// caller (main.go at startup, the theme effect on toggle, tests
// directly). Passing it as a parameter replaces the previous implicit
// "call ApplyTheme before New" contract — it is now impossible to
// construct a Model without its theme, which removes the fragile
// init() → ApplyTheme → New ordering that previously broke silently
// when a new dialog style was added.
func New(moduleDir string, settings config.Settings, store config.Store, shimPathWarning string, theme styles.Theme) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = theme.SpinnerStyle

	projection := newCatalogProjectionAdapter(theme)
	projection.prepareInitialLoad()

	m := Model{
		projection:      projection,
		Spinner:         sp,
		Layout:          styles.LayoutNormal,
		theme:           theme,
		deps:            newDepsTab(moduleDir, theme),
		settings:        newSettingsTab(settings, store),
		ShimPathWarning: shimPathWarning,
	}
	m.deps.applySettings(m.settings.values)
	return m
}

// VersionOperations contains the narrow process-composed seams used by the
// TUI for installed-version mutations and PATH presentation.
type VersionOperations struct {
	LoadCatalog         loadCatalogFunc
	DistributionSource  changeDistributionSourceFunc
	CheckUpgrade        checkUpgradeFunc
	Install             installFunc
	InstallWithProgress installProgressFunc
	Activate            activateFunc
	Delete              deleteFunc
	PreviewPrune        previewPruneFunc
	Prune               pruneFunc
	DiskUsage           diskUsageFunc
	ShimInPath          func() bool
}

// BindVersionOperations returns a copy of m bound to process-wide services.
func (m Model) BindVersionOperations(operations VersionOperations) Model {
	m.projection.loadCatalog = operations.LoadCatalog
	m.projection.distributionSource = operations.DistributionSource
	m.checkUpgrade = operations.CheckUpgrade
	m.installGo = operations.Install
	m.installWithProgress = operations.InstallWithProgress
	m.activateGo = operations.Activate
	m.deleteGo = operations.Delete
	m.previewPrune = operations.PreviewPrune
	m.runPrune = operations.Prune
	m.diskUsage = operations.DiskUsage
	m.shimInPath = operations.ShimInPath
	return m
}

// BindDeps returns a copy of m whose Deps tab uses the given executor
// factory. The factory receives the current backup limit before every
// operation, so a mid-session limit change in Settings is honoured;
// production wraps a single deps.Executor, tests a fake. Without it
// every dependency operation reports that it is unavailable.
func (m Model) BindDeps(executor func(backupLimit int) deps.API) Model {
	m.deps.newExecutor = executor
	return m
}

func (m Model) Init() tea.Cmd {
	var usage tea.Cmd
	if m.diskUsage != nil {
		usage = m.diskUsageCmd()
	}
	return tea.Batch(
		m.projection.init(),
		usage,
		m.initialUpgradeCheckCmd(),
		m.Spinner.Tick,
	)
}

func (m Model) viewHeight() int {
	if m.Height > 0 {
		return m.Height
	}
	available := m.projection.availableModel()
	if available.Height() > 0 {
		return available.Height()
	}
	return 24
}

// inMinimumViewport mirrors the View() condition that replaces the
// whole UI with the minimum-size warning. The Help overlay does not
// open there: it would not render, and silently remembering an open
// overlay across a resize would be surprising.
func (m Model) inMinimumViewport() bool {
	if m.TermWidth > 0 || m.TermHeight > 0 || (m.Width == 1 && m.Height == 1) {
		return m.TermWidth < styles.MinTermWidth || m.TermHeight < styles.MinTermHeight
	}
	return false
}

func (m Model) viewWidth() int {
	if m.Width > 0 {
		return m.Width
	}
	available := m.projection.availableModel()
	if available.Width() > 0 {
		return available.Width()
	}
	return 80
}
