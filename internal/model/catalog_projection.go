package model

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/install"
	"github.com/SmileOniks/govm/internal/lifecycle"
	"github.com/SmileOniks/govm/internal/styles"
	"github.com/SmileOniks/govm/internal/utils"
)

// catalogActivityKind describes the operation currently represented by the
// adapter. It is intentionally separate from catalogMutationKind: loading and
// reconciliation are adapter activities, not catalog mutations.
type catalogActivityKind int

const (
	catalogActivityIdle catalogActivityKind = iota
	catalogActivityLoading
	catalogActivityInstalling
	catalogActivityActivating
	catalogActivityDeleting
	catalogActivityReconciling
)

type catalogActivity struct {
	kind     catalogActivityKind
	version  string
	progress install.Progress
}

// installProgress returns the latest measurement reported by the active
// install, or ok == false when the operation has not reported one yet. The
// comma-ok form keeps the render layer from reading a measurement without
// first asking whether one exists.
func (a catalogActivity) installProgress() (install.Progress, bool) {
	if a.kind != catalogActivityInstalling || a.progress.Stage == install.StageUnknown {
		return install.Progress{}, false
	}
	return a.progress, true
}

type catalogOperationPhase int

const (
	catalogOperationPhaseIdle catalogOperationPhase = iota
	catalogOperationPhaseLoading
	catalogOperationPhaseMutating
	catalogOperationPhaseReconciling
)

type catalogMutationKind int

const (
	catalogMutationInstall catalogMutationKind = iota
	catalogMutationActivation
	catalogMutationDeletion
)

type catalogOperation struct {
	id                uint64
	kind              catalogMutationKind
	version           string
	installWarnings   []install.Warning
	lifecycleWarnings []lifecycle.Warning
	shimInPath        bool
}

type catalogReceipt struct {
	operation  catalogOperation
	reconciled bool
}

type catalogProjectionOutcomeKind int

const (
	catalogProjectionOutcomeNoop catalogProjectionOutcomeKind = iota
	catalogProjectionOutcomePublished
	catalogProjectionOutcomeLoadStarted
	catalogProjectionOutcomeReconciled
	catalogProjectionOutcomeStale
	catalogProjectionOutcomeRejected
	catalogProjectionOutcomeFailed
	catalogProjectionOutcomeSuppressed
	catalogProjectionOutcomeCommittedWarning
)

type catalogProjectionOutcome struct {
	kind        catalogProjectionOutcomeKind
	cmd         tea.Cmd
	loadRequest catalogLoadRequest
	receipt     catalogReceipt
	err         error
}

// catalogOperationState is the single carrier of what the adapter is doing.
// operation.id == 0 means no operation is in flight: startMutation
// pre-increments the counter, so a live id is always >= 1. The four
// transition methods below are the only writers of the phase; the sole
// other write is completeMutation storing its receipt on the operation
// already in flight.
//
// Invariants held by the type: phase == Reconciling implies operation.id != 0,
// because reconciliation continues the operation that started it; a progress
// measurement implies phase == Mutating on an install, because that is the
// only operation reporting one. Loading is a parallel axis and lives outside
// this value: a catalog refresh may run while a mutation is in flight, and
// then the phase stays mutational.
//
// The progress belongs here rather than on catalogOperation because the
// operation doubles as a receipt that outlives the flight, while progress is
// telemetry that is meaningless once the operation ends.
type catalogOperationState struct {
	phase     catalogOperationPhase
	operation catalogOperation
	progress  install.Progress
}

type catalogProjectionPendingRestore struct {
	active     bool
	generation uint64
	filterText string
	version    string
	index      int
}

type catalogProjectionRefilterMsg struct {
	matches    list.FilterMatchesMsg
	generation uint64
	filterText string
}

// catalogProjectionAdapter owns the shared operation lifecycle, canonical
// catalog and both render-facing widgets. The catalog is committed before a
// projection is published; widget updates form one transaction. An invalid snapshot
// or stale asynchronous result leaves both widgets and their prior selection
// unchanged.
type catalogProjectionAdapter struct {
	catalog             versionCatalog
	list                list.Model
	delegate            catalogItemDelegate
	installedTable      rowTable
	generation          uint64
	pendingRestore      catalogProjectionPendingRestore
	state               catalogOperationState
	refilterPending     bool
	load                catalogLoadRequest
	loadActive          bool
	nextOperationID     uint64
	nextLoadID          uint64
	initialLoad         catalogLoadRequest
	loadCatalog         loadCatalogFunc
	distributionSource  changeDistributionSourceFunc
	installGo           installFunc
	installWithProgress installProgressFunc
	activateGo          activateFunc
	deleteGo            deleteFunc
	shimInPath          func() bool
}

