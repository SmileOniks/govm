package deps

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultOperations_Load_PassesCheckUpdatesFlagAndErrors(t *testing.T) {
	loadErr := errors.New("load failed")
	want := []ModuleDependency{{
		Path:    "example.com/dependency",
		Version: "v1.0.0",
		Latest:  "v1.1.0",
	}}
	tests := []struct {
		name         string
		checkUpdates bool
		loadErr      error
	}{
		{name: "list without update check", checkUpdates: false},
		{name: "check available updates", checkUpdates: true},
		{name: "load error", loadErr: loadErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := moduleContext{Root: t.TempDir(), Path: "example.com/module"}
			ops := defaultOperations{operation: dependencyOperation{
				load: func(ctx moduleContext, checkUpdates bool) ([]ModuleDependency, error) {
					if ctx != context {
						t.Fatalf("context = %+v, want %+v", ctx, context)
					}
					if checkUpdates != tt.checkUpdates {
						t.Fatalf("check updates = %t, want %t", checkUpdates, tt.checkUpdates)
					}
					if tt.loadErr != nil {
						return nil, tt.loadErr
					}
					return want, nil
				},
			}}
			got, err := ops.Load(context, tt.checkUpdates)
			if !errors.Is(err, tt.loadErr) {
				t.Fatalf("error = %v, want %v", err, tt.loadErr)
			}
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("dependencies = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDefaultOperations_RunChecks_Success(t *testing.T) {
	root := t.TempDir()
	calls := [][]string{}
	ops := defaultOperations{operation: dependencyOperation{
		runCommand: func(ctx moduleContext, args ...string) ([]byte, error) {
			if ctx.Root != root {
				t.Fatalf("root = %q, want %q", ctx.Root, root)
			}
			calls = append(calls, append([]string{}, args...))
			return nil, nil
		},
	}}
	result, err := ops.RunChecks(moduleContext{Root: root, Path: "example.com/module"})
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v, want OK", result)
	}
	wantCalls := [][]string{{"test", "./..."}, {"vet", "./..."}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("commands = %#v, want %#v", calls, wantCalls)
	}
}

func TestDefaultOperations_RunChecks_CommandFailureIsResult(t *testing.T) {
	calls := 0
	const maxCheckOutputLines = 8
	output := strings.Repeat("failure\n", maxCheckOutputLines+2)
	ops := defaultOperations{operation: dependencyOperation{
		runCommand: func(_ moduleContext, _ ...string) ([]byte, error) {
			calls++
			return []byte(output), errors.New("exit status 1")
		},
	}}
	result, err := ops.RunChecks(moduleContext{Root: t.TempDir(), Path: "example.com/module"})
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	if result.OK || result.Command != "go test ./..." {
		t.Fatalf("result = %+v, want failed go test result", result)
	}
	if !strings.Contains(result.Output, "more lines") {
		t.Fatalf("output = %q, want trimmed output", result.Output)
	}
	if calls != 1 {
		t.Fatalf("command calls = %d, want 1", calls)
	}
}

func TestDefaultOperations_ApplyUpdates_TidyFailureLeavesUpdatedFiles(t *testing.T) {
	root := t.TempDir()
	originalMod := "module example.com/app\n\ngo 1.26\n"
	updatedMod := originalMod + "\nrequire example.com/dependency v1.1.0\n"
	writeFile(t, root, "go.mod", originalMod)

	commandCalls := 0
	ops := defaultOperations{operation: dependencyOperation{
		saveBackup: func(_ moduleContext, _ *DependencySnapshot, _ string, _ int) (DependencyBackupInfo, error) {
			return DependencyBackupInfo{}, nil
		},
		runCommand: func(ctx moduleContext, _ ...string) ([]byte, error) {
			commandCalls++
			if commandCalls == 1 {
				if err := os.WriteFile(filepath.Join(ctx.Root, "go.mod"), []byte(updatedMod), 0644); err != nil {
					t.Fatalf("write updated go.mod: %v", err)
				}
				return nil, nil
			}
			return []byte("tidy failed"), errors.New("exit status 1")
		},
		load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
			t.Fatal("loader must not run after tidy error")
			return nil, nil
		},
	}}
	_, _, _, err := ops.ApplyUpdates(
		moduleContext{Root: root, Path: "example.com/app"},
		[]DependencyUpdateEntry{{
			Path:       "example.com/dependency",
			OldVersion: "v1.0.0",
			NewVersion: "v1.1.0",
		}},
		3,
	)
	if err == nil || !strings.Contains(err.Error(), "go mod tidy failed: tidy failed") {
		t.Fatalf("error = %v, want contextual tidy error", err)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "go.mod"))
	if readErr != nil {
		t.Fatalf("read go.mod: %v", readErr)
	}
	if string(got) != updatedMod {
		t.Fatalf("go.mod = %q, want updated bytes %q", got, updatedMod)
	}
}

func TestDependencyMutationRefreshErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	refreshErr := errors.New("refresh failed")
	snapshot := &DependencySnapshot{
		ModFile: ModuleFileSnapshot{
			Exists:  true,
			Content: "module example.com/app\n\ngo 1.26\n",
		},
	}
	context := moduleContext{Root: root, Path: "example.com/app"}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "update",
			run: func() error {
				ops := defaultOperations{operation: dependencyOperation{
					saveBackup: func(_ moduleContext, _ *DependencySnapshot, _ string, _ int) (DependencyBackupInfo, error) {
						return DependencyBackupInfo{}, nil
					},
					runCommand: func(_ moduleContext, _ ...string) ([]byte, error) {
						return nil, nil
					},
					load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
						return nil, refreshErr
					},
				}}
				_, _, _, err := ops.ApplyUpdates(
					context,
					[]DependencyUpdateEntry{{
						Path:       "example.com/dependency",
						OldVersion: "v1.0.0",
						NewVersion: "v1.1.0",
					}},
					3,
				)
				return err
			},
		},
		{
			name: "rollback",
			run: func() error {
				ops := defaultOperations{operation: dependencyOperation{
					restoreFiles: func(_ moduleContext, _ *DependencySnapshot) error {
						return nil
					},
					load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
						return nil, refreshErr
					},
				}}
				_, err := ops.RestoreExact(context, snapshot)
				return err
			},
		},
		{
			name: "restore",
			run: func() error {
				ops := defaultOperations{operation: dependencyOperation{
					restoreFiles: func(_ moduleContext, _ *DependencySnapshot) error {
						return nil
					},
					load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
						return nil, refreshErr
					},
					saveBackup: func(_ moduleContext, _ *DependencySnapshot, _ string, _ int) (DependencyBackupInfo, error) {
						return DependencyBackupInfo{}, nil
					},
					loadBackup: func(moduleContext, string) (*DependencyBackup, error) {
						return &DependencyBackup{Snapshot: snapshot}, nil
					},
				}}
				_, err := ops.RestoreBackup(context, "saved.json", 3)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, refreshErr) {
				t.Fatalf("error = %v, want %v", err, refreshErr)
			}
		})
	}
}
