package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/smileoniks-ctrl/govm/internal/application"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/install"
	"github.com/smileoniks-ctrl/govm/internal/lifecycle"
	"github.com/smileoniks-ctrl/govm/internal/loader"
	"github.com/smileoniks-ctrl/govm/internal/prune"
	"github.com/smileoniks-ctrl/govm/internal/styles"
	"github.com/smileoniks-ctrl/govm/internal/utils"
)

func TestSettingsThemeChangeDeliversRefilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4"}, {Version: "1.25.0"}})
	m = applyFilter(t, m, "1.24")
	m = focusSetting(t, m, settingRowTheme)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("theme change dropped the refilter command")
	}
	m = settleCmd(t, m, cmd)
	if m.projection.refilterPending {
		t.Fatal("theme change left the catalog waiting for refilter")
	}
	if got := catalogProjectionVisibleItemNames(m); !reflect.DeepEqual(got, []string{"1.24.4"}) {
		t.Fatalf("visible versions = %v, want [1.24.4]", got)
	}
	selected := m.projection.selectedAvailableItem()
	wantTitle := styles.RenderItemTitle(m.Theme(), "1.24.4", false, false)
	if selected == nil || selected.RenderedTitle != wantTitle {
		t.Fatalf("filtered item = %+v, want the new theme", selected)
	}
	if m.Status.Text() != "Settings saved." {
		t.Fatalf("status = %q, want Settings saved.", m.Status.Text())
	}
}

func TestSettingsSourceChangeDeliversRefilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4"}, {Version: "1.25.0"}})
	m = applyFilter(t, m, "1.24")
	m = focusSetting(t, m, settingRowDistributionSource)
	m = m.BindVersionOperations(VersionOperations{
		DistributionSource: func(context.Context, string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{
				Source: "https://mirror.example/dl/",
				Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{
					{Version: "1.24.5"}, {Version: "1.25.0"},
				}},
			}, nil
		},
	})
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.settings.distributionSourceInput.SetValue("https://mirror.example/dl")
	updated, check := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if check == nil {
		t.Fatal("missing source check command")
	}
	updated, refilter := m.Update(check())
	m = updated.(Model)
	if refilter == nil {
		t.Fatal("source result dropped the refilter command")
	}
	m = settleCmd(t, m, refilter)
	if m.projection.refilterPending {
		t.Fatal("source change left the catalog waiting for refilter")
	}
	if got := catalogProjectionVisibleItemNames(m); !reflect.DeepEqual(got, []string{"1.24.5"}) {
		t.Fatalf("visible versions = %v, want [1.24.5]", got)
	}
	if m.settings.editingDistributionSource || m.Status.Text() != "Settings saved." {
		t.Fatalf("source completion: editor=%v, status=%q", m.settings.editingDistributionSource, m.Status.Text())
	}
}