func (a *catalogProjectionAdapter) bindOperations(operations VersionOperations) {
	a.loadCatalog = operations.LoadCatalog
	a.distributionSource = operations.DistributionSource
	a.installGo = operations.Install
	a.installWithProgress = operations.InstallWithProgress
	a.activateGo = operations.Activate
	a.deleteGo = operations.Delete
	a.shimInPath = operations.ShimInPath
}

// newCatalogProjectionAdapter constructs a complete adapter from a theme.
// Keeping widget construction here lets Model integration depend on the
// adapter's role methods instead of owning a second catalog projection.
func newCatalogProjectionAdapter(theme styles.Theme) catalogProjectionAdapter {
	delegate := listDefaultDelegate(theme)
	available := list.New([]list.Item{}, delegate, 0, 0)
	available.Title = "Available Versions"
	available.FilterInput.Prompt = "Find: "
	// The widget opens its filter on "/" by default; govm exposes the
	// feature as "f" (see handleFilterKey). The default NextPage binding
	// also lists "f" and is matched first, so it must drop the key.
	available.KeyMap.Filter = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "find"))
	available.KeyMap.NextPage = key.NewBinding(key.WithKeys("right", "l", "pgdown"), key.WithHelp("→/l/pgdn", "next page"))
	available.SetShowTitle(false)
	available.SetShowStatusBar(false)
	available.SetShowHelp(false)
	available.SetShowPagination(false)

	installed := newRowTable(
		installedTableColumns(defaultConstructionWidth),
		defaultConstructionWidth,
		10,
		tableStyles(theme),
	)

	return catalogProjectionAdapter{
		catalog:        newVersionCatalog(theme),
		list:           available,
		delegate:       delegate,
		installedTable: installed,
	}
}

