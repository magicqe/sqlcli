package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/godror/godror"
)

// ObjectInfo is a database object offered by code completion.
type ObjectInfo struct {
	Name string
	Type string // TABLE, VIEW, SYNONYM, SEQUENCE, PACKAGE, ..., or PUBLIC SYNONYM
}

// metaQuery runs a read-only data-dictionary query in the session without
// touching an open result cursor. Callers pass a context without a
// deadline: godror can leave a deadline behind as an Oracle call timeout.
func (c *Conn) metaQuery(ctx context.Context, query string, args ...any) ([][]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.sc == nil {
		return nil, sql.ErrConnDone
	}
	var q interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	} = c.sc
	if c.tx != nil {
		q = c.tx
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out [][]string
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = v.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Objects lists the current schema's tables, views, synonyms, sequences
// and stored code, plus public synonyms (DUAL, DBMS_OUTPUT, ...).
func (c *Conn) Objects(ctx context.Context) ([]ObjectInfo, error) {
	rows, err := c.metaQuery(ctx, `
		select object_name, object_type from user_objects
		 where object_type in ('TABLE', 'VIEW', 'SYNONYM', 'SEQUENCE', 'PACKAGE', 'PROCEDURE',
		                       'FUNCTION', 'MATERIALIZED VIEW', 'TYPE')
		   and object_name not like 'BIN$%'
		union all
		select synonym_name, 'PUBLIC SYNONYM' from all_synonyms
		 where owner = 'PUBLIC' and synonym_name not like '%/%'`)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectInfo, len(rows))
	for i, r := range rows {
		out[i] = ObjectInfo{Name: r[0], Type: r[1]}
	}
	return out, nil
}

// Columns returns the column names of a table or view, in column order.
// Without an owner it looks in the current schema, then follows a private
// or public synonym of that name.
func (c *Conn) Columns(ctx context.Context, owner, table string) ([]string, error) {
	query := `select column_name from all_tab_columns where owner = :1 and table_name = :2 order by column_id`
	names := func(rows [][]string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r[0]
		}
		return out
	}
	if owner != "" {
		rows, err := c.metaQuery(ctx, query, owner, table)
		return names(rows), err
	}
	rows, err := c.metaQuery(ctx, `select column_name from all_tab_columns
		where owner = sys_context('userenv', 'current_schema') and table_name = :1 order by column_id`, table)
	if err != nil || len(rows) > 0 {
		return names(rows), err
	}
	syn, err := c.metaQuery(ctx, `select table_owner, table_name from all_synonyms
		where synonym_name = :1 and owner in (sys_context('userenv', 'current_schema'), 'PUBLIC')
		order by decode(owner, 'PUBLIC', 2, 1)`, table)
	if err != nil || len(syn) == 0 {
		return nil, err
	}
	rows, err = c.metaQuery(ctx, query, syn[0][0], strings.TrimSpace(syn[0][1]))
	return names(rows), err
}

// Member is a function or procedure of a package.
type Member struct {
	Name       string
	IsFunction bool
}

