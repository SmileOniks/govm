package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/paths"
)

func TestDefaultSettings(t *testing.T) {
	got := DefaultSettings()

	if got.DepsDisplay != DepsDisplayDirect {
		t.Fatalf("DepsDisplay = %q, want %q", got.DepsDisplay, DepsDisplayDirect)
	}
	if got.Theme != ThemeCurrent {
		t.Fatalf("Theme = %q, want %q", got.Theme, ThemeCurrent)
	}
	if got.DepsBackupLimit != 10 {
		t.Fatalf("DepsBackupLimit = %d, want 10", got.DepsBackupLimit)
	}
	if got.DistributionSource != DefaultDistributionSource {
		t.Fatalf("DistributionSource = %q, want %q", got.DistributionSource, DefaultDistributionSource)
	}
	if got.UpgradeNotice != UpgradeNoticeOn {
		t.Fatalf("UpgradeNotice = %q, want %q", got.UpgradeNotice, UpgradeNoticeOn)
	}
}

func TestValidateDepsBackupLimit(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		wantErr bool
	}{
		{name: "minimum", limit: 1},
		{name: "in range", limit: 42},
		{name: "maximum", limit: 100},
		{name: "below range", limit: 0, wantErr: true},
		{name: "above range", limit: 101, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDepsBackupLimit(tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateDepsBackupLimit(%d) error = %v, wantErr %t", tt.limit, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDistributionSource(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "normalizes trailing slash",
			raw:  " https://mirror.example/go ",
			want: "https://mirror.example/go/",
		},
		{
			name: "preserves nested path",
			raw:  "https://mirror.example/releases///",
			want: "https://mirror.example/releases/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateDistributionSource(tt.raw)
			if err != nil {
				t.Fatalf("ValidateDistributionSource(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ValidateDistributionSource(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	for _, raw := range []string{
		"",
		"http://mirror.example/go/",
		"https://mirror.example/go/?token=secret",
		"https://user:pass@mirror.example/go/",
		"https://mirror.example/go/#archive",
	} {
		t.Run("rejects "+raw, func(t *testing.T) {
			if _, err := ValidateDistributionSource(raw); err == nil {
				t.Fatalf("ValidateDistributionSource(%q) error = nil, want error", raw)
			}
		})
	}
}

func TestNormalizeUnknowns(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     Settings
	}{
		{
			name: "keeps known values",
			settings: Settings{
				DepsDisplay:     DepsDisplayAll,
				Theme:           ThemeLight,
				DepsBackupLimit: 25,
			},
			want: Settings{
				DepsDisplay:        DepsDisplayAll,
				Theme:              ThemeLight,
				DepsBackupLimit:    25,
				DistributionSource: DefaultDistributionSource,
				UpgradeNotice:      UpgradeNoticeOn,
			},
		},
		{
			name: "keeps minimum backup limit",
			settings: Settings{
				DepsDisplay:     DepsDisplayDirect,
				Theme:           ThemeCurrent,
				DepsBackupLimit: 1,
			},
			want: Settings{
				DepsDisplay:        DepsDisplayDirect,
				Theme:              ThemeCurrent,
				DepsBackupLimit:    1,
				DistributionSource: DefaultDistributionSource,
				UpgradeNotice:      UpgradeNoticeOn,
			},
		},
		{
			name: "keeps maximum backup limit",
			settings: Settings{
				DepsDisplay:     DepsDisplayAll,
				Theme:           ThemeLight,
				DepsBackupLimit: 100,
			},
			want: Settings{
				DepsDisplay:        DepsDisplayAll,
				Theme:              ThemeLight,
				DepsBackupLimit:    100,
				DistributionSource: DefaultDistributionSource,
				UpgradeNotice:      UpgradeNoticeOn,
			},
		},
		{
			name:     "defaults empty values",
			settings: Settings{},
			want:     DefaultSettings(),
		},
		{
			name: "defaults unknown values",
			settings: Settings{
				DepsDisplay:     DepsDisplayMode("transitive"),
				Theme:           ThemeName("dark"),
				DepsBackupLimit: 101,
			},
			want: DefaultSettings(),
		},
		{
			name: "keeps upgrade notice off",
			settings: Settings{
				UpgradeNotice: UpgradeNoticeOff,
			},
			want: Settings{
				DepsDisplay:        DepsDisplayDirect,
				Theme:              ThemeCurrent,
				DepsBackupLimit:    10,
				DistributionSource: DefaultDistributionSource,
				UpgradeNotice:      UpgradeNoticeOff,
			},
		},
		{
			name: "defaults unknown upgrade notice",
			settings: Settings{
				UpgradeNotice: UpgradeNoticeMode("maybe"),
			},
			want: DefaultSettings(),
		},
		{
			name: "defaults missing and invalid backup limits",
			settings: Settings{
				DepsBackupLimit: -1,
			},
			want: DefaultSettings(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.settings)
			if got != tt.want {
				t.Fatalf("Normalize(%+v) = %+v, want %+v", tt.settings, got, tt.want)
			}
		})
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "settings.json")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got != DefaultSettings() {
		t.Fatalf("Load() = %+v, want %+v", got, DefaultSettings())
	}
}

func TestLoadInvalidJSONReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadNormalizesDepsBackupLimit(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "missing", data: `{"depsDisplay":"direct","theme":"current"}`, want: 10},
		{name: "zero", data: `{"depsBackupLimit":0}`, want: 10},
		{name: "below range", data: `{"depsBackupLimit":-1}`, want: 10},
		{name: "above range", data: `{"depsBackupLimit":101}`, want: 10},
		{name: "valid", data: `{"depsBackupLimit":25}`, want: 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.DepsBackupLimit != tt.want {
				t.Fatalf("DepsBackupLimit = %d, want %d", got.DepsBackupLimit, tt.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	settings := Settings{
		DepsDisplay:        DepsDisplayAll,
		Theme:              ThemeLight,
		DepsBackupLimit:    25,
		DistributionSource: DefaultDistributionSource,
		UpgradeNotice:      UpgradeNoticeOn,
	}

	if err := Save(path, settings); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("file mode = %v, want %v", got, os.FileMode(0o644))
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got != settings {
		t.Fatalf("Load() = %+v, want %+v", got, settings)
	}
}

func TestSaveWritesNormalizedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := Settings{
		DepsDisplay: DepsDisplayMode("unknown"),
		Theme:       ThemeName("unknown"),
	}

	if err := Save(path, settings); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != DefaultSettings() {
		t.Fatalf("Load() = %+v, want %+v", got, DefaultSettings())
	}
}

func TestSaveUsesTempFileAndLeavesNoTempFilesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := Save(path, Settings{DepsDisplay: DepsDisplayAll, Theme: ThemeLight}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempFilePrefix) {
			t.Fatalf("Save() left temp file %q behind", entry.Name())
		}
	}
}

func TestSavePreservesExistingFileWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	oldData := []byte("{\"depsDisplay\":\"direct\",\"theme\":\"current\"}\n")
	if err := os.WriteFile(path, oldData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	renameErr := errors.New("rename failed")
	err := saveWithRename(path, Settings{DepsDisplay: DepsDisplayAll, Theme: ThemeLight}, func(_, _ string) error {
		return renameErr
	})
	if !errors.Is(err, renameErr) {
		t.Fatalf("Save() error = %v, want wrapped %v", err, renameErr)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(oldData) {
		t.Fatalf("settings file = %q, want original %q", got, oldData)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempFilePrefix) {
			t.Fatalf("Save() left temp file %q behind", entry.Name())
		}
	}
}

// stubResolver returns a paths.Resolver with deterministic
// HomeDir and ConfigDir under fresh t.TempDir() directories so
// tests do not touch the real filesystem. The returned home and
// config paths are exposed so a test can pre-populate the
// directories or plant blocker files inside them.
func stubResolver(t *testing.T) (*paths.Resolver, string, string) {
	t.Helper()
	home := t.TempDir()
	config := t.TempDir()
	return &paths.Resolver{
		HomeDir:   func() (string, error) { return home, nil },
		ConfigDir: func() (string, error) { return config, nil },
	}, home, config
}

func TestDefaultPathUsesHomeDir(t *testing.T) {
	stubHome := t.TempDir()
	t.Setenv("HOME", stubHome)

	resolver := &paths.Resolver{
		HomeDir:   func() (string, error) { return stubHome, nil },
		ConfigDir: func() (string, error) { return "", nil },
	}
	want, err := resolver.SettingsFile()
	if err != nil {
		t.Fatalf("resolver.SettingsFile() error = %v", err)
	}

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q (from stub resolver)", got, want)
	}
	if !strings.HasSuffix(got, ".govm/settings.json") {
		t.Fatalf("DefaultPath() = %q, want suffix .govm/settings.json", got)
	}
	expectedRoot, err := resolver.RootDir()
	if err != nil {
		t.Fatalf("resolver.RootDir() error = %v", err)
	}
	if filepath.Dir(got) != expectedRoot {
		t.Fatalf("DefaultPath() parent dir = %q, want %q (from stub resolver HomeDir)", filepath.Dir(got), expectedRoot)
	}
}