func (a *catalogProjectionAdapter) startLoad(purpose catalogLoadPurpose) catalogProjectionOutcome {
	if a.state.phase == catalogOperationPhaseReconciling {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	a.refilterPending = false
	a.pendingRestore = catalogProjectionPendingRestore{}
	request := a.registerLoad(purpose)
	if !a.hasActiveMutation() {
		a.beginLoad()
	}
	return catalogProjectionOutcome{
		kind:        catalogProjectionOutcomeLoadStarted,
		loadRequest: request,
	}
}

// startReconciliationLoad verifies the operation already in flight, so it
// reads that operation from the state instead of taking a second copy of it.
func (a *catalogProjectionAdapter) startReconciliationLoad() catalogProjectionOutcome {
	request := a.registerLoad(catalogLoadPurposeReconcile)
	a.beginReconcile()
	return catalogProjectionOutcome{
		kind:        catalogProjectionOutcomeLoadStarted,
		loadRequest: request,
		receipt:     catalogReceipt{operation: a.state.operation},
	}
}

// acceptLoad accepts only the latest known request. Older responses are
// dropped even when they arrive after a newer request has already published.
func (a *catalogProjectionAdapter) acceptLoad(requestID uint64, versions []utils.GoVersion) catalogProjectionOutcome {
	if !a.loadActive || requestID != a.load.ID {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	request := a.load
	reconciliationRequest := a.isReconciliationRequest(requestID)
	if a.state.phase == catalogOperationPhaseReconciling && !reconciliationRequest {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	a.invalidateLoad()

	changed, err := a.catalog.replace(versions)
	if err != nil {
		if !reconciliationRequest {
			if a.hasActiveMutation() {
				return catalogProjectionOutcome{
					kind: catalogProjectionOutcomeSuppressed,
					err:  err,
				}
			}
			a.toIdle()
			return catalogProjectionOutcome{
				kind: catalogProjectionOutcomeRejected,
				err:  err,
			}
		}
		return a.finishReconciliation(catalogProjectionOutcome{
			kind: catalogProjectionOutcomeRejected,
			err:  err,
		})
	}

	outcome := catalogProjectionOutcome{kind: catalogProjectionOutcomeNoop}
	if changed {
		outcome.kind = catalogProjectionOutcomePublished
		outcome.cmd = a.publish(a.catalog.projection())
	}

	if a.state.phase == catalogOperationPhaseReconciling {
		pending := a.state.operation
		if !a.verify(pending) {
			return a.finishReconciliation(catalogProjectionOutcome{
				kind: catalogProjectionOutcomeFailed,
				cmd:  outcome.cmd,
				err:  fmt.Errorf("operation %d could not be confirmed against the installed catalog", pending.id),
			})
		}
		a.toIdle()
		outcome.kind = catalogProjectionOutcomeReconciled
		outcome.receipt = catalogReceipt{
			operation:  pending,
			reconciled: true,
		}
		return outcome
	}

	if a.hasActiveMutation() {
		return outcome
	}
	if request.Purpose != catalogLoadPurposeReconcile {
		a.toIdle()
	}
	return outcome
}

func (a *catalogProjectionAdapter) failLoad(requestID uint64, err error) catalogProjectionOutcome {
	if !a.loadActive || requestID != a.load.ID {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	reconciliationRequest := a.isReconciliationRequest(requestID)
	if a.state.phase == catalogOperationPhaseReconciling && !reconciliationRequest {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	a.invalidateLoad()
	if err == nil {
		err = errors.New("catalog load failed")
	}
	if !reconciliationRequest && a.hasActiveMutation() {
		return catalogProjectionOutcome{
			kind: catalogProjectionOutcomeSuppressed,
			err:  err,
		}
	}
	if !reconciliationRequest {
		a.toIdle()
		return catalogProjectionOutcome{
			kind: catalogProjectionOutcomeFailed,
			err:  err,
		}
	}
	return a.finishReconciliation(catalogProjectionOutcome{
		kind: catalogProjectionOutcomeFailed,
		err:  err,
	})
}

// replaceSnapshot applies a synchronous snapshot without request
// correlation. Asynchronous callers should use acceptLoad instead.
func (a *catalogProjectionAdapter) replaceSnapshot(versions []utils.GoVersion) catalogProjectionOutcome {
	changed, err := a.catalog.replace(versions)
	if err != nil {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeRejected, err: err}
	}
	if !changed {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeNoop}
	}
	return catalogProjectionOutcome{
		kind: catalogProjectionOutcomePublished,
		cmd:  a.publish(a.catalog.projection()),
	}
}

// startMutation refuses to start while another operation is in flight. A
// reconciling adapter still carries the operation that started the
// reconciliation, so the single check below covers both phases.
func (a *catalogProjectionAdapter) startMutation(kind catalogMutationKind, version string) catalogOperation {
	if a.hasActiveMutation() {
		return catalogOperation{}
	}
	return a.beginMutation(kind, version)
}

func (a *catalogProjectionAdapter) completeInstall(
	operationID uint64,
	version, path string,
	warnings []install.Warning,
) catalogProjectionOutcome {
	return a.completeMutation(operationID, catalogMutationInstall, version, func(op *catalogOperation) {
		op.installWarnings = cloneInstallWarnings(warnings)
	}, func() (bool, error) {
		return a.catalog.markInstalled(version, path)
	})
}

func (a *catalogProjectionAdapter) completeActivation(
	operationID uint64,
	version string,
	warnings []lifecycle.Warning,
	shimInPath bool,
) catalogProjectionOutcome {
	return a.completeMutation(operationID, catalogMutationActivation, version, func(op *catalogOperation) {
		op.lifecycleWarnings = cloneLifecycleWarnings(warnings)
		op.shimInPath = shimInPath
	}, func() (bool, error) {
		return a.catalog.activate(version)
	})
}

func (a *catalogProjectionAdapter) completeDeletion(
	operationID uint64,
	version string,
	warnings []lifecycle.Warning,
) catalogProjectionOutcome {
	return a.completeMutation(operationID, catalogMutationDeletion, version, func(op *catalogOperation) {
		op.lifecycleWarnings = cloneLifecycleWarnings(warnings)
	}, func() (bool, error) {
		return a.catalog.markDeleted(version)
	})
}

func (a *catalogProjectionAdapter) completeMutation(
	operationID uint64,
	kind catalogMutationKind,
	version string,
	storeReceipt func(*catalogOperation),
	mutate func() (bool, error),
) catalogProjectionOutcome {
	if !a.hasActiveMutation() ||
		a.state.operation.id != operationID ||
		a.state.operation.kind != kind ||
		a.state.operation.version != version {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	op := a.state.operation
	storeReceipt(&op)
	a.state.operation = op
	a.invalidateLoads()

	changed, err := mutate()
	if err != nil {
		if errors.Is(err, errCatalogNotFound) {
			outcome := a.startReconciliationLoad()
			outcome.err = err
			return outcome
		}
		a.toIdle()
		return catalogProjectionOutcome{
			kind:    catalogProjectionOutcomeCommittedWarning,
			receipt: catalogReceipt{operation: op},
			err:     err,
		}
	}
	a.toIdle()
	receipt := catalogReceipt{operation: op}
	if !changed {
		return catalogProjectionOutcome{
			kind:    catalogProjectionOutcomeNoop,
			receipt: receipt,
		}
	}
	return catalogProjectionOutcome{
		kind:    catalogProjectionOutcomePublished,
		cmd:     a.publish(a.catalog.projection()),
		receipt: receipt,
	}
}

func (a *catalogProjectionAdapter) failMutation(operationID uint64, err error) catalogProjectionOutcome {
	if !a.hasActiveMutation() || a.state.operation.id != operationID {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeStale}
	}
	op := a.state.operation
	if a.state.phase == catalogOperationPhaseReconciling {
		a.invalidateLoads()
	}
	a.toIdle()
	return catalogProjectionOutcome{
		kind:    catalogProjectionOutcomeFailed,
		receipt: catalogReceipt{operation: op},
		err:     err,
	}
}

func (a *catalogProjectionAdapter) verify(op catalogOperation) bool {
	v, ok := a.catalog.lookup(op.version)
	switch op.kind {
	case catalogMutationInstall:
		return ok && v.Installed && v.Path != ""
	case catalogMutationActivation:
		return ok && v.Active
	case catalogMutationDeletion:
		return !ok || !v.Installed
	default:
		return false
	}
}

func (a *catalogProjectionAdapter) finishReconciliation(outcome catalogProjectionOutcome) catalogProjectionOutcome {
	if a.state.phase == catalogOperationPhaseReconciling {
		outcome.receipt.operation = a.state.operation
	}
	a.invalidateLoads()
	a.toIdle()
	return outcome
}

// toIdle drops the operation and returns the adapter to rest. It is the one
// place that spells out the reset the flow used to repeat verbatim.
func (a *catalogProjectionAdapter) toIdle() {
	a.state = catalogOperationState{}
}

// beginLoad marks a catalog load as the visible activity. It leaves the
// operation alone: startLoad only calls it when no mutation is in flight.
func (a *catalogProjectionAdapter) beginLoad() {
	a.state.phase = catalogOperationPhaseLoading
}

// beginMutation registers a new operation and returns it. The counter is
// pre-incremented, so the returned id is always >= 1.
func (a *catalogProjectionAdapter) beginMutation(kind catalogMutationKind, version string) catalogOperation {
	a.nextOperationID++
	a.state = catalogOperationState{
		phase: catalogOperationPhaseMutating,
		operation: catalogOperation{
			id:      a.nextOperationID,
			kind:    kind,
			version: version,
		},
	}
	return a.state.operation
}

// beginReconcile keeps the operation being verified: it takes no version, so
// the phase cannot drift away from the operation it reconciles. The install
// itself is over by now, so its progress is dropped.
func (a *catalogProjectionAdapter) beginReconcile() {
	a.state.phase = catalogOperationPhaseReconciling
	a.state.progress = install.Progress{}
}

// isActiveOperation reports whether operationID identifies the operation
// currently in flight. It is the one implementation of the correlation rule
// that used to be spelled out at every progress-handling site.
func (a *catalogProjectionAdapter) isActiveOperation(operationID uint64) bool {
	return a.state.operation.id != 0 && a.state.operation.id == operationID
}

// applyProgress records a measurement for the active install and reports
// whether it was accepted. A measurement outside a running install is
// dropped, which is what keeps the progress invariant of the state.
func (a *catalogProjectionAdapter) applyProgress(operationID uint64, p install.Progress) bool {
	if !a.isActiveOperation(operationID) ||
		a.state.phase != catalogOperationPhaseMutating ||
		a.state.operation.kind != catalogMutationInstall {
		return false
	}
	a.state.progress = p
	return true
}

func (a *catalogProjectionAdapter) hasActiveMutation() bool {
	return a.state.operation.id != 0
}

// isReconciliationRequest reports whether the in-flight load is the one
// started to verify an operation. The purpose travels with the request, so
// there is no second copy of the id to fall out of sync.
func (a *catalogProjectionAdapter) isReconciliationRequest(requestID uint64) bool {
	return a.loadActive &&
		a.load.ID == requestID &&
		a.load.Purpose == catalogLoadPurposeReconcile
}

func (a *catalogProjectionAdapter) registerLoad(purpose catalogLoadPurpose) catalogLoadRequest {
	a.nextLoadID++
	a.load = catalogLoadRequest{ID: a.nextLoadID, Purpose: purpose}
	a.loadActive = true
	return a.load
}

func (a *catalogProjectionAdapter) invalidateLoad() {
	a.load = catalogLoadRequest{}
	a.loadActive = false
}

func (a *catalogProjectionAdapter) invalidateLoads() {
	a.invalidateLoad()
}

func cloneInstallWarnings(warnings []install.Warning) []install.Warning {
	cloned := make([]install.Warning, len(warnings))
	copy(cloned, warnings)
	return cloned
}

func cloneLifecycleWarnings(warnings []lifecycle.Warning) []lifecycle.Warning {
	cloned := make([]lifecycle.Warning, len(warnings))
	copy(cloned, warnings)
	return cloned
}

func (a *catalogProjectionAdapter) publish(projection versionProjection) tea.Cmd {
	oldVersion := a.selectedAvailableVersion()
	oldIndex := a.list.Index()
	if oldVersion == "" && a.pendingRestore.active && a.list.FilterState() != list.Unfiltered {
		oldVersion = a.pendingRestore.version
		oldIndex = a.pendingRestore.index
	}
	oldInstalled, oldInstalledIndex := a.selectedInstalledIdentity()
	a.generation++

	a.installedTable.SetRows(projection.installed)
	a.restoreInstalledSelection(oldInstalled, oldInstalledIndex)
	cmd := a.list.SetItems(projection.available)
	if a.list.FilterState() == list.Unfiltered {
		a.restoreAvailableSelection(oldVersion, oldIndex)
		a.pendingRestore = catalogProjectionPendingRestore{}
		a.refilterPending = false
		return cmd
	}

	a.pendingRestore = catalogProjectionPendingRestore{
		active:     true,
		generation: a.generation,
		filterText: a.list.FilterInput.Value(),
		version:    oldVersion,
		index:      oldIndex,
	}
	a.refilterPending = cmd != nil
	return wrapCatalogProjectionRefilter(cmd, a.generation, a.pendingRestore.filterText)
}

func wrapCatalogProjectionRefilter(cmd tea.Cmd, generation uint64, filterText string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		matches, ok := msg.(list.FilterMatchesMsg)
		if !ok {
			return msg
		}
		return catalogProjectionRefilterMsg{
			matches:    matches,
			generation: generation,
			filterText: filterText,
		}
	}
}

func (a *catalogProjectionAdapter) settleRefilter(msg catalogProjectionRefilterMsg) tea.Cmd {
	a.refilterPending = false
	if msg.generation != a.generation || msg.filterText != a.list.FilterInput.Value() {
		if a.pendingRestore.active &&
			a.pendingRestore.generation == msg.generation &&
			a.pendingRestore.filterText == msg.filterText {
			a.pendingRestore = catalogProjectionPendingRestore{}
		}
		return nil
	}
	var cmd tea.Cmd
	a.list, cmd = a.list.Update(msg.matches)
	if a.pendingRestore.active &&
		a.pendingRestore.generation == msg.generation &&
		a.pendingRestore.filterText == msg.filterText {
		a.restoreAvailableSelection(a.pendingRestore.version, a.pendingRestore.index)
		a.pendingRestore = catalogProjectionPendingRestore{}
	}
	return cmd
}

func (a *catalogProjectionAdapter) selectedAvailableVersion() string {
	item := a.list.SelectedItem()
	if item == nil {
		return ""
	}
	selected, ok := item.(styles.Item)
	if !ok {
		return ""
	}
	return selected.Name
}

func (a *catalogProjectionAdapter) selectedInstalledIdentity() (string, int) {
	row := a.installedTable.SelectedRow()
	version := ""
	if len(row) > 0 {
		version = row[0]
	}
	return version, a.installedTable.Cursor()
}

func (a *catalogProjectionAdapter) restoreAvailableSelection(version string, oldIndex int) {
	visible := a.list.VisibleItems()
	if len(visible) == 0 {
		return
	}
	target := -1
	if version != "" {
		for i, item := range visible {
			if selected, ok := item.(styles.Item); ok && selected.Name == version {
				target = i
				break
			}
		}
	}
	if target < 0 {
		target = oldIndex
	}
	if target < 0 {
		target = 0
	}
	if target >= len(visible) {
		target = len(visible) - 1
	}
	a.list.Select(target)
}

func (a *catalogProjectionAdapter) restoreInstalledSelection(version string, oldIndex int) {
	rows := a.installedTable.Rows()
	if len(rows) == 0 {
		return
	}
	target := -1
	if version != "" {
		for i, row := range rows {
			if len(row) > 0 && row[0] == version {
				target = i
				break
			}
		}
	}
	if target < 0 {
		target = oldIndex
	}
	if target < 0 {
		target = 0
	}
	if target >= len(rows) {
		target = len(rows) - 1
	}
	a.installedTable.SetCursor(target)
}

func (a *catalogProjectionAdapter) resize(width, availableHeight, installedHeight int) {
	a.list.SetSize(width, availableHeight)
	a.installedTable.Resize(width, installedHeight, installedTableColumns(width))
}

func (a *catalogProjectionAdapter) updateAvailable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	a.list, cmd = a.list.Update(msg)
	if _, ok := msg.(tea.KeyPressMsg); ok && !a.list.SettingFilter() {
		a.rememberAvailableSelection()
	}
	return cmd
}

func (a *catalogProjectionAdapter) updateInstalled(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up", "k":
			a.installedTable.Move(-1)
		case "down", "j":
			a.installedTable.Move(1)
		}
	}
	return nil
}

