package model

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/SmileOniks/govm/internal/application"
	"github.com/SmileOniks/govm/internal/utils"
)

// catalogLoadPurpose identifies why a catalog load was requested. The
// purpose is carried with the request so callers can correlate refreshes
// without inferring intent from the request ID.
type catalogLoadPurpose string

const (
	catalogLoadPurposeInitial   catalogLoadPurpose = "initial"
	catalogLoadPurposeRefresh   catalogLoadPurpose = "refresh"
	catalogLoadPurposeReconcile catalogLoadPurpose = "reconcile"
)

// catalogLoadRequest identifies one catalog load operation.
type catalogLoadRequest struct {
	ID      uint64
	Purpose catalogLoadPurpose
}

// catalogLoadedMsg carries the result of one catalog load operation.
type catalogLoadedMsg struct {
	RequestID uint64
	Versions  []utils.GoVersion
}

// catalogLoadFailedMsg carries the error from one catalog load operation.
type catalogLoadFailedMsg struct {
	RequestID uint64
	Err       error
}

type distributionSourceValidatedMsg struct {
	RequestID uint64
	Result    application.DistributionSourceResult
	Err       error
}

// loadCatalogFunc returns the available Go version catalog. The TUI
// depends on this narrow seam rather than on the composition root, so
// tests can supply a catalog without wiring real services.
type loadCatalogFunc func(context.Context) ([]utils.GoVersion, error)

// LoadVersionsCmd wraps the synchronous catalog load in a tea.Cmd.
// This is the TUI adapter: it translates between the loader's domain
// result and Bubbletea's message protocol.
func LoadVersionsCmd(load loadCatalogFunc, request catalogLoadRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if load == nil {
			return catalogLoadFailedMsg{
				RequestID: request.ID,
				Err:       errors.New("no loader configured"),
			}
		}
		versions, err := load(ctx)
		if err != nil {
			return catalogLoadFailedMsg{RequestID: request.ID, Err: err}
		}

		return catalogLoadedMsg{RequestID: request.ID, Versions: versions}
	}
}

func ChangeDistributionSourceCmd(operation changeDistributionSourceFunc, request catalogLoadRequest, source string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if operation == nil {
			return distributionSourceValidatedMsg{
				RequestID: request.ID,
				Err:       errors.New("no distribution source operation configured"),
			}
		}
		result, err := operation(ctx, source)
		return distributionSourceValidatedMsg{
			RequestID: request.ID,
			Result:    result,
			Err:       err,
		}
	}
}