func TestCatalogSourceCheckEffects(t *testing.T) {
	t.Run("request and command share correlation", func(t *testing.T) {
		a := newCatalogProjectionAdapter(testTheme())
		a.bindOperations(VersionOperations{DistributionSource: func(_ context.Context, source string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{Source: source}, nil
		}})
		cmd, status := a.apply(catalogSourceCheckMsg{source: "https://mirror.example/dl/"})
		started, ok := status.settingsMsg.(sourceCheckStartedMsg)
		if !ok || started.requestID == 0 || cmd == nil {
			t.Fatalf("start effect = %+v, command present=%v", status, cmd != nil)
		}
		msg, ok := cmd().(distributionSourceValidatedMsg)
		if !ok || msg.RequestID != started.requestID || msg.Result.Source != "https://mirror.example/dl/" {
			t.Fatalf("source command result = %+v", msg)
		}
		if status.scope != catalogStatusGlobal || status.text != "Checking distribution source..." {
			t.Fatalf("start status = %+v", status)
		}
	})

	t.Run("reconciliation rejects source check without losing verification", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
		op, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
		refreshCatalogTestSnapshot(t, &a, nil)
		verify, _ := a.apply(completion)
		requestID := catalogRequestID(t, verify)
		cmd, status := a.apply(catalogSourceCheckMsg{source: config.DefaultDistributionSource})
		if cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatalf("rejected check = %+v, command present=%v", status, cmd != nil)
		}
		if _, ok := status.settingsMsg.(sourceCheckRejectedMsg); !ok {
			t.Fatalf("notification = %T, want sourceCheckRejectedMsg", status.settingsMsg)
		}
		_, status = a.apply(catalogLoadedMsg{
			RequestID: requestID,
			Versions:  []utils.GoVersion{{Version: op.version, Installed: true, Path: "/go/1.30.0"}},
		})
		if status.kind != "success" {
			t.Fatalf("verification status = %+v", status)
		}
	})

	t.Run("invalid source catalog keeps prior projection and reports to editor", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.24.4"}})
		_, start := a.apply(catalogSourceCheckMsg{source: config.DefaultDistributionSource})
		requestID := start.settingsMsg.(sourceCheckStartedMsg).requestID
		cmd, status := a.apply(catalogSourceAcceptedMsg{
			requestID: requestID,
			versions:  []utils.GoVersion{{Version: "1.25.0"}, {Version: "1.25.0"}},
		})
		rejected, ok := status.settingsMsg.(sourceCheckRejectedMsg)
		if !ok || !strings.Contains(rejected.reason, "Failed to apply catalog:") {
			t.Fatalf("notification = %+v, want catalog validation error", status.settingsMsg)
		}
		if cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatalf("rejection effect = %+v, command present=%v", status, cmd != nil)
		}
		if _, ok := a.lookup("1.24.4"); !ok {
			t.Fatal("invalid source replaced prior catalog")
		}
	})
}

func TestCatalogInitialLoadUsesBoundLoader(t *testing.T) {
	m := New(
		"",
		config.DefaultSettings(),
		newMemorySettingsStore(config.DefaultSettings()),
		"",
		testTheme(),
	)
	called := 0
	m = m.BindVersionOperations(VersionOperations{
		LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
			called++
			return []utils.GoVersion{{Version: "1.30.0"}}, nil
		},
	})
	m = settleCmd(t, m, m.Init())
	if called != 1 {
		t.Fatalf("loader calls = %d, want 1", called)
	}
	if _, ok := m.projection.lookup("1.30.0"); !ok {
		t.Fatal("initial command did not use the bound loader")
	}
}

func TestCatalogFailureEffects(t *testing.T) {
	for _, tt := range []struct {
		name      string
		reconcile bool
		response  func(uint64) tea.Msg
		want      string
	}{
		{
			name: "ordinary load error",
			response: func(id uint64) tea.Msg {
				return catalogLoadFailedMsg{RequestID: id, Err: errors.New("offline")}
			},
			want: "offline",
		},
		{
			name:     "ordinary nil error",
			response: func(id uint64) tea.Msg { return catalogLoadFailedMsg{RequestID: id} },
			want:     "catalog load failed",
		},
		{
			name: "invalid ordinary snapshot",
			response: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}, {Version: "1.30.0"}}}
			},
			want: "Failed to load Go versions:",
		},
		{
			name: "invalid verification snapshot", reconcile: true,
			response: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}, {Version: "1.30.0"}}}
			},
			want: "Could not verify the operation:",
		},
		{
			name: "verification error", reconcile: true,
			response: func(id uint64) tea.Msg {
				return catalogLoadFailedMsg{RequestID: id, Err: errors.New("offline")}
			},
			want: "The operation could not be confirmed against the installed catalog.",
		},
		{
			name: "unconfirmed installation", reconcile: true,
			response: func(id uint64) tea.Msg {
				return catalogLoadedMsg{RequestID: id, Versions: []utils.GoVersion{{Version: "1.30.0"}}}
			},
			want: "The operation could not be confirmed against the installed catalog.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
			var load tea.Cmd
			if tt.reconcile {
				_, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
				refreshCatalogTestSnapshot(t, &a, nil)
				load, _ = a.apply(completion)
			} else {
				load, _ = a.apply(catalogRefreshMsg{})
			}
			_, status := a.apply(tt.response(catalogRequestID(t, load)))
			if status.scope != catalogStatusGlobal || status.kind != "error" || !strings.HasPrefix(status.text, tt.want) {
				t.Fatalf("failure status = %+v, want global error %q", status, tt.want)
			}
			refreshCatalogTestSnapshot(t, &a, []utils.GoVersion{{Version: "1.31.0"}})
			_, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.31.0")
			a.apply(completion)
			if got, _ := a.lookup("1.31.0"); !got.Installed {
				t.Fatal("failed verification blocked the next install")
			}
		})
	}
}