func (a *catalogProjectionAdapter) update(msg tea.Msg) tea.Cmd {
	return tea.Batch(a.updateAvailable(msg), a.updateInstalled(msg))
}

// availableSettingFilter reports whether the Available list's filter
// input currently has focus (the widget calls this "setting the
// filter"). While true, keys routed to the list reach the input.
func (a *catalogProjectionAdapter) availableSettingFilter() bool {
	return a.list.SettingFilter()
}

// availableFilterApplied reports whether a committed filter is
// narrowing the Available list.
func (a *catalogProjectionAdapter) availableFilterApplied() bool {
	return a.list.FilterState() == list.FilterApplied
}

// availableFilterSummary returns the committed filter query and the
// visible/total item counts for the applied-filter indicator line.
func (a *catalogProjectionAdapter) availableFilterSummary() (query string, visible, total int) {
	return strings.TrimSpace(a.list.FilterInput.Value()), len(a.list.VisibleItems()), len(a.list.Items())
}

func (a *catalogProjectionAdapter) setAvailableFilteringEnabled(enabled bool) {
	a.list.SetFilteringEnabled(enabled)
}

func (a *catalogProjectionAdapter) setAvailableFilterText(filter string) {
	a.list.SetFilterText(filter)
}

func (a *catalogProjectionAdapter) selectAvailable(index int) {
	a.list.Select(index)
	a.rememberAvailableSelection()
}

