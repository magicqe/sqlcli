package editor

import (
	"strings"
	"unicode"
)

// tokKind is the syntax class of one character for highlighting.
type tokKind uint8

const (
	tkPlain tokKind = iota
	tkKeyword
	tkType
	tkFunc
	tkString
	tkNumber
	tkComment
	tkBind
)

// lexState is what carries over from one line to the next.
type lexState struct {
	inComment bool // inside /* ... */
	inString  bool // inside a string literal
	closer    rune // for q'X...X' strings: the closing delimiter; 0 for '...'
}

var sqlKeywords = setOf(`
	ACCESS ADD ALL ALTER AND ANY AS ASC AUDIT BEGIN BETWEEN BODY BULK BY CASE CAST CHECK CLOSE
	CLUSTER COLLECT COLUMN COMMENT COMMIT COMPRESS CONNECT CONSTANT CONSTRAINT CONTINUE CREATE CROSS
	CURRENT CURSOR DECLARE DEFAULT DELETE DESC DISTINCT DROP EACH ELSE ELSIF END ERRORS ESCAPE
	EXCEPTION EXCEPTIONS EXCLUSIVE EXECUTE EXISTS EXIT EXPLAIN EXTERNAL FETCH FILE FIRST FOR FORALL
	FOREIGN FROM FULL FUNCTION GOTO GRANT GROUP HAVING IDENTIFIED IF IMMEDIATE IN INDEX INNER INSERT
	INSTEAD INTERSECT INTO IS JOIN KEY LAST LATERAL LEFT LEVEL LIKE LIMIT LOCK LOOP MATCHED MERGE
	MINUS MODE NATURAL NEXT NOCOPY NOT NOWAIT NULL NULLS OF OFFSET ON ONLY OPEN OPTION OR ORDER OTHERS
	OUT OUTER OVER PACKAGE PARTITION PIPE PIPELINED PIVOT PLAN PRAGMA PRIMARY PRIOR PROCEDURE PUBLIC
	RAISE RANGE READ RECORD REFERENCES RENAME REPLACE RESOURCE RETURN RETURNING REVERSE REVOKE RIGHT
	ROLLBACK ROW ROWS ROWTYPE SAVEPOINT SCHEMA SELECT SEQUENCE SESSION SET SHARE SIBLINGS SIZE SOME
	START SYNONYM SYSDATE SYSTIMESTAMP TABLE THEN TO TRIGGER TRUNCATE TYPE UNION UNIQUE UNPIVOT UPDATE
	USER USING VALUES VIEW WHEN WHENEVER WHERE WHILE WITH WORK WRITE TRUE FALSE`)

var sqlTypes = setOf(`
	BFILE BINARY_DOUBLE BINARY_FLOAT BINARY_INTEGER BLOB BOOLEAN CHAR CLOB DATE DECIMAL DOUBLE FLOAT
	INT INTEGER INTERVAL LONG NCHAR NCLOB NUMBER NUMERIC NVARCHAR2 PLS_INTEGER RAW REAL ROWID
	SIMPLE_INTEGER SMALLINT TIMESTAMP UROWID VARCHAR VARCHAR2 XMLTYPE`)

var sqlFuncs = setOf(`
	ABS ADD_MONTHS AVG CEIL CHR COALESCE CONCAT COUNT CUME_DIST DECODE DENSE_RANK EXTRACT FLOOR
	GREATEST INITCAP INSTR LAG LAST_DAY LEAD LEAST LENGTH LISTAGG LOWER LPAD LTRIM MAX MEDIAN MIN MOD
	MONTHS_BETWEEN NEXT_DAY NULLIF NVL NVL2 POWER RANK RATIO_TO_REPORT REGEXP_COUNT REGEXP_INSTR
	REGEXP_LIKE REGEXP_REPLACE REGEXP_SUBSTR ROUND ROW_NUMBER RPAD RTRIM SIGN SQRT STDDEV SUBSTR SUM
	SYS_CONTEXT SYS_GUID TO_CHAR TO_CLOB TO_DATE TO_NUMBER TO_TIMESTAMP TRANSLATE TRIM TRUNC UPPER
	VARIANCE`)

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func isIdentStart(r rune) bool { return unicode.IsLetter(r) || r == '_' }
func isIdentPart(r rune) bool  { return isWord(r) }

// qCloser returns the closing delimiter of an Oracle q'X...X' string.
func qCloser(open rune) rune {
	switch open {
	case '[':
		return ']'
	case '(':
		return ')'
	case '{':
		return '}'
	case '<':
		return '>'
	}
	return open
}

