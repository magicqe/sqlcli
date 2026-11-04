// Package db wraps an Oracle connection made through godror.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/godror/godror"
	"github.com/godror/godror/dsn"

	"sqlcli/internal/config"
)

type Result struct {
	Columns  []string
	Types    []string // Oracle type of each column (NUMBER, VARCHAR2, DATE, ...)
	Rows     [][]string
	More     bool  // the cursor is still open: Conn.FetchMore returns the next rows
	Affected int64 // for DML
	IsQuery  bool
	Message  string
	Output   []string // DBMS_OUTPUT lines produced by the statement
	Errors   []string // compilation errors of a created PL/SQL object ("LINE/COL text")
	SQL      string   // the query that produced the rows (for editing them)
	Duration time.Duration
}

type Conn struct {
	Name     string
	ReadOnly bool
	db       *sql.DB
	sc       *sql.Conn // the one Oracle session all statements run in
	tx       *sql.Tx   // open transaction when AutoCommit is off
	auto     bool
	output   bool      // DBMS_OUTPUT is enabled and read after each statement
	lastObj  [3]string // kind, owner, name of the last created PL/SQL object (SHOW ERRORS)
	closed   bool

	mu  sync.Mutex // serializes all use of the session
	cur *cursor    // open query cursor with rows not fetched yet
}

// cursor is an open query whose remaining rows can be fetched later.
type cursor struct {
	rows   *sql.Rows
	cancel context.CancelFunc
	vals   []any
	ptrs   []any
	peeked []string // one row read ahead to learn whether more rows exist
}

func (k *cursor) close() {
	k.rows.Close()
	k.cancel()
}

// fetch returns up to n rows (n < 0: all). done reports that the cursor is
// exhausted.
func (k *cursor) fetch(n int) (out [][]string, done bool, err error) {
	if k.peeked != nil {
		out = append(out, k.peeked)
		k.peeked = nil
	}
	next := func() ([]string, bool, error) {
		if !k.rows.Next() {
			return nil, false, k.rows.Err()
		}
		if err := k.rows.Scan(k.ptrs...); err != nil {
			return nil, false, err
		}
		row := make([]string, len(k.vals))
		for i, v := range k.vals {
			row[i] = FormatValue(v)
		}
		return row, true, nil
	}
	for n < 0 || len(out) < n {
		row, ok, err := next()
		if err != nil {
			return out, true, err
		}
		if !ok {
			return out, true, nil
		}
		out = append(out, row)
	}
	row, ok, err := next() // read ahead one row
	if err != nil || !ok {
		return out, true, err
	}
	k.peeked = row
	return out, false, nil
}

// Options control how a connection behaves.
type Options struct {
	LibDir     string // Oracle Instant Client directory
	AutoCommit bool
	DBMSOutput bool // enable DBMS_OUTPUT and collect it after each statement
}