func (a *catalogProjectionAdapter) rememberAvailableSelection() {
	pending := &a.pendingRestore
	if pending.active && pending.generation == a.generation && pending.filterText == a.list.FilterInput.Value() {
		pending.version, pending.index = a.selectedAvailableVersion(), a.list.Index()
	}
}

func (a *catalogProjectionAdapter) selectAvailableVersion(version string) bool {
	for index, item := range a.list.VisibleItems() {
		if value, ok := item.(styles.Item); ok && value.Name == version {
			a.selectAvailable(index)
			return true
		}
	}
	return false
}

func (a *catalogProjectionAdapter) moveAvailableSelection(delta int) {
	count, index := len(a.list.VisibleItems()), a.list.Index()
	if count == 0 || (delta < 0 && index == 0) || (delta > 0 && index >= count-1) {
		return
	}
	if delta < 0 {
		a.list.CursorUp()
	} else {
		a.list.CursorDown()
	}
	a.rememberAvailableSelection()
}

func (a *catalogProjectionAdapter) selectedAvailableItem() *styles.Item {
	item := a.list.SelectedItem()
	selected, ok := item.(styles.Item)
	if !ok {
		return nil
	}
	return &selected
}

func (a catalogProjectionAdapter) selectedInstalledItem() table.Row {
	return a.installedTable.SelectedRow()
}

