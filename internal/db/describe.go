package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/godror/godror"
)

// SQL*Plus commands that sqlcli runs itself. They take exactly one line,
// read as written (quotes and ';' inside are just text).
var (
	reLineCommand = regexp.MustCompile(`(?i)^(@|(DESC|DESCRIBE|EXEC|EXECUTE|SHOW|SHO|SET|PROMPT|PRO|SPOOL|SPO|` +
		`DEFINE|DEF|UNDEFINE|UNDEF|START|STA|REM|REMARK)(\s|;|$))`)
	// SET statements that are SQL, not SQL*Plus
	reSQLSet = regexp.MustCompile(`(?i)^SET\s+(TRANSACTION|ROLE|CONSTRAINTS?)\b`)
)

// IsLineCommand reports whether a statement is a one-line SQL*Plus command.
func IsLineCommand(stmt string) bool {
	s := strings.TrimSpace(reLeadingComments.ReplaceAllString(stmt, ""))
	return reLineCommand.MatchString(s) && !reSQLSet.MatchString(s)
}

func isLineCommand(stmt string) bool { return IsLineCommand(stmt) }

// execAsBlock turns "EXEC proc(args)" into "BEGIN proc(args); END;".
func execAsBlock(stmt string) string {
	s := reLeadingComments.ReplaceAllString(stmt, "")
	s = strings.TrimSpace(s[len(reFirstWord.FindString(s)):])
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	return "begin " + s + "; end;"
}

var reDescName = regexp.MustCompile(`^("[^"]+"|[A-Za-z][\w$#]*)(?:\s*\.\s*("[^"]+"|[A-Za-z][\w$#]*))?$`)

// describe implements DESC[RIBE] [owner.]name for tables, views, synonyms,
// packages, procedures and functions. Callers hold c.mu.
func (c *Conn) describe(ctx context.Context, q execer, stmt string) (*Result, error) {
	s := reLeadingComments.ReplaceAllString(stmt, "")
	arg := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[len(reFirstWord.FindString(s)):]), ";"))
	m := reDescName.FindStringSubmatch(arg)
	if m == nil {
		return nil, fmt.Errorf("usage: DESC [owner.]name")
	}
	ident := func(x string) string {
		if strings.HasPrefix(x, `"`) {
			return strings.Trim(x, `"`)
		}
		return strings.ToUpper(x)
	}
	owner, name := "", ident(m[1])
	if m[2] != "" {
		owner, name = name, ident(m[2])
	}

	owner, name, typ, err := c.resolveObject(ctx, q, owner, name)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "TABLE", "VIEW", "MATERIALIZED VIEW":
		return c.describeColumns(ctx, q, owner, name, typ)
	case "PACKAGE", "PROCEDURE", "FUNCTION":
		return c.describeArguments(ctx, q, owner, name, typ)
	}
	return nil, fmt.Errorf("DESC doesn't describe a %s (%s.%s)", strings.ToLower(typ), owner, name)
}

// resolveObject finds an object by name (current schema unless an owner is
// given), following private and public synonyms.
func (c *Conn) resolveObject(ctx context.Context, q execer, owner, name string) (string, string, string, error) {
	for hop := 0; hop < 5; hop++ { // a synonym may point to a synonym
		var rows [][]string
		var err error
		if owner == "" {
			rows, err = queryRows(ctx, q, `select owner, object_type from all_objects
				where object_name = :1 and owner = sys_context('userenv', 'current_schema')
				  and object_type in ('TABLE', 'VIEW', 'MATERIALIZED VIEW', 'SYNONYM', 'PACKAGE', 'PROCEDURE', 'FUNCTION', 'TYPE')
				order by decode(object_type, 'SYNONYM', 2, 1)`, name)
		} else {
			rows, err = queryRows(ctx, q, `select owner, object_type from all_objects
				where object_name = :1 and owner = :2
				  and object_type in ('TABLE', 'VIEW', 'MATERIALIZED VIEW', 'SYNONYM', 'PACKAGE', 'PROCEDURE', 'FUNCTION', 'TYPE')
				order by decode(object_type, 'SYNONYM', 2, 1)`, name, owner)
		}
		if err != nil {
			return "", "", "", err
		}
		if len(rows) > 0 && rows[0][1] != "SYNONYM" {
			return rows[0][0], name, rows[0][1], nil
		}
		// a synonym: private (in the schema) first, then public
		synOwner := owner
		if len(rows) > 0 {
			synOwner = rows[0][0]
		}
		var syn [][]string
		if synOwner != "" {
			syn, err = queryRows(ctx, q, `select table_owner, table_name from all_synonyms where owner = :1 and synonym_name = :2`, synOwner, name)
		}
		if err == nil && len(syn) == 0 && owner == "" {
			syn, err = queryRows(ctx, q, `select table_owner, table_name from all_synonyms where owner = 'PUBLIC' and synonym_name = :1`, name)
		}
		if err != nil {
			return "", "", "", err
		}
		if len(syn) == 0 {
			break
		}
		owner, name = syn[0][0], syn[0][1]
	}
	shown := name
	if owner != "" {
		shown = owner + "." + name
	}
	return "", "", "", fmt.Errorf("ORA-04043: object %s does not exist", shown)
}