func Open(ctx context.Context, c config.Connection, o Options) (*Conn, error) {
	var p godror.ConnectionParams
	p.Username = c.User
	p.Password = godror.NewPassword(c.ResolvedPassword())
	p.ConnectString = c.ConnectString
	if c.SysDBA {
		p.AdminRole = dsn.SysDBA
	}
	p.LibDir = o.LibDir
	p.StandaloneConnection = sql.NullBool{Bool: true, Valid: true}
	p.Timezone = time.Local

	db := sql.OpenDB(godror.NewConnector(p))
	db.SetMaxOpenConns(1)
	// Pin one session: transactions and DBMS_OUTPUT are per session.
	sc, err := db.Conn(ctx)
	if err == nil {
		err = sc.PingContext(ctx)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	conn := &Conn{Name: c.Name, ReadOnly: c.ReadOnly, db: db, sc: sc, auto: o.AutoCommit}
	if o.DBMSOutput {
		// Without EXECUTE on DBMS_OUTPUT the connection still works, just
		// without collecting output.
		// No deadline here: godror turns a context deadline on this PL/SQL
		// call into an Oracle call timeout that stays on the session, which
		// would cancel every later statement after that many seconds.
		conn.output = godror.EnableDbmsOutput(context.Background(), sc) == nil
	}
	return conn, nil
}

// readOutput returns and clears the session's DBMS_OUTPUT buffer.
// Callers hold c.mu.
func (c *Conn) readOutput() []string {
	if !c.output {
		return nil
	}
	// No deadline: see Open (a deadline would stay on the session as an
	// Oracle call timeout).
	ctx := context.Background()
	var p interface {
		PrepareContext(context.Context, string) (*sql.Stmt, error)
	} = c.sc
	if c.tx != nil {
		p = c.tx
	}
	var buf strings.Builder
	if err := godror.ReadDbmsOutput(ctx, &buf, p); err != nil {
		return []string{"(could not read DBMS_OUTPUT: " + CleanError(err) + ")"}
	}
	s := strings.TrimSuffix(buf.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Close rolls back any open transaction and ends the session. It is safe
// to call more than once.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.closeCursor()
	if c.tx != nil {
		_ = c.tx.Rollback()
	}
	_ = c.sc.Close()
	return c.db.Close()
}

func (c *Conn) InTransaction() bool { return c.tx != nil }

// closeCursor discards an open query cursor. Callers hold c.mu.
func (c *Conn) closeCursor() {
	if c.cur != nil {
		c.cur.close()
		c.cur = nil
	}
}

// HasMore reports whether the last query still has rows to fetch.
func (c *Conn) HasMore() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur != nil
}

// CloseCursor discards the rows of the last query that were not fetched.
func (c *Conn) CloseCursor() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCursor()
}

func (c *Conn) Commit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commit()
}

func (c *Conn) Rollback() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rollback()
}

func (c *Conn) commit() error {
	c.closeCursor()
	if c.tx == nil {
		return nil
	}
	err := c.tx.Commit()
	c.tx = nil
	return err
}

func (c *Conn) rollback() error {
	c.closeCursor()
	if c.tx == nil {
		return nil
	}
	err := c.tx.Rollback()
	c.tx = nil
	return err
}

// FetchMore returns up to n further rows of the last query (n < 0: all).
// done reports that no rows are left; the cursor is then closed. Cancelling
// ctx stops the fetch and closes the cursor, returning the rows read so far.
func (c *Conn) FetchMore(ctx context.Context, n int) (rows [][]string, done bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil {
		return nil, true, nil
	}
	stop := context.AfterFunc(ctx, c.cur.cancel)
	rows, done, err = c.cur.fetch(n)
	stop()
	if ctx.Err() != nil {
		err, done = ctx.Err(), true
	}
	if done {
		c.closeCursor()
	}
	return rows, done, err
}

type execer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

var (
	reLeadingComments = regexp.MustCompile(`^(\s*(--[^\n]*\n|/\*(?s:.*?)\*/))*\s*`)
	reFirstWord       = regexp.MustCompile(`^[A-Za-z]+`)
)

// FirstKeyword returns the uppercased first keyword of a statement.
func FirstKeyword(stmt string) string {
	s := reLeadingComments.ReplaceAllString(stmt, "")
	return strings.ToUpper(reFirstWord.FindString(s))
}

// Normalize trims the statement and strips a trailing ';' or '/' except
// for PL/SQL blocks, which need their final ';'.
func Normalize(stmt string) string {
	s := strings.TrimSpace(stmt)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSpace(s)
	switch FirstKeyword(s) {
	case "BEGIN", "DECLARE":
		return s
	case "CREATE":
		up := strings.ToUpper(s)
		if regexp.MustCompile(`^CREATE\s+(OR\s+REPLACE\s+)?(EDITIONABLE\s+|NONEDITIONABLE\s+)?(PROCEDURE|FUNCTION|PACKAGE|TRIGGER|TYPE)\b`).MatchString(up) {
			return s
		}
	}
	return strings.TrimSpace(strings.TrimSuffix(s, ";"))
}