func (a *catalogProjectionAdapter) setTheme(theme styles.Theme) catalogProjectionOutcome {
	if !a.catalog.setTheme(theme) {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeNoop}
	}
	a.delegate = listDefaultDelegate(theme)
	a.list.SetDelegate(a.delegate)
	a.installedTable.SetStyles(tableStyles(theme))
	return catalogProjectionOutcome{
		kind: catalogProjectionOutcomePublished,
		cmd:  a.publish(a.catalog.projection()),
	}
}

func (a *catalogProjectionAdapter) setDiskUsage(sizes map[string]int64) catalogProjectionOutcome {
	changed, err := a.catalog.setDiskUsage(sizes)
	if err != nil {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeRejected, err: err}
	}
	if !changed {
		return catalogProjectionOutcome{kind: catalogProjectionOutcomeNoop}
	}
	return catalogProjectionOutcome{
		kind: catalogProjectionOutcomePublished,
		cmd:  a.publish(a.catalog.projection()),
	}
}

func (a *catalogProjectionAdapter) availableModel() list.Model {
	return a.list
}

func (a *catalogProjectionAdapter) installedModel() rowTable {
	return a.installedTable
}

func (a *catalogProjectionAdapter) availableView() renderedSurface {
	surface := renderedSurface{content: a.list.View()}
	if a.list.SettingFilter() {
		return surface
	}
	visible := a.list.VisibleItems()
	if len(visible) == 0 {
		return surface
	}
	start, end := a.list.Paginator.GetSliceBounds(len(visible))
	headerHeight := a.delegate.headerHeight(a.list)
	bodyHeight := max(0, a.list.Height()-headerHeight)
	surface.targets = make([]mouseTarget, 0, end-start+1)
	surface.targets = append(surface.targets, mouseTarget{
		rect:   cellRect{y: headerHeight, width: a.list.Width(), height: bodyHeight},
		action: mouseAction{kind: mouseScroll},
	})
	for index := start; index < end; index++ {
		item, ok := visible[index].(styles.Item)
		if !ok {
			continue
		}
		surface.targets = append(surface.targets, mouseTarget{
			rect: cellRect{
				y:     headerHeight + (index-start)*(a.delegate.Height()+a.delegate.Spacing()),
				width: a.list.Width(), height: a.delegate.Height(),
			},
			action: mouseAction{kind: mouseAvailableRow, index: index, identity: item.Name},
		})
	}
	return surface.translated(0, 0, cellRect{width: a.list.Width(), height: a.list.Height()})
}

