package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/SmileOniks/govm/internal/paths"
)

type DepsDisplayMode string

const (
	DepsDisplayDirect DepsDisplayMode = "direct"
	DepsDisplayAll    DepsDisplayMode = "all"
)

type ThemeName string

const (
	ThemeCurrent ThemeName = "current"
	ThemeLight   ThemeName = "light"
)

// UpgradeNoticeMode controls whether the TUI checks GitHub once per
// session for a newer govm release and shows the Upgrade notice in
// the header. It is a string enum rather than a bool so that a
// missing key in settings.json normalises to "on" instead of the
// zero value.
type UpgradeNoticeMode string

const (
	UpgradeNoticeOn  UpgradeNoticeMode = "on"
	UpgradeNoticeOff UpgradeNoticeMode = "off"
)

const (
	tempFilePrefix            = ".settings-"
	defaultDepsBackupLimit    = 10
	MinDepsBackupLimit        = 1
	MaxDepsBackupLimit        = 100
	DefaultDistributionSource = "https://go.dev/dl/"
)

type Settings struct {
	DepsDisplay        DepsDisplayMode   `json:"depsDisplay"`
	Theme              ThemeName         `json:"theme"`
	DepsBackupLimit    int               `json:"depsBackupLimit"`
	DistributionSource string            `json:"distributionSource"`
	UpgradeNotice      UpgradeNoticeMode `json:"upgradeNotice"`
}

func DefaultSettings() Settings {
	return Settings{
		DepsDisplay:        DepsDisplayDirect,
		Theme:              ThemeCurrent,
		DepsBackupLimit:    defaultDepsBackupLimit,
		DistributionSource: DefaultDistributionSource,
		UpgradeNotice:      UpgradeNoticeOn,
	}
}

func Normalize(settings Settings) Settings {
	if settings.DepsDisplay != DepsDisplayDirect && settings.DepsDisplay != DepsDisplayAll {
		settings.DepsDisplay = DepsDisplayDirect
	}
	if settings.Theme != ThemeCurrent && settings.Theme != ThemeLight {
		settings.Theme = ThemeCurrent
	}
	if ValidateDepsBackupLimit(settings.DepsBackupLimit) != nil {
		settings.DepsBackupLimit = defaultDepsBackupLimit
	}
	if settings.UpgradeNotice != UpgradeNoticeOn && settings.UpgradeNotice != UpgradeNoticeOff {
		settings.UpgradeNotice = UpgradeNoticeOn
	}
	if strings.TrimSpace(settings.DistributionSource) == "" {
		settings.DistributionSource = DefaultDistributionSource
	} else if normalized, err := ValidateDistributionSource(settings.DistributionSource); err == nil {
		settings.DistributionSource = normalized
	}
	return settings
}

func ValidateDistributionSource(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("distribution source is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse distribution source: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("distribution source scheme must be https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("distribution source host is required")
	}
	if parsed.User != nil {
		return "", errors.New("distribution source must not contain user information")
	}
	if parsed.RawQuery != "" {
		return "", errors.New("distribution source must not contain query parameters")
	}
	if parsed.Fragment != "" {
		return "", errors.New("distribution source must not contain a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func ValidateDepsBackupLimit(limit int) error {
	if limit < MinDepsBackupLimit || limit > MaxDepsBackupLimit {
		return fmt.Errorf("must be between %d and %d", MinDepsBackupLimit, MaxDepsBackupLimit)
	}
	return nil
}

// DefaultPath returns the canonical settings file location under
// the user's home directory (e.g. ~/.govm/settings.json). The
// (string, error) signature is preserved so existing callers can
// keep wiring it up as a function value.
func DefaultPath() (string, error) {
	return defaultPathFor(paths.New())
}

// defaultPathFor returns the canonical settings file location
// using the provided resolver. It is the single place where
// DefaultPath asks for a settings path so tests can inject a
// stub resolver without touching the real filesystem.
func defaultPathFor(r *paths.Resolver) (string, error) {
	return r.SettingsFile()
}

func Load(path string) (Settings, error) {
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return Settings{}, err
		}
		path = defaultPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultSettings(), nil
		}
		return Settings{}, err
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, err
	}
	return Normalize(settings), nil
}

func Save(path string, settings Settings) error {
	return saveWithRename(path, settings, os.Rename)
}

func saveWithRename(path string, settings Settings, rename func(string, string) error) error {
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return fmt.Errorf("resolve default settings path: %w", err)
		}
		path = defaultPath
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	data, err := json.MarshalIndent(Normalize(settings), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')

	tempFile, err := os.CreateTemp(dir, tempFilePrefix)
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}
	tempPath := tempFile.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		if closeErr := tempFile.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("write temp settings file: %w", err), fmt.Errorf("close temp settings file: %w", closeErr))
		}
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := tempFile.Chmod(0o644); err != nil {
		if closeErr := tempFile.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("set temp settings file mode: %w", err), fmt.Errorf("close temp settings file: %w", closeErr))
		}
		return fmt.Errorf("set temp settings file mode: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp settings file: %w", err)
	}
	if err := rename(tempPath, path); err != nil {
		return fmt.Errorf("rename settings file: %w", err)
	}
	renamed = true
	return nil
}

// LoadWithMigration loads settings from the canonical location,
// performing a one-shot migration from the legacy user-config
// directory when needed. The returned path is the active settings
// file location (empty when neither location has a file), the
// migrated bool signals whether a legacy file was copied to the
// new location and removed, and any migration error is returned
// without deleting the legacy file.
func LoadWithMigration() (string, Settings, bool, error) {
	return loadWithMigrationFor(paths.New())
}

func loadWithMigrationFor(r *paths.Resolver) (string, Settings, bool, error) {
	newPath, err := r.SettingsFile()
	if err != nil {
		return "", Settings{}, false, fmt.Errorf("resolve settings path: %w", err)
	}

	switch _, statErr := os.Stat(newPath); {
	case statErr == nil:
		settings, loadErr := Load(newPath)
		if loadErr != nil {
			return "", Settings{}, false, loadErr
		}
		return newPath, settings, false, nil
	case !errors.Is(statErr, os.ErrNotExist):
		return "", Settings{}, false, fmt.Errorf("check settings file: %w", statErr)
	}

	legacyPath, hasLegacy, err := r.LegacySettingsFile()
	if err != nil {
		return "", Settings{}, false, fmt.Errorf("resolve legacy settings path: %w", err)
	}
	if !hasLegacy {
		return "", DefaultSettings(), false, nil
	}

	if _, statErr := os.Stat(legacyPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", DefaultSettings(), false, nil
		}
		return "", Settings{}, false, fmt.Errorf("check legacy settings file: %w", statErr)
	}

	settings, err := Load(legacyPath)
	if err != nil {
		return "", Settings{}, false, err
	}

	if err := Save(newPath, settings); err != nil {
		return "", Settings{}, false, fmt.Errorf("migrate settings to %s: %w", newPath, err)
	}

	if err := os.Remove(legacyPath); err != nil {
		return newPath, settings, true, fmt.Errorf("remove legacy settings file %s: %w", legacyPath, err)
	}

	return newPath, settings, true, nil
}
