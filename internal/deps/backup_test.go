package deps

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDependencyBackupStore_SaveUsesInjectedClockWithoutGlobalState(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	now := time.Date(2026, 7, 10, 12, 34, 56, 0, time.UTC)
	store := dependencyBackupStore{now: func() time.Time { return now }}
	info, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if !info.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %s, want %s", info.CreatedAt, now)
	}
}

func TestDependencyBackupStore_SaveCreatesUniqueNamesForSameTimestamp(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	store := dependencyBackupStore{
		now: func() time.Time {
			return time.Date(2026, 7, 10, 12, 34, 56, 0, time.UTC)
		},
	}
	first, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreRestore)
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	if first.Name == second.Name {
		t.Fatalf("backup names collide: %q", first.Name)
	}
}

func TestDependencyBackupStore_SaveWithRetentionKeepsPublishedBackupAtSameTimestamp(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	now := time.Date(2026, 7, 10, 12, 34, 56, 0, time.UTC)
	store := dependencyBackupStore{
		now: func() time.Time { return now },
	}

	first, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate, 1)
	if err != nil {
		t.Fatalf("first saveWithRetention: %v", err)
	}
	second, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreRestore, 1)
	if err != nil {
		t.Fatalf("second saveWithRetention: %v", err)
	}

	if _, err := os.Stat(second.Path); err != nil {
		t.Errorf("published backup %q: %v", second.Name, err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Errorf("first backup still exists or stat failed: %v", err)
	}
}

func TestDependencyBackupStore_SaveWithRetentionPrunesOldestValidBackups(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	writeDependencyBackupFile(t, dir, "oldest.json", context.Path, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	writeDependencyBackupFile(t, dir, "same-created-a.json", context.Path, time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	writeDependencyBackupFile(t, dir, "same-created-z.json", context.Path, time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))

	store := dependencyBackupStore{
		now: func() time.Time {
			return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		},
	}
	info, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate, 3)
	if err != nil {
		t.Fatalf("saveWithRetention: %v", err)
	}

	for _, name := range []string{info.Name, "same-created-z.json", "same-created-a.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("retained backup %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "oldest.json")); !os.IsNotExist(err) {
		t.Errorf("oldest backup still exists or stat failed: %v", err)
	}
}

func TestListDependencyBackupsSortsNewestFirstAndEqualTimesByNameDesc(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	writeDependencyBackupFile(t, dir, "older.json", context.Path, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	writeDependencyBackupFile(t, dir, "same-time-a.json", context.Path, time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	writeDependencyBackupFile(t, dir, "newest.json", context.Path, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	writeDependencyBackupFile(t, dir, "same-time-z.json", context.Path, time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))

	backups, err := listDependencyBackupsResolved(context)
	if err != nil {
		t.Fatalf("listDependencyBackupsResolved: %v", err)
	}
	got := make([]string, len(backups))
	for i, backup := range backups {
		got[i] = backup.Name
	}
	want := []string{"newest.json", "same-time-z.json", "same-time-a.json", "older.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backup sort order = %v, want %v", got, want)
	}
}

func TestSortDependencyBackupsNewestFirst(t *testing.T) {
	older := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	sameTime := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	backups := []DependencyBackupInfo{
		{Name: "older.json", CreatedAt: older},
		{Name: "same-time-a.json", CreatedAt: sameTime},
		{Name: "newest.json", CreatedAt: newest},
		{Name: "same-time-z.json", CreatedAt: sameTime},
	}
	want := []string{"newest.json", "same-time-z.json", "same-time-a.json", "older.json"}

	sortDependencyBackupsNewestFirst(backups)

	got := make([]string, len(backups))
	for i, backup := range backups {
		got[i] = backup.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backup sort order = %v, want %v", got, want)
	}
}

func TestDependencyBackupStore_SaveWithRetentionProtectsIneligibleFiles(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	writeDependencyBackupFile(t, dir, "old-valid.json", context.Path, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	writeFile(t, dir, "malformed.json", "{")
	writeFile(t, dir, "unsupported.json", `{"schema_version":2}`)
	writeDependencyBackupFile(t, dir, "foreign.json", "example.com/other", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	writeFile(t, dir, "notes.txt", "do not remove")

	store := dependencyBackupStore{
		now: func() time.Time {
			return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		},
	}
	if _, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate, 1); err != nil {
		t.Fatalf("saveWithRetention: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "old-valid.json")); !os.IsNotExist(err) {
		t.Errorf("old valid backup still exists or stat failed: %v", err)
	}
	for _, name := range []string{"malformed.json", "unsupported.json", "foreign.json", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("protected file %q: %v", name, err)
		}
	}
}

func TestDependencyBackupStore_SaveWithRetentionKeepsPublishedFileOnRemoveFailure(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	writeDependencyBackupFile(t, dir, "old-valid.json", context.Path, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))

	removeErr := errors.New("injected remove failure")
	store := dependencyBackupStore{
		now: func() time.Time {
			return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		},
		remove: func(string) error { return removeErr },
	}
	info, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate, 1)
	if !errors.Is(err, removeErr) {
		t.Fatalf("saveWithRetention error = %v, want injected remove failure", err)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Errorf("published backup %q: %v", info.Name, err)
	}
}

