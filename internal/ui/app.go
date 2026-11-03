// Package ui is the Bubble Tea front end of sqlcli.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
	"sqlcli/internal/editor"
	"sqlcli/internal/export"
	"sqlcli/internal/history"
	"sqlcli/internal/version"
)

type focus int

const (
	focusEditor focus = iota
	focusResults
	focusConns
)

type mode int

const (
	modeNormal mode = iota
	modePrompt
	modeHistory
	modeHelp
	modeRecord
)

type prompt struct {
	label    string
	input    []rune
	cur      int
	mask     bool
	onSubmit func(string) tea.Cmd
}

// tab is one SQL panel: an editor, its results and its own Oracle session.
type tab struct {
	id   int
	conn *db.Conn
	ed   *editor.Editor
	res  resultView

	connecting   bool
	connectingTo string // name of the connection being opened

	status    string
	statusErr bool

	running  bool
	cancel   context.CancelFunc
	runStart time.Time

	fetching bool // running is a FetchMore of the current result
	fetchAll bool // keep fetching batches until the cursor is exhausted
	jumpEnd  bool // select the last row when the fetch-all finishes

	afterFetch func() tea.Cmd // runs when a fetch-all completes (export)
	pendingRow int            // row to select once enough rows are fetched
	hasPending bool

	// last statement run, shown at the bottom of the SQL panel
	runState   runState
	runElapsed time.Duration
	runAt      time.Time // when it finished
	runSummary string    // e.g. "3 rows updated", "ORA-00942: ..."
	runIsQuery bool      // summary is the live row count of the result
	exporting  bool
}

type runState int

const (
	runNone runState = iota
	runOK
	runFailed
	runCancelled
)

type Model struct {
	cfg  *config.Config
	hist *history.Store

	*tab             // the active tab; its fields are used as m.ed, m.conn, ...
	tabs      []*tab // all tabs, in display order
	nextTabID int
	inBg      bool // handling a message for a tab that is not active

	w, h  int
	focus focus
	mode  mode

	prompt     prompt
	connSel    int
	showConns  bool   // connections panel visible (F2)
	lastExport string // file name offered by the next export

	helpTop      int       // first visible line of the help screen
	recTop       int       // first visible line of the record view
	kPrefix      bool      // ^K pressed outside the editor
	lastClickAt  time.Time // for double-click on a connection
	lastClickRow int

	histEntries []history.Entry
	histSel     int
	histFilter  []rune

	quitting bool
}

// New builds the UI model. initialConn, if non-empty, is connected at start.
func New(cfg *config.Config, hist *history.Store, initialConn string) *Model {
	m := &Model{cfg: cfg, hist: hist, showConns: cfg.UI.ShowConnections}
	m.tab = m.newTab()
	for i, c := range cfg.Connections {
		if strings.EqualFold(c.Name, initialConn) {
			m.connSel = i
		}
	}
	if initialConn == "" {
		m.showConns = true // the user has to pick a connection
		m.focus = focusConns
		m.status = "Select a connection and press Enter (a = add, d = delete). Config: " + cfg.Path()
	}
	return m
}

// newTab creates an empty tab after the existing ones.
func (m *Model) newTab() *tab {
	ed := editor.New()
	ed.TabWidth = m.cfg.Editor.TabWidth
	ed.AutoIndent = m.cfg.Editor.AutoIndent
	ed.Highlight = bool(m.cfg.Editor.SyntaxHighlight)
	t := &tab{id: m.nextTabID, ed: ed}
	m.nextTabID++
	m.tabs = append(m.tabs, t)
	return t
}

