package deps

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SmileOniks/govm/internal/paths"
)

const (
	// DependencyBackupKindPreUpdate marks a snapshot captured before
	// a dependency update is applied.
	DependencyBackupKindPreUpdate = "pre-update"
	// DependencyBackupKindPreRestore marks a snapshot of the current
	// module files captured before a manual backup restore, so the
	// restore can be undone.
	DependencyBackupKindPreRestore = "pre-restore"
	dependencyBackupSchemaVersion  = 1
	defaultDependencyBackupLimit   = 10
)

// DependencyBackup is the on-disk JSON format for a dependency snapshot.
type DependencyBackup struct {
	SchemaVersion int                 `json:"schema_version"`
	CreatedAt     time.Time           `json:"created_at"`
	ModulePath    string              `json:"module_path"`
	ModuleDir     string              `json:"module_dir"`
	Kind          string              `json:"kind"`
	Snapshot      *DependencySnapshot `json:"snapshot"`
}

// DependencyBackupInfo is a compact listing item for backup selection.
type DependencyBackupInfo struct {
	Name       string
	Path       string
	CreatedAt  time.Time
	ModulePath string
	Kind       string
	Updated    int
}

type dependencyBackupStore struct {
	now    func() time.Time
	write  func(*os.File, []byte) (int, error)
	sync   func(*os.File) error
	close  func(*os.File) error
	remove func(string) error
}

func defaultDependencyBackupStore() dependencyBackupStore {
	return dependencyBackupStore{
		now:    time.Now,
		write:  (*os.File).Write,
		sync:   (*os.File).Sync,
		close:  (*os.File).Close,
		remove: os.Remove,
	}
}

// listDependencyBackupsResolved lists the saved backups of the module,
// newest first; a module without a backup directory has none.
func listDependencyBackupsResolved(context moduleContext) ([]DependencyBackupInfo, error) {
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		return nil, err
	}
	backups, err := loadDependencyBackupInfos(dir, context.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return []DependencyBackupInfo{}, nil
		}
		return nil, fmt.Errorf("read dependency backups: %w", err)
	}
	return backups, nil
}

func loadDependencyBackupInfos(dir, modulePath string) ([]DependencyBackupInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	backups := make([]DependencyBackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		backup, err := readDependencyBackupFile(path)
		if err != nil {
			continue
		}
		if backup.ModulePath != modulePath {
			continue
		}
		backups = append(backups, backupInfo(entry.Name(), path, backup))
	}
	sortDependencyBackupsNewestFirst(backups)
	return backups, nil
}

func sortDependencyBackupsNewestFirst(backups []DependencyBackupInfo) {
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAt.Equal(backups[j].CreatedAt) {
			return backups[i].Name > backups[j].Name
		}
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
}

func loadDependencyBackupResolved(context moduleContext, name string) (*DependencyBackup, error) {
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(name)
	backup, err := readDependencyBackupFile(filepath.Join(dir, base))
	if err != nil {
		return nil, err
	}
	if backup.ModulePath != context.Path {
		return nil, fmt.Errorf("backup %q belongs to module %q, current module is %q", base, backup.ModulePath, context.Path)
	}
	return &backup, nil
}

func dependencyBackupProjectDir(modulePath string) (string, error) {
	root, err := paths.New().DepsBackupDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, sanitizeModulePath(modulePath))
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", fmt.Errorf("resolve dependency backup dir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("resolve dependency backup dir: module path escapes backup root")
	}
	return dir, nil
}

func saveDependencyBackupResolvedWithRetention(context moduleContext, snap *DependencySnapshot, kind string, retentionLimit int) (DependencyBackupInfo, error) {
	return defaultDependencyBackupStore().saveWithRetention(context, snap, kind, retentionLimit)
}

func (store dependencyBackupStore) save(context moduleContext, snap *DependencySnapshot, kind string) (DependencyBackupInfo, error) {
	return store.saveWithRetention(context, snap, kind, defaultDependencyBackupLimit)
}