func (c *Conn) describeColumns(ctx context.Context, q execer, owner, name, typ string) (*Result, error) {
	rows, err := queryRows(ctx, q, `
		select column_name, decode(nullable, 'N', 'NOT NULL'), data_type, to_char(data_length),
		       to_char(data_precision), to_char(data_scale), to_char(char_length), char_used
		  from all_tab_columns where owner = :1 and table_name = :2 order by column_id`, owner, name)
	if err != nil {
		return nil, err
	}
	res := &Result{IsQuery: true, Columns: []string{"NAME", "NULL?", "TYPE"}}
	for _, r := range rows {
		null := r[1]
		if null == "" {
			null = Null
		}
		res.Rows = append(res.Rows, []string{r[0], null, columnType(r[2], r[3], r[4], r[5], r[6], r[7])})
	}
	res.Message = fmt.Sprintf("%s %s.%s: %d column(s).", typ, owner, name, len(res.Rows))
	return res, nil
}

// columnType formats a column type the way SQL*Plus DESCRIBE shows it.
func columnType(dataType, length, precision, scale, charLength, charUsed string) string {
	switch dataType {
	case "VARCHAR2", "CHAR":
		if charUsed == "C" {
			return fmt.Sprintf("%s(%s CHAR)", dataType, charLength)
		}
		return fmt.Sprintf("%s(%s)", dataType, length)
	case "NVARCHAR2", "NCHAR":
		return fmt.Sprintf("%s(%s)", dataType, charLength)
	case "RAW":
		return fmt.Sprintf("RAW(%s)", length)
	case "NUMBER":
		switch {
		case precision != "" && (scale == "" || scale == "0"):
			return fmt.Sprintf("NUMBER(%s)", precision)
		case precision != "":
			return fmt.Sprintf("NUMBER(%s,%s)", precision, scale)
		case scale == "0":
			return "NUMBER(38)" // INTEGER
		}
		return "NUMBER"
	case "FLOAT":
		if precision != "" {
			return fmt.Sprintf("FLOAT(%s)", precision)
		}
	}
	return dataType
}

func (c *Conn) describeArguments(ctx context.Context, q execer, owner, name, typ string) (*Result, error) {
	// the subprograms come from ALL_PROCEDURES: subprograms without
	// arguments have no row in ALL_ARGUMENTS
	var subWhere, argWhere string
	if typ == "PACKAGE" {
		subWhere = "owner = :1 and object_name = :2 and procedure_name is not null"
		argWhere = "owner = :1 and package_name = :2"
	} else {
		subWhere = "owner = :1 and object_name = :2 and procedure_name is null"
		argWhere = "owner = :1 and package_name is null and object_name = :2"
	}
	subs, err := queryRows(ctx, q, `select nvl(procedure_name, object_name), nvl(overload, '0')
		from all_procedures where `+subWhere+` order by subprogram_id`, owner, name)
	if err != nil {
		return nil, err
	}
	argRows, err := queryRows(ctx, q, `
		select object_name, nvl(overload, '0'), to_char(position), argument_name,
		       nvl(type_name, data_type), type_subname, in_out, defaulted
		  from all_arguments
		 where `+argWhere+` and data_level = 0
		 order by subprogram_id, sequence`, owner, name)
	if err != nil {
		return nil, err
	}
	type sub struct {
		ret  string
		args [][]string
	}
	byKey := map[string]*sub{}
	for _, r := range argRows {
		obj, overload, position, argName, argType, subName, inOut, defaulted := r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7]
		key := obj + "#" + overload
		s := byKey[key]
		if s == nil {
			s = &sub{}
			byKey[key] = s
		}
		if subName != "" {
			argType += "." + subName
		}
		switch {
		case position == "0":
			s.ret = argType
		case argName != "" || argType != "":
			def := Null
			if defaulted == "Y" {
				def = "yes"
			}
			s.args = append(s.args, []string{argName, argType, inOut, def})
		}
	}
	res := &Result{IsQuery: true, Columns: []string{"SUBPROGRAM", "ARGUMENT", "TYPE", "IN/OUT", "DEFAULT"}}
	for _, sp := range subs {
		s := byKey[sp[0]+"#"+sp[1]]
		if s == nil {
			s = &sub{}
		}
		kind, ret := "PROCEDURE", Null
		if s.ret != "" {
			kind, ret = "FUNCTION", "RETURN "+s.ret
		}
		res.Rows = append(res.Rows, []string{kind + " " + sp[0], Null, ret, Null, Null})
		for _, a := range s.args {
			res.Rows = append(res.Rows, append([]string{""}, a...))
		}
	}
	if typ == "PACKAGE" {
		res.Message = fmt.Sprintf("PACKAGE %s.%s: %d subprogram(s).", owner, name, len(subs))
	} else {
		res.Message = fmt.Sprintf("%s %s.%s", typ, owner, name)
	}
	return res, nil
}

