// Package state coordinates durable installed-version state mutations.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/SmileOniks/govm/internal/version"
)

const (
	// SchemaVersion is the current durable transaction marker schema.
	SchemaVersion = 1

	OperationInstall  Operation = "install"
	OperationActivate Operation = "activate"
	OperationDelete   Operation = "delete"
)

// Operation identifies the mutation that owns a marker.
type Operation string

// Marker is the durable transaction envelope. Artifacts are names relative
// to the govm root and are deliberately restricted to safe basenames.
type Marker struct {
	SchemaVersion int               `json:"schema_version"`
	Operation     Operation         `json:"operation"`
	Phase         string            `json:"phase"`
	Version       string            `json:"version"`
	Artifacts     map[string]string `json:"artifacts,omitempty"`
}

// MarkerError reports malformed, unsupported, or unsafe marker data.
type MarkerError struct {
	Reason string
	Err    error
}

func (e *MarkerError) Error() string {
	if e.Err == nil {
		return "invalid transaction marker: " + e.Reason
	}
	return fmt.Sprintf("invalid transaction marker: %s: %v", e.Reason, e.Err)
}

func (e *MarkerError) Unwrap() error { return e.Err }

var markerToken = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
var artifactName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var artifactRoles = map[string]struct{}{
	"backup":     {},
	"download":   {},
	"live":       {},
	"quarantine": {},
	"source":     {},
	"staging":    {},
	"target":     {},
}

// Validate checks the marker envelope and every artifact basename.
func (m Marker) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return &MarkerError{Reason: fmt.Sprintf("unsupported schema version %d", m.SchemaVersion)}
	}
	if !markerToken.MatchString(string(m.Operation)) {
		return &MarkerError{Reason: "operation is empty or malformed"}
	}
	if !markerToken.MatchString(m.Phase) {
		return &MarkerError{Reason: "phase is empty or malformed"}
	}
	if err := version.Validate(m.Version); err != nil {
		return &MarkerError{Reason: "version is not canonical", Err: err}
	}
	for role, artifact := range m.Artifacts {
		if _, ok := artifactRoles[role]; !ok {
			return &MarkerError{Reason: fmt.Sprintf("unknown artifact role %q", role)}
		}
		if !isSafeArtifactName(artifact) {
			return &MarkerError{Reason: fmt.Sprintf("unsafe %s artifact %q", role, artifact)}
		}
	}
	return nil
}

func isSafeArtifactName(name string) bool {
	if name == "." || name == ".." || filepath.Base(name) != name ||
		filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) ||
		strings.ContainsRune(name, '\x00') {
		return false
	}
	return artifactName.MatchString(name)
}

type markerFileSystem interface {
	createTemp(dir, pattern string) (*os.File, error)
	readFile(name string) ([]byte, error)
	remove(name string) error
	rename(oldPath, newPath string) error
	stat(name string) error
}

type osMarkerFileSystem struct{}

func (osMarkerFileSystem) createTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}
func (osMarkerFileSystem) readFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (osMarkerFileSystem) remove(name string) error             { return os.Remove(name) }
func (osMarkerFileSystem) rename(oldPath, newPath string) error {
	return replaceMarkerFile(oldPath, newPath)
}
func (osMarkerFileSystem) stat(name string) error {
	_, err := os.Stat(name)
	return err
}

// MarkerStore persists one marker at markerPath. Construct it with
// NewMarkerStore; the zero value is not configured.
type MarkerStore struct {
	markerPath string
	root       string
	fs         markerFileSystem
	syncDir    func(string) error
}

// NewMarkerStore returns a durable marker store rooted at root.
func NewMarkerStore(root string) *MarkerStore {
	return newMarkerStore(root, filepath.Join(root, "transaction.json"))
}

func newMarkerStore(root, markerPath string) *MarkerStore {
	return &MarkerStore{
		markerPath: markerPath,
		root:       root,
		fs:         osMarkerFileSystem{},
		syncDir:    syncDirectory,
	}
}

// Path returns the marker's path.
func (s *MarkerStore) Path() string { return s.markerPath }

// Read loads and validates the marker. A missing marker is not an error.
func (s *MarkerStore) Read() (Marker, bool, error) {
	data, err := s.fs.readFile(s.markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return Marker{}, false, nil
	}
	if err != nil {
		return Marker{}, false, fmt.Errorf("read transaction marker: %w", err)
	}

	var marker Marker
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return Marker{}, false, &MarkerError{Reason: "invalid JSON", Err: err}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Marker{}, false, &MarkerError{Reason: "trailing JSON data"}
		}
		return Marker{}, false, &MarkerError{Reason: "trailing JSON data", Err: err}
	}
	if err := marker.Validate(); err != nil {
		return Marker{}, false, err
	}
	return marker, true, nil
}

// Write validates and durably replaces the marker.
func (s *MarkerStore) Write(marker Marker) error {
	if err := marker.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode transaction marker: %w", err)
	}
	temp, err := s.fs.createTemp(s.root, ".transaction-*.tmp")
	if err != nil {
		return fmt.Errorf("create transaction marker temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("restrict transaction marker temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write transaction marker: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync transaction marker: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close transaction marker: %w", err)
	}
	if err := s.fs.rename(tempName, s.markerPath); err != nil {
		return fmt.Errorf("replace transaction marker: %w", err)
	}
	if err := s.syncDir(s.root); err != nil {
		return fmt.Errorf("sync transaction marker directory: %w", err)
	}
	return nil
}

// Delete durably removes the marker. It succeeds when the marker is absent.
func (s *MarkerStore) Delete() error {
	if err := s.fs.remove(s.markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove transaction marker: %w", err)
	}
	if err := s.syncDir(s.root); err != nil {
		return fmt.Errorf("sync transaction marker directory: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