func TestUnconfirmedCatalogStillDeliversRefilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4"}, {Version: "1.30.0"}})
	m = applyFilter(t, m, "1.30")
	loads := 0
	operations := catalogTestOperations()
	operations.LoadCatalog = func(context.Context) ([]utils.GoVersion, error) {
		loads++
		if loads == 1 {
			return []utils.GoVersion{{Version: "1.24.4"}}, nil
		}
		return []utils.GoVersion{{Version: "1.24.5"}, {Version: "1.30.0"}}, nil
	}
	operations.DiskUsage = func(context.Context) (prune.Summary, error) { return prune.Summary{}, nil }
	m = m.BindVersionOperations(operations)
	updated, installCmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	m = updated.(Model)
	completion := installCmd()
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = applyFilter(t, m, "1.24")
	updated, refresh := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = runCatalogTestCmd(t, updated.(Model), refresh)
	updated, verify := m.Update(completion)
	if verify == nil {
		t.Fatal("missing verification command")
	}
	m = runCatalogTestCmd(t, updated.(Model), verify)
	if loads != 2 {
		t.Fatalf("loads=%d, want refresh and verification", loads)
	}
	if m.projection.refilterPending || selectedListVersion(m) != "1.24.5" {
		t.Fatalf("refilter did not finish: pending=%v, selection=%q", m.projection.refilterPending, selectedListVersion(m))
	}
	if m.Status.Text() != "The operation could not be confirmed against the installed catalog." {
		t.Fatalf("status = %q", m.Status.Text())
	}
	assertVersionViewsConsistent(t, m)
}

func TestPruneRefreshPreservesOwnStatus(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: "Pruned 1 object(s), freed"},
		{name: "warning", err: errors.New("cleanup failed"), want: "Prune completed with warnings: cleanup failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4", Installed: true, Path: "/go/1.24.4"}})
			loads, usageCalls := 0, 0
			m = m.BindVersionOperations(VersionOperations{
				PreviewPrune: func(context.Context) (prune.Result, error) {
					return prune.Result{Candidates: []prune.Candidate{{Version: "1.24.4", Bytes: 1024}}}, nil
				},
				Prune: func(context.Context) (prune.Result, error) {
					return prune.Result{Removed: []prune.Candidate{{Version: "1.24.4", Bytes: 1024}}}, tt.err
				},
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
					loads++
					return []utils.GoVersion{{Version: "1.24.4"}}, nil
				},
				DiskUsage: func(context.Context) (prune.Summary, error) {
					usageCalls++
					return prune.Summary{}, nil
				},
			})
			m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			updated, preview := m.Update(tea.KeyPressMsg{Code: 'p'})
			if preview == nil {
				t.Fatal("missing prune preview command")
			}
			m = runCatalogTestCmd(t, updated.(Model), preview)
			updated, run := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if run == nil {
				t.Fatal("missing confirmed prune command")
			}
			m = runCatalogTestCmd(t, updated.(Model), run)
			if !strings.HasPrefix(m.Status.Text(), tt.want) || m.Status.Scope() != statusScopeGlobal {
				t.Fatalf("prune status = %q, scope=%v", m.Status.Text(), m.Status.Scope())
			}
			if loads != 1 || usageCalls != 1 {
				t.Fatalf("load calls=%d, disk usage calls=%d, want 1 each", loads, usageCalls)
			}
		})
	}
}