func (store dependencyBackupStore) saveWithRetention(context moduleContext, snap *DependencySnapshot, kind string, retentionLimit int) (DependencyBackupInfo, error) {
	if snap == nil {
		return DependencyBackupInfo{}, fmt.Errorf("save dependency backup: nil snapshot")
	}
	if retentionLimit < 1 {
		return DependencyBackupInfo{}, fmt.Errorf("save dependency backup: retention limit must be positive")
	}
	if store.now == nil {
		store.now = time.Now
	}
	if store.write == nil {
		store.write = (*os.File).Write
	}
	if store.sync == nil {
		store.sync = (*os.File).Sync
	}
	if store.close == nil {
		store.close = (*os.File).Close
	}
	if store.remove == nil {
		store.remove = os.Remove
	}
	createdAt := store.now().UTC()
	dir, err := dependencyBackupProjectDir(context.Path)
	if err != nil {
		return DependencyBackupInfo{}, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return DependencyBackupInfo{}, fmt.Errorf("create dependency backup dir: %w", err)
	}
	backup := DependencyBackup{
		SchemaVersion: dependencyBackupSchemaVersion,
		CreatedAt:     createdAt,
		ModulePath:    context.Path,
		ModuleDir:     context.Root,
		Kind:          kind,
		Snapshot:      snap,
	}
	bytes, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return DependencyBackupInfo{}, fmt.Errorf("marshal dependency backup: %w", err)
	}
	name, path, err := store.writeFile(dir, createdAt, bytes)
	if err != nil {
		return DependencyBackupInfo{}, fmt.Errorf("write dependency backup: %w", err)
	}
	info := backupInfo(name, path, backup)
	if err := store.prune(dir, context.Path, retentionLimit, info.Path); err != nil {
		return info, fmt.Errorf("prune dependency backups: %w", err)
	}
	return info, nil
}

func (store dependencyBackupStore) prune(dir, modulePath string, retentionLimit int, protectedPath string) error {
	backups, err := loadDependencyBackupInfos(dir, modulePath)
	if err != nil {
		return err
	}

	remaining := retentionLimit - 1
	for _, backup := range backups {
		if backup.Path == protectedPath {
			continue
		}
		if remaining > 0 {
			remaining--
			continue
		}
		if err := store.remove(backup.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (store dependencyBackupStore) writeFile(dir string, createdAt time.Time, bytes []byte) (string, string, error) {
	base := createdAt.Format("2006-01-02_15-04-05")
	for i := 0; i < 1000; i++ {
		name := base + ".json"
		if i > 0 {
			name = fmt.Sprintf("%s-%03d.json", base, i)
		}
		path := filepath.Join(dir, name)
		reservation := path + ".reserve"
		file, err := os.CreateTemp(dir, ".dependency-backup-*")
		if err != nil {
			return "", "", err
		}
		temp := file.Name()
		cleanup := func() {
			_ = os.Remove(temp)
			_ = os.Remove(reservation)
		}
		n, err := store.write(file, bytes)
		if err != nil {
			_ = store.close(file)
			cleanup()
			return "", "", err
		}
		if n < len(bytes) {
			_ = store.close(file)
			cleanup()
			return "", "", io.ErrShortWrite
		}
		if err := store.sync(file); err != nil {
			_ = store.close(file)
			cleanup()
			return "", "", err
		}
		if err := store.close(file); err != nil {
			cleanup()
			return "", "", err
		}
		reserved, err := os.OpenFile(reservation, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				_ = os.Remove(temp)
				continue
			}
			cleanup()
			return "", "", err
		}
		if err := reserved.Close(); err != nil {
			cleanup()
			return "", "", err
		}
		if _, err := os.Stat(path); err == nil {
			cleanup()
			continue
		} else if !os.IsNotExist(err) {
			cleanup()
			return "", "", err
		}
		if err := os.Rename(temp, path); err != nil {
			cleanup()
			return "", "", err
		}
		if err := os.Remove(reservation); err != nil {
			return "", "", err
		}
		return name, path, nil
	}
	return "", "", fmt.Errorf("too many dependency backup name collisions for %s", base)
}

func readDependencyBackupFile(path string) (DependencyBackup, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return DependencyBackup{}, fmt.Errorf("read dependency backup: %w", err)
	}
	var backup DependencyBackup
	if err := json.Unmarshal(bytes, &backup); err != nil {
		return DependencyBackup{}, fmt.Errorf("parse dependency backup: %w", err)
	}
	if backup.SchemaVersion != dependencyBackupSchemaVersion {
		return DependencyBackup{}, fmt.Errorf("unsupported dependency backup schema version %d", backup.SchemaVersion)
	}
	if backup.Snapshot == nil {
		return DependencyBackup{}, fmt.Errorf("dependency backup %q has no snapshot", filepath.Base(path))
	}
	return backup, nil
}

func backupInfo(name, path string, backup DependencyBackup) DependencyBackupInfo {
	updated := 0
	if backup.Snapshot != nil {
		updated = len(backup.Snapshot.Updatable)
	}
	return DependencyBackupInfo{
		Name:       name,
		Path:       path,
		CreatedAt:  backup.CreatedAt,
		ModulePath: backup.ModulePath,
		Kind:       backup.Kind,
		Updated:    updated,
	}
}

var unsafeModulePathChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeModulePath(modulePath string) string {
	safe := unsafeModulePathChars.ReplaceAllString(modulePath, "_")
	safe = strings.Trim(safe, "_")
	if safe == "" || safe == "." || safe == ".." {
		return "module"
	}
	return safe
}
