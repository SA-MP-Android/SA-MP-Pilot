package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/gpci"
)

const maxDataFileBytes int64 = 32 << 20

type Data struct {
	Servers  []domain.Server       `json:"servers"`
	Commands []domain.QuickCommand `json:"commands"`
	GPCI     string                `json:"gpci"`
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
		if _, err = s.EnsureGPCI(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if _, err = s.EnsureGPCI(); err != nil {
		return nil, err
	}
	return s, nil
}

// GPCI returns the persisted per-installation client identifier.
func (s *Store) GPCI() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.GPCI
}

// EnsureGPCI creates and persists a client identifier when loading a legacy
// data file that does not have one yet.
func (s *Store) EnsureGPCI() (string, error) {
	s.mu.RLock()
	value := strings.TrimSpace(s.data.GPCI)
	s.mu.RUnlock()
	if value != "" {
		return value, nil
	}

	var generated string
	err := s.Update(func(d *Data) error {
		if value = strings.TrimSpace(d.GPCI); value != "" {
			generated = value
			return nil
		}
		var err error
		generated, err = gpci.Generate()
		if err != nil {
			return err
		}
		d.GPCI = generated
		return nil
	})
	return generated, err
}

// RefreshGPCI replaces the persisted client identifier. Existing connections
// keep the value that was sent during their handshake; subsequent connections
// use the new value.
func (s *Store) RefreshGPCI() (string, error) {
	generated, err := gpci.Generate()
	if err != nil {
		return "", err
	}
	if err = s.Update(func(d *Data) error {
		d.GPCI = generated
		return nil
	}); err != nil {
		return "", err
	}
	return generated, nil
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
