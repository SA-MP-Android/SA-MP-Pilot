package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
)

const maxDataFileBytes int64 = 32 << 20

type Data struct {
	Servers  []domain.Server       `json:"servers"`
	Commands []domain.QuickCommand `json:"commands"`
}
type Store struct {
	mu   sync.RWMutex
	path string
	data Data
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: Data{Servers: []domain.Server{}, Commands: []domain.QuickCommand{}}}
	info, err := os.Stat(path)
	if err == nil && info.Size() > maxDataFileBytes {
		return nil, errors.New("store: data file is too large")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) Data() Data { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.data) }
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.data)
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.data = next
	return nil
}
func (s *Store) save(d Data) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
func clone(d Data) Data {
	b, _ := json.Marshal(d)
	var out Data
	_ = json.Unmarshal(b, &out)
	return out
}