// Execute runs one statement. A query returns its first maxRows rows
// (maxRows < 0: all); if more exist, Result.More is set and FetchMore
// returns them. Running any statement discards the previous query's cursor.
func (c *Conn) Execute(ctx context.Context, stmt string, maxRows int) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCursor()
	stmt = Normalize(stmt)
	if stmt == "" {
		return nil, fmt.Errorf("empty statement")
	}
	kw := FirstKeyword(stmt)
	start := time.Now()
	if kw == "EXEC" || kw == "EXECUTE" { // SQL*Plus EXEC proc(...): run as a PL/SQL block
		stmt, kw = execAsBlock(stmt), "BEGIN"
	}

	switch kw {
	case "COMMIT":
		err := c.commit()
		return &Result{Message: "Commit complete.", Duration: time.Since(start)}, err
	case "ROLLBACK":
		err := c.rollback()
		return &Result{Message: "Rollback complete.", Duration: time.Since(start)}, err
	}

	isQuery := kw == "SELECT" || kw == "WITH"
	if c.ReadOnly && !isQuery && kw != "EXPLAIN" && kw != "DESC" && kw != "DESCRIBE" && kw != "SHOW" && kw != "SHO" &&
		!(kw == "SET" && !reSQLSet.MatchString(stmt)) {
		return nil, fmt.Errorf("connection %q is read-only; %s refused", c.Name, kw)
	}

	if kw == "SHOW" || kw == "SHO" || (kw == "SET" && !reSQLSet.MatchString(stmt)) {
		var q execer = c.sc
		if c.tx != nil {
			q = c.tx
		}
		var res *Result
		var err error
		if kw == "SET" {
			res, err = c.sqlplusSet(stmt)
		} else {
			res, err = c.show(ctx, q, stmt)
		}
		if res != nil {
			res.Duration = time.Since(start)
		}
		return res, err
	}
	if kw == "DESC" || kw == "DESCRIBE" { // SQL*Plus DESCRIBE, from the data dictionary
		var q execer = c.sc
		if c.tx != nil {
			q = c.tx
		}
		res, err := c.describe(ctx, q, stmt)
		if err != nil {
			return nil, err
		}
		res.Duration = time.Since(start)
		return res, nil
	}

	var ex execer = c.sc
	if !isQuery && !c.auto && (isTransactional(kw) || isPLSQL(kw)) {
		if c.tx == nil {
			tx, err := c.sc.BeginTx(context.Background(), nil)
			if err != nil {
				return nil, err
			}
			c.tx = tx
		}
	}
	if c.tx != nil {
		ex = c.tx
	}

	if isQuery {
		res, err := c.query(ctx, ex, stmt, maxRows)
		if err != nil {
			return c.failed(err)
		}
		res.Duration = time.Since(start)
		if !res.More { // while the cursor is open the session is busy
			res.Output = c.readOutput()
		}
		return res, nil
	}

	r, err := ex.ExecContext(ctx, stmt)
	if err != nil {
		return c.failed(err)
	}
	n, _ := r.RowsAffected()
	res := &Result{Affected: n, Duration: time.Since(start)}
	switch {
	case isTransactional(kw):
		res.Message = fmt.Sprintf("%d row(s) %s.", n, pastTense(kw))
	case isPLSQL(kw):
		res.Message = "PL/SQL procedure successfully completed."
	default:
		res.Message = kw + " executed."
	}
	if kind, owner, name, ok := compiledObject(stmt); ok {
		c.lastObj = [3]string{kind, owner, name}
		res.Message = fmt.Sprintf("%s %s created.", kind, name)
		if errs := c.compileErrors(ex, kind, owner, name); len(errs) > 0 {
			res.Message = fmt.Sprintf("%s %s created with compilation errors.", kind, name)
			res.Errors = errs
		}
	}
	if c.tx != nil && commitsImplicitly(kw) {
		// Oracle committed the open transaction before running the DDL;
		// close our handle so the TX state matches the server.
		_ = c.tx.Commit()
		c.tx = nil
		res.Message += " (open transaction was committed by DDL)"
	}
	res.Output = c.readOutput()
	return res, nil
}

