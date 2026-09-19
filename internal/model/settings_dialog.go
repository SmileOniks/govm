package model

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/smileoniks-ctrl/govm/internal/config"
	"github.com/smileoniks-ctrl/govm/internal/styles"
)

func renderDepsBackupLimitDialog(t styles.Theme, settings settingsTab, viewport viewportSize) string {
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
	lines = append(lines,
		"",
		t.DialogMutedStyle.Render("enter: save  esc: cancel"),
	)

	return renderDialog(
		t,
		lipgloss.JoinVertical(lipgloss.Left, lines...),
		errMessage != "",
		viewport,
	)
}

func renderDistributionSourceDialog(t styles.Theme, settings settingsTab, viewport viewportSize) string {
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
	lines = append(lines,
		"",
		t.DialogMutedStyle.Render("enter: check and save  r: reset to official  esc: cancel"),
	)

	return renderDialog(
		t,
		lipgloss.JoinVertical(lipgloss.Left, lines...),
		errMessage != "",
		viewport,
	)
}
