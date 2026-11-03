package db

import (
	"context"
	"database/sql"
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