// failed returns err together with any DBMS_OUTPUT the failed statement
// produced (often printed just before an exception).
func (c *Conn) failed(err error) (*Result, error) {
	if out := c.readOutput(); out != nil {
		return &Result{Output: out}, err
	}
	return nil, err
}

func isPLSQL(kw string) bool {
	switch kw {
	case "BEGIN", "DECLARE", "CALL":
		return true
	}
	return false
}

// commitsImplicitly reports DDL statements that make Oracle commit.
func commitsImplicitly(kw string) bool {
	switch kw {
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "GRANT", "REVOKE",
		"COMMENT", "ANALYZE", "AUDIT", "NOAUDIT", "PURGE", "FLASHBACK":
		return true
	}
	return false
}

func isTransactional(kw string) bool {
	switch kw {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return true
	}
	return false
}

func pastTense(kw string) string {
	switch kw {
	case "INSERT":
		return "inserted"
	case "UPDATE":
		return "updated"
	case "DELETE":
		return "deleted"
	case "MERGE":
		return "merged"
	}
	return "affected"
}

func (c *Conn) query(ctx context.Context, ex execer, stmt string, maxRows int) (*Result, error) {
	// The cursor may outlive this call, so it gets its own context; ctx
	// only cancels it while the first rows are fetched.
	cctx, ccancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, ccancel)
	defer stop()
	rows, err := ex.QueryContext(cctx, stmt, godror.FetchArraySize(256))
	if err != nil {
		ccancel()
		return nil, err
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		ccancel()
		return nil, err
	}
	var types []string
	if cts, err := rows.ColumnTypes(); err == nil {
		for _, ct := range cts {
			types = append(types, ct.DatabaseTypeName())
		}
	}
	k := &cursor{rows: rows, cancel: ccancel, vals: make([]any, len(cols)), ptrs: make([]any, len(cols))}
	for i := range k.vals {
		k.ptrs[i] = &k.vals[i]
	}
	out, done, err := k.fetch(maxRows)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		k.close()
		return nil, err
	}
	res := &Result{Columns: cols, Types: types, Rows: out, IsQuery: true, More: !done, SQL: stmt}
	if done {
		k.close()
	} else {
		c.cur = k
	}
	res.Message = RowsMessage(len(out), res.More)
	return res, nil
}

// RowsMessage describes how many rows a query has returned so far.
func RowsMessage(n int, more bool) string {
	if more {
		return fmt.Sprintf("%d row(s) fetched, more available.", n)
	}
	return fmt.Sprintf("%d row(s) selected.", n)
}

// rePLSQLStart matches statements that contain PL/SQL and therefore end
// only at a "/" line: blocks, stored code, and queries with inline
// functions (WITH FUNCTION / WITH PROCEDURE, Oracle 12c+).
// reDDLLine matches a line starting with a DDL keyword (reserved words that
// can't begin a line of PL/SQL code).
var reDDLLine = regexp.MustCompile(`(?i)^(CREATE|ALTER|DROP|GRANT|REVOKE)\b`)

var rePLSQLStart = regexp.MustCompile(`(?i)^(DECLARE|BEGIN|CREATE\s+(OR\s+REPLACE\s+)?(EDITIONABLE\s+|NONEDITIONABLE\s+)?(PROCEDURE|FUNCTION|PACKAGE|TRIGGER|TYPE)\b|WITH\s+(FUNCTION|PROCEDURE)\b)`)

// Statement is one statement of a script with its position (0-based rows
// and columns, end inclusive) in the script.
type Statement struct {
	Text               string
	StartRow, StartCol int
	EndRow, EndCol     int
}