func TestDependencyBackupStore_SaveWithRetentionIgnoresMissingOldBackup(t *testing.T) {
	setTestHome(t)
	context := dependencyBackupTestContext(t)
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	writeDependencyBackupFile(t, dir, "old-valid.json", context.Path, time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))

	store := dependencyBackupStore{
		now: func() time.Time {
			return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		},
		remove: func(string) error { return os.ErrNotExist },
	}
	info, err := store.saveWithRetention(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate, 1)
	if err != nil {
		t.Fatalf("saveWithRetention: %v", err)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Errorf("published backup %q: %v", info.Name, err)
	}
}

func dependencyBackupTestContext(t *testing.T) moduleContext {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}
	return context
}

func writeDependencyBackupFile(t *testing.T, dir, name, modulePath string, createdAt time.Time) {
	t.Helper()
	bytes, err := json.Marshal(DependencyBackup{
		SchemaVersion: dependencyBackupSchemaVersion,
		CreatedAt:     createdAt,
		ModulePath:    modulePath,
		Snapshot:      &DependencySnapshot{},
	})
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	writeFile(t, dir, name, string(bytes))
}

func TestDependencyBackupStore_SaveDoesNotPublishPartialFileOnWriteFailure(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	store := dependencyBackupStore{
		now:   time.Now,
		write: func(*os.File, []byte) (int, error) { return 0, errors.New("injected write failure") },
	}
	if _, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate); err == nil {
		t.Fatal("save succeeded, want injected write failure")
	}

	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			t.Fatalf("published incomplete backup %q", entry.Name())
		}
	}
}

func TestDependencyBackupStore_SaveDoesNotPublishPartialFileOnShortWrite(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	store := dependencyBackupStore{
		now: time.Now,
		write: func(file *os.File, bytes []byte) (int, error) {
			return file.Write(bytes[:len(bytes)-1])
		},
	}
	if _, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("save error = %v, want io.ErrShortWrite", err)
	}
	assertDependencyBackupDirectoryEmpty(t, context.Path)
}

func TestDependencyBackupStore_SaveDoesNotPublishPartialFileOnSyncFailure(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	store := dependencyBackupStore{
		now:  time.Now,
		sync: func(*os.File) error { return errors.New("injected sync failure") },
	}
	if _, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate); err == nil {
		t.Fatal("save succeeded, want injected sync failure")
	}
	assertDependencyBackupDirectoryEmpty(t, context.Path)
}

