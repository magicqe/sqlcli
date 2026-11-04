// Package complete computes SQL code-completion candidates for the editor.
package complete

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"sqlcli/internal/db"
	"sqlcli/internal/sqlwords"
)

// Kinds of candidates, in display order (most relevant first).
const (
	KindColumn    = "column"
	KindTable     = "table"
	KindView      = "view"
	KindSynonym   = "synonym"
	KindObject    = "object" // sequence, package, procedure, function ...
	KindKeyword   = "keyword"
	KindFunction  = "function"
	KindProcedure = "procedure"
	KindType      = "type"
	KindPublic    = "public synonym"
)

var kindRank = map[string]int{
	KindColumn: 0, KindTable: 1, KindView: 1, KindSynonym: 2, KindObject: 2,
	KindKeyword: 3, KindFunction: 4, KindProcedure: 4, KindType: 5, KindPublic: 6,
}

// Item is one candidate.
type Item struct {
	Text string // as stored (database names are usually upper case)
	Kind string
}

// TableRef names a table (or view/synonym) used in the statement.
type TableRef struct {
	Owner, Name string // upper case; Owner may be ""
}

// Catalog supplies database objects and columns.
type Catalog interface {
	Objects() []Item
	// HasObject reports whether a table/view/synonym of that (upper-case)
	// name is known, so half-typed names don't trigger column lookups.
	HasObject(name string) bool
	// Columns returns the columns of a table and whether they are known
	// (false: not loaded yet; the caller should load them).
	Columns(t TableRef) ([]string, bool)
	// Members returns the functions and procedures of a package and whether
	// they are known (false: not loaded yet).
	Members(t TableRef) ([]Item, bool)
}

// Result is what the editor needs to show and apply a completion.
type Result struct {
	Start  int    // column where the word being completed starts
	Prefix string // the typed part of the word
	Items  []Item
	// Need lists tables whose columns are not loaded yet; once they are,
	// completing again gives more candidates.
	Need []TableRef
	// NeedMembers lists packages whose functions/procedures are not loaded.
	NeedMembers []TableRef
}

const maxItems = 200

func isIdent(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#'
}

// Complete computes candidates for the cursor at (row, col) in lines.
func Complete(lines []string, row, col int, cat Catalog) Result {
	if row < 0 || row >= len(lines) {
		return Result{}
	}
	line := []rune(lines[row])
	col = min(max(col, 0), len(line))
	start := col
	for start > 0 && isIdent(line[start-1]) {
		start--
	}
	res := Result{Start: start, Prefix: string(line[start:col])}

	// qualifier: the identifier before a '.' right before the word, and an
	// owner before that one (owner.package.)
	qualifier, owner := "", ""
	if start > 0 && line[start-1] == '.' {
		q := start - 1
		for q > 0 && isIdent(line[q-1]) {
			q--
		}
		qualifier = strings.ToUpper(string(line[q : start-1]))
		if q > 1 && line[q-1] == '.' {
			o := q - 1
			for o > 0 && isIdent(line[o-1]) {
				o--
			}
			owner = strings.ToUpper(string(line[o : q-1]))
		}
	}

	stmt, _ := db.StatementAtCursor(strings.Join(lines, "\n"), row, col)
	refs := TableRefs(stmt.Text)

	if qualifier != "" {
		t, ok := resolve(qualifier, refs)
		if ok && owner == "" { // an alias or table of the statement: its columns
			cols, known := cat.Columns(t)
			if !known {
				res.Need = append(res.Need, t)
			}
			var items []Item
			for _, c := range cols {
				items = append(items, Item{c, KindColumn})
			}
			res.Items = filter(items, res.Prefix)
			return res
		}
		// a package (functions and procedures) or a table not in FROM yet
		t = TableRef{Owner: owner, Name: qualifier}
		var items []Item
		if owner != "" || cat.HasObject(qualifier) {
			members, known := cat.Members(t)
			if !known {
				res.NeedMembers = append(res.NeedMembers, t)
			}
			items = append(items, members...)
		}
		cols, known := cat.Columns(t)
		if !known {
			res.Need = append(res.Need, t)
		}
		for _, c := range cols {
			items = append(items, Item{c, KindColumn})
		}
		res.Items = filter(items, res.Prefix)
		return res
	}

	var items []Item
	seen := map[string]bool{}
	for _, t := range refs {
		cols, known := cat.Columns(t)
		if !known && (t.Owner != "" || cat.HasObject(t.Name)) {
			res.Need = append(res.Need, t)
		}
		for _, c := range cols {
			if !seen[c] {
				seen[c] = true
				items = append(items, Item{c, KindColumn})
			}
		}
	}
	items = append(items, cat.Objects()...)
	for _, w := range sortedKeywords {
		items = append(items, Item{w, KindKeyword})
	}
	for _, w := range sortedFuncs {
		items = append(items, Item{w, KindFunction})
	}
	for _, w := range sortedTypes {
		items = append(items, Item{w, KindType})
	}
	res.Items = filter(items, res.Prefix)
	return res
}

var (
	sortedKeywords = sqlwords.Sorted(sqlwords.Keywords)
	sortedFuncs    = sqlwords.Sorted(sqlwords.Funcs)
	sortedTypes    = sqlwords.Sorted(sqlwords.Types)
)

