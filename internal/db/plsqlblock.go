package db

// plsqlBlock follows the block structure of a PL/SQL statement, word by
// word (outside strings and comments), to find where the unit ends: the
// "END ...;" that closes it. Text after that is a new statement even when
// there is no "/" line in between.
//
// Rules:
//   - a package, package body, type body, procedure or function opens with
//     its first IS/AS; an anonymous block or trigger with DECLARE or BEGIN;
//     a compound trigger with COMPOUND TRIGGER
//   - BEGIN and CASE open a block, END closes one (END IF / END LOOP don't)
//   - at the unit's top level, FUNCTION/PROCEDURE ... IS/AS declares a
//     subprogram whose BEGIN opens a block; the unit's own BEGIN (procedure
//     body, package initialisation) belongs to the unit's IS/AS or DECLARE
//     and doesn't open another one
//
// Type specs, external (LANGUAGE C/JAVA) subprograms and queries with inline
// functions (WITH FUNCTION) are not followed: they end at "/" as before.
type plsqlBlock struct {
	words    []string // leading words, to recognise the kind of statement
	kind     string   // "unit", "anon", "trigger", "off", or "" (not known yet)
	depth    int
	opened   bool   // the unit has started (depth went above 0)
	ownBegin bool   // the unit's own BEGIN is still to come (it doesn't open a block)
	compound bool   // compound trigger: every top-level IS opens a timing point
	header   bool   // after FUNCTION/PROCEDURE at top level, before IS/AS or ';'
	parens   int    // parenthesis depth (IS/AS inside parentheses don't count)
	pending  int    // declared subprograms whose BEGIN is still to come
	afterEnd bool   // the previous word was END: the next word decides
	prev     string // the previous token
	done     bool   // the unit is complete; the next ';' (already seen) ends it
}

// feed takes the next word (upper case) or one of "(", ")", ";".
func (b *plsqlBlock) feed(tok string) {
	if b.done || b.kind == "off" {
		return
	}
	defer func() { b.prev = tok }()
	if b.kind == "" {
		b.words = append(b.words, tok)
		b.classify()
		if b.kind == "" || b.kind == "off" {
			return
		}
	} else if len(b.words) < 8 {
		b.words = append(b.words, tok)
	}
	if b.afterEnd {
		b.afterEnd = false
		if tok == "IF" || tok == "LOOP" {
			return // END IF / END LOOP: no block was opened for these
		}
		b.depth--
		if tok == "CASE" {
			b.checkDone()
			return // END CASE closes the CASE
		}
		b.checkDone()
		if b.done && tok == ";" {
			return
		}
	}
	switch tok {
	case "(":
		b.parens++
	case ")":
		b.parens = max(b.parens-1, 0)
	case ";":
		b.header = false // a forward declaration ends without IS/AS
		b.checkDone()
	case "IS", "AS":
		switch {
		case b.parens > 0:
		case b.kind == "unit" && !b.opened:
			b.depth, b.opened, b.ownBegin = 1, true, true
		case b.header:
			b.header = false
			b.pending++
		case b.compound && b.depth == 1:
			b.pending++
		}
	case "DECLARE":
		if b.depth == 0 && !b.opened {
			b.depth, b.opened, b.ownBegin = 1, true, true
		}
	case "BEGIN":
		switch {
		case b.depth == 0:
			b.depth, b.opened, b.ownBegin = 1, true, false
		case b.depth == 1 && b.pending > 0:
			b.pending--
			b.depth++
		case b.depth == 1 && b.ownBegin:
			b.ownBegin = false
		default:
			b.depth++
		}
	case "CASE":
		if b.opened {
			b.depth++
		}
	case "END":
		if b.opened {
			b.afterEnd = true
		}
	case "FUNCTION", "PROCEDURE":
		if b.opened && b.depth == 1 && b.parens == 0 {
			b.header = true
		}
	case "TRIGGER":
		if b.kind == "trigger" && b.prev == "COMPOUND" && !b.opened {
			b.depth, b.opened, b.compound = 1, true, true
		}
	case "LANGUAGE", "EXTERNAL":
		if b.depth == 1 && b.ownBegin && b.pending == 0 && !b.header {
			b.kind = "off" // external subprogram: no BEGIN ... END
		}
	}
}

func (b *plsqlBlock) checkDone() {
	if b.opened && b.depth == 0 {
		b.done = true
	}
}

// classify decides from the leading words what kind of statement this is;
// it leaves kind "" while more words are needed.
func (b *plsqlBlock) classify() {
	w := b.words
	switch w[0] {
	case "DECLARE", "BEGIN":
		b.kind = "anon"
		return
	case "CREATE":
	default:
		b.kind = "off"
		return
	}
	for i := 1; i < len(w); i++ {
		switch w[i] {
		case "OR", "REPLACE", "EDITIONABLE", "NONEDITIONABLE":
			continue
		case "PACKAGE", "PROCEDURE", "FUNCTION":
			b.kind = "unit"
		case "TRIGGER":
			b.kind = "trigger"
		case "TYPE":
			if i+1 == len(w) {
				return // need the next word: BODY or the type's name
			}
			if w[i+1] == "BODY" {
				b.kind = "unit"
			} else {
				b.kind = "off" // type spec: ends at "/"
			}
		default:
			b.kind = "off"
		}
		return
	}
}