func isPLSQLText(s string) bool {
	return rePLSQLStart.MatchString(reLeadingComments.ReplaceAllString(s, ""))
}

// ParseStatements splits a script into statements, SQL*Plus style:
//   - plain SQL ends at a ';' (outside strings and comments, also in the
//     middle of a line), at a blank line, or at a line holding only "/";
//   - PL/SQL blocks (DECLARE/BEGIN/CREATE PROCEDURE...) end only at a line
//     holding only "/", so they may contain ';' and blank lines.
func ParseStatements(script string) []Statement {
	var (
		out                 []Statement
		pieces              []string
		started             bool
		st                  Statement
		lastR, lastC        int
		inQuote, inBlockCmt bool
		// first and last code character (not comment, not blank) of the
		// statement, as (piece index, rune index in the piece)
		hasCode            bool
		firstP, firstC     int
		lastCodeP, lastCod int

		qClose rune       // closing delimiter of a q'X...X' string (0: ordinary '...')
		blk    plsqlBlock // where a PL/SQL unit ends (see plsqlblock.go)
		word   []rune     // the word being read, outside strings and comments
	)
	feedWord := func() {
		if len(word) > 0 {
			blk.feed(strings.ToUpper(string(word)))
			word = word[:0]
		}
	}
	text := func() string { return strings.Join(pieces, "\n") }
	flush := func() {
		if started && hasCode {
			// keep only what lies between the first and the last code
			// character: comments before and after the statement are
			// skipped, comments inside it (and optimizer hints) stay
			var b []rune
			off := func(p, c int) int {
				n := 0
				for k := 0; k < p; k++ {
					n += len([]rune(pieces[k])) + 1
				}
				return n + c
			}
			b = []rune(text())
			st.Text = strings.TrimSpace(string(b[off(firstP, firstC) : off(lastCodeP, lastCod)+1]))
			st.EndRow, st.EndCol = lastR, lastC
			out = append(out, st)
		}
		pieces, started, st, hasCode = nil, false, Statement{}, false
		blk, word = plsqlBlock{}, word[:0]
	}
	code := func(c, segStart int) { // record a code character of the current line
		p, ci := len(pieces), c-segStart
		if !hasCode {
			hasCode, firstP, firstC = true, p, ci
		}
		lastCodeP, lastCod = p, ci
	}
	mark := func(r, c int) { // record non-blank content at r,c
		if !started {
			started = true
			st.StartRow, st.StartCol = r, c
		}
		lastR, lastC = r, c
	}

	for r, line := range strings.Split(script, "\n") {
		rs := []rune(line)
		t := strings.TrimSpace(line)
		if !inQuote && !inBlockCmt {
			if t == "/" {
				flush()
				continue
			}
			if !started && IsLineCommand(t) {
				// a SQL*Plus command: the whole line as written, no
				// quote, comment or ';' processing
				first := len(rs) - len([]rune(strings.TrimLeft(line, " \t")))
				out = append(out, Statement{Text: t, StartRow: r, StartCol: first, EndRow: r, EndCol: max(len(rs)-1, 0)})
				continue
			}
			// A PL/SQL statement (package, procedure ...) normally ends at a
			// "/" line. A line starting with DDL (CREATE, ALTER ...) can't be
			// PL/SQL code, so it starts the next statement even without "/".
			if started && hasCode && reDDLLine.MatchString(t) && isPLSQLText(text()) {
				flush()
			}
			if t == "" && !isPLSQLText(text()) {
				flush()
				continue
			}
		}
		segStart := 0
		for c := 0; c < len(rs); c++ {
			ch := rs[c]
			next := rune(0)
			if c+1 < len(rs) {
				next = rs[c+1]
			}
			switch {
			case inBlockCmt:
				mark(r, c)
				if ch == '*' && next == '/' {
					inBlockCmt = false
					c++
					mark(r, c)
				}
				continue
			case inQuote:
				mark(r, c)
				code(c, segStart)
				switch {
				case qClose != 0:
					if ch == qClose && next == '\'' {
						c++
						mark(r, c)
						code(c, segStart)
						inQuote, qClose = false, 0
					}
				case ch == '\'':
					if next == '\'' { // escaped quote
						c++
						mark(r, c)
						code(c, segStart)
					} else {
						inQuote = false
					}
				}
				continue
			case (ch == 'q' || ch == 'Q') && next == '\'' && c+2 < len(rs) && (c == 0 || !isIdentRune(rs[c-1])):
				// q'[...]' string: ends at the matching delimiter followed by '
				feedWord()
				mark(r, c)
				code(c, segStart)
				qClose, inQuote = qDelimiter(rs[c+2]), true
				c += 2
				mark(r, c)
				code(c, segStart)
				continue
			case ch == '-' && next == '-': // line comment: rest of line
				feedWord()
				if !started && segStart > 0 {
					// trailing comment after a statement on this line:
					// don't carry it into the next statement
					segStart = len(rs)
				} else {
					mark(r, len(rs)-1)
				}
				c = len(rs)
				continue
			case ch == '/' && next == '*':
				feedWord()
				inBlockCmt = true
				mark(r, c)
				c++
				mark(r, c)
				continue
			case ch == '\'':
				feedWord()
				inQuote = true
			}
			if ch != ' ' && ch != '\t' {
				mark(r, c)
				code(c, segStart)
			}
			if isIdentRune(ch) && !inQuote {
				word = append(word, ch)
			} else {
				feedWord()
				if ch == '(' || ch == ')' || ch == ';' {
					blk.feed(string(ch))
				}
			}
			if ch == ';' {
				seg := string(rs[segStart : c+1])
				cand := strings.Join(append(append([]string{}, pieces...), seg), "\n")
				if !isPLSQLText(cand) || blk.done {
					pieces = append(pieces, seg)
					flush()
					segStart = c + 1
				}
			}
		}
		feedWord() // words don't continue on the next line
		rest := string(rs[segStart:])
		if started || strings.TrimSpace(rest) != "" {
			pieces = append(pieces, rest)
		}
		// SQL*Plus commands (DESC, EXEC) end at the end of their line
		if started && hasCode && !inQuote && !inBlockCmt && isLineCommand(text()) {
			flush()
		}
	}
	flush()
	return out
}

