# Changelog

All notable changes to sqlcli. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Editor**
  - Code completion for tables, columns and package members (`editor.completion`).
  - Search and replace: `^R`, or `^K F` with options `r` (replace), `c` (match case), `a` (all without asking).
  - `^K D` saves the SQL panel to a file, `^K I` loads one; Tab completes file names.
  - `^K C` / `^K M` copy and move blocks between SQL panels.
- **Running SQL**
  - Package spec and body in one panel; compilation errors reported.
  - PL/SQL units end at their last `END` without `/`.
  - `q'[…]'` strings.
  - Comments around statements are skipped.
  - F5 does nothing inside a comment, and runs the statement before a trailing `--` comment.
  - `CREATE JAVA SOURCE` ends only at `/`; its Java text is left alone.
- **SQL\*Plus commands**: `DESC`, `EXEC`, `SHOW ERRORS` / `SHOW USER`, `SET SERVEROUTPUT`, `DEFINE` / `&variables`, `ACCEPT`, `PROMPT`, `@` / `@@` / `START`, `SPOOL`, `REM` and others.
- **Object tree** below the connections:
  - Shows every object type of the schema, Java sources included.
  - Name filter (`/`), `x` removes the filter, `c` collapses the tree.
  - `Ctrl-D` opens the DDL in a new tab.
  - `s` opens `select *` in a new tab; `e` opens an editable `select rowid, …` in a new tab.
- **Panels**
  - F3 / F4 show or hide the results and SQL panels.
  - F12, Ctrl-arrows and Tab / Shift-Tab move between panels.
  - Key hints of the focused panel on the bottom line.
  - The tab bar scrolls when tabs don't fit.
- **Connections**: edit (`e`) and view (`v`) a connection; adding one asks for read-only and SYSDBA.
- **Sessions**
  - Save and load all SQL tabs (F10, `^K E`, `Ctrl-S`).
  - `-l` and `-s NAME` command-line options.
  - Session name shown top-left; saved on exit (`ui.save_session_on_exit`).
- **Editing data**
  - Edit cells (`u`), insert (`i`) and delete (`d`) rows by ROWID.
  - CLOB values (and, from the record view, any value) in a full-screen text editor, with search and replace and file save/load.
- **Results**
  - Copy cell / row (`c` / `C`) in the grid and the record view.
  - Select rows or cells with Shift + cursor keys, or with selection mode `v` / Space.
  - Export to `.xlsx` and `.xls`.
  - Double click on a row opens the record view.
  - Selected row colour (`ui.selection_color`).
- **Record view**
  - Field cursor: ↑↓ move between fields, ←→ between records.
  - Shift-←→ scroll long values sideways.
- **History**: run time of each statement in the history browser (F6).
- **Mouse**: drag to copy to the clipboard (OSC 52 and the system clipboard via `github.com/atotto/clipboard`).
- **Other**
  - Version on the key-hint line.
  - `README.md`.

### Changed

- F8 rolls back, F9 commits (they were the other way round).
- History: Enter adds the SQL at the end of the panel.
- The connections panel shows only connections; its keys moved to the bottom line.
- `-s` no longer hides the connections panel and object tree.
- In the object tree, `Ctrl-→` goes to the SQL editor; `Ctrl-←` from the right panels goes to the object tree.

### Fixed

- Record view shows values with CR LF line ends correctly; the CLOB editor keeps CR LF.
- Java source DDL keeps its line breaks (built from `USER_SOURCE`).

## [0.1] - 2026-11-03

First version.

- **Core**: terminal SQL client for Oracle (godror), TOML configuration and SQLite history of every statement.
- **SQL panel**:
  - SQL editor with JOE keybindings and syntax highlighting.
  - F5 runs the statement at the cursor or the marked block; F7 runs the whole panel.
- **Results**:
  - Rows fetched on scroll; End fetches all (`query.fetch_all`).
  - Record view, `DBMS_OUTPUT`, run time and status footer.
  - Export to CSV, TSV and JSON.
- **Layout**:
  - Several SQL tabs, each with its own Oracle session and connection.
  - Connections panel (F2); mouse support.
- **Documentation**: manual (`MANUAL.md`, `manual.html`).

[Unreleased]: https://github.com/magicqe/sqlcli/compare/v0.1...HEAD
[0.1]: https://github.com/magicqe/sqlcli/releases/tag/v0.1
