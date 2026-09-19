package config

// Store is the seam through which settings changes are persisted.
// Production binds FileStore (atomic file write); tests bind an
// in-memory store. Two adapters make the seam real.
type Store interface {
	Save(Settings) error
}

// FileStore persists settings to the file at Path.
type FileStore struct {
	Path string
}

// Save writes the settings atomically to the file at Path.
func (f FileStore) Save(settings Settings) error {
	return Save(f.Path, settings)
}