// queryRows runs a small dictionary query and returns its rows as strings.
func queryRows(ctx context.Context, q execer, query string, args ...any) ([][]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out [][]string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			if v != nil {
				row[i] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// commandArgs returns the words after a command keyword.
func commandArgs(stmt string) []string {
	s := strings.TrimSpace(reLeadingComments.ReplaceAllString(stmt, ""))
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f[1:]
}

// sqlplusSet handles SQL*Plus SET: SERVEROUTPUT ON|OFF changes DBMS_OUTPUT
// collection; display settings (LINESIZE, PAGESIZE ...) don't apply to
// sqlcli and are accepted without effect. Callers hold c.mu.
func (c *Conn) sqlplusSet(stmt string) (*Result, error) {
	args := commandArgs(stmt)
	if len(args) == 0 {
		return nil, fmt.Errorf("usage: SET option value")
	}
	opt := strings.ToUpper(args[0])
	if strings.HasPrefix("SERVEROUTPUT", opt) && len(opt) >= 4 { // SERVEROUTPUT, SERVEROUT, SERVEROUT...
		if len(args) < 2 {
			return nil, fmt.Errorf("usage: SET SERVEROUTPUT ON|OFF")
		}
		switch strings.ToUpper(args[1]) {
		case "ON":
			if !c.output {
				c.output = godror.EnableDbmsOutput(context.Background(), c.sc) == nil
				if !c.output {
					return nil, fmt.Errorf("DBMS_OUTPUT could not be enabled")
				}
			}
			return &Result{Message: "SERVEROUTPUT is ON."}, nil
		case "OFF":
			c.output = false
			_, _ = c.sc.ExecContext(context.Background(), "begin dbms_output.disable; end;")
			return &Result{Message: "SERVEROUTPUT is OFF."}, nil
		}
		return nil, fmt.Errorf("usage: SET SERVEROUTPUT ON|OFF")
	}
	return &Result{Message: fmt.Sprintf("SET %s has no effect in sqlcli.", opt)}, nil
}

var reShowErrors = regexp.MustCompile(`(?i)^(?:ERR|ERRO|ERROR|ERRORS)(?:\s+(PACKAGE\s+BODY|TYPE\s+BODY|PACKAGE|PROCEDURE|FUNCTION|TRIGGER|TYPE|VIEW)\s+(?:("[^"]+"|[A-Za-z][\w$#]*)\s*\.\s*)?("[^"]+"|[A-Za-z][\w$#]*))?$`)

// show handles SQL*Plus SHOW ERRORS [type [owner.]name] and SHOW USER.
// Callers hold c.mu.
func (c *Conn) show(ctx context.Context, q execer, stmt string) (*Result, error) {
	args := commandArgs(stmt)
	rest := strings.Join(args, " ")
	switch {
	case len(args) == 1 && strings.EqualFold(args[0], "USER"):
		rows, err := queryRows(ctx, q, "select user from dual")
		if err != nil {
			return nil, err
		}
		return &Result{Message: fmt.Sprintf("USER is %q", rows[0][0])}, nil
	case reShowErrors.MatchString(rest):
		m := reShowErrors.FindStringSubmatch(rest)
		kind, owner, name := c.lastObj[0], c.lastObj[1], c.lastObj[2]
		if m[1] != "" {
			ident := func(x string) string {
				if strings.HasPrefix(x, `"`) {
					return strings.Trim(x, `"`)
				}
				return strings.ToUpper(x)
			}
			kind = strings.ToUpper(strings.Join(strings.Fields(m[1]), " "))
			owner, name = "", ident(m[3])
			if m[2] != "" {
				owner = ident(m[2])
			}
		}
		if name == "" {
			return &Result{Message: "No errors: nothing was created in this session yet (or use SHOW ERRORS type name)."}, nil
		}
		errs := c.compileErrors(q, kind, owner, name)
		if len(errs) == 0 {
			return &Result{Message: fmt.Sprintf("No errors for %s %s.", kind, name)}, nil
		}
		res := &Result{IsQuery: true, Columns: []string{"LINE/COL", "ERROR"}}
		for _, e := range errs {
			lc, text, _ := strings.Cut(e, "  ")
			res.Rows = append(res.Rows, []string{lc, text})
		}
		res.Message = fmt.Sprintf("Errors for %s %s: %d.", kind, name, len(errs))
		return res, nil
	}
	return nil, fmt.Errorf("SHOW %s is not supported (use SHOW ERRORS or SHOW USER)", rest)
}