// SplitStatements returns the statement texts of a script.
func SplitStatements(script string) []string {
	var out []string
	for _, s := range ParseStatements(script) {
		out = append(out, s.Text)
	}
	return out
}

// StatementAtCursor returns the statement containing the cursor (row, col).
// The position just after a statement's last character still counts as
// inside it. Elsewhere (e.g. on a blank line) the nearest statement above
// is used, or the first one below if there is none above.
func StatementAtCursor(script string, row, col int) (Statement, bool) {
	stmts := ParseStatements(script)
	before := func(r1, c1, r2, c2 int) bool { return r1 < r2 || (r1 == r2 && c1 <= c2) }
	var above *Statement
	for i := range stmts {
		s := &stmts[i]
		if before(s.StartRow, s.StartCol, row, col) && before(row, col, s.EndRow, s.EndCol+1) {
			return *s, true
		}
		if before(s.EndRow, s.EndCol, row, col) {
			above = s
		}
	}
	if above != nil {
		return *above, true
	}
	if len(stmts) > 0 {
		return stmts[0], true
	}
	return Statement{}, false
}

var reOraCode = regexp.MustCompile(`(ORA|DPI|PLS|TNS)-\d{4,5}`)

// CleanError strips godror's parameter dump, keeping the Oracle message.
func CleanError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if loc := reOraCode.FindStringIndex(s); loc != nil {
		prefix := ""
		if i := strings.Index(s[:loc[0]], "statement "); i == 0 {
			prefix = s[:strings.Index(s, ":")+2] // keep "statement N: "
		}
		return prefix + s[loc[0]:]
	}
	return s
}

