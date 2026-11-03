package ui

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeText(m *Model, s string) {
	for _, r := range s {
		press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func answer(m *Model, s string) {
	press(m, key('y', tea.ModCtrl)) // clear the pre-filled input
	typeText(m, s)
	press(m, key(tea.KeyEnter))
}

func TestUIExport(t *testing.T) {
	m := newTestModel(t, false)
	dir := t.TempDir()
	run(m, `select level id, decode(mod(level, 10), 0, null, 'name '||level) name,
	               level / 4 amount, 'say "hi", ok' quote
	          from dual connect by level <= 2500`)
	if len(m.res.res.Rows) != 1000 || !m.res.res.More {
		t.Fatalf("expected a partial first batch, got %d", len(m.res.res.Rows))
	}
	press(m, key(tea.KeyF3))

	// JSON: all 2500 rows, although only 1000 were fetched
	jsonPath := filepath.Join(dir, "out.json")
	press(m, key('e'))
	if m.mode != modePrompt || string(m.prompt.input) != "result.csv" {
		t.Fatalf("prompt: mode=%v input=%q", m.mode, string(m.prompt.input))
	}
	answer(m, jsonPath)
	if !strings.HasPrefix(m.status, "Exported 2500 row(s) to ") {
		t.Fatalf("status: %q", m.status)
	}
	b, _ := os.ReadFile(jsonPath)
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 2500 || got[0]["ID"] != 1.0 || got[0]["AMOUNT"] != 0.25 || got[9]["NAME"] != nil || got[0]["QUOTE"] != `say "hi", ok` {
		t.Fatalf("JSON rows: n=%d first=%v tenth=%v", len(got), got[0], got[9])
	}

	// CSV, then export again over it (overwrite confirmation)
	csvPath := filepath.Join(dir, "out.csv")
	for i, confirm := range []string{"", "y"} {
		press(m, key('e'))
		if i == 1 && string(m.prompt.input) != csvPath {
			t.Fatalf("prompt should offer the last file, got %q", string(m.prompt.input))
		}
		answer(m, csvPath)
		if confirm != "" {
			if !strings.Contains(m.prompt.label, "Overwrite?") {
				t.Fatalf("no overwrite question: %q", m.prompt.label)
			}
			answer(m, confirm)
		}
	}
	f, _ := os.Open(csvPath)
	recs, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || len(recs) != 2501 || recs[0][1] != "NAME" || recs[10][1] != "" || recs[1][3] != `say "hi", ok` {
		t.Fatalf("CSV: err=%v n=%d", err, len(recs))
	}

	// declining the overwrite leaves the file alone
	before, _ := os.ReadFile(csvPath)
	press(m, key('e'))
	answer(m, csvPath)
	answer(m, "n")
	after, _ := os.ReadFile(csvPath)
	if string(before) != string(after) || m.status != "Export cancelled" {
		t.Fatalf("declined overwrite changed the file / status %q", m.status)
	}

	// bad extension
	press(m, key('e'))
	answer(m, filepath.Join(dir, "out.xlsx"))
	if !m.statusErr || !strings.Contains(m.status, "use .csv, .tsv or .json") {
		t.Fatalf("status: %q", m.status)
	}
}
