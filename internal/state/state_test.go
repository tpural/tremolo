package state

import (
	"errors"
	"testing"
)

type data struct {
	Names map[string]int `json:"names"`
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open[data](dir, "x.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(d *data) error {
		d.Names = map[string]int{"a": 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A failed update changes nothing.
	if err := s.Update(func(d *data) error {
		d.Names["a"] = 2
		return errors.New("no")
	}); err == nil {
		t.Fatal("want error")
	}
	again, err := Open[data](dir, "x.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []*Store[data]{s, again} {
		st.Read(func(d *data) {
			if d.Names["a"] != 1 {
				t.Errorf("names = %v", d.Names)
			}
		})
	}
}

func TestMemory(t *testing.T) {
	s, err := Open[data]("", "x.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(d *data) error { d.Names = map[string]int{"a": 1}; return nil }); err != nil {
		t.Fatal(err)
	}
	s.Read(func(d *data) {
		if d.Names["a"] != 1 {
			t.Errorf("names = %v", d.Names)
		}
	})
}