// Null marks a NULL value in Result.Rows. It can't come from the database,
// so a real '(null)' string stays distinguishable. Display it as NullDisplay.
const Null = "\x00NULL\x00"

// NullDisplay is how NULL is shown on screen.
const NullDisplay = "(null)"

// FormatValue renders a scanned column value for display.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return Null
	case string:
		if x == "" { // Oracle: '' IS NULL; godror returns NULL strings as ""
			return Null
		}
		return x
	case []byte:
		return fmt.Sprintf("%X", x)
	case time.Time:
		if x.IsZero() {
			return Null
		}
		if x.Nanosecond() != 0 {
			return x.Format("2006-01-02 15:04:05.000000")
		}
		return x.Format("2006-01-02 15:04:05")
	case godror.Number:
		return string(x)
	case *godror.Lob:
		return "(lob)"
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// InComment reports whether the cursor at (row, col) is inside a comment:
// within /* ... */ (including on its delimiters), anywhere from "--" to the
// end of that line, or anywhere on a line that holds nothing but comments
// (e.g. on the indentation before "-- note", or just after a closing "*/").
// Quotes are respected.
func InComment(script string, row, col int) bool {
	if commentOnlyLine(script, row) {
		return true
	}
	return inCommentAt(script, row, col)
}

// scanLine walks one line from the given state and returns the state at its
// end and whether the line has any code (not blank, not inside a comment).
func scanLine(rs []rune, inBlock, inQuote bool, qClose *rune) (bool, bool, bool) {
	hasCode := false
	for c := 0; c < len(rs); c++ {
		next := rune(0)
		if c+1 < len(rs) {
			next = rs[c+1]
		}
		switch {
		case inBlock:
			if rs[c] == '*' && next == '/' {
				inBlock = false
				c++
			}
		case inQuote && *qClose != 0:
			hasCode = true
			if rs[c] == *qClose && next == '\'' {
				inQuote, *qClose = false, 0
				c++
			}
		case inQuote:
			hasCode = true
			if rs[c] == '\'' {
				if next == '\'' {
					c++
				} else {
					inQuote = false
				}
			}
		case (rs[c] == 'q' || rs[c] == 'Q') && next == '\'' && c+2 < len(rs) && (c == 0 || !isIdentRune(rs[c-1])):
			hasCode, inQuote, *qClose = true, true, qDelimiter(rs[c+2])
			c += 2
		case rs[c] == '-' && next == '-':
			return inBlock, inQuote, hasCode
		case rs[c] == '/' && next == '*':
			inBlock = true
			c++
		case rs[c] == '\'':
			hasCode, inQuote = true, true
		case rs[c] != ' ' && rs[c] != '\t':
			hasCode = true
		}
	}
	return inBlock, inQuote, hasCode
}

// commentOnlyLine reports whether line row is non-blank and holds only
// comments.
func commentOnlyLine(script string, row int) bool {
	lines := strings.Split(script, "\n")
	if row < 0 || row >= len(lines) || strings.TrimSpace(lines[row]) == "" {
		return false
	}
	inBlock, inQuote := false, false
	var q rune
	for r := 0; r < row; r++ {
		inBlock, inQuote, _ = scanLine([]rune(lines[r]), inBlock, inQuote, &q)
	}
	_, _, hasCode := scanLine([]rune(lines[row]), inBlock, inQuote, &q)
	return !hasCode
}

