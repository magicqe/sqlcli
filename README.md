# sqlcli

A terminal SQL client for **Oracle**, written in Go. A split-screen TUI with
connections, an object browser, a SQL editor with **JOE** keybindings and a
results grid, configured with a TOML file and keeping every executed statement
in a searchable SQLite history.

```
 work │ 1 ora │ 2 ora TX │ 3 rep
╭─ Connections ──╮╭─ SQL · ora · report.sql ─────────────────────────────[x]─╮
│● ora           ││ 1 select e.ename, d.dname, e.sal                          │
│  rep           ││ 2   from emp e join dept d on d.deptno = e.deptno         │
╰────────────────╯│ 3  where e.sal > 2000;                                    │
╭─ Objects · ora ╮╰─ ✓ 6 rows · 12ms ─────────────────────────────────────────╯
│▾ Tables  4     │╭─ Results ─────────────────────────────── row 1/6  col 1/3 ╮
│    DEPT        ││   │ ENAME  │ DNAME      │ SAL                              │
│    EMP         ││───┼────────┼────────────┼──────                            │
│▸ Views  2      ││ 1 │ JONES  │ RESEARCH   │ 2975                             │
│▸ Packages  1   ││ 2 │ BLAKE  │ SALES      │ 2850                             │
╰────────────────╯╰───────────────────────────────────────────────────────────╯
 ora │ Ln 3, Col 22 │ 6 row(s) selected.
 F1 help  F2 conns  F3 results  F4 sql  F5 run  F6 history  F7 run all  F8 rollback …
```

## Features

- **Editor** – JOE keys (`^K B`/`^K K` blocks, `^Y`, `^_` undo, …), syntax
  highlighting, code completion for tables, columns and package members,
  search and replace, load/save files with Tab completion.
- **Running SQL** – `F5` runs the statement under the cursor (or the marked
  block), `F7` the whole buffer as a script. Statements end with `;` or `/`;
  PL/SQL blocks, packages and package bodies are recognised, comments skipped.
- **SQL\*Plus commands** – `DESC`, `EXEC`, `SHOW ERRORS`, `SET SERVEROUTPUT`,
  `DEFINE` / `&variables`, `ACCEPT`, `PROMPT`, `@file` / `START`, `SPOOL`, …
- **Results** – fetch on scroll (`End` fetches all), record view, `DBMS_OUTPUT`,
  compile errors, cell / row / block selection copied to the clipboard,
  export to **CSV, TSV, JSON, XLSX and XLS**.
- **Editing data** – edit cells (`u`), insert (`i`) and delete (`d`) rows by
  ROWID in the tab's transaction (`F9` commit, `F8` rollback); CLOB values in a
  full-screen text editor.
- **Object tree** – browse tables, views, packages, … with a name filter;
  open DDL (`DBMS_METADATA`), `select *` or an editable query in a new tab.
- **Tabs and sessions** – several SQL tabs, each with its own Oracle session
  and connection; save and restore all tabs as named sessions.
- **History** – every statement with its time, duration, row count and error,
  searchable (`F6`).
- **Mouse** – click panels and tabs, wheel scrolling, drag to copy.

## Requirements

| | |
|---|---|
| Go 1.26+ and a C compiler | to build ([godror](https://github.com/godror/godror) uses cgo) |
| [Oracle Instant Client](https://www.oracle.com/database/technologies/instant-client.html) | any recent version; databases 11.2 and newer |
| `ORACLE_HOME` | the Instant Client directory (or `oracle.lib_dir` in the config) |
| `TNS_ADMIN` | optional, for TNS aliases from `tnsnames.ora` |

`LD_LIBRARY_PATH` doesn't need to be set: sqlcli loads the client library from
`ORACLE_HOME`. On Linux, copying to the system clipboard uses `xclip`, `xsel`
or `wl-copy` when installed (OSC 52 terminal copy works without them).

## Build

```sh
git clone https://github.com/magicqe/sqlcli.git
cd sqlcli
make build            # → ./sqlcli
make build-windows    # → sqlcli.exe (64-bit Windows; needs mingw-w64: apt install gcc-mingw-w64-x86-64)
```

On Windows, `sqlcli.exe` needs the Oracle Instant Client for Windows (`ORACLE_HOME`
pointing at the directory with `oci.dll`); use Windows Terminal.

## Quick start

```sh
export ORACLE_HOME=/usr/local/oracle
./sqlcli              # press a in the connections panel to add a connection
./sqlcli -c ora       # connect to the saved connection "ora" at start
./sqlcli -s work      # open the saved session "work" (all its tabs)
./sqlcli -l           # list saved sessions
./sqlcli -version
```

Connections live in `~/.config/sqlcli/config.toml`; they can be added and
edited from the app (`a`, `e`) or by hand:

```toml
[[connections]]
  name = "ora"
  user = "scott"
  password_env = "ORA_PW"            # or: password = "..." (plain text)
  connect_string = "dbhost:1521/ORCLPDB1"   # Easy Connect, TNS alias or descriptor

[[connections]]
  name = "prod"
  user = "report"
  password_env = "PROD_PW"
  connect_string = "PROD"
  read_only = true                   # refuse DML and DDL
```

See the manual for all settings (`[editor]`, `[query]`, `[ui]`, `[history]`).

## Essential keys

| Key | Action |
|---|---|
| `F5` / `F7` | run statement at cursor / run all |
| `F8` / `F9` | rollback / commit |
| `F6` | SQL history |
| `F10`, `Ctrl-S` | sessions / save session |
| `F2` `F3` `F4` | show or hide connections, results, SQL panel |
| `F12`, `Ctrl-←→↑↓`, `Tab` | object tree / move between panels |
| `^K O` `^K N` `^K P` `^K Q` | new / next / previous / close tab |
| `^K F`, `^R` | find / search and replace |
| `^K D`, `^K I` | save / load the SQL panel to/from a file |
| `Enter` (results) | record view |
| `u` `i` `d` `e` (results) | edit cell / insert row / delete row / export |
| `c` `C`, `Shift`+arrows (results) | copy cell / row, select rows or cells |
| `F1` | help |
| `^K X` | quit |

## Documentation

- [MANUAL.md](MANUAL.md) – the full user manual
- [manual.html](manual.html) – the same as a web page
- [CHANGELOG.md](CHANGELOG.md) – changes in each version

## Development

```
cmd/sqlcli/        entry point, command-line options
internal/config/   TOML configuration
internal/db/       Oracle access (godror), statement splitting, SQL*Plus commands
internal/script/   SQL*Plus script expansion (DEFINE, @file, SPOOL, …)
internal/editor/   JOE-style editor, highlighting, completion popup
internal/complete/ code completion
internal/export/   CSV / TSV / JSON / XLSX / XLS export
internal/history/  SQLite history and sessions
internal/ui/       Bubble Tea user interface
```

```sh
make test         # unit tests + Oracle integration tests (reads test.env)
make test-unit    # unit tests only
```

Integration tests run when `SQLCLI_TEST_CONNECT`, `SQLCLI_TEST_USER` and
`SQLCLI_TEST_PASSWORD` are set (e.g. in the git-ignored `test.env`). They
change nothing permanently.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss),
[godror](https://github.com/godror/godror),
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite),
[BurntSushi/toml](https://github.com/BurntSushi/toml),
[excelize](https://github.com/xuri/excelize) and
[atotto/clipboard](https://github.com/atotto/clipboard).