func (m *Model) tabIndex(t *tab) int {
	for i, x := range m.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

func (m *Model) findTab(id int) *tab {
	for _, t := range m.tabs {
		if t.id == id {
			return t
		}
	}
	return nil
}

// onTab runs f with tab id temporarily active, so handlers for background
// work update the tab that started it.
func (m *Model) onTab(id int, f func() tea.Cmd) tea.Cmd {
	t := m.findTab(id)
	if t == nil {
		return nil
	}
	saved, savedBg := m.tab, m.inBg
	m.tab, m.inBg = t, t != saved
	cmd := f()
	m.tab, m.inBg = saved, savedBg
	return cmd
}

func (m *Model) anyRunning() bool {
	for _, t := range m.tabs {
		if t.running || t.connecting {
			return true
		}
	}
	return false
}

// CloseAll closes every tab's connection, rolling back open transactions.
func (m *Model) CloseAll() {
	for _, t := range m.tabs {
		if t.conn != nil {
			_ = t.conn.Close()
		}
	}
}

// connIndex returns the config index of the named connection, or -1.
func (m *Model) connIndex(name string) int {
	for i, c := range m.cfg.Connections {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// openTab opens a new tab on the active tab's connection and switches to it.
func (m *Model) openTab() tea.Cmd {
	from := m.conn
	t := m.newTab()
	m.tab = t
	m.focus = focusEditor
	m.layout()
	if from != nil {
		if i := m.connIndex(from.Name); i >= 0 {
			return m.connect(i)
		}
	}
	m.setStatus("New tab. Choose a connection in the connections panel (F2 shows it).", false)
	return nil
}

func (m *Model) switchTab(i int) {
	if len(m.tabs) == 0 {
		return
	}
	i = (i%len(m.tabs) + len(m.tabs)) % len(m.tabs)
	m.tab = m.tabs[i]
	if m.focus == focusConns && !m.showConns {
		m.focus = focusEditor
	}
}

// closeTab closes the active tab; closing the last one quits.
func (m *Model) closeTab() tea.Cmd {
	if len(m.tabs) == 1 {
		return m.quit()
	}
	if m.running {
		m.setStatus("A statement is running in this tab; Ctrl-C cancels it first", true)
		return nil
	}
	doClose := func() tea.Cmd {
		i := m.tabIndex(m.tab)
		if m.conn != nil {
			_ = m.conn.Close()
		}
		m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
		m.tab = m.tabs[min(i, len(m.tabs)-1)]
		m.layout()
		m.setStatus(fmt.Sprintf("Tab closed; %d tab(s) open", len(m.tabs)), false)
		return nil
	}
	if m.conn != nil && m.conn.InTransaction() {
		m.ask("This tab has an open transaction; it will be rolled back. Close? (y/N)", "", false, func(v string) tea.Cmd {
			if strings.EqualFold(strings.TrimSpace(v), "y") {
				return doClose()
			}
			return nil
		})
		return nil
	}
	return doClose()
}

func (m *Model) Init() tea.Cmd {
	if m.focus != focusConns && len(m.cfg.Connections) > 0 {
		return m.connect(m.connSel)
	}
	return nil
}

// ---- messages ----

type connectedMsg struct {
	tab  int
	conn *db.Conn
	err  error
	name string
}

type execDoneMsg struct {
	tab      int
	last     *db.Result
	messages []string
	output   []string // DBMS_OUTPUT of all statements
	err      error
	elapsed  time.Duration
}

type fetchMsg struct {
	tab  int
	rows [][]string
	done bool
	err  error
}

type exportDoneMsg struct {
	tab  int
	path string
	rows int
	err  error
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) setStatus(s string, isErr bool) { m.status, m.statusErr = s, isErr }

func (m *Model) connect(i int) tea.Cmd {
	if i < 0 || i >= len(m.cfg.Connections) {
		return nil
	}
	c := m.cfg.Connections[i]
	if m.conn != nil && m.conn.InTransaction() {
		m.setStatus("Commit (F8) or roll back (F9) the open transaction before switching connections", true)
		return nil
	}
	m.connecting = true
	m.connectingTo = c.Name
	m.setStatus("Connecting to "+c.Name+" ...", false)
	opts := db.Options{LibDir: m.cfg.OracleLibDir(), AutoCommit: m.cfg.Query.AutoCommit, DBMSOutput: m.cfg.Query.DBMSOutput}
	id := m.id
	return tea.Batch(tick(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := db.Open(ctx, c, opts)
		return connectedMsg{tab: id, conn: conn, err: err, name: c.Name}
	})
}

func (m *Model) execute(stmts []string) tea.Cmd {
	if m.conn == nil {
		m.setStatus("Not connected: choose a connection in the connections panel (F2 shows it)", true)
		return nil
	}
	if m.running {
		m.setStatus("A statement is already running (Ctrl-C cancels)", true)
		return nil
	}
	if len(stmts) == 0 {
		m.setStatus("Nothing to execute", true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	if t := m.cfg.Query.TimeoutSeconds; t > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(t)*time.Second)
	}
	m.cancel = cancel
	m.running = true
	m.runStart = time.Now()
	m.setStatus("Running...", false)
	conn, hist, maxRows, id := m.conn, m.hist, m.cfg.Query.MaxRows, m.id
	return tea.Batch(tick(), func() tea.Msg {
		defer cancel()
		out := execDoneMsg{tab: id}
		start := time.Now()
		for i, s := range stmts {
			t0 := time.Now()
			res, err := conn.Execute(ctx, s, maxRows)
			if res != nil {
				out.output = append(out.output, res.Output...)
			}
			e := history.Entry{Connection: conn.Name, SQL: db.Normalize(s), ExecutedAt: t0,
				DurationMS: time.Since(t0).Milliseconds()}
			if err != nil {
				e.Error = db.CleanError(err)
			} else if res.IsQuery {
				e.Rows = int64(len(res.Rows))
			} else {
				e.Rows = res.Affected
			}
			if hist != nil {
				_ = hist.Add(e)
			}
			if err != nil {
				if len(stmts) > 1 {
					err = fmt.Errorf("statement %d: %w", i+1, err)
				}
				out.err = err
				break
			}
			msg := res.Message
			if len(stmts) > 1 {
				msg = fmt.Sprintf("[%d] %s", i+1, msg)
			}
			out.messages = append(out.messages, fmt.Sprintf("%s  (%s)", msg, res.Duration.Round(time.Millisecond)))
			if res.IsQuery || out.last == nil {
				out.last = res
			}
		}
		out.elapsed = time.Since(start)
		return out
	})
}

// currentStatements returns what F5 executes: the statements of the marked
// block, or else the single statement under the cursor.
func (m *Model) currentStatements() []string {
	if m.ed.HasBlock() {
		return db.SplitStatements(m.ed.BlockText())
	}
	st, ok := db.StatementAtCursor(m.ed.Text(), m.ed.CursorRow(), m.ed.CursorCol())
	if !ok {
		return nil
	}
	return []string{st.Text}
}

const fetchAllBatch = 5000

// fetch reads the next batch of rows of the current result in the background.
// With all set it keeps going until every row is fetched (Ctrl-C stops).
func (m *Model) fetch(all bool) tea.Cmd {
	r := m.res.res
	if m.conn == nil || m.running || r == nil || !r.More {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running, m.fetching, m.fetchAll = true, true, all
	if !all {
		m.runStart = time.Now()
	}
	n := m.cfg.Query.MaxRows
	if all {
		n = fetchAllBatch
	}
	conn, id := m.conn, m.id
	return tea.Batch(tick(), func() tea.Msg {
		defer cancel()
		rows, done, err := conn.FetchMore(ctx, n)
		return fetchMsg{tab: id, rows: rows, done: done, err: err}
	})
}

func (m *Model) rowsStatus() string {
	r := m.res.res
	s := db.RowsMessage(len(r.Rows), r.More)
	if r.More {
		s += " Scroll down for more, End fetches all."
	}
	return s
}

// ---- prompts ----

func (m *Model) ask(label, initial string, mask bool, onSubmit func(string) tea.Cmd) {
	m.mode = modePrompt
	m.prompt = prompt{label: label, input: []rune(initial), cur: len([]rune(initial)), mask: mask, onSubmit: onSubmit}
}

func (m *Model) updatePrompt(key, text string) tea.Cmd {
	p := &m.prompt
	switch key {
	case "esc", "ctrl+c", "ctrl+g":
		m.mode = modeNormal
		m.setStatus("Cancelled", false)
	case "enter":
		m.mode = modeNormal
		if p.onSubmit != nil {
			return p.onSubmit(string(p.input))
		}
	case "left", "ctrl+b":
		p.cur = max(p.cur-1, 0)
	case "right", "ctrl+f":
		p.cur = min(p.cur+1, len(p.input))
	case "home", "ctrl+a":
		p.cur = 0
	case "end", "ctrl+e":
		p.cur = len(p.input)
	case "backspace", "ctrl+h":
		if p.cur > 0 {
			p.input = append(p.input[:p.cur-1], p.input[p.cur:]...)
			p.cur--
		}
	case "delete", "ctrl+d":
		if p.cur < len(p.input) {
			p.input = append(p.input[:p.cur], p.input[p.cur+1:]...)
		}
	case "ctrl+y":
		p.input, p.cur = nil, 0
	case "space":
		text = " "
		fallthrough
	default:
		if text != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
			r := []rune(text)
			p.input = append(p.input[:p.cur], append(r, p.input[p.cur:]...)...)
			p.cur += len(r)
		}
	}
	return nil
}

// addConnectionWizard asks for the fields of a new connection and saves config.
func (m *Model) addConnectionWizard() {
	var c config.Connection
	m.ask("Connection name:", "", false, func(v string) tea.Cmd {
		c.Name = strings.TrimSpace(v)
		if c.Name == "" {
			m.setStatus("Name is required", true)
			return nil
		}
		m.ask("User:", "", false, func(v string) tea.Cmd {
			c.User = strings.TrimSpace(v)
			m.ask("Connect string (host:port/service or TNS alias):", "", false, func(v string) tea.Cmd {
				c.ConnectString = strings.TrimSpace(v)
				m.ask("Password (empty = take it from an env var):", "", true, func(v string) tea.Cmd {
					if v != "" {
						c.Password = v
						return m.saveNewConnection(c)
					}
					m.ask("Password env var:", "SQLCLI_PASSWORD", false, func(v string) tea.Cmd {
						c.PasswordEnv = strings.TrimSpace(v)
						return m.saveNewConnection(c)
					})
					return nil
				})
				return nil
			})
			return nil
		})
		return nil
	})
}

func (m *Model) saveNewConnection(c config.Connection) tea.Cmd {
	m.cfg.Connections = append(m.cfg.Connections, c)
	if err := m.cfg.Save(); err != nil {
		m.setStatus("Saving config: "+err.Error(), true)
		return nil
	}
	m.connSel = len(m.cfg.Connections) - 1
	m.setStatus(fmt.Sprintf("Connection %q saved to %s", c.Name, m.cfg.Path()), false)
	return nil
}

// ---- update ----

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		if m.anyRunning() {
			return m, tick()
		}
		return m, nil

	case connectedMsg:
		if m.findTab(msg.tab) == nil { // tab was closed meanwhile
			if msg.conn != nil {
				_ = msg.conn.Close()
			}
			return m, nil
		}
		return m, m.onTab(msg.tab, func() tea.Cmd {
			m.connecting = false
			if msg.err != nil {
				e := db.CleanError(msg.err)
				m.setStatus("Connect "+msg.name+": "+firstLine(e), true)
				m.res.set(nil, nil, nil, e)
				return nil
			}
			if m.conn != nil {
				_ = m.conn.Close()
			}
			m.conn = msg.conn
			m.setStatus("Connected to "+msg.name, false)
			if !m.inBg {
				m.focus = focusEditor
			}
			return nil
		})

	case execDoneMsg:
		return m, m.onTab(msg.tab, func() tea.Cmd { return m.execDone(msg) })

	case fetchMsg:
		return m, m.onTab(msg.tab, func() tea.Cmd { return m.fetchDone(msg) })

	case exportDoneMsg:
		return m, m.onTab(msg.tab, func() tea.Cmd {
			m.running, m.exporting = false, false
			if msg.err != nil {
				m.setStatus("Export failed: "+msg.err.Error(), true)
			} else {
				m.setStatus(fmt.Sprintf("Exported %d row(s) to %s", msg.rows, msg.path), false)
			}
			return nil
		})

	case tea.MouseWheelMsg:
		return m, m.wheel(msg)

	case tea.MouseClickMsg:
		return m, m.click(msg)

	case tea.PasteMsg:
		switch m.mode {
		case modePrompt:
			r := []rune(strings.ReplaceAll(msg.Content, "\n", " "))
			m.prompt.input = append(m.prompt.input[:m.prompt.cur], append(r, m.prompt.input[m.prompt.cur:]...)...)
			m.prompt.cur += len(r)
		case modeNormal:
			if m.focus == focusEditor {
				m.ed.InsertText(msg.Content)
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) execDone(msg execDoneMsg) tea.Cmd {
	m.running = false
	m.cancel = nil
	m.recordRun(msg)
	errText := ""
	if msg.err != nil {
		errText = db.CleanError(msg.err)
		m.setStatus(firstLine(errText), true)
	} else {
		st := "Done"
		if n := len(msg.messages); n > 0 {
			st = withoutTime(msg.messages[n-1])
			if n > 1 {
				st = fmt.Sprintf("%d statements OK; last: %s", n, st)
			}
		}
		m.setStatus(st, false)
	}
	m.res.set(msg.last, msg.messages, msg.output, errText)
	if n := len(msg.output); n > 0 {
		hint := ""
		if m.res.canToggle() {
			hint = ", o in results shows it"
		}
		m.setStatus(fmt.Sprintf("%s  (%d line(s) of DBMS_OUTPUT%s)", m.status, n, hint), m.statusErr)
	}
	if msg.err == nil && msg.last != nil && msg.last.More {
		if len(msg.messages) == 1 {
			m.setStatus(m.rowsStatus(), false)
		}
		if m.cfg.Query.FetchAll {
			m.runStart = time.Now()
			return m.fetch(true)
		}
	}
	return nil
}

func (m *Model) fetchDone(msg fetchMsg) tea.Cmd {
	m.running, m.cancel = false, nil
	r := m.res.res
	if r == nil {
		m.fetching = false
		return nil
	}
	m.res.appendRows(msg.rows)
	r.More = !msg.done
	r.Message = db.RowsMessage(len(r.Rows), r.More)
	if m.hasPending {
		if msg.err == nil && r.More && m.pendingRow >= len(r.Rows) {
			return m.fetch(false) // still short of the target row
		}
		m.res.row, m.hasPending = m.pendingRow, false
		m.res.clampRow()
	}
	if msg.err == nil && m.fetchAll && r.More {
		return m.fetch(true) // next batch
	}
	m.fetching, m.fetchAll = false, false
	if after := m.afterFetch; after != nil {
		m.afterFetch = nil
		if msg.err == nil && !r.More {
			return after()
		}
		m.jumpEnd = false
		m.setStatus(fmt.Sprintf("Export cancelled: only %d row(s) were fetched.", len(r.Rows)), true)
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.setStatus("Export cancelled: "+db.CleanError(msg.err), true)
		}
		return nil
	}
	switch {
	case errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, context.DeadlineExceeded):
		m.setStatus(fmt.Sprintf("Fetch stopped at %d row(s); the remaining rows were discarded.", len(r.Rows)), false)
	case msg.err != nil:
		m.setStatus(db.CleanError(msg.err), true)
	default:
		m.setStatus(m.rowsStatus(), false)
	}
	if m.jumpEnd {
		m.jumpEnd = false
		m.res.handleKey("end")
	}
	return nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key, text := msg.String(), msg.Text

	switch m.mode {
	case modePrompt:
		return m.updatePrompt(key, text)
	case modeHelp:
		if !m.scrollHelp(key) {
			m.mode = modeNormal
		}
		return nil
	case modeRecord:
		return m.updateRecord(key)
	case modeHistory:
		return m.updateHistory(key, text)
	}

	// ^K commands outside the editor (the editor handles its own)
	if m.kPrefix {
		m.kPrefix = false
		return m.ctrlKCommand(key)
	}
	if key == "ctrl+k" && m.focus != focusEditor {
		m.kPrefix = true
		return nil
	}

	// global keys
	switch key {
	case "ctrl+c":
		if m.running && m.cancel != nil {
			m.cancel()
			m.setStatus("Cancelling...", false)
			return nil
		}
		if m.ed.PrefixPending() {
			m.ed.HandleKey("esc", "")
			return nil
		}
		return m.quit()
	case "ctrl+pgdown":
		m.switchTab(m.tabIndex(m.tab) + 1)
		return nil
	case "ctrl+pgup":
		m.switchTab(m.tabIndex(m.tab) - 1)
		return nil
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		if i := int(key[4] - '1'); i < len(m.tabs) {
			m.switchTab(i)
		}
		return nil
	case "f1":
		m.mode, m.helpTop = modeHelp, 0
		return nil
	case "f2": // show (and focus) or hide the connections panel
		m.showConns = !m.showConns
		if m.showConns {
			m.focus = focusConns
		} else if m.focus == focusConns {
			m.focus = focusEditor
		}
		m.layout()
		return nil
	case "f3":
		m.focus = focusResults
		return nil
	case "f5", "ctrl+enter", "alt+enter":
		return m.execute(m.currentStatements())
	case "f7":
		return m.execute(db.SplitStatements(m.ed.Text()))
	case "f6":
		m.openHistory()
		return nil
	case "f8", "f9":
		if m.conn == nil || m.running {
			return nil
		}
		var err error
		if key == "f8" {
			err = m.conn.Commit()
		} else {
			err = m.conn.Rollback()
		}
		if r := m.res.res; r != nil && r.More { // commit/rollback closed the cursor
			r.More = false
			r.Message = db.RowsMessage(len(r.Rows), false)
		}
		if err != nil {
			m.setStatus(err.Error(), true)
		} else if key == "f8" {
			m.setStatus("Commit complete", false)
		} else {
			m.setStatus("Rollback complete", false)
		}
		return nil
	}

	switch m.focus {
	case focusConns:
		return m.updateConns(key)
	case focusResults:
		switch key {
		case "esc", "tab":
			m.focus = focusEditor
		case "enter":
			if m.res.selectedRow() != nil {
				m.mode, m.recTop = modeRecord, 0
			}
		case "o":
			m.res.toggleText()
		case "e":
			return m.askExport()
		case "end", "ctrl+e", "G":
			m.res.handleKey("end")
			if !m.res.showText() && m.res.res.More {
				m.jumpEnd = true
				m.runStart = time.Now()
				return m.fetch(true)
			}
		default:
			m.res.handleKey(key)
			if m.res.nearEnd() {
				return m.fetch(false)
			}
		}
		return nil
	}

	// editor
	if key == "esc" && !m.ed.PrefixPending() {
		return nil
	}
	switch m.ed.HandleKey(key, text) {
	case editor.ActionExit:
		return m.quit()
	case editor.ActionNewWindow:
		return m.openTab()
	case editor.ActionNextWindow:
		m.switchTab(m.tabIndex(m.tab) + 1)
	case editor.ActionPrevWindow:
		m.switchTab(m.tabIndex(m.tab) - 1)
	case editor.ActionCloseWindow:
		return m.closeTab()
	case editor.ActionHelp:
		m.mode = modeHelp
	case editor.ActionFindPrompt:
		m.ask("Find:", "", false, func(v string) tea.Cmd {
			m.ed.Find(v)
			m.setStatus(m.ed.Status, m.ed.Status != "")
			return nil
		})
	case editor.ActionGotoPrompt:
		m.ask("Go to line:", "", false, func(v string) tea.Cmd {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				m.setStatus("Not a number", true)
				return nil
			}
			m.ed.GotoLine(n)
			return nil
		})
	case editor.ActionSavePrompt:
		m.ask("Save buffer to file:", "", false, func(v string) tea.Cmd {
			m.writeFile(v, m.ed.Text())
			return nil
		})
	case editor.ActionWriteBlock:
		m.ask("Write block to file:", "", false, func(v string) tea.Cmd {
			m.writeFile(v, m.ed.BlockText())
			return nil
		})
	case editor.ActionInsertFile:
		m.ask("Insert file:", "", false, func(v string) tea.Cmd {
			b, err := os.ReadFile(expandHome(strings.TrimSpace(v)))
			if err != nil {
				m.setStatus(err.Error(), true)
				return nil
			}
			m.ed.InsertText(string(b))
			return nil
		})
	}
	if m.ed.Status != "" {
		m.setStatus(m.ed.Status, strings.HasPrefix(m.ed.Status, "No") || strings.HasPrefix(m.ed.Status, "Unknown"))
	}
	return nil
}

func (m *Model) writeFile(name, content string) {
	name = expandHome(strings.TrimSpace(name))
	if name == "" {
		return
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.setStatus("Wrote "+name, false)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}

func (m *Model) quit() tea.Cmd {
	var tx, running int
	for _, t := range m.tabs {
		if t.conn != nil && t.conn.InTransaction() {
			tx++
		}
		if t.running {
			running++
		}
	}
	if tx > 0 || running > 0 {
		var why []string
		if tx > 0 {
			why = append(why, fmt.Sprintf("%d open transaction(s) will be rolled back", tx))
		}
		if running > 0 {
			why = append(why, fmt.Sprintf("%d running statement(s) will be cancelled", running))
		}
		m.ask(strings.Join(why, ", ")+". Quit? (y/N)", "", false, func(v string) tea.Cmd {
			if strings.EqualFold(strings.TrimSpace(v), "y") {
				m.quitting = true
				return tea.Quit
			}
			return nil
		})
		return nil
	}
	m.quitting = true
	return tea.Quit
}

func (m *Model) updateConns(key string) tea.Cmd {
	n := len(m.cfg.Connections)
	switch key {
	case "up", "ctrl+p", "k":
		m.connSel = max(m.connSel-1, 0)
	case "down", "ctrl+n", "j":
		m.connSel = min(m.connSel+1, max(n-1, 0))
	case "enter":
		return m.connect(m.connSel)
	case "a":
		m.addConnectionWizard()
	case "d":
		if n == 0 {
			return nil
		}
		name := m.cfg.Connections[m.connSel].Name
		m.ask(fmt.Sprintf("Delete connection %q? (y/N)", name), "", false, func(v string) tea.Cmd {
			if !strings.EqualFold(strings.TrimSpace(v), "y") {
				return nil
			}
			m.cfg.Connections = append(m.cfg.Connections[:m.connSel], m.cfg.Connections[m.connSel+1:]...)
			m.connSel = min(m.connSel, max(len(m.cfg.Connections)-1, 0))
			if err := m.cfg.Save(); err != nil {
				m.setStatus(err.Error(), true)
			} else {
				m.setStatus("Deleted "+name, false)
			}
			return nil
		})
	case "esc", "tab":
		m.focus = focusEditor
	}
	return nil
}

// ---- history browser ----

func (m *Model) openHistory() {
	if m.hist == nil {
		m.setStatus("History is not available", true)
		return
	}
	m.mode = modeHistory
	m.histFilter = nil
	m.histSel = 0
	m.reloadHistory()
}

func (m *Model) reloadHistory() {
	es, err := m.hist.Recent(m.cfg.History.MaxEntries, string(m.histFilter))
	if err != nil {
		m.setStatus(err.Error(), true)
	}
	m.histEntries = es
	m.histSel = min(m.histSel, max(len(es)-1, 0))
}

func (m *Model) updateHistory(key, text string) tea.Cmd {
	switch key {
	case "esc", "f6", "ctrl+c":
		m.mode = modeNormal
	case "up", "ctrl+p":
		m.histSel = max(m.histSel-1, 0)
	case "down", "ctrl+n":
		m.histSel = min(m.histSel+1, max(len(m.histEntries)-1, 0))
	case "pgup", "ctrl+u":
		m.histSel = max(m.histSel-10, 0)
	case "pgdown", "ctrl+v":
		m.histSel = min(m.histSel+10, max(len(m.histEntries)-1, 0))
	case "enter", "ctrl+r":
		if len(m.histEntries) == 0 {
			return nil
		}
		s := m.histEntries[m.histSel].SQL
		m.mode = modeNormal
		m.focus = focusEditor
		if key == "ctrl+r" { // replace buffer
			m.ed.SetText(s)
		} else {
			m.ed.InsertText(s)
		}
		return nil
	case "delete":
		if len(m.histEntries) > 0 {
			_ = m.hist.Delete(m.histEntries[m.histSel].ID)
			m.reloadHistory()
		}
	case "backspace", "ctrl+h":
		if len(m.histFilter) > 0 {
			m.histFilter = m.histFilter[:len(m.histFilter)-1]
			m.reloadHistory()
		}
	case "ctrl+y":
		m.histFilter = nil
		m.reloadHistory()
	case "space":
		text = " "
		fallthrough
	default:
		if text != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
			m.histFilter = append(m.histFilter, []rune(text)...)
			m.histSel = 0
			m.reloadHistory()
		}
	}
	return nil
}

// ---- layout & view ----

func (m *Model) connWidth() int {
	if !m.showConns {
		return 0
	}
	return min(max(m.w/5, 18), 30)
}

// sqlTitle is the SQL panel title: "SQL · <connection>".
func (m *Model) sqlTitle() string {
	t := "SQL · "
	if len(m.tabs) > 1 {
		t = fmt.Sprintf("SQL %d · ", m.tabIndex(m.tab)+1)
	}
	switch {
	case m.connecting:
		t += "connecting to " + m.connectingTo + "…"
	case m.conn == nil:
		t += "not connected"
	default:
		t += m.conn.Name
		if m.conn.ReadOnly {
			t += " (read-only)"
		}
	}
	if m.ed.PrefixPending() {
		t += "  ^K-"
	}
	return t
}

// tabBarH is the height of the tab bar: shown only with several tabs.
func (m *Model) tabBarH() int {
	if len(m.tabs) > 1 {
		return 1
	}
	return 0
}

// paneH is the height available to the panes.
func (m *Model) paneH() int { return m.h - 2 - m.tabBarH() } // status + key hints

func (m *Model) layout() {
	rightW := m.w - m.connWidth()
	avail := m.paneH()
	edH := max(avail*45/100, 5)
	resH := max(avail-edH, 4)
	for _, t := range m.tabs {
		t.ed.SetSize(rightW-2, edH-2)
		t.res.w, t.res.h = rightW-2, resH-2
	}
}

var (
	tabActive   = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")).Bold(true)
	tabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// tabName is how a tab is labelled: its connection, or a state.
func (t *tab) name() string {
	switch {
	case t.connecting:
		return t.connectingTo + "…"
	case t.conn != nil:
		return t.conn.Name
	}
	return "not connected"
}

func (m *Model) tabLabels() []string {
	var out []string
	for i, t := range m.tabs {
		label := fmt.Sprintf(" %d %s", i+1, t.name())
		if t.conn != nil && t.conn.InTransaction() {
			label += " TX"
		}
		if t.running {
			label += " …"
		}
		out = append(out, label+" ")
	}
	return out
}

func (m *Model) tabBar() string {
	var parts []string
	for i, label := range m.tabLabels() {
		if m.tabs[i] == m.tab {
			parts = append(parts, tabActive.Render(label))
		} else {
			parts = append(parts, tabInactive.Render(label))
		}
	}
	return ansi.Truncate(strings.Join(parts, hintText.Render("│")), m.w, "…")
}

// tabAt returns the index of the tab whose label is at column x, or -1.
func (m *Model) tabAt(x int) int {
	pos := 0
	for i, label := range m.tabLabels() {
		w := ansi.StringWidth(label)
		if x >= pos && x < pos+w {
			return i
		}
		pos += w + 1 // separator
	}
	return -1
}

var (
	borderFocused = lipgloss.Color("75")
	borderBlurred = lipgloss.Color("240")
	statusStyle   = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
	statusErr     = lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("255"))
	hintKey       = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	hintText      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	connSelStyle  = lipgloss.NewStyle().Reverse(true)
	connActStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Bold(true)
	txStyle       = lipgloss.NewStyle().Background(lipgloss.Color("130")).Foreground(lipgloss.Color("255")).Bold(true)
)

// box draws content inside a rounded border of outer size w x h with a title.
// closeButton is drawn at the right of the SQL panel's top border; a click
// on it closes the tab.
const closeButton = "[x]"

func box(title, content string, w, h int, focused bool) string {
	return boxWithButton(title, "", content, w, h, focused)
}

// boxWithButton is box with a label (button) at the right of the top
// border, just before the corner: ╭─ title ─────[x]─╮
func boxWithButton(title, button, content string, w, h int, focused bool) string {
	return boxFull(title, button, "", content, w, h, focused)
}

// boxFull draws a box with a title and button in the top border and a
// footer (already styled) in the bottom border: ╰─ footer ───╯
func boxFull(title, button, footer, content string, w, h int, focused bool) string {
	col := borderBlurred
	if focused {
		col = borderFocused
	}
	bs := lipgloss.NewStyle().Foreground(col)
	iw, ih := max(w-2, 0), max(h-2, 0)
	t := ""
	if title != "" {
		t = " " + title + " "
	}
	bw := ansi.StringWidth(button)
	if bw > 0 {
		bw++ // the "─" after the button
	}
	t = ansi.Truncate(t, max(iw-1-bw, 0), "")
	fill := strings.Repeat("─", max(iw-1-bw-ansi.StringWidth(t), 0))
	top := bs.Render("╭─"+t+fill) + button
	if bw > 0 {
		top += bs.Render("─")
	}
	top += bs.Render("╮")
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.WriteString(top)
	for i := 0; i < ih; i++ {
		l := ""
		if i < len(lines) {
			l = ansi.Truncate(lines[i], iw, "")
		}
		b.WriteByte('\n')
		b.WriteString(bs.Render("│"))
		b.WriteString(l)
		b.WriteString(strings.Repeat(" ", max(iw-ansi.StringWidth(l), 0)))
		b.WriteString(bs.Render("│"))
	}
	b.WriteByte('\n')
	if footer == "" {
		b.WriteString(bs.Render("╰" + strings.Repeat("─", iw) + "╯"))
		return b.String()
	}
	footer = ansi.Truncate(" "+footer+" ", max(iw-2, 0), "…")
	b.WriteString(bs.Render("╰─") + footer + bs.Render(strings.Repeat("─", max(iw-1-ansi.StringWidth(footer), 0))+"╯"))
	return b.String()
}

func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.cfg.UI.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.WindowTitle = "sqlcli " + version.Version
	if m.w == 0 || m.quitting {
		return v
	}
	switch m.mode {
	case modeHelp:
		lines := strings.Split(strings.TrimSuffix(helpText, "\n"), "\n")
		vis := max(m.h-2, 1)
		m.helpTop = min(max(m.helpTop, 0), max(len(lines)-vis, 0))
		title := "sqlcli " + version.Version + " help — any key closes"
		if len(lines) > vis {
			title = fmt.Sprintf("sqlcli %s help %d-%d/%d — ↑↓ PgUp PgDn scroll, any other key closes", version.Version, m.helpTop+1, min(m.helpTop+vis, len(lines)), len(lines))
		}
		v.SetContent(box(title, strings.Join(lines[m.helpTop:], "\n"), m.w, m.h, true))
		return v
	case modeHistory:
		v.SetContent(m.historyView())
		return v
	case modeRecord:
		v.SetContent(m.recordView())
		return v
	}

	cw := m.connWidth()
	rightW := m.w - cw
	m.ed.Focused = m.focus == focusEditor && m.mode == modeNormal

	// connections
	var cl []string
	for i, c := range m.cfg.Connections {
		name := c.Name
		if c.ReadOnly {
			name += " (ro)"
		}
		mark := "  "
		for _, t := range m.tabs {
			if t.conn != nil && t.conn.Name == c.Name && mark == "  " {
				mark = "○ " // open in another tab
			}
		}
		if m.conn != nil && m.conn.Name == c.Name {
			mark = "● "
		}
		line := ansi.Truncate(mark+name, cw-2, "…")
		switch {
		case i == m.connSel && m.focus == focusConns:
			line = connSelStyle.Render(line + strings.Repeat(" ", max(cw-2-ansi.StringWidth(line), 0)))
		case m.conn != nil && m.conn.Name == c.Name:
			line = connActStyle.Render(line)
		}
		cl = append(cl, line)
	}
	if len(cl) == 0 {
		cl = append(cl, hintText.Render("none — press a"))
	}
	if m.focus == focusConns {
		cl = append(cl, "", hintText.Render("Enter connect"), hintText.Render("a add  d delete"))
	}
	avail := m.paneH()
	left := box("Connections", strings.Join(cl, "\n"), cw, avail, m.focus == focusConns)

	edH := m.ed.View()
	edTitle := m.sqlTitle()
	edOuter := avail - (m.res.h + 2) // matches layout()
	edBox := boxFull(edTitle, closeButton, m.runFooter(), edH, rightW, edOuter, m.focus == focusEditor)
	resTitle := "Results"
	if s := m.res.summary(); s != "" {
		resTitle += "  " + s
	}
	resBox := box(resTitle, m.res.view(m.focus == focusResults), rightW, m.res.h+2, m.focus == focusResults)

	right := lipgloss.JoinVertical(lipgloss.Left, edBox, resBox)
	body := right
	if m.showConns {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	if m.tabBarH() > 0 {
		body = m.tabBar() + "\n" + body
	}
	v.SetContent(body + "\n" + m.statusLine() + "\n" + m.bottomLine())
	return v
}

func (m *Model) statusLine() string {
	conn := "not connected"
	if m.connecting {
		conn = "connecting…"
	} else if m.conn != nil {
		conn = m.conn.Name
	}
	left := " " + conn
	tx := ""
	if m.conn != nil && m.conn.InTransaction() {
		tx = txStyle.Render(" TX ")
	}
	pos := fmt.Sprintf(" Ln %d, Col %d ", m.ed.CursorRow()+1, m.ed.CursorCol()+1)
	msg := m.status
	if m.kPrefix {
		msg = "^K-  (X quit, Q close tab, O new tab, N/P next/previous tab, H help)"
	} else if m.running && m.exporting {
		msg = fmt.Sprintf("Exporting %d rows…", len(m.res.res.Rows))
	} else if m.running && m.fetching && m.afterFetch != nil && m.res.res != nil {
		msg = fmt.Sprintf("Fetching all rows for export… %d (Ctrl-C stops)", len(m.res.res.Rows))
	} else if m.running && m.fetching && m.res.res != nil {
		msg = fmt.Sprintf("Fetching… %d rows (Ctrl-C stops)", len(m.res.res.Rows))
	} else if m.running {
		msg = "Running… (Ctrl-C cancels)"
	}
	st := statusStyle
	if m.statusErr && !m.running {
		st = statusErr
	}
	head := statusStyle.Render(left+" ") + tx + statusStyle.Render(" │"+pos+"│ ")
	rest := max(m.w-ansi.StringWidth(head), 0)
	msg = ansi.Truncate(msg, rest, "…")
	return head + st.Render(msg+strings.Repeat(" ", max(rest-ansi.StringWidth(msg), 0)))
}

func (m *Model) bottomLine() string {
	if m.mode == modePrompt {
		p := m.prompt
		in := p.input
		if p.mask {
			in = []rune(strings.Repeat("*", len(in)))
		}
		before, after := string(in[:p.cur]), ""
		curCh := " "
		if p.cur < len(in) {
			curCh = string(in[p.cur])
			after = string(in[p.cur+1:])
		}
		return hintKey.Render(p.label) + " " + before + lipgloss.NewStyle().Reverse(true).Render(curCh) + after
	}
	hints := [][2]string{{"F1", "help"}, {"F2", "conns"}, {"F3", "results"}, {"F5", "run"}, {"F7", "run all"},
		{"F6", "history"}, {"F8", "commit"}, {"F9", "rollback"}, {"^K O/N/P/Q", "tabs"}, {"^K X", "quit"}}
	var parts []string
	for _, h := range hints {
		parts = append(parts, hintKey.Render(h[0])+" "+hintText.Render(h[1]))
	}
	return ansi.Truncate(" "+strings.Join(parts, "  "), m.w, "")
}

func (m *Model) historyView() string {
	listH := max(m.h-2-2-8, 3) // filter line, borders, preview
	var lines []string
	start := 0
	if m.histSel >= listH {
		start = m.histSel - listH + 1
	}
	iw := m.w - 2
	for i := start; i < len(m.histEntries) && i < start+listH; i++ {
		e := m.histEntries[i]
		flag := " "
		if e.Error != "" {
			flag = errStyle.Render("✗")
		}
		sqlOne := strings.Join(strings.Fields(e.SQL), " ")
		meta := fmt.Sprintf("%s %-12s ", e.ExecutedAt.Format("2006-01-02 15:04:05"), ansi.Truncate(e.Connection, 12, "…"))
		line := meta + flag + " " + sqlOne
		line = ansi.Truncate(line, iw, "…")
		if i == m.histSel {
			line = connSelStyle.Render(ansi.Strip(line) + strings.Repeat(" ", max(iw-ansi.StringWidth(line), 0)))
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, hintText.Render("No history entries"))
	}
	for len(lines) < listH {
		lines = append(lines, "")
	}
	lines = append(lines, sepStyle.Render(strings.Repeat("─", iw)))
	if len(m.histEntries) > 0 {
		e := m.histEntries[m.histSel]
		info := fmt.Sprintf("%d ms, %d row(s)", e.DurationMS, e.Rows)
		if e.Error != "" {
			info = errStyle.Render(firstLine(e.Error))
		}
		lines = append(lines, hintText.Render(info))
		pl := strings.Split(e.SQL, "\n")
		for i := 0; i < 6 && i < len(pl); i++ {
			lines = append(lines, pl[i])
		}
	}
	title := fmt.Sprintf("History (%d)  filter: %s▏", len(m.histEntries), string(m.histFilter))
	b := box(title, strings.Join(lines, "\n"), m.w, m.h-1, true)
	hint := " " + hintKey.Render("Enter") + hintText.Render(" insert  ") + hintKey.Render("^R") + hintText.Render(" replace buffer  ") +
		hintKey.Render("Del") + hintText.Render(" delete  ") + hintKey.Render("type") + hintText.Render(" to filter  ") +
		hintKey.Render("Esc") + hintText.Render(" close")
	return b + "\n" + ansi.Truncate(hint, m.w, "")
}

func (m *Model) recordView() string {
	r := m.res.res
	row := m.res.selectedRow()
	if row == nil { // e.g. the result was replaced meanwhile
		m.mode = modeNormal
		return ""
	}
	nw := 0
	for _, c := range r.Columns {
		nw = max(nw, ansi.StringWidth(c))
	}
	var lines []string
	for i, c := range r.Columns {
		name := headerStyle.Render(fmt.Sprintf("%-*s", nw, c)) + sepStyle.Render(" │ ")
		if row[i] == db.Null {
			lines = append(lines, name+nullStyle.Render(db.NullDisplay))
			continue
		}
		vl := strings.Split(row[i], "\n")
		lines = append(lines, name+vl[0])
		for _, extra := range vl[1:] {
			lines = append(lines, strings.Repeat(" ", nw)+sepStyle.Render(" │ ")+extra)
		}
	}
	vis := max(m.h-3, 1) // box borders + hint line
	m.recTop = min(max(m.recTop, 0), max(len(lines)-vis, 0))

	more := ""
	if r.More {
		more = "+"
	}
	title := fmt.Sprintf("Record %d of %d%s", m.res.row+1, len(r.Rows), more)
	if len(lines) > vis {
		title += fmt.Sprintf("  lines %d-%d/%d", m.recTop+1, min(m.recTop+vis, len(lines)), len(lines))
	}
	if m.running && m.fetching {
		title += fmt.Sprintf("  fetching… %d rows (Ctrl-C stops)", len(r.Rows))
	}
	hint := " " + hintKey.Render("↑↓") + hintText.Render(" previous/next  ") +
		hintKey.Render("Home End") + hintText.Render(" first/last  ") +
		hintKey.Render("PgUp PgDn") + hintText.Render(fmt.Sprintf(" ±%d records  ", m.cfg.UI.RecordPage)) +
		hintKey.Render("Shift-↑↓") + hintText.Render(" scroll  ") +
		hintKey.Render("other keys") + hintText.Render(" close")
	return box(title, strings.Join(lines[m.recTop:], "\n"), m.w, m.h-1, true) + "\n" + ansi.Truncate(hint, m.w, "")
}

// updateRecord handles keys in the record view: move between records,
// scroll a long record, or close the view.
func (m *Model) updateRecord(key string) tea.Cmd {
	r := m.res.res
	switch key {
	case "up", "ctrl+p", "k":
		m.res.handleKey("up")
		m.recTop = 0
	case "down", "ctrl+n", "j":
		m.res.handleKey("down")
		m.recTop = 0
		if m.res.nearEnd() {
			return m.fetch(false)
		}
	case "home", "ctrl+a", "g":
		m.res.handleKey("home")
		m.recTop = 0
	case "end", "ctrl+e", "G":
		m.res.handleKey("end")
		m.recTop = 0
		if r != nil && r.More {
			m.jumpEnd = true
			m.runStart = time.Now()
			return m.fetch(true)
		}
	case "pgup", "ctrl+u":
		m.res.moveBy(-m.cfg.UI.RecordPage)
		m.recTop = 0
	case "pgdown", "ctrl+v":
		m.recTop = 0
		target := m.res.row + m.cfg.UI.RecordPage
		if r != nil && r.More && target >= len(r.Rows) {
			// the target row isn't fetched yet: fetch, then move there
			if !m.running {
				m.pendingRow, m.hasPending = target, true
				return m.fetch(false)
			}
			return nil
		}
		m.res.moveBy(m.cfg.UI.RecordPage)
		if m.res.nearEnd() {
			return m.fetch(false)
		}
	case "shift+up":
		m.recTop--
	case "shift+down":
		m.recTop++
	case "shift+pgup":
		m.recTop -= max(m.h-3, 1)
	case "shift+pgdown":
		m.recTop += max(m.h-3, 1)
	case "ctrl+c":
		if m.running && m.cancel != nil {
			m.cancel()
		}
	default:
		m.mode = modeNormal
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const helpText = `
 GLOBAL                                    EDITOR (JOE keys)
 F1          this help                     ^B ^F ^P ^N / arrows   move
 F2          show / hide connections pane  ^A / ^E                line start / end
 F3          results pane                  ^Z / ^X                word left / right
 Esc         back to editor                ^U / ^V                page up / down
 F5          run statement at cursor       ^K U / ^K V            top / bottom of buffer
             (or the marked block)         ^K L                   go to line
 F7          run whole buffer as script    ^D                     delete char
 F6          SQL history (SQLite)          ^Y                     delete line
 F8 / F9     commit / rollback             ^W / ^O                delete word right / left
 Ctrl-C      cancel running query / quit   ^J                     delete to end of line
 ^K X        quit                          ^_ / ^^                undo / redo
                                           ^K B / ^K K            mark block begin / end
 CONNECTIONS PANE                          ^K C / ^K M / ^K Y     copy / move / delete block
 Enter       connect this tab              ^K A                   clear block marks
 a / d       add / delete (saved to TOML)  ^K F / ^L              find / find next
                                           ^K D                   save buffer to file
 RESULTS PANE                              ^K R                   insert file
 arrows, PgUp/PgDn, wheel   scroll         ^K W                   write block to file
 End         fetch all rows, last row
 Ctrl-C      stop fetching                 RECORD VIEW (Enter on a row)
 Enter       show row as record            ↑↓ / Home End          previous, next / first, last
 o           grid / DBMS_OUTPUT            PgUp / PgDn            ± record_page records
 e           export to .csv/.tsv/.json     Shift-↑↓, wheel        scroll a long record

 TABS (each tab has its own editor, results and Oracle session)
 ^K O        new tab on the same connection      ^K N / ^K P   next / previous tab
 ^K Q        close tab (last tab: quit)          Ctrl-PgDn / Ctrl-PgUp, Alt-1..9   switch tab
 Enter in the connections pane connects this tab to the selected connection

 MOUSE  click a pane to focus it · click in the editor to move the cursor · double-click a connection
        to connect · click a tab to switch · [x] on the SQL panel closes the tab · Shift+drag selects text

 F5 runs the one statement the cursor is in. Statements end with ';', a blank line, or a '/' line.
 PL/SQL blocks (BEGIN/DECLARE/CREATE PROCEDURE ...) must end with a '/' line.
`

// wheel scrolls the pane under the mouse pointer by three lines.
func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	if m.mode == modeHelp || m.mode == modeRecord {
		d := 0
		if msg.Button == tea.MouseWheelUp {
			d = -3
		} else if msg.Button == tea.MouseWheelDown {
			d = 3
		}
		if m.mode == modeHelp {
			m.helpTop += d
		} else {
			m.recTop += d
		}
		return nil
	}
	if m.mode != modeNormal {
		return nil
	}
	key := "down"
	if msg.Button == tea.MouseWheelUp {
		key = "up"
	}
	if msg.Button != tea.MouseWheelUp && msg.Button != tea.MouseWheelDown {
		return nil
	}
	if msg.X < m.connWidth() {
		return nil
	}
	if msg.Y < m.tabBarH() {
		return nil
	}
	msg.Y -= m.tabBarH()
	edOuter := m.paneH() - (m.res.h + 2)
	if msg.Y < edOuter {
		for i := 0; i < 3; i++ {
			m.ed.HandleKey(key, "")
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		m.res.handleKey(key)
	}
	if key == "down" && m.res.nearEnd() {
		return m.fetch(false)
	}
	return nil
}

// askExport asks for a file name and exports the current result to it.
func (m *Model) askExport() tea.Cmd {
	if m.res.showText() || m.res.res == nil {
		m.setStatus("Nothing to export: run a query first", true)
		return nil
	}
	if m.running {
		m.setStatus("Wait for the running statement to finish (Ctrl-C cancels it)", true)
		return nil
	}
	def := m.lastExport
	if def == "" {
		def = "result.csv"
	}
	m.ask("Export to (.csv, .tsv or .json):", def, false, func(v string) tea.Cmd {
		path := expandHome(strings.TrimSpace(v))
		if path == "" {
			return nil
		}
		if _, err := export.FormatFor(path); err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		m.lastExport = strings.TrimSpace(v)
		if _, err := os.Stat(path); err == nil {
			m.ask(path+" exists. Overwrite? (y/N)", "", false, func(v string) tea.Cmd {
				if strings.EqualFold(strings.TrimSpace(v), "y") {
					return m.startExport(path)
				}
				m.setStatus("Export cancelled", false)
				return nil
			})
			return nil
		}
		return m.startExport(path)
	})
	return nil
}

// startExport fetches any rows still on the server, then writes the file.
func (m *Model) startExport(path string) tea.Cmd {
	if r := m.res.res; r != nil && r.More {
		m.afterFetch = func() tea.Cmd { return m.export(path) }
		m.runStart = time.Now()
		return m.fetch(true)
	}
	return m.export(path)
}

// export writes the current result to path in the background.
func (m *Model) export(path string) tea.Cmd {
	r := m.res.res
	cols, types, rows := r.Columns, r.Types, r.Rows
	m.running, m.exporting = true, true
	m.runStart = time.Now()
	id := m.id
	return tea.Batch(tick(), func() tea.Msg {
		err := export.WriteFile(path, cols, types, rows)
		return exportDoneMsg{tab: id, path: path, rows: len(rows), err: err}
	})
}

// ctrlKCommand runs the ^K command for key when the editor doesn't have
// focus. Window commands work everywhere; editing ones need the editor.
func (m *Model) ctrlKCommand(key string) tea.Cmd {
	k := strings.ToLower(strings.TrimPrefix(key, "ctrl+"))
	switch k {
	case "x":
		return m.quit()
	case "q":
		return m.closeTab()
	case "o":
		return m.openTab()
	case "n":
		m.switchTab(m.tabIndex(m.tab) + 1)
	case "p":
		m.switchTab(m.tabIndex(m.tab) - 1)
	case "h":
		m.mode = modeHelp
	case "esc", "g", "c":
		// cancel
	default:
		m.setStatus("^K "+strings.ToUpper(k)+" works in the SQL editor (Esc goes there)", true)
	}
	return nil
}

// click handles a left mouse click: it focuses the pane under the pointer
// and acts on what was clicked.
func (m *Model) click(msg tea.MouseClickMsg) tea.Cmd {
	if m.mode != modeNormal || msg.Button != tea.MouseLeft {
		return nil
	}
	m.kPrefix = false
	if m.tabBarH() > 0 && msg.Y == 0 {
		if i := m.tabAt(msg.X); i >= 0 {
			m.switchTab(i)
		}
		return nil
	}
	y := msg.Y - m.tabBarH()
	if y < 0 || y >= m.paneH() {
		return nil // status / hint lines
	}
	cw := m.connWidth()
	rightW := m.w - cw
	edOuter := m.paneH() - (m.res.h + 2)

	switch {
	case msg.X < cw: // connections
		m.focus = focusConns
		row := y - 1
		if row < 0 || row >= len(m.cfg.Connections) {
			return nil
		}
		double := row == m.lastClickRow && row == m.connSel && time.Since(m.lastClickAt) < 400*time.Millisecond
		m.connSel, m.lastClickRow, m.lastClickAt = row, row, time.Now()
		if double {
			m.lastClickAt = time.Time{}
			return m.connect(row)
		}

	case y < edOuter: // SQL editor
		btn := cw + rightW - 2 - ansi.StringWidth(closeButton)
		if y == 0 && msg.X >= btn && msg.X < btn+ansi.StringWidth(closeButton) {
			return m.closeTabByMouse()
		}
		m.focus = focusEditor
		if y >= 1 && y < edOuter-1 && msg.X > cw && msg.X < cw+rightW-1 {
			m.ed.ClickAt(msg.X-cw-1, y-1)
		}

	default: // results
		m.focus = focusResults
		m.res.clickRow(y - edOuter - 1)
	}
	return nil
}

// closeTabByMouse is the [x] button: like ^K Q, but closing the last tab
// asks first, since a stray click shouldn't end the program.
func (m *Model) closeTabByMouse() tea.Cmd {
	if len(m.tabs) > 1 {
		return m.closeTab()
	}
	m.ask("Close the last SQL window and quit sqlcli? (y/N)", "", false, func(v string) tea.Cmd {
		if strings.EqualFold(strings.TrimSpace(v), "y") {
			return m.quit()
		}
		return nil
	})
	return nil
}

// scrollHelp scrolls the help screen; it reports false for keys that
// should close it instead.
func (m *Model) scrollHelp(key string) bool {
	switch key {
	case "up", "ctrl+p":
		m.helpTop--
	case "down", "ctrl+n":
		m.helpTop++
	case "pgup", "ctrl+u":
		m.helpTop -= m.h - 2
	case "pgdown", "ctrl+v":
		m.helpTop += m.h - 2
	case "home":
		m.helpTop = 0
	case "end":
		m.helpTop = 1 << 20
	default:
		return false
	}
	return true // View clamps helpTop
}

var runningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

// fmtDuration renders an elapsed time compactly: 12ms, 3.2s, 1m05s.
func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// recordRun remembers how the last execution went, for the SQL panel footer.
func (m *Model) recordRun(msg execDoneMsg) {
	m.runElapsed, m.runAt, m.runIsQuery = msg.elapsed, time.Now(), false
	switch {
	case msg.err != nil && (errors.Is(msg.err, context.Canceled) || strings.Contains(msg.err.Error(), "ORA-01013")):
		m.runState, m.runSummary = runCancelled, ""
	case msg.err != nil && (errors.Is(msg.err, context.DeadlineExceeded) || strings.Contains(msg.err.Error(), "DPI-1067")):
		m.runState = runFailed
		m.runSummary = fmt.Sprintf("timed out after %ds (query.timeout_seconds)", m.cfg.Query.TimeoutSeconds)
	case msg.err != nil:
		m.runState, m.runSummary = runFailed, firstLine(db.CleanError(msg.err))
	default:
		m.runState = runOK
		switch r := msg.last; {
		case len(msg.messages) > 1:
			m.runSummary = fmt.Sprintf("%d statements", len(msg.messages))
			m.runIsQuery = r != nil && r.IsQuery
		case r != nil && r.IsQuery:
			m.runSummary, m.runIsQuery = "", true
		case r != nil:
			m.runSummary = strings.TrimSuffix(r.Message, ".")
			if strings.HasPrefix(r.Message, "PL/SQL procedure successfully completed") {
				m.runSummary = "PL/SQL OK"
			}
		}
	}
}

// runFooter is the status shown in the SQL panel's bottom border.
func (m *Model) runFooter() string {
	dot := hintText.Render(" · ")
	if m.running {
		elapsed := fmtDuration(time.Since(m.runStart))
		switch {
		case m.exporting:
			return runningStyle.Render("● exporting " + elapsed)
		case m.fetching && m.res.res != nil:
			return runningStyle.Render(fmt.Sprintf("● fetching %d rows %s", len(m.res.res.Rows), elapsed))
		}
		return runningStyle.Render("● running " + elapsed)
	}
	if m.connecting {
		return runningStyle.Render("● connecting")
	}
	at := hintText.Render("at " + m.runAt.Format("15:04:05"))
	took := hintText.Render(fmtDuration(m.runElapsed))
	switch m.runState {
	case runCancelled:
		return runningStyle.Render("■ cancelled after "+fmtDuration(m.runElapsed)) + dot + at
	case runFailed:
		return errStyle.Render("✗ "+m.runSummary) + dot + took + dot + at
	case runOK:
		s := m.runSummary
		if r := m.res.res; m.runIsQuery && r != nil && r.IsQuery {
			rows := fmt.Sprintf("%d rows", len(r.Rows))
			if r.More {
				rows = fmt.Sprintf("%d+ rows", len(r.Rows))
			}
			if s != "" {
				s += ", last: " + rows
			} else {
				s = rows
			}
		}
		return okStyle.Render("✓ "+s) + dot + took + dot + at
	}
	return ""
}

// withoutTime drops the "  (12ms)" execute appends to a result message;
// the status line leaves timing to the SQL panel footer.
func withoutTime(msg string) string {
	if i := strings.LastIndex(msg, "  ("); i >= 0 && strings.HasSuffix(msg, ")") {
		return msg[:i]
	}
	return msg
}