// inCommentAt is the character-level part of InComment.
func inCommentAt(script string, row, col int) bool {
	lines := strings.Split(script, "\n")
	if row < 0 || row >= len(lines) {
		return false
	}
	inBlock, inQuote := false, false
	var qClose rune
	for r := 0; r <= row; r++ {
		rs := []rune(lines[r])
		end := len(rs)
		if r == row {
			end = min(max(col, 0), len(rs))
		}
		for c := 0; c < len(rs); c++ {
			atCursor := r == row && c == end
			next := rune(0)
			if c+1 < len(rs) {
				next = rs[c+1]
			}
			switch {
			case inBlock:
				if atCursor {
					return true
				}
				if rs[c] == '*' && next == '/' {
					if r == row && c+1 == end { // cursor on the closing '/'
						return true
					}
					inBlock = false
					c++
				}
			case inQuote:
				if atCursor {
					return false
				}
				switch {
				case qClose != 0:
					if rs[c] == qClose && next == '\'' {
						inQuote, qClose = false, 0
						c++
					}
				case rs[c] == '\'':
					if next == '\'' {
						c++
					} else {
						inQuote = false
					}
				}
			case (rs[c] == 'q' || rs[c] == 'Q') && next == '\'' && c+2 < len(rs) && (c == 0 || !isIdentRune(rs[c-1])):
				if atCursor || (r == row && c < end && end <= c+2) {
					return false
				}
				inQuote, qClose = true, qDelimiter(rs[c+2])
				c += 2
			case rs[c] == '-' && next == '-':
				if r == row && end >= c {
					return true // cursor on or after "--" on this line
				}
				c = len(rs) // rest of an earlier line
			case rs[c] == '/' && next == '*':
				if atCursor || (r == row && c+1 == end) {
					return true
				}
				inBlock = true
				c++
			case rs[c] == '\'':
				if atCursor {
					return false
				}
				inQuote = true
			default:
				if atCursor {
					return false
				}
			}
			if r == row && c >= end {
				break
			}
		}
		if r == row {
			return inBlock // cursor at the end of the line
		}
	}
	return false
}

var reCompiled = regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?(?:(?:NON)?EDITIONABLE\s+)?` +
	`(PACKAGE\s+BODY|TYPE\s+BODY|PACKAGE|PROCEDURE|FUNCTION|TRIGGER|TYPE)\s+` +
	`(?:("[^"]+"|[A-Za-z][\w$#]*)\s*\.\s*)?("[^"]+"|[A-Za-z][\w$#]*)`)

// compiledObject recognises CREATE of stored PL/SQL and returns its type
// (as in USER_ERRORS: "PACKAGE BODY" ...), owner ("" = current schema) and
// name, upper-cased unless quoted.
func compiledObject(stmt string) (kind, owner, name string, ok bool) {
	m := reCompiled.FindStringSubmatch(reLeadingComments.ReplaceAllString(stmt, ""))
	if m == nil {
		return "", "", "", false
	}
	ident := func(s string) string {
		if strings.HasPrefix(s, `"`) {
			return strings.Trim(s, `"`)
		}
		return strings.ToUpper(s)
	}
	kind = strings.ToUpper(strings.Join(strings.Fields(m[1]), " "))
	if m[2] != "" {
		owner = ident(m[2])
	}
	return kind, owner, ident(m[3]), true
}

// compileErrors lists an object's compilation errors like SQL*Plus SHOW
// ERRORS: "LINE/COL  text". Callers hold c.mu.
func (c *Conn) compileErrors(q execer, kind, owner, name string) []string {
	rows, err := q.QueryContext(context.Background(), `
		select line, position, text from all_errors
		 where owner = nvl(:1, sys_context('userenv', 'current_schema'))
		   and name = :2 and type = :3
		 order by sequence`, owner, name, kind)
	if err != nil {
		return []string{"(could not read the compilation errors: " + CleanError(err) + ")"}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line, pos int
		var text string
		if err := rows.Scan(&line, &pos, &text); err != nil {
			break
		}
		out = append(out, fmt.Sprintf("%d/%d  %s", line, pos, strings.TrimSpace(text)))
	}
	return out
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || r == '#' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// qDelimiter returns the closing delimiter of a q'X...X' string.
func qDelimiter(open rune) rune {
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