// Members returns the functions and procedures of a package, in declaration
// order (overloads once). Without an owner it looks in the current schema,
// then follows a private or public synonym (DBMS_OUTPUT -> SYS.DBMS_OUTPUT).
func (c *Conn) Members(ctx context.Context, owner, pkg string) ([]Member, error) {
	query := `
		select p.procedure_name, max(case when a.position = 0 then 1 else 0 end)
		  from all_procedures p
		  left join all_arguments a
		    on a.owner = p.owner and a.package_name = p.object_name
		   and a.object_name = p.procedure_name and a.position = 0
		 where p.owner = %s and p.object_name = :%d and p.procedure_name is not null
		 group by p.procedure_name
		 order by min(p.subprogram_id)`
	toMembers := func(rows [][]string) []Member {
		out := make([]Member, len(rows))
		for i, r := range rows {
			out[i] = Member{Name: r[0], IsFunction: r[1] == "1"}
		}
		return out
	}
	if owner != "" {
		rows, err := c.metaQuery(ctx, fmt.Sprintf(query, ":1", 2), owner, pkg)
		return toMembers(rows), err
	}
	rows, err := c.metaQuery(ctx, fmt.Sprintf(query, "sys_context('userenv', 'current_schema')", 1), pkg)
	if err != nil || len(rows) > 0 {
		return toMembers(rows), err
	}
	syn, err := c.metaQuery(ctx, `select table_owner, table_name from all_synonyms
		where synonym_name = :1 and owner in (sys_context('userenv', 'current_schema'), 'PUBLIC')
		order by decode(owner, 'PUBLIC', 2, 1)`, pkg)
	if err != nil || len(syn) == 0 {
		return nil, err
	}
	rows, err = c.metaQuery(ctx, fmt.Sprintf(query, ":1", 2), syn[0][0], syn[0][1])
	return toMembers(rows), err
}

// SchemaObject is an object of the current schema for the object tree.
type SchemaObject struct {
	Type, Name string
	Valid      bool
}

// SchemaObjects lists the current schema's objects for the object tree.
func (c *Conn) SchemaObjects(ctx context.Context) ([]SchemaObject, error) {
	rows, err := c.metaQuery(ctx, `
		select object_type, object_name, status from user_objects
		 where object_type not in ('PACKAGE BODY', 'TYPE BODY', 'LOB', 'LOB PARTITION', 'LOB SUBPARTITION',
		                           'TABLE PARTITION', 'TABLE SUBPARTITION', 'INDEX PARTITION', 'INDEX SUBPARTITION')
		   and object_name not like 'BIN$%'
		 order by object_name`)
	if err != nil {
		return nil, err
	}
	out := make([]SchemaObject, len(rows))
	for i, r := range rows {
		out[i] = SchemaObject{Type: r[0], Name: r[1], Valid: r[2] == "VALID"}
	}
	return out, nil
}

// ColumnInfo is a column with its type as SQL*Plus DESCRIBE shows it.
type ColumnInfo struct {
	Name, Type string
	NotNull    bool
}

// ColumnInfos returns the columns of a table or view of the current schema.
func (c *Conn) ColumnInfos(ctx context.Context, table string) ([]ColumnInfo, error) {
	rows, err := c.metaQuery(ctx, `
		select column_name, nullable, data_type, to_char(data_length), to_char(data_precision),
		       to_char(data_scale), to_char(char_length), char_used
		  from user_tab_columns where table_name = :1 order by column_id`, table)
	if err != nil {
		return nil, err
	}
	out := make([]ColumnInfo, len(rows))
	for i, r := range rows {
		out[i] = ColumnInfo{Name: r[0], NotNull: r[1] == "N", Type: columnType(r[2], r[3], r[4], r[5], r[6], r[7])}
	}
	return out, nil
}

// metadataTypes maps an object type to the DBMS_METADATA object types whose
// DDL makes it up (a package is its spec, then its body).
var metadataTypes = map[string][]string{
	"TABLE": {"TABLE"}, "VIEW": {"VIEW"}, "MATERIALIZED VIEW": {"MATERIALIZED_VIEW"},
	"PACKAGE": {"PACKAGE_SPEC", "PACKAGE_BODY"}, "TYPE": {"TYPE_SPEC", "TYPE_BODY"},
	"PROCEDURE": {"PROCEDURE"}, "FUNCTION": {"FUNCTION"}, "SEQUENCE": {"SEQUENCE"},
	"SYNONYM": {"SYNONYM"}, "TRIGGER": {"TRIGGER"}, "INDEX": {"INDEX"},
	"JAVA SOURCE": {"JAVA_SOURCE"}, "DATABASE LINK": {"DB_LINK"}, "JOB": {"PROCOBJ"},
	"PROGRAM": {"PROCOBJ"}, "SCHEDULE": {"PROCOBJ"},
}