func TestLoadWithMigrationNewFilePresent(t *testing.T) {
	r, _, _ := stubResolver(t)
	newPath, err := r.SettingsFile()
	if err != nil {
		t.Fatalf("SettingsFile() error = %v", err)
	}
	legacyPath, _, err := r.LegacySettingsFile()
	if err != nil {
		t.Fatalf("LegacySettingsFile() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	newData := []byte("{\"depsDisplay\":\"all\",\"theme\":\"light\"}\n")
	if err := os.WriteFile(newPath, newData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Also create a legacy file to verify it is not touched.
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyData := []byte("{\"depsDisplay\":\"direct\",\"theme\":\"current\"}\n")
	if err := os.WriteFile(legacyPath, legacyData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	gotPath, gotSettings, migrated, err := loadWithMigrationFor(r)
	if err != nil {
		t.Fatalf("loadWithMigrationFor() error = %v", err)
	}
	if gotPath != newPath {
		t.Fatalf("path = %q, want %q", gotPath, newPath)
	}
	if migrated {
		t.Fatal("migrated = true, want false")
	}
	want := Settings{
		DepsDisplay:        DepsDisplayAll,
		Theme:              ThemeLight,
		DepsBackupLimit:    10,
		DistributionSource: DefaultDistributionSource,
		UpgradeNotice:      UpgradeNoticeOn,
	}
	if gotSettings != want {
		t.Fatalf("settings = %+v, want %+v", gotSettings, want)
	}

	// New file content is unchanged.
	newAfter, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("ReadFile(new) error = %v", err)
	}
	if string(newAfter) != string(newData) {
		t.Fatalf("new file = %q, want %q", newAfter, newData)
	}

	// Legacy file is still present and unchanged.
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy) error = %v", err)
	}
	if string(legacyAfter) != string(legacyData) {
		t.Fatalf("legacy file = %q, want %q", legacyAfter, legacyData)
	}
}

func TestLoadWithMigrationMigratesLegacy(t *testing.T) {
	r, _, _ := stubResolver(t)
	newPath, err := r.SettingsFile()
	if err != nil {
		t.Fatalf("SettingsFile() error = %v", err)
	}
	legacyPath, _, err := r.LegacySettingsFile()
	if err != nil {
		t.Fatalf("LegacySettingsFile() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyData := []byte("{\"depsDisplay\":\"all\",\"theme\":\"light\"}\n")
	if err := os.WriteFile(legacyPath, legacyData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	gotPath, gotSettings, migrated, err := loadWithMigrationFor(r)
	if err != nil {
		t.Fatalf("loadWithMigrationFor() error = %v", err)
	}
	if gotPath != newPath {
		t.Fatalf("path = %q, want %q", gotPath, newPath)
	}
	if !migrated {
		t.Fatal("migrated = false, want true")
	}
	want := Settings{
		DepsDisplay:        DepsDisplayAll,
		Theme:              ThemeLight,
		DepsBackupLimit:    10,
		DistributionSource: DefaultDistributionSource,
		UpgradeNotice:      UpgradeNoticeOn,
	}
	if gotSettings != want {
		t.Fatalf("settings = %+v, want %+v", gotSettings, want)
	}

	// New file must exist with migrated content.
	newData, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("ReadFile(new) error = %v", err)
	}
	if !strings.Contains(string(newData), "\"all\"") {
		t.Fatalf("new file = %q, want to contain \"all\"", newData)
	}
	if !strings.Contains(string(newData), "\"light\"") {
		t.Fatalf("new file = %q, want to contain \"light\"", newData)
	}

	// Legacy file must be removed.
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy file still present: %v", err)
	}
}

func TestLoadWithMigrationSaveFails(t *testing.T) {
	r, stubHome, _ := stubResolver(t)
	newPath, err := r.SettingsFile()
	if err != nil {
		t.Fatalf("SettingsFile() error = %v", err)
	}
	legacyPath, _, err := r.LegacySettingsFile()
	if err != nil {
		t.Fatalf("LegacySettingsFile() error = %v", err)
	}

	// Plant a regular file at ~/.govm so MkdirAll fails when Save
	// tries to create the settings directory.
	govmDir := filepath.Join(stubHome, ".govm")
	if err := os.WriteFile(govmDir, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Setup a legacy file with real content.
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyData := []byte("{\"depsDisplay\":\"all\",\"theme\":\"light\"}\n")
	if err := os.WriteFile(legacyPath, legacyData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	gotPath, gotSettings, migrated, err := loadWithMigrationFor(r)
	if err == nil {
		t.Fatal("loadWithMigrationFor() error = nil, want error")
	}
	if gotPath != "" {
		t.Fatalf("path = %q, want empty", gotPath)
	}
	if migrated {
		t.Fatal("migrated = true, want false")
	}
	if gotSettings != (Settings{}) {
		t.Fatalf("settings = %+v, want zero value", gotSettings)
	}

	// Legacy file must still be present because migration failed.
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy) error = %v", err)
	}
	if string(legacyAfter) != string(legacyData) {
		t.Fatalf("legacy file = %q, want %q", legacyAfter, legacyData)
	}

	// The new file must not have been written. We only care that no
	// regular file exists at newPath; os.Stat may return ENOTDIR
	// because the parent is a regular file, which is also fine.
	if _, err := os.Stat(newPath); err == nil {
		t.Fatalf("new file present despite save failure")
	}
}

func TestLoadWithMigrationBothAbsent(t *testing.T) {
	r, _, _ := stubResolver(t)

	gotPath, gotSettings, migrated, err := loadWithMigrationFor(r)
	if err != nil {
		t.Fatalf("loadWithMigrationFor() error = %v", err)
	}
	if gotPath != "" {
		t.Fatalf("path = %q, want empty", gotPath)
	}
	if migrated {
		t.Fatal("migrated = true, want false")
	}
	if gotSettings != DefaultSettings() {
		t.Fatalf("settings = %+v, want %+v", gotSettings, DefaultSettings())
	}
}
