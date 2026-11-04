package history

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	tabs := []SessionTab{
		{Connection: "ora", SQL: "select 1 from dual;\nselect 'ž' from dual", Row: 1, Col: 7},
		{Connection: "", SQL: ""},
		{Connection: "rep", SQL: "begin null; end;\n/", Row: 0, Col: 3},
		{Connection: "ora", SQL: "x"},
	}
	if err := s.SaveSession("work", 2, tabs); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession("other", 0, tabs[:1]); err != nil {
		t.Fatal(err)
	}

	list, err := s.Sessions()
	if err != nil || len(list) != 2 || list[0].Name != "other" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list[1].Tabs != 4 || list[1].Connections != "ora, rep" {
		t.Fatalf("work info: %+v", list[1])
	}

	got, err := s.LoadSession("work")
	if err != nil || got.Active != 2 || !reflect.DeepEqual(got.Tabs, tabs) {
		t.Fatalf("load: %+v %v", got, err)
	}

	// saving under an existing name replaces it
	if err := s.SaveSession("work", 0, tabs[2:3]); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LoadSession("work")
	if len(got.Tabs) != 1 || got.Tabs[0].Connection != "rep" || got.Active != 0 {
		t.Fatalf("overwrite: %+v", got)
	}
	if ok, _ := s.SessionExists("work"); !ok {
		t.Fatal("exists")
	}

	if err := s.DeleteSession("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSession("work"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("deleted session still loads: %v", err)
	}
	s.Close()

	// sessions survive reopening (and the history schema stays compatible)
	s, _ = Open(p)
	defer s.Close()
	if list, _ := s.Sessions(); len(list) != 1 || list[0].Name != "other" {
		t.Fatalf("after reopen: %+v", list)
	}
}
