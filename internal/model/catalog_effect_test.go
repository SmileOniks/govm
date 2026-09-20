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
		a.distributionSource = func(_ context.Context, source string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{Source: source}, nil
		}
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
		a := newCatalogProjectionAdapter(testTheme())
		op := a.startMutation(catalogMutationInstall, "1.30.0")
		verify, _ := a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"})
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
			a := newCatalogProjectionAdapter(testTheme())
			var load tea.Cmd
			if tt.reconcile {
				op := a.startMutation(catalogMutationInstall, "1.30.0")
				load, _ = a.apply(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"})
			} else {
				load, _ = a.apply(catalogRefreshMsg{})
			}
			_, status := a.apply(tt.response(catalogRequestID(t, load)))
			if status.scope != catalogStatusGlobal || status.kind != "error" || !strings.HasPrefix(status.text, tt.want) {
				t.Fatalf("failure status = %+v, want global error %q", status, tt.want)
			}
			if a.activityState().kind != catalogActivityIdle {
				t.Fatal("failed load remained active")
			}
		})
	}
}

func TestUnconfirmedCatalogStillDeliversRefilter(t *testing.T) {
	m := newTestModel(t)
	seedVersions(t, &m, []utils.GoVersion{{Version: "1.24.4"}})
	m = applyFilter(t, m, "1.24")
	op := m.projection.startMutation(catalogMutationInstall, "1.30.0")
	updated, verify := m.Update(installSuccessMsg{OperationID: op.id, Version: op.version, Path: "/go/1.30.0"})
	m = updated.(Model)
	updated, cmd := m.Update(catalogLoadedMsg{
		RequestID: catalogRequestID(t, verify),
		Versions:  []utils.GoVersion{{Version: "1.24.5"}, {Version: "1.30.0"}},
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("failed verification dropped the published snapshot's refilter")
	}
	m = settleCmd(t, m, cmd)
	if m.projection.refilterPending || selectedListVersion(m) != "1.24.5" {
		t.Fatalf("refilter did not finish: pending=%v, selection=%q", m.projection.refilterPending, selectedListVersion(m))
	}
	if m.Status.Text() != "The operation could not be confirmed against the installed catalog." {
		t.Fatalf("status = %q", m.Status.Text())
	}
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
				LoadCatalog: func(context.Context) ([]utils.GoVersion, error) {
					loads++
					return []utils.GoVersion{{Version: "1.24.4"}}, nil
				},
				DiskUsage: func(context.Context) (prune.Summary, error) {
					usageCalls++
					return prune.Summary{}, nil
				},
			})
			updated, cmd := m.Update(pruneDoneMsg{
				Result: prune.Result{Removed: []prune.Candidate{{Version: "1.24.4", Bytes: 1024}}},
				Err:    tt.err,
			})
			m = settleCmd(t, updated.(Model), cmd)
			if !strings.HasPrefix(m.Status.Text(), tt.want) || m.Status.Scope() != statusScopeGlobal {
				t.Fatalf("prune status = %q, scope=%v", m.Status.Text(), m.Status.Scope())
			}
			if loads != 1 || usageCalls != 1 {
				t.Fatalf("load calls=%d, disk usage calls=%d, want 1 each", loads, usageCalls)
			}
		})
	}
}
