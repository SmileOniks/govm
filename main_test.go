package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/application"
	"github.com/SmileOniks/govm/internal/cli"
	"github.com/SmileOniks/govm/internal/config"
	"github.com/SmileOniks/govm/internal/utils"
)

func TestHandleCommandLineSourceReturnsNonZeroOnOperationFailure(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"govm", "source", "https://mirror.example/dl"}
	defer func() { os.Args = originalArgs }()

	var out bytes.Buffer
	app := cli.NewApp(cli.Operations{
		ChangeDistributionSource: func(context.Context, string) (application.DistributionSourceResult, error) {
			return application.DistributionSourceResult{}, errors.New("activation failed")
		},
	}, nil, &out, &out)

	if code := handleCommandLine(app); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "Previous distribution source was preserved.") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPrintUsageShowsVersion(t *testing.T) {
	prev := utils.Version
	utils.Version = "v9.9.9-test"
	defer func() { utils.Version = prev }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	printUsage()

	w.Close()
	os.Stdout = orig

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)

	if !strings.Contains(output, "v9.9.9-test") {
		t.Fatalf("expected help output to contain version %q, got:\n%s", "v9.9.9-test", output)
	}

	if !strings.Contains(output, "GoVM") {
		t.Fatal("expected help output to mention GoVM")
	}

	for _, want := range []string{
		"govm doctor [options]",
		"govm deps list",
		"govm deps check",
		"govm deps update",
		"govm deps backups",
		"govm deps restore <file>",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected help output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestLoadTUISettings(t *testing.T) {
	defaultSettings := config.DefaultSettings()

	tests := []struct {
		name         string
		load         func() (string, config.Settings, error)
		wantPath     string
		wantSettings config.Settings
		wantWarning  bool
	}{
		{
			name: "load error warns and returns defaults with empty path",
			load: func() (string, config.Settings, error) {
				return "", defaultSettings, errors.New("config dir unavailable")
			},
			wantPath:     "",
			wantSettings: defaultSettings,
			wantWarning:  true,
		},
		{
			name: "successful load returns settings without warning",
			load: func() (string, config.Settings, error) {
				return "/tmp/govm-settings.json", config.Settings{
					DepsDisplay: config.DepsDisplayAll,
					Theme:       config.ThemeLight,
				}, nil
			},
			wantPath: "/tmp/govm-settings.json",
			wantSettings: config.Settings{
				DepsDisplay: config.DepsDisplayAll,
				Theme:       config.ThemeLight,
			},
			wantWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			gotPath, gotSettings := loadTUISettings(&stderr, tt.load)

			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotSettings != tt.wantSettings {
				t.Fatalf("settings = %#v, want %#v", gotSettings, tt.wantSettings)
			}
			hasWarning := strings.Contains(stderr.String(), "Warning:")
			if hasWarning != tt.wantWarning {
				t.Fatalf("warning presence = %t, want %t; stderr:\n%s", hasWarning, tt.wantWarning, stderr.String())
			}
		})
	}
}

func TestLoadTUISettingsMissingFileDoesNotWarn(t *testing.T) {
	// Use a fresh HOME that has no .govm and no legacy file, so
	// LoadWithMigration returns defaults without an error.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", t.TempDir())
	}

	var stderr bytes.Buffer

	gotPath, gotSettings := loadTUISettings(&stderr, func() (string, config.Settings, error) {
		path, s, _, err := config.LoadWithMigration()
		return path, s, err
	})

	if gotPath != "" {
		t.Fatalf("path = %q, want empty", gotPath)
	}
	if gotSettings != config.DefaultSettings() {
		t.Fatalf("settings = %#v, want %#v", gotSettings, config.DefaultSettings())
	}
	if stderr.String() != "" {
		t.Fatalf("expected no warning for missing settings file, got:\n%s", stderr.String())
	}
}

func TestRunDoctorUnknownFlagIsUsageError(t *testing.T) {
	var out bytes.Buffer
	if code := runDoctor([]string{"--bogus"}, &out); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(out.String(), "govm doctor  (") {
		t.Fatalf("checks ran despite a usage error:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "usage: govm doctor [--offline]") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunDoctorMissingRootFailsWithoutCreatingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// An empty PATH keeps exec.LookPath deterministic on any host.
	t.Setenv("PATH", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", t.TempDir())
	}
	root := filepath.Join(home, ".govm")

	var out bytes.Buffer
	code := runDoctor([]string{"--offline"}, &out)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output:\n%s", code, out.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor must not create %s (stat err = %v)", root, err)
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[2], "[fail] govm root "+root+" does not exist") {
		t.Fatalf("first check should fail on the missing root:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "skipped (--offline)") {
		t.Fatalf("expected the source check to be skipped:\n%s", out.String())
	}
}
