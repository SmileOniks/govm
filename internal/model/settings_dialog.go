package model

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

func renderDepsBackupLimitDialog(t styles.Theme, settings settingsTab, viewport viewportSize) renderedSurface {
	errMessage := settings.depsBackupLimitInputErr
	if errMessage == "" && settings.depsBackupLimitInput.Err != nil {
		errMessage = settings.depsBackupLimitInput.Err.Error()
	}

	lines := []string{
		t.DialogTitleStyle.Render("Set dependency backup limit"),
		"",
		t.DialogBodyStyle.Render(fmt.Sprintf(
			"Enter a whole number from %d to %d.",
			config.MinDepsBackupLimit,
			config.MaxDepsBackupLimit,
		)),
		t.DialogBodyStyle.Render(settings.depsBackupLimitInput.View()),
	}
	if errMessage != "" {
		lines = append(lines, t.DialogWarningStyle.Render(errMessage))
	}
	footer := renderControls(t, []helpSection{editingKeyBindings(false)}, dialogWidth(viewport)-6, false)
	if budget := dialogBodyHeight(t, viewport, footer); len(lines) > budget {
		lines = lines[:budget]
	}
	body := renderedSurface{content: lipgloss.JoinVertical(lipgloss.Left, lines...)}
	return renderDialog(t, joinSurfaces(body, footer), errMessage != "", viewport)
}

func renderDistributionSourceDialog(t styles.Theme, settings settingsTab, viewport viewportSize) renderedSurface {
	errMessage := settings.distributionSourceInputErr
	if errMessage == "" && settings.distributionSourceInput.Err != nil {
		errMessage = settings.distributionSourceInput.Err.Error()
	}
	if settings.checkingDistributionSource {
		errMessage = "Checking distribution source..."
	}

	lines := []string{
		t.DialogTitleStyle.Render("Set distribution source"),
		"",
		t.DialogBodyStyle.Render("Enter an HTTPS base URL for the catalog and archives."),
		t.DialogBodyStyle.Render(settings.distributionSourceInput.View()),
	}
	if errMessage != "" {
		lines = append(lines, t.DialogWarningStyle.Render(errMessage))
	}
	footer := renderControls(
		t,
		[]helpSection{editingKeyBindings(true)},
		dialogWidth(viewport)-6,
		settings.checkingDistributionSource,
	)
	if budget := dialogBodyHeight(t, viewport, footer); len(lines) > budget {
		lines = lines[:budget]
	}
	body := renderedSurface{content: lipgloss.JoinVertical(lipgloss.Left, lines...)}
	return renderDialog(t, joinSurfaces(body, footer), errMessage != "", viewport)
}