// DDL returns the DDL of an object of the current schema from
// DBMS_METADATA.GET_DDL, with statement terminators (";" and "/") so it can
// be run as it is. A package (or type) is its spec followed by its body; a
// missing body is left out. It doesn't open a transaction.
func (c *Conn) DDL(ctx context.Context, objectType, name string) (string, error) {
	parts, ok := metadataTypes[objectType]
	if !ok { // other types: DBMS_METADATA's name is mostly the type with "_"
		parts = []string{strings.ReplaceAll(objectType, " ", "_")}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.sc == nil {
		return "", sql.ErrConnDone
	}
	var q execer = c.sc
	if c.tx != nil {
		q = c.tx
	}
	// session settings for GET_DDL: end statements with ; and /
	if _, err := q.ExecContext(ctx, `begin
		dbms_metadata.set_transform_param(dbms_metadata.session_transform, 'SQLTERMINATOR', true);
		dbms_metadata.set_transform_param(dbms_metadata.session_transform, 'PRETTY', true);
	end;`); err != nil {
		return "", err
	}
	var out []string
	for i, mt := range parts {
		rows, err := queryRows(ctx, q, `select dbms_metadata.get_ddl(:1, :2) from dual`, mt, name)
		if err != nil {
			if i > 0 && strings.Contains(err.Error(), "ORA-31603") { // e.g. a package without a body
				continue
			}
			return "", err
		}
		if len(rows) > 0 {
			out = append(out, strings.Trim(strings.TrimLeft(rows[0][0], "\n"), "\n "))
		}
	}
	return strings.Join(out, "\n\n") + "\n", nil
}

// ColumnType returns a table column's data type (e.g. "NUMBER", "DATE",
// "TIMESTAMP(6)", "VARCHAR2"), or an error if the table has no such column.
func (c *Conn) ColumnType(ctx context.Context, owner, table, column string) (string, error) {
	rows, err := c.metaQuery(ctx, `select data_type from all_tab_columns
		where owner = nvl(:1, sys_context('userenv', 'current_schema')) and table_name = :2 and column_name = :3`,
		owner, table, column)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%s is not a column of %s (computed columns and aliases can't be edited)", column, table)
	}
	return rows[0][0], nil
}

// UpdateCell sets one column of the row with the given ROWID:
// UPDATE owner.table SET column = :value WHERE ROWID = :rowid. The value is
// converted for the column type (numbers with '.', dates as
// "YYYY-MM-DD HH24:MI:SS"); null sets NULL. Like other DML it runs in the
// open transaction (or starts one) unless auto-commit is on. An open result
// cursor stays open.
func (c *Conn) UpdateCell(ctx context.Context, owner, table, column, colType, value string, null bool, rowid string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.sc == nil {
		return sql.ErrConnDone
	}
	if c.ReadOnly {
		return fmt.Errorf("connection %q is read-only", c.Name)
	}
	var arg any
	if null {
		arg = nil
	} else {
		v, err := bindValue(colType, value)
		if err != nil {
			return err
		}
		arg = v
	}
	stmt := fmt.Sprintf("update %s set %s = :1 where rowid = chartorowid(:2)", tableName(owner, table), quoteName(column))
	ex, err := c.dmlExecer()
	if err != nil {
		return err
	}
	r, err := ex.ExecContext(ctx, stmt, arg, rowid)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("the row was not found (deleted meanwhile?): %d rows updated", n)
	}
	return nil
}