// lexLine classifies each character of line, starting in state st, and
// returns the state at the end of the line.
func lexLine(line []rune, st lexState) ([]tokKind, lexState) {
	kinds := make([]tokKind, len(line))
	mark := func(from, to int, k tokKind) {
		for i := from; i < to && i < len(kinds); i++ {
			kinds[i] = k
		}
	}
	i := 0
	for i < len(line) {
		switch {
		case st.inComment:
			start := i
			for i < len(line) && !(line[i] == '*' && i+1 < len(line) && line[i+1] == '/') {
				i++
			}
			if i < len(line) {
				i += 2
				st.inComment = false
			}
			mark(start, i, tkComment)

		case st.inString:
			start := i
			for i < len(line) {
				if st.closer == 0 && line[i] == '\'' {
					if i+1 < len(line) && line[i+1] == '\'' { // '' escape
						i += 2
						continue
					}
					i++
					st.inString = false
					break
				}
				if st.closer != 0 && line[i] == st.closer && i+1 < len(line) && line[i+1] == '\'' {
					i += 2
					st.inString, st.closer = false, 0
					break
				}
				i++
			}
			mark(start, i, tkString)

		default:
			c := line[i]
			next := rune(0)
			if i+1 < len(line) {
				next = line[i+1]
			}
			switch {
			case c == '-' && next == '-':
				mark(i, len(line), tkComment)
				i = len(line)
			case c == '/' && next == '*':
				mark(i, i+2, tkComment)
				i += 2
				st.inComment = true
			case (c == 'q' || c == 'Q') && next == '\'' && i+2 < len(line) && (i == 0 || !isIdentPart(line[i-1])):
				mark(i, i+3, tkString)
				st.inString, st.closer = true, qCloser(line[i+2])
				i += 3
			case (c == 'n' || c == 'N') && next == '\'' && (i == 0 || !isIdentPart(line[i-1])):
				mark(i, i+2, tkString)
				st.inString = true
				i += 2
			case c == '\'':
				mark(i, i+1, tkString)
				st.inString = true
				i++
			case c == '"': // quoted identifier: plain
				j := i + 1
				for j < len(line) && line[j] != '"' {
					j++
				}
				i = min(j+1, len(line))
			case c == ':' && i+1 < len(line) && (isIdentStart(next) || unicode.IsDigit(next)):
				j := i + 1
				for j < len(line) && isIdentPart(line[j]) {
					j++
				}
				mark(i, j, tkBind)
				i = j
			case unicode.IsDigit(c) || (c == '.' && unicode.IsDigit(next)):
				if i > 0 && isIdentPart(line[i-1]) { // part of a word like t1
					i++
					continue
				}
				j := i
				for j < len(line) && (unicode.IsDigit(line[j]) || line[j] == '.') {
					j++
				}
				if j < len(line) && (line[j] == 'e' || line[j] == 'E') {
					k := j + 1
					if k < len(line) && (line[k] == '+' || line[k] == '-') {
						k++
					}
					if k < len(line) && unicode.IsDigit(line[k]) {
						j = k
						for j < len(line) && unicode.IsDigit(line[j]) {
							j++
						}
					}
				}
				mark(i, j, tkNumber)
				i = j
			case isIdentStart(c):
				j := i
				for j < len(line) && isIdentPart(line[j]) {
					j++
				}
				// a name after '.' (schema.table, pkg.proc) is never a keyword
				if i == 0 || line[i-1] != '.' {
					w := strings.ToUpper(string(line[i:j]))
					switch {
					case sqlKeywords[w]:
						mark(i, j, tkKeyword)
					case sqlTypes[w]:
						mark(i, j, tkType)
					case sqlFuncs[w] && j < len(line) && line[j] == '(':
						mark(i, j, tkFunc)
					}
				}
				i = j
			default:
				i++
			}
		}
	}
	return kinds, st
}

// scanState returns the lexer state at the end of line, like lexLine but
// without classifying words: only comments and strings change the state.
// It is used for the lines above the visible area.
func scanState(line []rune, st lexState) lexState {
	i := 0
	for i < len(line) {
		switch {
		case st.inComment:
			for i < len(line) && !(line[i] == '*' && i+1 < len(line) && line[i+1] == '/') {
				i++
			}
			if i < len(line) {
				i += 2
				st.inComment = false
			}
		case st.inString:
			for i < len(line) {
				if st.closer == 0 && line[i] == '\'' {
					if i+1 < len(line) && line[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					st.inString = false
					break
				}
				if st.closer != 0 && line[i] == st.closer && i+1 < len(line) && line[i+1] == '\'' {
					i += 2
					st.inString, st.closer = false, 0
					break
				}
				i++
			}
		default:
			c := line[i]
			next := rune(0)
			if i+1 < len(line) {
				next = line[i+1]
			}
			switch {
			case c == '-' && next == '-':
				return st
			case c == '/' && next == '*':
				st.inComment = true
				i += 2
			case (c == 'q' || c == 'Q') && next == '\'' && i+2 < len(line) && (i == 0 || !isIdentPart(line[i-1])):
				st.inString, st.closer = true, qCloser(line[i+2])
				i += 3
			case c == '\'':
				st.inString = true
				i++
			case c == '"':
				j := i + 1
				for j < len(line) && line[j] != '"' {
					j++
				}
				i = j + 1
			default:
				i++
			}
		}
	}
	return st
}
