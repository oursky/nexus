package tokenstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	Load() (token string, found bool, err error)
	Save(token string) error
}

type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (f *FileStore) Load() (string, bool, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(data)), true, nil
}

func (f *FileStore) Save(token string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
		return err
	}
	return os.WriteFile(f.path, []byte(token), 0600)
}
