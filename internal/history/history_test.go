package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{"select 1 from dual", "update emp set sal = 1", "select ename from emp"} {
		if err := s.Add(Entry{Connection: "dev", SQL: q, ExecutedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.Recent(10, "")
	if len(all) != 3 || all[0].SQL != "select ename from emp" {
		t.Fatalf("recent: %+v", all)
	}
	f, _ := s.Recent(10, "SELECT")
	if len(f) != 2 {
		t.Fatalf("filter: %d", len(f))
	}
	_ = s.Delete(all[0].ID)
	all, _ = s.Recent(10, "")
	if len(all) != 2 {
		t.Fatalf("after delete: %d", len(all))
	}
}
