package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		 where object_type in ('TABLE', 'VIEW', 'MATERIALIZED VIEW', 'PACKAGE', 'PROCEDURE', 'FUNCTION',
		                       'TYPE', 'SEQUENCE', 'SYNONYM', 'TRIGGER', 'INDEX')
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
}

// DDL returns the DDL of an object of the current schema from
// DBMS_METADATA.GET_DDL, with statement terminators (";" and "/") so it can
// be run as it is. A package (or type) is its spec followed by its body; a
// missing body is left out. It doesn't open a transaction.
func (c *Conn) DDL(ctx context.Context, objectType, name string) (string, error) {
	parts, ok := metadataTypes[objectType]
	if !ok {
		return "", fmt.Errorf("no DDL for a %s", strings.ToLower(objectType))
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