func TestCatalogFlowAdmission(t *testing.T) {
	for _, tt := range []struct {
		name     string
		action   catalogActionMsg
		loading  bool
		scope    catalogStatusScope
		kind     string
		dispatch bool
		confirm  bool
		clear    bool
	}{
		{name: "loading install", loading: true, action: catalogActionMsg{kind: catalogActionInstall, version: "1.25.0", tab: AvailableTab}},
		{name: "loading activation", loading: true, action: catalogActionMsg{kind: catalogActionActivate, version: "1.26.0", tab: InstalledTab}},
		{name: "loading delete request", loading: true, action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.26.0", tab: AvailableTab}},
		{name: "loading delete confirmation", loading: true, action: catalogActionMsg{kind: catalogActionConfirmDelete, version: "1.26.0", tab: InstalledTab}},
		{name: "unknown action", action: catalogActionMsg{kind: catalogActionKind(99), version: "1.25.0", tab: AvailableTab}},
		{name: "unknown tab", action: catalogActionMsg{kind: catalogActionInstall, version: "1.25.0", tab: DepsTab}},
		{name: "empty install", action: catalogActionMsg{kind: catalogActionInstall, tab: AvailableTab}},
		{name: "missing install", action: catalogActionMsg{kind: catalogActionInstall, version: "1.99.0", tab: AvailableTab}},
		{name: "installed install", action: catalogActionMsg{kind: catalogActionInstall, version: "1.26.0", tab: AvailableTab}},
		{name: "installed tab install", action: catalogActionMsg{kind: catalogActionInstall, version: "1.25.0", tab: InstalledTab}},
		{name: "install", action: catalogActionMsg{kind: catalogActionInstall, version: "1.25.0", tab: AvailableTab}, scope: catalogStatusGlobal, dispatch: true},
		{name: "empty available activation", action: catalogActionMsg{kind: catalogActionActivate, tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "missing available activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.99.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "uninstalled available activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.25.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "empty installed activation", action: catalogActionMsg{kind: catalogActionActivate, tab: InstalledTab}},
		{name: "missing installed activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.99.0", tab: InstalledTab}},
		{name: "uninstalled installed activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.25.0", tab: InstalledTab}},
		{name: "active available activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.24.4", tab: AvailableTab}, scope: catalogStatusGlobal, kind: "info", dispatch: true},
		{name: "active installed activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.24.4", tab: InstalledTab}, scope: catalogStatusTab, kind: "info"},
		{name: "activation", action: catalogActionMsg{kind: catalogActionActivate, version: "1.26.0", tab: InstalledTab}, scope: catalogStatusGlobal, kind: "info", dispatch: true},
		{name: "empty delete", action: catalogActionMsg{kind: catalogActionRequestDelete, tab: AvailableTab}},
		{name: "missing available delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.99.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "uninstalled available delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.25.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "missing installed delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.99.0", tab: InstalledTab}},
		{name: "uninstalled installed delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.25.0", tab: InstalledTab}},
		{name: "active available delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.24.4", tab: AvailableTab}, scope: catalogStatusTab, kind: "error"},
		{name: "active installed delete", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.24.4", tab: InstalledTab}, scope: catalogStatusTab, kind: "error"},
		{name: "available delete request", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.26.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "warning", confirm: true},
		{name: "installed delete request", action: catalogActionMsg{kind: catalogActionRequestDelete, version: "1.26.0", tab: InstalledTab}, scope: catalogStatusTab, kind: "warning", confirm: true},
		{name: "confirmed deletion", action: catalogActionMsg{kind: catalogActionConfirmDelete, version: "1.26.0", tab: InstalledTab}, scope: catalogStatusGlobal, kind: "info", dispatch: true, clear: true},
		{name: "missing confirmed deletion", action: catalogActionMsg{kind: catalogActionConfirmDelete, version: "1.99.0", tab: AvailableTab}, scope: catalogStatusTab, kind: "error", clear: true},
		{name: "uninstalled confirmed deletion", action: catalogActionMsg{kind: catalogActionConfirmDelete, version: "1.25.0", tab: InstalledTab}, scope: catalogStatusTab, kind: "info", clear: true},
		{name: "active confirmed deletion", action: catalogActionMsg{kind: catalogActionConfirmDelete, version: "1.24.4", tab: InstalledTab}, scope: catalogStatusTab, kind: "error", clear: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newVersionCacheTestModel(t)
			calls := 0
			var identity string
			operations := catalogTestOperations()
			operations.Install = func(_ context.Context, request install.Request) (install.Result, error) {
				calls++
				identity = request.Version
				return install.Result{Version: request.Version, Path: "/admitted/" + request.Version}, nil
			}
			operations.Activate = func(_ context.Context, version string) (lifecycle.ActivationResult, error) {
				calls++
				identity = version
				return lifecycle.ActivationResult{Version: version}, nil
			}
			operations.Delete = func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
				calls++
				identity = version
				return lifecycle.DeletionResult{Version: version}, nil
			}
			operations.DiskUsage = func(context.Context) (prune.Summary, error) { return prune.Summary{}, nil }
			m = m.BindVersionOperations(operations)
			if tt.loading {
				m.projection.apply(catalogRefreshMsg{manual: true})
			}
			before := m.projection.projection()
			cmd, status := m.projection.apply(tt.action)
			if status.scope != tt.scope || status.kind != tt.kind || status.clearDeleteConfirmation != tt.clear {
				t.Fatalf("admission effect = %+v", status)
			}
			if (status.confirmDeleteVersion != "") != tt.confirm || (tt.confirm && status.confirmDeleteVersion != tt.action.version) {
				t.Fatalf("confirmation effect = %+v", status)
			}
			if (cmd != nil) != tt.dispatch {
				t.Fatalf("command present=%v, want %v", cmd != nil, tt.dispatch)
			}
			m.applyCatalogStatus(status)
			if tt.dispatch {
				m = runCatalogTestCmd(t, m, cmd)
				if calls != 1 || identity != tt.action.version {
					t.Fatalf("calls=%d identity=%q", calls, identity)
				}
				v, ok := m.projection.lookup(tt.action.version)
				switch tt.action.kind {
				case catalogActionInstall:
					if !ok || !v.Installed || v.Path != "/admitted/"+identity {
						t.Fatalf("install did not publish: %+v", v)
					}
				case catalogActionActivate:
					if !ok || !v.Active {
						t.Fatalf("activation did not publish: %+v", v)
					}
					if identity != "1.24.4" {
						old, _ := m.projection.lookup("1.24.4")
						if old.Active {
							t.Fatal("old version remained active")
						}
					}
				case catalogActionConfirmDelete:
					if !ok || v.Installed || v.Path != "" {
						t.Fatalf("deletion did not publish: %+v", v)
					}
				}
				if m.Status.Kind() != "success" {
					t.Fatalf("completion status=%q kind=%q", m.Status.Text(), m.Status.Kind())
				}
			} else if calls != 0 || !reflect.DeepEqual(m.projection.projection(), before) {
				t.Fatal("non-dispatched action changed the catalog or called core")
			}
			assertVersionViewsConsistent(t, m)
		})
	}
}

