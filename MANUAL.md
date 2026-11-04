# sqlcli — User Manual

sqlcli is a terminal SQL client for Oracle databases. It has a split-screen
interface (connections, SQL editor, results), an editor with **JOE**
(Joe's Own Editor) keybindings, a TOML configuration file, and a searchable
history of every executed statement stored in SQLite.

---

## Contents

1. [Requirements](#1-requirements)
2. [Building](#2-building)
3. [Starting sqlcli](#3-starting-sqlcli)
4. [The screen](#4-the-screen)
5. [Connections](#5-connections)
6. [The SQL editor (JOE keys)](#6-the-sql-editor-joe-keys)
7. [Running SQL](#7-running-sql)
8. [Transactions](#8-transactions)
9. [Results](#9-results)
10. [SQL history](#10-sql-history)
11. [Configuration file](#11-configuration-file)
12. [Files and locations](#12-files-and-locations)
13. [Key reference](#13-key-reference)
14. [Troubleshooting](#14-troubleshooting)
15. [Development and tests](#15-development-and-tests)

---

## 1. Requirements

| Requirement | Notes |
|---|---|
| Go 1.26+ | to build |
| C compiler (gcc) | godror uses cgo |
| Oracle Instant Client | any recent version (tested with 23.26); the database can be 11.2 or newer (tested with 19c) |
| `ORACLE_HOME` | must point at the Instant Client directory, e.g. `/usr/local/oracle` |
| `TNS_ADMIN` | optional; directory containing `tnsnames.ora` if you use TNS aliases |

sqlcli loads the Oracle client library from `ORACLE_HOME` (or from
`oracle.lib_dir` in the config file), so `LD_LIBRARY_PATH` does **not** need
to be set.

## 2. Building

```sh
cd sqlcli
make build          # same as: go build -o sqlcli ./cmd/sqlcli
```

The result is a single binary, `./sqlcli`. Copy it anywhere on your `PATH`.

## 3. Starting sqlcli

```sh
sqlcli                      # start; choose a connection in the left pane
sqlcli -c ora               # connect to the saved connection "ora" at start
sqlcli -config ./my.toml    # use a different configuration file
sqlcli -l                   # list the saved sessions and exit
sqlcli -s "daily reports"   # start with a saved session (all its SQL tabs)
```

| Option | Meaning |
|---|---|
| `-c NAME` | Connect to the saved connection `NAME` (case-insensitive) on startup. sqlcli exits with an error if no such connection exists. |
| `-config PATH` | Configuration file to use. Default: `~/.config/sqlcli/config.toml`. |
| `-l` | List the saved sessions (name, time saved, number of tabs, connections) and exit. |
| `-s NAME` | Start with the saved session `NAME`: its tabs, their SQL and connections (see [Sessions](#sessions-f10)). The connections panel starts hidden; F2 shows it. sqlcli exits with an error if no such session exists. Can't be combined with `-c`. |
| `-version` | Print the version (`sqlcli 0.1`) and exit. |

On first start, if the configuration file does not exist, sqlcli creates it
with default settings and an `example` connection.

To quit, press **^K X** (or **Ctrl-C** when no query is running). If a
transaction is open, sqlcli asks for confirmation and then rolls it back.

## 4. The screen

```
╭─ Connections ──╮╭─ SQL · ora ──────────────────────────────────[x]─╮
│● ora           ││ 1 select level n, 'row '||level label           │
│  test (ro)     ││ 2   from dual connect by level <= 25            │
│                ││ 3                                               │
│                │╰─ ✓ 25 rows · 12ms · at 14:02:31 ────────────────╯
│                │╭─ Results  row 1/25  col 1/2 ────────────────────╮
│                ││   │ N │ LABEL                                   │
│                ││───┼───┼────────                                 │
│                ││ 1 │ 1 │ row 1                                   │
│                ││ 2 │ 2 │ row 2                                   │
╰────────────────╯╰─────────────────────────────────────────────────╯
 ora  TX  │ Ln 2, Col 7 │ 25 row(s) selected.
 F1 help  F2 conns  F3 results  F4 sql  F5 run  F7 run all  F6 history  F8 commit  F9 rollback  F10 sessions …   sqlcli 0.1
```

* **Tab bar** (top, only with two or more tabs): one entry per SQL tab,
  showing its connection, `TX` for an open transaction and `…` while a
  statement runs. See [Tabs](#tabs-several-sql-panels-and-connections).
* **Connections** (left): saved connections. `●` marks the active tab's
  connection, `○` one open in another tab, `(ro)` a read-only connection. **F2** hides or shows this panel; when
  hidden, the editor and results use the full width.
* **SQL** (top right): the editor. The title names the active connection
  (`SQL · ora`), adding `(read-only)` for read-only connections, or says
  `not connected`. It shows `^K-` while a ^K command is waiting for its
  second key. The **bottom border** shows how the tab's last statement went:

  | Footer | Meaning |
  |---|---|
  | `● running 3.2s` | still running (the timer counts up); also `● fetching 12000 rows`, `● exporting` |
  | `✓ 25 rows · 12ms · at 14:02:31` | query finished; the row count follows later fetches (`1000+ rows` → `2500 rows`) |
  | `✓ 3 row(s) updated`, `✓ PL/SQL OK`, `✓ 4 statements, last: 25 rows` | DML, PL/SQL, script (F7) |
  | `✗ ORA-00942: table or view does not exist · 8ms` | failed |
  | `✗ timed out after 30s (query.timeout_seconds)` | stopped by the timeout setting |
  | `■ cancelled after 2.1s` | cancelled with Ctrl-C |

  Each tab keeps its own footer.
* **Results** (bottom right): query results, messages and errors. The title
  shows the selected row and the first visible column.
* **Status bar**: active connection, a **TX** marker when a transaction is
  open, the cursor position, and the last message (red for errors). Running
  times are shown in the SQL panel's bottom border, not here.
* **Bottom line**: key hints, or the input line when sqlcli asks you something
  (file name, search text, confirmation, ...).

The focused pane has a blue border. Switch panes with:

| Key | Pane |
|---|---|
| F2 | show the connections panel (focused) / hide it |
| F3 | show the results panel (focused) / hide it |
| F4 | show the SQL panel (focused) / hide it |
| Esc or Tab (in Connections/Results) | back to the editor |

You can also click a pane to focus it (see [Mouse](#mouse)).

The SQL and results panels are never hidden together: hiding one while the
other is hidden brings the other back (so F4 with the results hidden switches
to "results only"). Going back to the editor (Esc in the results, connecting,
inserting from history) shows a hidden SQL panel again.

### Mouse

With `ui.mouse = true` (the default):

| Click | Action |
|---|---|
| a pane | focus it |
| in the SQL editor | move the cursor there |
| a row in the results grid | select it |
| a connection | select it; **double-click** connects the current tab |
| a tab in the tab bar | switch to that tab |
| `[x]` on the SQL panel's top border | close the tab (on the last tab sqlcli asks before quitting) |
| mouse wheel | scroll the pane under the pointer |

Hold Shift while dragging to select text for copying (most terminals).

## 5. Connections

Press **F2** to show the connections panel; it opens focused. F2 again
hides it. When the panel is already visible, click it to focus it, or press
F2 twice.

| Key | Action |
|---|---|
| ↑ / ↓ (also ^P / ^N, k / j) | select |
| Enter | connect to the selected connection |
| a | add a new connection |
| e | edit the selected connection ([Editing](#editing-and-viewing-a-connection)) |
| v | view the selected connection's settings |
| d | delete the selected connection (asks for confirmation) |
| Esc / Tab | back to the editor |

Adding (**a**) asks, on the bottom line, for:

1. **Connection name**: any label, used with `-c`.
2. **User**: the database user.
3. **Connect string**: one of
   * a TNS alias from `tnsnames.ora`, e.g. `net.ora.sk`
   * Easy Connect, e.g. `dbhost:1521/ORCLPDB1`
   * a full descriptor, e.g. `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=dbhost)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=orcl)))`
4. **Password**: typed as `*`. Leave it empty to read the password from an
   environment variable instead, and then enter the variable name
   (default `SQLCLI_PASSWORD`).

Added and deleted connections are saved to the configuration file at once.
Press Esc at any question to cancel.

### Editing and viewing a connection

**e** edits the selected connection. sqlcli asks, on the bottom line, for:

1. **Connection name**, **user** and **connect string**: each pre-filled with
   the current value; press Enter to keep it.
2. **New password**: hidden as you type. Leave it empty to keep the current
   password setting. A typed password is stored in the config file and
   replaces a `password_env` setting.
3. **Password env var** (only when you left the password empty): pre-filled
   with the current variable; clear it for none.
4. **Read-only?** and **Connect as SYSDBA?**: `yes` or `no`, pre-filled.

Nothing is saved until the last answer; Esc at any question cancels without
changes. A name another connection already uses is refused. Tabs that are
connected keep their old settings until you reconnect them (Enter).

**v** shows the selected connection's settings: name, user, connect string
(and whether it is a TNS alias, Easy Connect string or descriptor), where
the password comes from (never the password itself), read-only, SYSDBA,
which tabs have it open, and the config file. Any key closes it.

Switching connections is refused while a transaction is open: commit (F8) or
roll back (F9) first. Read-only connections (`read_only = true`, set in the
config file) allow only `SELECT`, `WITH`, `EXPLAIN` and `DESC`.

**Passwords**: a password typed in the wizard is stored in plain text in the
configuration file. sqlcli writes that file with mode `0600`, readable only by
you. Using `password_env` keeps passwords out of the file entirely.

### Tabs: several SQL panels and connections

Each tab is a separate SQL panel with its own editor, results, status and
**its own Oracle session**. Tabs can use the same connection or different
ones, all at the same time.

| Key | Action |
|---|---|
| ^K O | open a new tab on the same connection as the current tab |
| ^K N / ^K P | next / previous tab |
| Ctrl-PgDn / Ctrl-PgUp | next / previous tab (works in every pane) |
| Alt-1 … Alt-9 | go to tab 1 … 9 |
| ^K Q | close the tab (on the last tab: quit) |
| select in the connections panel, Enter | connect the current tab to another connection |

```
 1 ora TX  │ 2 ora …  │ 3 report
╭─ Connections ──╮╭─ SQL 2 · ora ───────────────────╮
│● ora           ││ select count(*) from big_table  │
│○ report        ││                                 │
```

* Separate sessions mean separate transactions: a change made in tab 1 is
  invisible in tab 2 until you commit it, and F8 / F9 only affect the
  current tab.
* A long statement in one tab doesn't block the others. Switch to another
  tab and keep working; the result appears in its own tab when it finishes.
* Ctrl-C cancels the statement in the current tab only.
* Closing a tab with an open transaction asks first and rolls it back.
  Quitting lists every open transaction and running statement before it
  asks.

## 6. The SQL editor (JOE keys)

The editor uses the keybindings of JOE. Arrow keys, Home, End, PgUp, PgDn,
Delete and Backspace work as usual. Text you type is inserted at the cursor.
Pasting from the terminal (bracketed paste) inserts the whole text at once.

### Moving

| Key | Action |
|---|---|
| ^B / ^F | character left / right |
| ^P / ^N | line up / down |
| ^Z / ^X | word left / right (also Ctrl-← / Ctrl-→) |
| ^A / ^E | beginning / end of line |
| Home | first non-blank character; press again for column 1 |
| ^U / ^V | page up / down |
| ^K U / ^K V | top / end of buffer (also Ctrl-Home / Ctrl-End) |
| ^K L | go to line (asks for the number) |

### Deleting

| Key | Action |
|---|---|
| ^D | delete character under cursor |
| Backspace | delete character left of cursor |
| ^Y | delete the whole line |
| ^W | delete word to the right |
| ^O | delete word to the left |
| ^J | delete to end of line |

### Undo

| Key | Action |
|---|---|
| ^_ | undo (typed text is undone one word at a time) |
| ^^ | redo |

Terminals send these keys differently. sqlcli also accepts Ctrl-/ and Ctrl-7
for undo, and Ctrl-6 for redo.

### Blocks

A block is the text between two marks. Set the marks, then act on the block.
The block is highlighted.

| Key | Action |
|---|---|
| ^K B | mark block begin at the cursor |
| ^K K | mark block end at the cursor |
| ^K C | copy the block to the cursor |
| ^K M | move the block to the cursor |
| ^K Y | delete the block |
| ^K W | write the block to a file |
| ^K A | clear the block marks (sqlcli-specific) |

When a block is marked, **F5 runs the block** instead of the statement under
the cursor (see [Running SQL](#7-running-sql)). Clear it with ^K A to return
to running single statements.

### Search and files

| Key | Action |
|---|---|
| ^K F | find (case-insensitive, wraps at end of buffer) |
| ^L | find next |
| ^K D | save the whole buffer to a file |
| ^K R | insert a file at the cursor |

File names may start with `~/`.

### Other

| Key | Action |
|---|---|
| Enter | new line, keeping the indentation (`editor.auto_indent`) |
| Tab | insert spaces to the next tab stop (`editor.tab_width`) |
| ^K H or F1 | help screen |
| ^K X | quit sqlcli |
| ^K O / ^K N / ^K P / ^K Q | new / next / previous / close tab ([Tabs](#tabs-several-sql-panels-and-connections)) |
| Ctrl-C after ^K | cancel the pending ^K command |

^K commands also accept the Ctrl form of the second key, as JOE does:
^K ^B works like ^K B.

The window commands ^K X (quit), ^K Q (close tab), ^K O (new tab),
^K N / ^K P (next / previous tab), ^K E (sessions) and ^K H (help) also work in the
connections and results panes. The status bar shows `^K-` while sqlcli waits
for the second key.

### Code completion

With `editor.completion = true` (the default) the editor suggests what to
type next:

* The list **opens by itself** once you have typed two letters of a word and
  something matches, and right after a `.` (`e.` for an alias). **Ctrl-Space**
  opens it at any time, also on an empty position.
* It offers the columns of the tables in the current statement first, then
  tables, views and synonyms of your schema, then other objects (sequences,
  packages, procedures …), SQL keywords, functions, data types and public
  synonyms (`DUAL`, `DBMS_OUTPUT` …).
* After `alias.` or `table.` it lists only that table's columns. Aliases are
  read from the statement's `FROM` and `JOIN` clauses (`from employees e`,
  `join hr.jobs as j`).
* Object names are loaded in the background when a tab connects (once per
  connection). Columns are loaded from the data dictionary the first time
  they are needed, following synonyms. After `CREATE`, `DROP`, `ALTER` or
  `RENAME` the names are loaded again.
* The inserted text follows how you type: after `emp` you get `employees`,
  after `EMP` you get `EMPLOYEES`. Names that need quotes (`"MixedCase"`) get
  them.

| Key | Action |
|---|---|
| typing | refines the list |
| ↑ / ↓ (^P / ^N), PgUp / PgDn | choose an entry |
| Tab | insert the chosen entry |
| Enter | insert the chosen entry; if the word is already complete, start a new line as usual |
| Esc | close the list |
| Ctrl-Space | open the list now |

Set `completion = false` (or `"no"`) to turn it off; Tab then indents as usual.

### Syntax highlighting

With `editor.syntax_highlight = true` (the default) the editor colours SQL and
PL/SQL as you type:

| Colour | What |
|---|---|
| bold blue | keywords (`SELECT`, `FROM`, `BEGIN`, `LOOP` …) |
| cyan | data types (`NUMBER`, `VARCHAR2`, `DATE` …) |
| yellow | built-in functions when followed by `(` (`COUNT(`, `TO_CHAR(`, `NVL(` …) |
| green | strings, including `N'…'` and Oracle's `q'[…]'` quoting |
| pink | numbers (`42`, `1.5`, `1e3`) |
| grey italic | comments, `-- …` and `/* … */` |
| orange | bind variables (`:id`, `:1`) |

Comments and strings that span several lines are coloured correctly. Names
after a dot (`t.from`, `pkg.select`) and quoted identifiers (`"SELECT"`) are
not treated as keywords. A marked block is shown in the block colour.

Set `syntax_highlight = false` (or `"no"`) to turn it off. The option also
accepts `"yes"` / `"no"` and `"on"` / `"off"`.

## 7. Running SQL

| Key | Runs |
|---|---|
| **F5** (also Ctrl-Enter / Alt-Enter if your terminal sends them) | the **marked block**, or else the **one statement the cursor is in** |
| **F7** | the **whole buffer** as a script |
| **Ctrl-C** | cancel the running statement |

Statements run one after another. If one fails, the rest are skipped. The
results pane shows the last query result, or the messages of all statements.

### How statements are separated

sqlcli follows SQL\*Plus conventions:

* A plain SQL statement ends with a `;`, at a blank line, or at a line holding
  only `/`. The `;` may be anywhere on a line, so one line can hold several
  statements. A `;` inside a string (`'a;b'`) or a comment does not end a
  statement.
* A **PL/SQL block** ends **only** with a line holding only `/`. PL/SQL blocks
  start with `DECLARE`, `BEGIN`, `CREATE [OR REPLACE] PROCEDURE | FUNCTION
  | PACKAGE | TRIGGER | TYPE`, or `WITH FUNCTION` / `WITH PROCEDURE` (a query
  with inline PL/SQL). This lets them contain blank lines and `;`.

**Comments** (`-- …` and `/* … */`) before and after a statement are
skipped: they are not sent to Oracle and not saved in the history. A part of
the buffer that holds only comments is not a statement at all, so commented-out
SQL never runs and never breaks an F7 script. Comments inside a statement stay,
including optimizer hints (`/*+ … */`).

**F5 runs exactly one statement: the one the cursor is in.** The position just
after a statement's closing `;` still counts as inside it. If the cursor is on
a blank line, the statement above it runs.

The trailing `;` or `/` is removed before the statement is sent to Oracle.
PL/SQL blocks keep their final `;`, which Oracle requires.

Example buffer:

```sql
select * from netlist;
select * from nsm;
select 1 from dual; select 2 from dual;

update emp set sal = sal * 1.1
 where empno = 7369

begin
  dbms_stats.gather_table_stats(user, 'EMP');

  dbms_output.put_line('done');
end;
/
```

* Cursor on line 1, F5: runs only `select * from netlist`.
* Cursor on line 2, F5: runs only `select * from nsm`.
* Line 3: the cursor's position on the line decides which `select` runs.
* Cursor on either line of the `update`, F5: runs the update. It has no `;`;
  the blank line ends it.
* Cursor anywhere in the PL/SQL block, including its blank line, F5: runs the
  block.
* F7: runs all six statements in order.

To run several statements with one key, mark them as a block
(^K B ... ^K K) and press F5, or press F7 for the whole buffer.

## 8. Transactions

By default (`query.auto_commit = false`) changes are **not** committed
automatically:

* `INSERT`, `UPDATE`, `DELETE`, `MERGE`, PL/SQL blocks and `CALL` start a
  transaction if none is open. The status bar shows **TX**.
* **F8** commits, **F9** rolls back. You can also run `COMMIT` or `ROLLBACK`
  as statements.
* DDL (`CREATE`, `ALTER`, `DROP`, `TRUNCATE`, `GRANT`, ...) makes Oracle
  **commit the open transaction** implicitly. sqlcli reports this in the
  message and clears the TX marker.
* Quitting with an open transaction asks for confirmation and rolls it back.
  Switching connections is refused until you commit or roll back.

With `auto_commit = true`, every statement is committed as soon as it
succeeds, and TX never appears.

## 9. Results

Press **F3** to show the results panel; it opens focused. F3 again hides it,
and the SQL panel gets the whole height. When the panel is visible, click it
to focus it, or press F3 twice.

| Key | Action |
|---|---|
| ↑ / ↓ (^P / ^N, k / j) | select row |
| PgUp / PgDn (^U / ^V) | page |
| mouse wheel | scroll three rows (works without focusing the pane) |
| Home (^A, g) | first row |
| End (^E, G) | last row; **fetches all remaining rows first** |
| ← / → (^B / ^F, h / l) | scroll one column left / right |
| Enter | show the selected row as a record ([Record view](#record-view)) |
| o | switch between the grid and DBMS_OUTPUT (when a query produced both) |
| e | export the result to a .csv, .tsv or .json file ([Exporting](#exporting)) |
| Esc / Tab | back to the editor |

### Record view

**Enter** on a grid row shows that row as a record: one line per column,
long values in full, NULL as `(null)`.

| Key | Action |
|---|---|
| ↑ / ↓ (^P / ^N) | previous / next record |
| Home (^A) | first record |
| End (^E) | last record; fetches all remaining rows first, like End in the grid |
| PgUp / PgDn (^U / ^V) | back / forward by `ui.record_page` records (default 10) |
| Shift-↑ / Shift-↓, Shift-PgUp / Shift-PgDn, mouse wheel | scroll a record that is taller than the screen |
| Ctrl-C | stop fetching |
| any other key | close the record view (the grid keeps the record selected) |

Stepping past the last fetched row with ↓ or PgDn fetches the next batch;
PgDn still lands exactly `record_page` records further.

### Fetching rows

A query first fetches `query.max_rows` rows (default 1000). If it has more,
the query stays open on the server, the status says
`1000 row(s) fetched, more available.`, and the row count in the title gets a
`+` (`row 1/1000+`).

* **Scrolling** (arrows, PgDn, mouse wheel) to within a page of the last
  fetched row fetches the next `max_rows` rows.
* **End** fetches all remaining rows in batches of 5000, showing
  `Fetching… N rows` in the status bar, then selects the last row.
* **Ctrl-C** stops a fetch. The rows fetched so far stay in the grid; the
  rest are discarded.
* To always get every row, set `query.fetch_all = true`. Every query then
  fetches all its rows right away, and Ctrl-C stops it in the same way.

Running another statement, commit (F8) or rollback (F9) closes the open query.
The rows already in the grid stay; no more can be fetched.

Every fetched row is held in memory. Fetching millions of wide rows can use
gigabytes, so prefer a `WHERE` clause or watch the counter and press Ctrl-C.

### Exporting

Press **e** in the results pane to save the result to a file. sqlcli asks for
the file name (it offers `result.csv`, then the last name you used); the
extension picks the format:

| Extension | Format |
|---|---|
| `.csv` | comma-separated, header row, quoted where needed (RFC 4180) |
| `.tsv` | tab-separated, header row |
| `.json` | an array of objects, one per row, keys in column order |

* **All rows are exported**, not only those on screen: if the query has
  more rows on the server, sqlcli fetches them first (`Fetching all rows for
  export… N`). Ctrl-C stops that and cancels the export.
* NULL is an empty field in CSV/TSV and `null` in JSON. `NUMBER` columns are
  JSON numbers, written exactly as Oracle returned them; everything else is a
  string, dates as `YYYY-MM-DD HH:MI:SS`.
* Duplicate column names (`select a.id, b.id ...`) become `ID` and `ID_2`
  in JSON.
* An existing file is only replaced after you confirm. The file is written
  to a temporary name first, so a failed export never leaves half a file.

### Display rules

* NULL is shown as a grey `(null)`. In Oracle an empty string is NULL.
* Dates: `YYYY-MM-DD HH:MI:SS`. Timestamps with fractional seconds add
  microseconds.
* RAW values are shown as hex, and CLOBs as their text.
* Columns are as wide as their widest value, capped at 50 characters. Longer
  values are cut with `…`. Use Enter (record view) to see the full value.
  Line breaks inside values are shown as `⏎`.

For DML and DDL the pane shows messages like `3 row(s) updated.`, with the
elapsed time. Errors are shown in red, starting with the Oracle error code.

### DBMS_OUTPUT

Output from `DBMS_OUTPUT.PUT_LINE` is shown in the results pane, below the
statement's message, like `SET SERVEROUTPUT ON` in SQL*Plus:

```
PL/SQL procedure successfully completed.  (3ms)
── DBMS_OUTPUT (2 line(s)) ──
Hello from PL/SQL
second line
```

* It is collected after every statement: PL/SQL blocks, `CALL`, DML that
  fires triggers, and queries calling functions that print.
* If a block fails, the lines it printed before the error are shown below
  the error message.
* F7 (whole script) shows the output of all statements together.
* Long output scrolls: arrows, PgUp/PgDn, Home/End and the mouse wheel move
  through it, and ← / → scroll long lines sideways. The title shows
  `lines 1-15/202`.
* When a query produced both rows and output, the grid is shown and the
  title says `o: DBMS_OUTPUT`. Press **o** in the results pane to switch.
* There is no size limit (`DBMS_OUTPUT.ENABLE(NULL)`). Set
  `query.dbms_output = false` to turn collection off.

## 10. SQL history

Every statement you run is saved, whether it succeeded or failed, with:
connection name, SQL text, start time, duration, row count and error message.

Press **F6** to open the history browser:

| Key | Action |
|---|---|
| type text | filter: show entries whose SQL contains the text (case-insensitive) |
| Backspace / ^Y | delete last filter character / clear filter |
| ↑ / ↓, PgUp / PgDn | select |
| Enter | insert the selected SQL at the editor cursor |
| ^R | replace the whole editor buffer with the selected SQL |
| Del | delete the entry from history |
| Esc / F6 | close |

Failed statements are marked `✗`. The lower part shows the full SQL of the
selected entry, its duration and row count, or its error.

The browser shows the newest `history.max_entries` entries (default 500). The
database itself is never trimmed automatically. It is a normal SQLite file,
so you can query it directly:

```sh
sqlite3 ~/.local/state/sqlcli/history.db \
  "select datetime(executed_at/1000,'unixepoch','localtime'), connection, sql
     from history where error = '' order by id desc limit 20"
```

Table layout:

| Column | Type | Content |
|---|---|---|
| id | INTEGER | auto-increment key |
| connection | TEXT | connection name |
| sql | TEXT | statement as sent to Oracle |
| executed_at | INTEGER | start time, Unix milliseconds |
| duration_ms | INTEGER | elapsed time |
| rows | INTEGER | rows fetched or affected |
| error | TEXT | error message, empty on success |

### Sessions (F10)

A session is the set of open SQL tabs: for each tab its connection and its
SQL text, plus which tab was active. Sessions are stored
in the same SQLite file as the history.

Press **F10** or **^K E** to open the session window, or **Ctrl-S** to save
the open tabs straight away (sqlcli asks for the name). It lists saved sessions, newest
first, with the time they were saved, the number of tabs and their
connections.

| Key | Action |
|---|---|
| s / Ctrl-S | save the open tabs as a session (asks for a name; offers the current session's name or the date and time; asks before overwriting) |
| Enter | load the selected session: it replaces the open tabs, reconnects each one and puts every tab's cursor at the top-left corner |
| d / Del | delete the selected session (asks first) |
| Esc / F10 | close the window |

Loading closes the current tabs' sessions. If a transaction is open, sqlcli
asks first, because it will be rolled back; while a statement is running,
loading is refused. A tab whose connection is no longer in the config file
opens unconnected, and the status bar names the missing connection.

Some terminals use F10 themselves (e.g. to open their menu); turn that off in
the terminal's settings if F10 doesn't reach sqlcli.

## 11. Configuration file

Location: `~/.config/sqlcli/config.toml` (or `$XDG_CONFIG_HOME/sqlcli/config.toml`,
or the path given with `-config`). It is plain TOML, so you can edit it in
any text editor. Changes take effect the next time you start sqlcli.

Complete example with all settings:

```toml
[oracle]
  # Instant Client directory. Empty = use $ORACLE_HOME.
  lib_dir = "/usr/local/oracle"

[editor]
  tab_width = 4          # spaces inserted by Tab, also used to expand pasted tabs
  auto_indent = true     # Enter keeps the indentation of the current line
  syntax_highlight = true  # colour SQL; also "yes" / "no"
  completion = true      # code completion (Ctrl-Space, or while typing); also "yes" / "no"

[query]
  max_rows = 1000        # rows per fetch; more are fetched when you scroll or press End
  fetch_all = false      # true = fetch all rows of every query right away
  timeout_seconds = 0    # cancel statements after N seconds; 0 = never
  auto_commit = false    # true = commit every statement immediately
  dbms_output = true     # show DBMS_OUTPUT in the results pane

[ui]
  mouse = true           # mouse wheel scrolls; hold Shift to select text with the mouse
  show_connections = true  # connections panel visible at start (F2 toggles it)
  record_page = 10       # records PgUp / PgDn move in the record view

[history]
  path = ""              # SQLite file; empty = ~/.local/state/sqlcli/history.db
  max_entries = 500      # entries listed in the history browser

[[connections]]
  name = "ora"
  user = "men"
  password_env = "SQLCLI_PASSWORD"   # read password from this variable
  connect_string = "net.ora.sk"      # TNS alias

[[connections]]
  name = "prod-readonly"
  user = "report"
  password = "secret"                # stored in plain text
  connect_string = "dbhost:1521/PROD"
  read_only = true                   # only SELECT / WITH / EXPLAIN / DESC

[[connections]]
  name = "admin"
  user = "sys"
  password_env = "ORA_SYS_PW"
  connect_string = "dbhost:1521/ORCLPDB1"
  sysdba = true                      # connect AS SYSDBA
```

### Connection settings

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | label shown in the list and used with `-c` |
| `user` | yes | database user |
| `connect_string` | yes | TNS alias, Easy Connect string, or full descriptor |
| `password` | no | password in plain text |
| `password_env` | no | environment variable holding the password; used instead of `password` when the variable is set |
| `read_only` | no | refuse everything except queries |
| `sysdba` | no | connect with the SYSDBA role |

## 12. Files and locations

| File | Default location | Override |
|---|---|---|
| configuration | `~/.config/sqlcli/config.toml` | `-config`, `$XDG_CONFIG_HOME` |
| SQL history | `~/.local/state/sqlcli/history.db` | `history.path`, `$XDG_STATE_HOME` |
| TNS aliases | `$TNS_ADMIN/tnsnames.ora` | Oracle standard |

## 13. Key reference

### Global

| Key | Action |
|---|---|
| F1 | help |
| F2 | show / hide connections panel |
| F3 | show / hide results panel |
| F4 | show / hide SQL panel |
| F5 | run block or statement under cursor |
| F7 | run whole buffer |
| F6 | history browser |
| F10 / ^K E | sessions: load / save / delete |
| Ctrl-S | save the open tabs as a session |
| F8 / F9 | commit / rollback |
| Ctrl-C | cancel running statement; otherwise quit |
| ^K X | quit |
| ^K O | new tab on the same connection |
| ^K N / ^K P, Ctrl-PgDn / Ctrl-PgUp | next / previous tab |
| Alt-1 … Alt-9 | go to tab |
| ^K Q | close tab (last tab: quit) |
| e (results pane) | export result to .csv / .tsv / .json |

### Editor

| Key | Action | Key | Action |
|---|---|---|---|
| ^B ^F ^P ^N | move | ^D | delete char |
| ^A ^E | line start / end | ^Y | delete line |
| ^Z ^X | word left / right | ^W ^O | delete word right / left |
| ^U ^V | page up / down | ^J | delete to end of line |
| ^K U ^K V | top / end of buffer | ^_ ^^ | undo / redo |
| ^K L | go to line | ^K F, ^L | find, find next |
| ^K B ^K K | block begin / end | ^K C ^K M ^K Y | copy / move / delete block |
| ^K A | clear block | ^K W | write block to file |
| ^K D | save buffer to file | ^K R | insert file |
| Ctrl-Space | code completion | Tab / Enter | accept completion |

### Input line (questions on the bottom line)

| Key | Action |
|---|---|
| Enter | accept |
| Esc, Ctrl-C, ^G | cancel |
| ← → ^B ^F, Home End ^A ^E | move |
| Backspace, ^D, ^Y | delete left / delete right / clear |

## 14. Troubleshooting

**`DPI-1047: Cannot locate a 64-bit Oracle Client library`**
Instant Client was not found. Set `ORACLE_HOME` to the Instant Client
directory (the one containing `libclntsh.so`) or set `oracle.lib_dir` in the
configuration file.

**`ORA-12154: could not resolve the connect identifier`**
The TNS alias is unknown. Check that `TNS_ADMIN` points to the directory with
`tnsnames.ora` and that the alias is spelled correctly, or use an Easy Connect
string (`host:port/service`) instead.

**`ORA-12541: Cannot connect. No listener`**
The host or port is wrong, or the database listener is down.

**`ORA-01017: invalid username/password`**
Wrong credentials. Oracle passwords are case-sensitive. If you use
`password_env`, check that the variable is exported in the shell that starts
sqlcli. Repeated failures can lock the account.

**A key does nothing (e.g. ^_ undo or F-keys)**
Some terminals or tmux configurations intercept or translate keys. Try the
alternatives listed in [section 6](#6-the-sql-editor-joe-keys), or check your
terminal's key settings. With tmux, `set -g extended-keys on` helps with
Ctrl combinations.

**A statement takes too long**
Press Ctrl-C to cancel it, or set `query.timeout_seconds`.

**I can't select text with the mouse**
sqlcli uses the mouse wheel, so the terminal passes mouse clicks to it. Hold
Shift while selecting (most terminals), or set `ui.mouse = false`.

**A PL/SQL block fails, or F5 runs too much**
Finish every PL/SQL block with a line holding only `/`. Until that line, sqlcli
treats everything after `BEGIN` or `DECLARE` as part of the block.

## 15. Development and tests

```
cmd/sqlcli/          entry point, command-line options
internal/config/     TOML configuration
internal/db/         Oracle access (godror), statement splitting
internal/editor/     JOE-style editor
internal/export/     CSV / TSV / JSON export
internal/history/    SQLite history store
internal/ui/         Bubble Tea user interface
```

```sh
make build        # build ./sqlcli
make test         # unit + integration tests (reads test.env if present)
make test-unit    # unit tests only
```

Integration tests run against a real database when these variables are set.
`make test` reads them from `test.env`, which is git-ignored:

```sh
SQLCLI_TEST_CONNECT=net.ora.sk
SQLCLI_TEST_USER=men
SQLCLI_TEST_PASSWORD=...
```

The integration tests change nothing permanently. Write tests use an Oracle
private temporary table (`ORA$PTT_...`), which exists only in the test session.
