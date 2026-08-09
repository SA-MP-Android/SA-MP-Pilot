package store

import (
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsAtomically(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "data.json")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(d *Data) error {
		d.Servers = append(d.Servers, domain.Server{ID: "one", Host: "localhost", Port: 7777})
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	again, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	got := again.Data()
	if len(got.Servers) != 1 || got.Servers[0].ID != "one" {
		t.Fatalf("unexpected: %+v", got)
	}
}
func TestStoreRollback(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "data.json"))
	want := assertErr{}
	if e := s.Update(func(*Data) error { return want }); e != want {
		t.Fatalf("got %v", e)
	}
	if len(s.Data().Servers) != 0 {
		t.Fatal("mutated after failed update")
	}
}

func TestOpenRejectsOversizedDataFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(maxDataFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(path); err == nil {
		t.Fatal("oversized data file was accepted")
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "expected" }