func TestDependencyBackupStore_SaveDoesNotPublishPartialFileOnCloseFailure(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	context, err := resolveModuleContext(root)
	if err != nil {
		t.Fatalf("resolveModuleContext: %v", err)
	}

	store := dependencyBackupStore{
		now:   time.Now,
		close: func(*os.File) error { return errors.New("injected close failure") },
	}
	if _, err := store.save(context, &DependencySnapshot{}, DependencyBackupKindPreUpdate); err == nil {
		t.Fatal("save succeeded, want injected close failure")
	}
	assertDependencyBackupDirectoryEmpty(t, context.Path)
}

func assertDependencyBackupDirectoryEmpty(t *testing.T, modulePath string) {
	t.Helper()
	dir, err := dependencyBackupProjectDir(modulePath)
	if err != nil {
		t.Fatalf("dependencyBackupProjectDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("backup directory entries = %v, want none", entries)
	}
}

func TestDefaultOperations_ApplyUpdates_PassesEntriesAndLimit(t *testing.T) {
	context := moduleContext{Root: t.TempDir(), Path: "example.com/app"}
	writeFile(t, context.Root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	entries := []DependencyUpdateEntry{{Path: "example.com/dep", OldVersion: "v1.0.0", NewVersion: "v1.1.0"}}
	const backupLimit = 3
	operation := dependencyOperation{
		saveBackup: func(_ moduleContext, snap *DependencySnapshot, _ string, gotLimit int) (DependencyBackupInfo, error) {
			if gotLimit != backupLimit {
				t.Fatalf("backup limit = %d, want %d", gotLimit, backupLimit)
			}
			if !reflect.DeepEqual(snap.Updatable, entries) {
				t.Fatalf("snapshot entries = %#v, want %#v", snap.Updatable, entries)
			}
			return DependencyBackupInfo{}, nil
		},
		runCommand: func(_ moduleContext, args ...string) ([]byte, error) {
			if args[0] == "get" {
				want := []string{"get", "example.com/dep@v1.1.0"}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("go get args = %#v, want %#v", args, want)
				}
			}
			return nil, nil
		},
		load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
			return []ModuleDependency{}, nil
		},
	}

	snap, _, deps, err := defaultOperations{operation: operation}.ApplyUpdates(context, entries, backupLimit)
	if err != nil {
		t.Fatalf("ApplyUpdates: %v", err)
	}
	if len(deps) != 0 || snap == nil {
		t.Fatalf("apply result = snap=%v deps=%v, want non-nil snapshot", snap, deps)
	}
}

func TestDefaultOperations_RestoreBackup_PassesLimitAndReportsBackup(t *testing.T) {
	context := moduleContext{Root: t.TempDir(), Path: "example.com/app"}
	writeFile(t, context.Root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	const backupLimit = 4
	backup := &DependencyBackup{
		SchemaVersion: 1,
		CreatedAt:     time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		ModulePath:    context.Path,
		ModuleDir:     context.Root,
		Kind:          DependencyBackupKindPreUpdate,
		Snapshot:      &DependencySnapshot{ModFile: ModuleFileSnapshot{Exists: true, Content: "old"}},
	}
	operation := dependencyOperation{
		loadBackup: func(_ moduleContext, _ string) (*DependencyBackup, error) { return backup, nil },
		saveBackup: func(_ moduleContext, _ *DependencySnapshot, _ string, gotLimit int) (DependencyBackupInfo, error) {
			if gotLimit != backupLimit {
				t.Fatalf("backup limit = %d, want %d", gotLimit, backupLimit)
			}
			return DependencyBackupInfo{}, nil
		},
		restoreFiles: func(_ moduleContext, _ *DependencySnapshot) error { return nil },
		load: func(_ moduleContext, _ bool) ([]ModuleDependency, error) {
			return []ModuleDependency{}, nil
		},
	}

	result, err := defaultOperations{operation: operation}.RestoreBackup(context, "saved.json", backupLimit)
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if result.BackupName != "saved.json" || !result.BackupCreated.Equal(backup.CreatedAt) {
		t.Fatalf("restore result = %+v, want saved backup metadata", result)
	}
}