func TestCatalogFlowLoadAdmission(t *testing.T) {
	t.Run("ordinary loads are superseded but manual refresh is throttled", func(t *testing.T) {
		a := newCatalogProjectionAdapter(testTheme())
		a.prepareInitialLoad()
		loads, sources := 0, 0
		a.bindOperations(VersionOperations{
			LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
				loads++
				return []utils.GoVersion{{Version: "1.30.0"}}, nil
			},
			DistributionSource: func(_ context.Context, source string) (application.DistributionSourceResult, error) {
				sources++
				return application.DistributionSourceResult{Source: source, Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{{Version: "1.31.0"}}}}, nil
			},
		})
		initial := a.init()()
		if cmd, status := a.apply(catalogRefreshMsg{manual: true}); cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatal("manual refresh interrupted initial loading")
		}
		refresh, _ := a.apply(catalogRefreshMsg{})
		if refresh == nil {
			t.Fatal("nonmanual refresh was blanket-blocked")
		}
		old := refresh()
		check, status := a.apply(catalogSourceCheckMsg{source: config.DefaultDistributionSource})
		if _, ok := status.settingsMsg.(sourceCheckStartedMsg); !ok || check == nil {
			t.Fatalf("source start=%+v", status)
		}
		validated := check().(distributionSourceValidatedMsg)
		a.apply(catalogSourceAcceptedMsg{requestID: validated.RequestID, versions: validated.Result.Catalog.Versions})
		for _, stale := range []tea.Msg{initial, old} {
			if cmd, effect := a.apply(stale); cmd != nil || effect.scope != catalogStatusUntouched {
				t.Fatal("superseded load had effects")
			}
		}
		if loads != 2 || sources != 1 {
			t.Fatalf("loads=%d sources=%d", loads, sources)
		}
		if _, ok := a.lookup("1.31.0"); !ok {
			t.Fatal("latest source catalog was not accepted")
		}
		if _, ok := a.lookup("1.30.0"); ok {
			t.Fatal("superseded catalog was accepted")
		}
	})

	t.Run("refilter pending blocks only manual refresh", func(t *testing.T) {
		m := applyFilter(t, newVersionCacheTestModel(t), "1.25")
		loads, sources := 0, 0
		m = m.BindVersionOperations(VersionOperations{
			LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
				loads++
				return []utils.GoVersion{{Version: "1.25.1"}}, nil
			},
			DistributionSource: func(_ context.Context, source string) (application.DistributionSourceResult, error) {
				sources++
				return application.DistributionSourceResult{Source: source, Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{{Version: "1.25.2"}}}}, nil
			},
		})
		refresh, _ := m.projection.apply(catalogRefreshMsg{manual: true})
		oldRefilter, _ := m.projection.apply(refresh())
		if oldRefilter == nil {
			t.Fatal("filtered publication lost continuation")
		}
		if cmd, status := m.projection.apply(catalogRefreshMsg{manual: true}); cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatal("manual refresh interrupted pending refilter")
		}
		refresh, _ = m.projection.apply(catalogRefreshMsg{})
		if refresh == nil {
			t.Fatal("nonmanual refresh was blocked by refilter")
		}
		staleLoad := refresh()
		check, start := m.projection.apply(catalogSourceCheckMsg{source: config.DefaultDistributionSource})
		if check == nil {
			t.Fatalf("source was blocked by refilter: %+v", start)
		}
		validated := check().(distributionSourceValidatedMsg)
		refilter, _ := m.projection.apply(catalogSourceAcceptedMsg{requestID: validated.RequestID, versions: validated.Result.Catalog.Versions})
		m = runCatalogTestCmd(t, m, refilter)
		m = runCatalogTestCmd(t, m, oldRefilter)
		if cmd, status := m.projection.apply(staleLoad); cmd != nil || status.scope != catalogStatusUntouched {
			t.Fatal("older refresh replaced source result")
		}
		if loads != 2 || sources != 1 || selectedListVersion(m) != "1.25.2" {
			t.Fatalf("loads=%d sources=%d selection=%q", loads, sources, selectedListVersion(m))
		}
		assertVersionViewsConsistent(t, m)
	})

	t.Run("mutation admits nonmanual refresh and source check", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}})
		loads, sources := 0, 0
		operations := catalogTestOperations()
		operations.LoadCatalog = func(context.Context) ([]utils.GoVersion, error) {
			loads++
			return []utils.GoVersion{{Version: "1.30.0"}}, nil
		}
		operations.DistributionSource = func(_ context.Context, source string) (application.DistributionSourceResult, error) {
			sources++
			return application.DistributionSourceResult{Source: source, Catalog: loader.VersionCatalog{Versions: []utils.GoVersion{{Version: "1.30.0"}}}}, nil
		}
		a.bindOperations(operations)
		_, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
		refresh, _ := a.apply(catalogRefreshMsg{})
		if refresh == nil {
			t.Fatal("mutation blocked nonmanual refresh")
		}
		older := refresh()
		check, start := a.apply(catalogSourceCheckMsg{source: config.DefaultDistributionSource})
		if check == nil {
			t.Fatalf("mutation blocked source: %+v", start)
		}
		validated := check().(distributionSourceValidatedMsg)
		a.apply(catalogSourceAcceptedMsg{requestID: validated.RequestID, versions: validated.Result.Catalog.Versions})
		a.apply(older)
		_, status := a.apply(completion)
		if status.kind != "success" || loads != 1 || sources != 1 {
			t.Fatalf("completion=%+v loads=%d sources=%d", status, loads, sources)
		}
		if got, _ := a.lookup("1.30.0"); !got.Installed || got.Path != "/go/1.30.0" {
			t.Fatalf("mutation lost after source check: %+v", got)
		}
	})

	t.Run("reconciliation retains exclusive verification", func(t *testing.T) {
		a := newCatalogProjectionAdapterTestFixture(t, []utils.GoVersion{{Version: "1.30.0"}, {Version: "1.31.0"}})
		loads, sources, installs := 0, 0, 0
		operations := catalogTestOperations()
		operations.Install = func(_ context.Context, request install.Request) (install.Result, error) {
			installs++
			return install.Result{Version: request.Version, Path: "/go/" + request.Version}, nil
		}
		operations.LoadCatalog = func(context.Context) ([]utils.GoVersion, error) {
			loads++
			if loads == 1 {
				return []utils.GoVersion{{Version: "1.31.0"}}, nil
			}
			return append(installedSnapshot("1.30.0"), utils.GoVersion{Version: "1.31.0"}), nil
		}
		operations.DistributionSource = func(context.Context, string) (application.DistributionSourceResult, error) {
			sources++
			return application.DistributionSourceResult{}, nil
		}
		a.bindOperations(operations)
		_, completion := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.30.0")
		refresh, _ := a.apply(catalogRefreshMsg{manual: true})
		a.apply(refresh())
		verify, _ := a.apply(completion)
		if verify == nil {
			t.Fatal("missing verification")
		}
		for _, msg := range []tea.Msg{
			catalogRefreshMsg{manual: true}, catalogRefreshMsg{},
			catalogSourceCheckMsg{source: config.DefaultDistributionSource},
			catalogActionMsg{kind: catalogActionInstall, version: "1.31.0", tab: AvailableTab},
		} {
			cmd, status := a.apply(msg)
			if cmd != nil || status.scope != catalogStatusUntouched {
				t.Fatalf("verification interrupted by %T: %+v", msg, status)
			}
			if _, source := msg.(catalogSourceCheckMsg); source {
				if _, ok := status.settingsMsg.(sourceCheckRejectedMsg); !ok {
					t.Fatalf("missing source rejection: %+v", status)
				}
			}
		}
		_, status := a.apply(verify())
		if status.kind != "success" || loads != 2 || sources != 0 || installs != 1 {
			t.Fatalf("verification=%+v loads=%d sources=%d installs=%d", status, loads, sources, installs)
		}
		_, next := admitCatalogTestMutation(t, &a, catalogMutationInstall, "1.31.0")
		a.apply(next)
		if installs != 2 {
			t.Fatal("verification did not release mutation admission")
		}
	})
}