func (a *catalogProjectionAdapter) installedView() renderedSurface {
	surface := a.installedTable.render(mouseInstalledRow)
	rows := a.installedTable.Rows()
	for index := range surface.targets {
		target := &surface.targets[index]
		if target.action.kind == mouseInstalledRow {
			row := rows[target.action.index]
			if len(row) > 0 {
				target.action.identity = row[0]
			}
		}
	}
	return surface
}

func (a *catalogProjectionAdapter) lookup(version string) (utils.GoVersion, bool) {
	return a.catalog.lookup(version)
}

func (a *catalogProjectionAdapter) projection() versionProjection {
	return a.catalog.projection()
}

// activityState derives what the adapter is doing from its state. It is a
// projection of the state, not a second copy of it.
func (a *catalogProjectionAdapter) activityState() catalogActivity {
	switch a.state.phase {
	case catalogOperationPhaseLoading:
		return catalogActivity{kind: catalogActivityLoading}
	case catalogOperationPhaseReconciling:
		return catalogActivity{
			kind:    catalogActivityReconciling,
			version: a.state.operation.version,
		}
	case catalogOperationPhaseMutating:
		activity := catalogActivity{
			kind:     catalogActivityIdle,
			version:  a.state.operation.version,
			progress: a.state.progress,
		}
		switch a.state.operation.kind {
		case catalogMutationInstall:
			activity.kind = catalogActivityInstalling
		case catalogMutationActivation:
			activity.kind = catalogActivityActivating
		case catalogMutationDeletion:
			activity.kind = catalogActivityDeleting
		}
		return activity
	}
	return catalogActivity{kind: catalogActivityIdle}
}

func (a *catalogProjectionAdapter) operationPhase() catalogOperationPhase {
	return a.state.phase
}