// filter keeps items starting with prefix (case-insensitive), ranked by
// kind then name, without duplicates.
func filter(items []Item, prefix string) []Item {
	p := strings.ToUpper(prefix)
	var out []Item
	seen := map[Item]bool{}
	for _, it := range items {
		if strings.HasPrefix(strings.ToUpper(it.Text), p) && !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Kind), rank(out[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if out[i].Kind == KindColumn { // columns keep table order
			return false
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > maxItems {
		out = out[:maxItems]
	}
	return out
}

// rank orders kinds; other object types (sequence, package ...) rank with
// synonyms.
func rank(kind string) int {
	if r, ok := kindRank[kind]; ok {
		return r
	}
	return kindRank[KindObject]
}

// KindFor maps an Oracle object type to a candidate kind.
func KindFor(objectType string) string {
	switch objectType {
	case "TABLE":
		return KindTable
	case "VIEW", "MATERIALIZED VIEW":
		return KindView
	case "SYNONYM":
		return KindSynonym
	case "PUBLIC SYNONYM":
		return KindPublic
	}
	return strings.ToLower(objectType)
}

// resolve maps a qualifier (alias or table name) to a table of the statement.
func resolve(q string, refs map[string]TableRef) (TableRef, bool) {
	t, ok := refs[q]
	return t, ok
}

// --- statement parsing -----------------------------------------------------

var reToken = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/|'(?:[^']|'')*'?|"[^"]*"?|[A-Za-z_][A-Za-z0-9_$#]*|[.,()]|\S`)

// stop words end a table reference list (and can't be aliases)
var stop = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`WHERE JOIN INNER LEFT RIGHT FULL CROSS NATURAL OUTER ON USING GROUP
		ORDER HAVING UNION MINUS INTERSECT CONNECT START SET VALUES PARTITION FETCH OFFSET WITH SELECT
		FOR LATERAL PIVOT UNPIVOT MODEL WINDOW RETURNING LOG SAMPLE AS`) {
		m[w] = true
	}
	return m
}()

// TableRefs finds the tables of a statement (after FROM, JOIN, UPDATE,
// INTO, MERGE INTO) and returns them keyed by alias and by table name.
func TableRefs(stmt string) map[string]TableRef {
	var toks []string
	for _, t := range reToken.FindAllString(stmt, -1) {
		if strings.HasPrefix(t, "--") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "'") {
			continue
		}
		toks = append(toks, t)
	}
	refs := map[string]TableRef{}
	name := func(t string) (string, bool) {
		if strings.HasPrefix(t, `"`) {
			return strings.Trim(t, `"`), true
		}
		if len(t) > 0 && (unicode.IsLetter(rune(t[0])) || t[0] == '_') {
			return strings.ToUpper(t), true
		}
		return "", false
	}
	for i := 0; i < len(toks); i++ {
		kw := strings.ToUpper(toks[i])
		if kw != "FROM" && kw != "JOIN" && kw != "UPDATE" && kw != "INTO" {
			continue
		}
		list := kw == "FROM" // only FROM takes a comma-separated list
		j := i + 1
		for j < len(toks) {
			var ref *TableRef
			if toks[j] == "(" { // subquery or table function: skip to the matching ')'
				for depth := 0; j < len(toks); j++ {
					if toks[j] == "(" {
						depth++
					} else if toks[j] == ")" {
						if depth--; depth == 0 {
							break
						}
					}
				}
				j++
			} else {
				n, ok := name(toks[j])
				if !ok || (stop[n] && !strings.HasPrefix(toks[j], `"`)) {
					break
				}
				ref = &TableRef{Name: n}
				j++
				if j+1 < len(toks) && toks[j] == "." {
					if n2, ok := name(toks[j+1]); ok {
						ref = &TableRef{Owner: n, Name: n2}
						j += 2
					}
				}
				refs[ref.Name] = *ref
			}
			// optional alias: [AS] name
			if j < len(toks) && strings.EqualFold(toks[j], "AS") {
				j++
			}
			if j < len(toks) {
				quoted := strings.HasPrefix(toks[j], `"`)
				if a, ok := name(toks[j]); ok && (quoted || (!stop[a] && !sqlwords.Keywords[a])) {
					if ref != nil {
						refs[a] = *ref
					}
					j++
				}
			}
			if list && j < len(toks) && toks[j] == "," {
				j++
				continue
			}
			break
		}
	}
	return refs
}

// --- applying a candidate ---------------------------------------------------

var reSimpleName = regexp.MustCompile(`^[A-Z][A-Z0-9_$#]*$`)

// Insert returns the text to insert for it, matching the case of what the
// user typed: a lower-case prefix gives lower case, upper gives upper.
// Database names that need quotes (mixed case, spaces) are quoted.
func Insert(it Item, prefix string) string {
	fromDB := it.Kind != KindKeyword && it.Kind != KindFunction && it.Kind != KindType
	if fromDB && !reSimpleName.MatchString(it.Text) {
		return `"` + it.Text + `"`
	}
	hasLetter, lower, upper := false, true, true
	for _, r := range prefix {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsUpper(r) {
				lower = false
			} else {
				upper = false
			}
		}
	}
	switch {
	case hasLetter && lower:
		return strings.ToLower(it.Text)
	case hasLetter && upper:
		return strings.ToUpper(it.Text)
	}
	return it.Text
}