func TestInstalledTabCatalogDeleteConfirmationUsesActionTab(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target int
		other  int
	}{
		{name: "Installed from Available", target: InstalledTab, other: AvailableTab},
		{name: "Available from Installed", target: AvailableTab, other: InstalledTab},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newVersionCacheTestModel(t)
			var deleted string
			m = m.BindVersionOperations(VersionOperations{Delete: func(_ context.Context, version string) (lifecycle.DeletionResult, error) {
				deleted = version
				return lifecycle.DeletionResult{Version: version}, nil
			}})
			m.CurrentTab = tt.other
			cmd := m.applyCatalog(catalogActionMsg{kind: catalogActionRequestDelete, version: "1.26.0", tab: tt.target})
			if cmd != nil || m.inputContext() == inputDeleteConfirm {
				t.Fatal("confirmation was addressed to the active tab instead of the action tab")
			}
			// Change only the presentation context: tab-switch teardown would cancel the request.
			m.CurrentTab = tt.target
			if m.inputContext() != inputDeleteConfirm || !strings.Contains(m.Status.Text(), "delete Go 1.26.0?") {
				t.Fatalf("action tab confirmation: context=%v status=%q", m.inputContext(), m.Status.Text())
			}
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'Y'})
			m = runCatalogTestCmd(t, updated.(Model), cmd)
			if deleted != "1.26.0" || m.inputContext() == inputDeleteConfirm {
				t.Fatalf("confirmation target=%q, context=%v", deleted, m.inputContext())
			}
			m.CurrentTab = tt.other
			if m.inputContext() == inputDeleteConfirm {
				t.Fatal("confirmation leaked to the other catalog tab")
			}
		})
	}
}