// bindValue converts a typed-in value for a column type.
func bindValue(colType, value string) (any, error) {
	t := strings.ToUpper(colType)
	switch {
	case t == "NUMBER" || t == "FLOAT" || t == "BINARY_FLOAT" || t == "BINARY_DOUBLE" || t == "INTEGER":
		v := strings.ReplaceAll(strings.TrimSpace(value), ",", ".")
		if !reNumberValue.MatchString(v) {
			return nil, fmt.Errorf("%q is not a number", value)
		}
		return godror.Number(v), nil
	case t == "DATE" || strings.HasPrefix(t, "TIMESTAMP"):
		v := strings.TrimSpace(value)
		for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
			if tm, err := time.ParseInLocation(layout, v, time.Local); err == nil {
				return tm, nil
			}
		}
		return nil, fmt.Errorf("%q is not a date: use YYYY-MM-DD [HH:MI[:SS]]", value)
	case strings.HasSuffix(t, "CLOB"): // as a LOB: a string bind is limited to 32k
		return godror.Lob{IsClob: true, Reader: strings.NewReader(value)}, nil
	case strings.Contains(t, "CHAR"):
		return value, nil
	}
	return nil, fmt.Errorf("columns of type %s can't be edited here", colType)
}

var reNumberValue = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)

// quoteName quotes an identifier unless it's a plain upper-case name.
func quoteName(s string) string {
	if regexp.MustCompile(`^[A-Z][A-Z0-9_$#]*$`).MatchString(s) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ColumnTypes returns the data types of a table's columns by column name.
func (c *Conn) ColumnTypes(ctx context.Context, owner, table string) (map[string]string, error) {
	rows, err := c.metaQuery(ctx, `select column_name, data_type from all_tab_columns
		where owner = nvl(:1, sys_context('userenv', 'current_schema')) and table_name = :2`, owner, table)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("table %s not found", table)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r[0]] = r[1]
	}
	return out, nil
}

// dmlExecer returns where DML runs: the open transaction, a new one (unless
// auto-commit is on), or the session. Callers hold c.mu.
func (c *Conn) dmlExecer() (execer, error) {
	if c.tx == nil && !c.auto {
		tx, err := c.sc.BeginTx(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		c.tx = tx
	}
	if c.tx != nil {
		return c.tx, nil
	}
	return c.sc, nil
}

func (c *Conn) checkWritable() error {
	if c.closed || c.sc == nil {
		return sql.ErrConnDone
	}
	if c.ReadOnly {
		return fmt.Errorf("connection %q is read-only", c.Name)
	}
	return nil
}

func tableName(owner, table string) string {
	if owner != "" {
		return quoteName(owner) + "." + quoteName(table)
	}
	return quoteName(table)
}

// InsertRow inserts one row: INSERT INTO table (cols) VALUES (:1, ...)
// RETURNING ROWID. Values are converted for the column types; an empty
// value is NULL. It returns the new row's ROWID.
func (c *Conn) InsertRow(ctx context.Context, owner, table string, cols, types, values []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkWritable(); err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("no columns to insert")
	}
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	args := make([]any, 0, len(cols)+1)
	for i, col := range cols {
		names[i], marks[i] = quoteName(col), fmt.Sprintf(":%d", i+1)
		if values[i] == "" {
			args = append(args, nil)
			continue
		}
		v, err := bindValue(types[i], values[i])
		if err != nil {
			return "", fmt.Errorf("%s: %w", col, err)
		}
		args = append(args, v)
	}
	var rowid string
	args = append(args, sql.Out{Dest: &rowid})
	stmt := fmt.Sprintf("insert into %s (%s) values (%s) returning rowidtochar(rowid) into :%d",
		tableName(owner, table), strings.Join(names, ", "), strings.Join(marks, ", "), len(cols)+1)
	ex, err := c.dmlExecer()
	if err != nil {
		return "", err
	}
	if _, err := ex.ExecContext(ctx, stmt, args...); err != nil {
		return "", err
	}
	return rowid, nil
}

// DeleteRow deletes the row with the given ROWID.
func (c *Conn) DeleteRow(ctx context.Context, owner, table, rowid string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkWritable(); err != nil {
		return err
	}
	ex, err := c.dmlExecer()
	if err != nil {
		return err
	}
	r, err := ex.ExecContext(ctx, fmt.Sprintf("delete from %s where rowid = chartorowid(:1)", tableName(owner, table)), rowid)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("the row was not found (deleted meanwhile?)")
	}
	return nil
}
