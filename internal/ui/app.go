// Package ui is the Bubble Tea front end of sqlcli.
package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/complete"
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
	modeConnView
	modeSessions
)

type prompt struct {
	label    string
	input    []rune
	cur      int
	mask     bool
	files    bool // Tab completes file names
	onSubmit func(string) tea.Cmd

	// Tab cycling through several matching file names
	cycleDir string
	cycle    []string
	cycleIdx int
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
	file       string         // file the SQL was last saved to (^K D)
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

	prompt      prompt
	connSel     int
	showConns   bool   // connections panel visible (F2)
	showResults bool   // results panel visible (F3)
	showSQL     bool   // SQL panel visible (F4)
	lastExport  string // file name offered by the next export

	catalogs map[string]*catalog // completion data per connection name

	helpTop      int       // first visible line of the help screen
	recTop       int       // first visible line of the record view
	kPrefix      bool      // ^K pressed outside the editor
	drag         dragState // mouse selection in progress
	mouseBlock   bool      // the editor block was made by dragging (a click clears it)
	lastClickAt  time.Time // for double-click on a connection
	lastClickRow int

	sessList     []history.SessionInfo // F10 session window
	sessSel      int
	sessionName  string // last saved or loaded session, offered when saving
	startSession string // session to load at start (-s)
	exitMsg      string // shown after the program ends

	histEntries []history.Entry
	histSel     int
	histFilter  []rune

	quitting bool
}

// New builds the UI model. initialConn, if non-empty, is connected at start.
func New(cfg *config.Config, hist *history.Store, initialConn string) *Model {
	m := &Model{cfg: cfg, hist: hist, showConns: cfg.UI.ShowConnections, showResults: true, showSQL: true}
	SetSelectionColor(cfg.UI.SelectionColor)
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
	m.focusEditor()
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
		m.focusMain()
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

// StartWithSession makes the program load a saved session when it starts,
// instead of connecting a single tab. The connections panel starts hidden
// (F2 shows it): the session brings its own connections.
func (m *Model) StartWithSession(name string) {
	m.startSession = name
	m.showConns = false
	m.focus = focusEditor
	m.status = ""
}

func (m *Model) Init() tea.Cmd {
	if m.startSession != "" {
		return m.loadSession(m.startSession)
	}
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
	ddl      bool // a statement changed the schema (reload completion data)
	last     *db.Result
	messages []string
	output   []string // DBMS_OUTPUT of all statements
	compile  []string // compilation errors (object line, then "LINE/COL text" lines)
	err      error
	elapsed  time.Duration
}

type fetchMsg struct {
	tab  int
	rows [][]string
	done bool
	err  error
}

type objectsMsg struct {
	conn string
	objs []db.ObjectInfo
	err  error
}

type membersMsg struct {
	conn    string
	ref     complete.TableRef
	members []db.Member
	err     error
}

type columnsMsg struct {
	conn string
	ref  complete.TableRef
	cols []string
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
		m.setStatus("Roll back (F8) or commit (F9) the open transaction before switching connections", true)
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
		out := execDoneMsg{tab: id, ddl: changesSchema(stmts)}
		start := time.Now()
		for i, s := range stmts {
			t0 := time.Now()
			res, err := conn.Execute(ctx, s, maxRows)
			if res != nil {
				out.output = append(out.output, res.Output...)
				if len(res.Errors) > 0 {
					head := res.Message
					if len(stmts) > 1 {
						head = fmt.Sprintf("[%d] %s", i+1, head)
					}
					out.compile = append(out.compile, head)
					for _, e := range res.Errors {
						out.compile = append(out.compile, "  "+e)
					}
				}
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

// askFile is ask for a file name: Tab completes it.
func (m *Model) askFile(label, initial string, onSubmit func(string) tea.Cmd) {
	m.ask(label, initial, false, onSubmit)
	m.prompt.files = true
}

func (m *Model) updatePrompt(key, text string) tea.Cmd {
	p := &m.prompt
	if key == "tab" && p.files {
		m.completeFileName()
		return nil
	}
	p.cycle = nil // any other key ends Tab cycling
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
				m.focusEditor()
			}
			return m.loadObjects(false)
		})

	case execDoneMsg:
		return m, m.onTab(msg.tab, func() tea.Cmd { return m.execDone(msg) })

	case fetchMsg:
		return m, m.onTab(msg.tab, func() tea.Cmd { return m.fetchDone(msg) })

	case objectsMsg:
		if cat := m.catalogs[msg.conn]; cat != nil {
			cat.loading = false
			if msg.err == nil {
				cat.setObjects(msg.objs)
			}
		}
		return m, nil

	case membersMsg:
		if cat := m.catalogs[msg.conn]; cat != nil {
			delete(cat.memLoading, msg.ref)
			items := []complete.Item{}
			for _, mb := range msg.members {
				kind := complete.KindProcedure
				if mb.IsFunction {
					kind = complete.KindFunction
				}
				items = append(items, complete.Item{Text: mb.Name, Kind: kind})
			}
			if msg.err == nil {
				cat.members[msg.ref] = items
			}
		}
		if m.conn != nil && m.conn.Name == msg.conn && m.focus == focusEditor && m.mode == modeNormal {
			return m, m.completeNow(false)
		}
		return m, nil

	case columnsMsg:
		if cat := m.catalogs[msg.conn]; cat != nil {
			delete(cat.colLoading, msg.ref)
			if msg.err == nil {
				cat.cols[msg.ref] = msg.cols
			}
		}
		// refresh an open list (or one waiting for these columns after a '.')
		if m.conn != nil && m.conn.Name == msg.conn && m.focus == focusEditor && m.mode == modeNormal {
			return m, m.completeNow(false)
		}
		return m, nil

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

	case tea.MouseMotionMsg:
		return m, m.dragTo(msg.X, msg.Y)

	case tea.MouseReleaseMsg:
		return m, m.dragEnd()

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

func (m *Model) execDone(msg execDoneMsg) (cmd tea.Cmd) {
	m.running = false
	m.cancel = nil
	m.recordRun(msg)
	if msg.ddl && msg.err == nil { // new or changed objects: reload completion data
		reload := m.loadObjects(true)
		defer func() { cmd = tea.Batch(cmd, reload) }()
	}
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
	m.res.compile = msg.compile
	m.res.textMode = m.res.textMode || len(msg.compile) > 0
	if len(msg.compile) > 0 && msg.err == nil {
		m.setStatus(msg.compile[0], true)
	}
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
	case modeConnView:
		m.mode = modeNormal
		return nil
	case modeSessions:
		return m.updateSessions(key)
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
			m.focusMain()
		}
		m.layout()
		return nil
	case "f3":
		m.toggleResults()
		return nil
	case "f4":
		m.toggleSQL()
		return nil
	case "f5", "ctrl+enter", "alt+enter":
		if !m.ed.HasBlock() && db.InComment(m.ed.Text(), m.ed.CursorRow(), m.ed.CursorCol()) {
			m.setStatus("The cursor is in a comment: nothing to run", true)
			return nil
		}
		return m.execute(m.currentStatements())
	case "f7":
		return m.execute(db.SplitStatements(m.ed.Text()))
	case "f6":
		m.openHistory()
		return nil
	case "f10": // session window (load / save / delete); also ^K E
		m.openSessions()
		return nil
	case "ctrl+s": // save the open tabs as a session
		if m.hist == nil {
			m.setStatus("Sessions are not available (no history database)", true)
			return nil
		}
		if m.sessionName != "" { // a session is loaded: save it again, no questions
			if err := m.saveSession(m.sessionName); err != nil {
				m.setStatus("Saving session: "+err.Error(), true)
			} else {
				m.setStatus(fmt.Sprintf("Session %q saved: %d tab(s)", m.sessionName, len(m.tabs)), false)
			}
			return nil
		}
		m.askSaveSession(modeNormal)
		return nil
	case "f8", "f9": // F8 rollback, F9 commit
		if m.conn == nil || m.running {
			return nil
		}
		commit := key == "f9"
		var err error
		if commit {
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
		} else if commit {
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
		m.res.rangeOn = false // a key ends a mouse selection
		switch key {
		case "esc", "tab":
			m.focusEditor()
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

	// editor: the completion list takes its keys first
	if m.completionOn() && !m.ed.PrefixPending() {
		if m.ed.PopupOpen() {
			switch key {
			case "up", "ctrl+p":
				m.ed.PopupMove(-1)
				return nil
			case "down", "ctrl+n":
				m.ed.PopupMove(1)
				return nil
			case "pgup":
				m.ed.PopupMove(-8)
				return nil
			case "pgdown":
				m.ed.PopupMove(8)
				return nil
			case "tab":
				m.acceptCompletion()
				return nil
			case "enter":
				if m.acceptCompletion() {
					return nil
				}
			case "esc":
				m.ed.ClosePopup()
				return nil
			}
		}
		if key == "ctrl+space" || key == "ctrl+@" {
			return m.completeNow(true)
		}
	}
	if key == "esc" && !m.ed.PrefixPending() {
		return nil
	}
	action := m.ed.HandleKey(key, text)
	if action != editor.ActionNone {
		m.ed.ClosePopup()
	}
	switch action {
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
	case editor.ActionSessions:
		m.openSessions()
	case editor.ActionLoadFile:
		m.askLoadSQL()
	case editor.ActionCopyOtherBlock:
		m.blockFromOtherTab(false)
	case editor.ActionMoveOtherBlock:
		m.blockFromOtherTab(true)
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
		m.askSaveSQL()
	case editor.ActionWriteBlock:
		m.askFile("Write block to file:", "", func(v string) tea.Cmd {
			m.writeFile(v, m.ed.BlockText())
			return nil
		})
	case editor.ActionInsertFile:
		m.askFile("Insert file:", "", func(v string) tea.Cmd {
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
	if m.completionOn() && m.mode == modeNormal && m.focus == focusEditor {
		// typing a word (or '.') opens or refines the list; other keys close it
		typed := text != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+")
		if m.ed.PopupOpen() || typed && (isIdentRune(text) || text == ".") {
			return m.completeNow(false)
		}
		m.ed.ClosePopup()
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
				return m.finishQuit()
			}
			return nil
		})
		return nil
	}
	return m.finishQuit()
}

// finishQuit ends the program, first saving the current session when
// ui.save_session_on_exit is on.
func (m *Model) finishQuit() tea.Cmd {
	if m.cfg.UI.SaveSessionOnExit && m.sessionName != "" && m.hist != nil {
		if err := m.saveSession(m.sessionName); err != nil {
			m.exitMsg = fmt.Sprintf("warning: session %q not saved: %v", m.sessionName, err)
		} else {
			m.exitMsg = fmt.Sprintf("session %q saved", m.sessionName)
		}
	}
	m.quitting = true
	return tea.Quit
}

// ExitMessage is printed by main after the screen is closed (e.g. that the
// session was saved), or "".
func (m *Model) ExitMessage() string { return m.exitMsg }

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
	case "e":
		if n > 0 {
			m.editConnectionWizard(m.connSel)
		}
	case "v":
		if n > 0 {
			m.mode = modeConnView
		}
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
		m.focusEditor()
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
	case "enter", "ctrl+r", "tab":
		if len(m.histEntries) == 0 {
			return nil
		}
		s := m.histEntries[m.histSel].SQL
		m.mode = modeNormal
		m.focusEditor()
		m.ed.ClosePopup()
		switch key {
		case "ctrl+r": // replace the panel's text
			m.ed.SetText(s)
		case "tab": // insert at the cursor
			m.ed.InsertText(s)
		default: // add at the end, as a statement of its own
			m.ed.AppendStatement(s)
			m.setStatus("Added to the end of the SQL panel (F5 runs it)", false)
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
	if m.file != "" {
		t += " · " + filepath.Base(m.file)
	}
	if m.ed.PrefixPending() {
		t += "  ^K-"
	}
	return t
}

// tabBarH is the height of the tab bar: shown only with several tabs.
// tabBarH is the height of the top line: shown with several tabs or when a
// session is loaded (its name sits in the top-left corner).
func (m *Model) tabBarH() int {
	if len(m.tabs) > 1 || m.sessionName != "" {
		return 1
	}
	return 0
}

var sessionStyle = lipgloss.NewStyle().Background(lipgloss.Color("130")).Foreground(lipgloss.Color("255")).Bold(true)

// sessionLabel is the session name shown in the top-left corner, or "".
func (m *Model) sessionLabel() string {
	if m.sessionName == "" {
		return ""
	}
	return " " + m.sessionName + " "
}

// paneH is the height available to the panes.
func (m *Model) paneH() int { return m.h - 2 - m.tabBarH() } // status + key hints

// paneHeights returns the outer heights of the SQL and results panels;
// a hidden panel gets 0 and the other one the whole height.
func (m *Model) paneHeights() (sqlH, resH int) {
	avail := m.paneH()
	switch {
	case !m.showResults:
		return avail, 0
	case !m.showSQL:
		return 0, avail
	}
	sqlH = max(avail*45/100, 5)
	return sqlH, max(avail-sqlH, 4)
}

func (m *Model) layout() {
	rightW := m.w - m.connWidth()
	sqlH, resH := m.paneHeights()
	if sqlH == 0 { // hidden: keep a usable size for cursor logic
		sqlH = m.paneH()
	}
	if resH == 0 {
		resH = m.paneH()
	}
	for _, t := range m.tabs {
		t.ed.SetSize(rightW-2, max(sqlH-2, 1))
		t.res.w, t.res.h = rightW-2, max(resH-2, 1)
	}
}

// focusEditor focuses the SQL editor, showing the SQL panel if hidden.
func (m *Model) focusEditor() {
	m.focus = focusEditor
	if !m.showSQL {
		m.showSQL = true
		m.layout()
	}
}

// focusMain focuses the editor, or the results when the SQL panel is hidden.
func (m *Model) focusMain() {
	if m.showSQL {
		m.focus = focusEditor
	} else {
		m.focus = focusResults
	}
}

// toggleResults hides the results panel or shows (and focuses) it. Hiding
// it while the SQL panel is hidden brings the SQL panel back.
func (m *Model) toggleResults() {
	if m.showResults {
		m.showResults = false
		m.showSQL = true
		if m.focus == focusResults {
			m.focus = focusEditor
		}
	} else {
		m.showResults = true
		m.focus = focusResults
	}
	m.layout()
}

// toggleSQL hides the SQL panel or shows (and focuses) it. Hiding it while
// the results panel is hidden brings the results back.
func (m *Model) toggleSQL() {
	if m.showSQL {
		m.showSQL = false
		m.showResults = true
		m.ed.ClosePopup()
		if m.focus == focusEditor {
			m.focus = focusResults
		}
	} else {
		m.showSQL = true
		m.focus = focusEditor
	}
	m.layout()
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
	if l := m.sessionLabel(); l != "" {
		parts = append(parts, sessionStyle.Render(l))
	}
	if len(m.tabs) == 1 && len(parts) > 0 {
		return ansi.Truncate(parts[0], m.w, "…") // session name only
	}
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
	if len(m.tabs) == 1 {
		return -1 // only the session name is shown
	}
	pos := 0
	if l := m.sessionLabel(); l != "" {
		pos = ansi.StringWidth(l) + 1 // session name and separator
	}
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
	case modeConnView:
		v.SetContent(m.connView())
		return v
	case modeSessions:
		v.SetContent(m.sessionsView())
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
		cl = append(cl, "", hintText.Render("Enter connect"), hintText.Render("a add  e edit"), hintText.Render("v view  d delete"))
	}
	avail := m.paneH()
	left := box("Connections", strings.Join(cl, "\n"), cw, avail, m.focus == focusConns)

	edH := m.ed.View()
	edTitle := m.sqlTitle()
	sqlH, resH := m.paneHeights()
	var panels []string
	if sqlH > 0 {
		panels = append(panels, boxFull(edTitle, closeButton, m.runFooter(), edH, rightW, sqlH, m.focus == focusEditor))
	}
	if resH > 0 {
		resTitle := "Results"
		if s := m.res.summary(); s != "" {
			resTitle += "  " + s
		}
		panels = append(panels, box(resTitle, m.res.view(m.focus == focusResults), rightW, resH, m.focus == focusResults))
	}
	right := lipgloss.JoinVertical(lipgloss.Left, panels...)
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
		msg = "^K-  (X quit, Q close tab, O new tab, N/P next/previous tab, E sessions, D save / I load SQL, H help)"
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
	hints := [][2]string{{"F1", "help"}, {"F2", "conns"}, {"F3", "results"}, {"F4", "sql"}, {"F5", "run"}, {"F6", "history"},
		{"F7", "run all"}, {"F8", "rollback"}, {"F9", "commit"}, {"F10", "sessions"}, {"^K D/I", "save/load sql"}, {"^K O/N/P/Q", "tabs"}, {"^K X", "quit"}}
	var parts []string
	for _, h := range hints {
		parts = append(parts, hintKey.Render(h[0])+" "+hintText.Render(h[1]))
	}
	// the version sits at the right end; the hints give way first
	ver := hintText.Render("sqlcli " + version.Version + " ")
	room := max(m.w-ansi.StringWidth(ver), 0)
	keys := ansi.Truncate(" "+strings.Join(parts, "  "), max(room-1, 0), "…")
	return keys + strings.Repeat(" ", max(room-ansi.StringWidth(keys), 0)) + ver
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
	hint := " " + hintKey.Render("Enter") + hintText.Render(" add at the end  ") + hintKey.Render("Tab") + hintText.Render(" insert at cursor  ") + hintKey.Render("^R") + hintText.Render(" replace all  ") +
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
 F3 / F4     show / hide results / SQL     ^Z / ^X                word left / right
 Esc         back to editor                ^U / ^V                page up / down
 F5          run statement at cursor       ^K U / ^K V            top / bottom of buffer
             (or the marked block)         ^K L                   go to line
 F7          run whole buffer as script    ^D                     delete char
 F6          SQL history (SQLite)          ^Y                     delete line
 F8 / F9     rollback / commit             ^W / ^O                delete word right / left
 F10, ^K E   sessions (load / save)        ^J                     delete to end of line
 ^S          save session                  ^_ / ^^                undo / redo
 Ctrl-C      cancel running query / quit   ^K B / ^K K            mark block begin / end
 ^K X        quit                          ^K C / ^K M / ^K Y     copy / move / delete block
                                           ^K A                   clear block marks
 CONNECTIONS PANE                          ^K F / ^L              find / find next
 Enter       connect this tab              ^K D                   save buffer to file
 a / e / d   add / edit / delete           ^K R                   insert file
 v           view connection settings      ^K W                   write block to file
                                           ^K I                   load file (replace text)

 RESULTS PANE                              RECORD VIEW (Enter on a row)
 arrows, PgUp/PgDn, wheel   scroll         ↑↓ / Home End          previous, next / first, last
 End         fetch all rows, last row      PgUp / PgDn            ± record_page records
 Ctrl-C      stop fetching                 Shift-↑↓, wheel        scroll a long record
 Enter       show row as record
 o           grid / DBMS_OUTPUT
 e           export to .csv/.tsv/.json

 TABS (each tab has its own editor, results and Oracle session)
 ^K O        new tab on the same connection      ^K N / ^K P   next / previous tab
 ^K Q        close tab (last tab: quit)          Ctrl-PgDn / Ctrl-PgUp, Alt-1..9   switch tab
 Enter in the connections pane connects this tab to the selected connection

 MOUSE  click a pane to focus it · click in the editor to move the cursor · double-click a connection
        to connect · click a tab to switch · [x] on the SQL panel closes the tab
        drag in the editor or results copies to the clipboard · Shift+drag: terminal selection

 COMPLETION  opens while typing (2 letters, or after '.') · Ctrl-Space opens it · ↑↓ choose · Tab/Enter insert · Esc close

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
	edOuter, _ := m.paneHeights()
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
	m.askFile("Export to (.csv, .tsv or .json):", def, func(v string) tea.Cmd {
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
	case "e":
		m.openSessions()
	case "d":
		m.askSaveSQL()
	case "i":
		m.askLoadSQL()
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
	edOuter, _ := m.paneHeights()

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
		m.focusEditor()
		if m.mouseBlock { // a plain click clears a selection made by dragging
			m.ed.ClearBlock()
			m.mouseBlock = false
		}
		if y >= 1 && y < edOuter-1 && msg.X > cw && msg.X < cw+rightW-1 {
			m.ed.ClickAt(msg.X-cw-1, y-1)
			r, c := m.ed.PosAt(msg.X-cw-1, y-1)
			m.drag = dragState{active: true, pane: focusEditor, row: r, col: c}
		}

	default: // results
		m.focus = focusResults
		m.res.rangeOn = false
		m.res.clickRow(y - edOuter - 1)
		if i, ok := m.res.indexAt(y - edOuter - 1); ok {
			m.drag = dragState{active: true, pane: focusResults, row: i}
		}
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
	case len(msg.compile) > 0:
		m.runState, m.runSummary = runFailed, msg.compile[0]
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

// ---- code completion ----

// catalog holds the completion data of one connection, shared by its tabs.
type catalog struct {
	objects    []complete.Item
	names      map[string]bool
	cols       map[complete.TableRef][]string
	colLoading map[complete.TableRef]bool
	members    map[complete.TableRef][]complete.Item // package functions/procedures
	memLoading map[complete.TableRef]bool
	loading    bool
}

func newCatalog() *catalog {
	return &catalog{
		cols: map[complete.TableRef][]string{}, colLoading: map[complete.TableRef]bool{},
		members: map[complete.TableRef][]complete.Item{}, memLoading: map[complete.TableRef]bool{},
	}
}

func (c *catalog) Members(t complete.TableRef) ([]complete.Item, bool) {
	ms, ok := c.members[t]
	return ms, ok
}

func (c *catalog) Objects() []complete.Item   { return c.objects }
func (c *catalog) HasObject(name string) bool { return c.names[name] }

func (c *catalog) Columns(t complete.TableRef) ([]string, bool) {
	cols, ok := c.cols[t]
	return cols, ok
}

func (c *catalog) setObjects(objs []db.ObjectInfo) {
	c.objects = make([]complete.Item, len(objs))
	c.names = make(map[string]bool, len(objs))
	for i, o := range objs {
		c.objects[i] = complete.Item{Text: o.Name, Kind: complete.KindFor(o.Type)}
		c.names[o.Name] = true
	}
}

func (m *Model) completionOn() bool { return bool(m.cfg.Editor.Completion) }

func isIdentRune(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#') {
			return false
		}
	}
	return s != ""
}

// changesSchema reports whether any statement is DDL that can add, drop or
// change objects or columns.
func changesSchema(stmts []string) bool {
	for _, s := range stmts {
		switch db.FirstKeyword(s) {
		case "CREATE", "DROP", "ALTER", "RENAME":
			return true
		}
	}
	return false
}

// loadObjects loads the active connection's object names in the background;
// with reload it also forgets cached columns.
func (m *Model) loadObjects(reload bool) tea.Cmd {
	if !m.completionOn() || m.conn == nil {
		return nil
	}
	if m.catalogs == nil {
		m.catalogs = map[string]*catalog{}
	}
	name := m.conn.Name
	cat := m.catalogs[name]
	if cat == nil || reload {
		cat = newCatalog()
		m.catalogs[name] = cat
	} else if cat.loading || cat.objects != nil {
		return nil // loaded already, or on its way
	}
	cat.loading = true
	conn := m.conn
	return func() tea.Msg {
		objs, err := conn.Objects(context.Background())
		return objectsMsg{conn: name, objs: objs, err: err}
	}
}

// completeNow computes candidates at the cursor and shows or updates the
// list. force (Ctrl-Space) shows it even before two letters are typed.
func (m *Model) completeNow(force bool) tea.Cmd {
	var cat complete.Catalog = newCatalog()
	if m.conn != nil && m.catalogs[m.conn.Name] != nil {
		cat = m.catalogs[m.conn.Name]
	}
	lines := m.ed.Lines()
	row, col := m.ed.CursorRow(), m.ed.CursorCol()
	r := complete.Complete(lines, row, col, cat)

	var cmds []tea.Cmd
	if m.conn != nil {
		if c := m.catalogs[m.conn.Name]; c != nil {
			for _, t := range r.NeedMembers {
				if !c.memLoading[t] {
					c.memLoading[t] = true
					conn, name, ref := m.conn, m.conn.Name, t
					cmds = append(cmds, func() tea.Msg {
						ms, err := conn.Members(context.Background(), ref.Owner, ref.Name)
						return membersMsg{conn: name, ref: ref, members: ms, err: err}
					})
				}
			}
			for _, t := range r.Need {
				if !c.colLoading[t] {
					c.colLoading[t] = true
					conn, name, ref := m.conn, m.conn.Name, t
					cmds = append(cmds, func() tea.Msg {
						cols, err := conn.Columns(context.Background(), ref.Owner, ref.Name)
						return columnsMsg{conn: name, ref: ref, cols: cols, err: err}
					})
				}
			}
		}
	}

	line := []rune(lines[row])
	afterDot := r.Start > 0 && line[r.Start-1] == '.'
	switch {
	case len(r.Items) == 0:
		m.ed.ClosePopup()
	case !force && !m.ed.PopupOpen() && len([]rune(r.Prefix)) < 2 && !afterDot:
		// wait for two letters before opening on its own
	case !force && r.Prefix == "" && !afterDot:
		m.ed.ClosePopup() // the word was deleted or ended
	case !force && len(r.Items) == 1 && strings.EqualFold(r.Items[0].Text, r.Prefix):
		m.ed.ClosePopup() // already complete
	default:
		items := make([]editor.PopupItem, len(r.Items))
		for i, it := range r.Items {
			items[i] = editor.PopupItem{Text: it.Text, Kind: it.Kind}
		}
		m.ed.OpenPopup(items, r.Start)
	}
	return tea.Batch(cmds...)
}

// acceptCompletion inserts the selected candidate. It reports false (and
// closes the list) when the word is already exactly the candidate, so
// Enter can start a new line instead.
func (m *Model) acceptCompletion() bool {
	it, ok := m.ed.PopupSelected()
	if !ok {
		return false
	}
	r := complete.Complete(m.ed.Lines(), m.ed.CursorRow(), m.ed.CursorCol(), newCatalog())
	text := complete.Insert(complete.Item{Text: it.Text, Kind: it.Kind}, r.Prefix)
	m.ed.ClosePopup()
	if text == r.Prefix {
		return false
	}
	m.ed.ReplaceWord(r.Start, text)
	return true
}

// ---- editing and viewing connections ----

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// parseYesNo reads a yes/no answer; empty keeps def.
func parseYesNo(v string, def bool) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return def, true
	case "y", "yes", "true", "on", "1":
		return true, true
	case "n", "no", "false", "off", "0":
		return false, true
	}
	return false, false
}

// editConnectionWizard asks for each field of connection i, pre-filled with
// its current value, and saves the config after the last answer. Esc at
// any question cancels without changing anything.
func (m *Model) editConnectionWizard(i int) {
	orig := m.cfg.Connections[i]
	c := orig
	yn := func(label string, def bool, set func(bool), next func() tea.Cmd) {
		m.ask(label, yesNo(def), false, func(v string) tea.Cmd {
			b, ok := parseYesNo(v, def)
			if !ok {
				m.setStatus(fmt.Sprintf("%q is not yes or no; connection not changed", v), true)
				return nil
			}
			set(b)
			return next()
		})
	}
	save := func() tea.Cmd {
		m.cfg.Connections[i] = c
		if err := m.cfg.Save(); err != nil {
			m.cfg.Connections[i] = orig
			m.setStatus("Saving config: "+err.Error(), true)
			return nil
		}
		msg := fmt.Sprintf("Connection %q saved to %s", c.Name, m.cfg.Path())
		for _, t := range m.tabs {
			if t.conn != nil && t.conn.Name == orig.Name {
				msg += ". Open tabs keep the old settings until you reconnect (Enter)."
				break
			}
		}
		m.setStatus(msg, false)
		return nil
	}
	m.ask("Connection name:", orig.Name, false, func(v string) tea.Cmd {
		c.Name = strings.TrimSpace(v)
		if c.Name == "" {
			m.setStatus("Name is required; connection not changed", true)
			return nil
		}
		for j, o := range m.cfg.Connections {
			if j != i && strings.EqualFold(o.Name, c.Name) {
				m.setStatus(fmt.Sprintf("A connection named %q already exists; connection not changed", o.Name), true)
				return nil
			}
		}
		m.ask("User:", orig.User, false, func(v string) tea.Cmd {
			c.User = strings.TrimSpace(v)
			m.ask("Connect string:", orig.ConnectString, false, func(v string) tea.Cmd {
				c.ConnectString = strings.TrimSpace(v)
				m.ask("New password (empty = keep the current one):", "", true, func(v string) tea.Cmd {
					askFlags := func() tea.Cmd {
						yn("Read-only? (yes/no):", orig.ReadOnly, func(b bool) { c.ReadOnly = b }, func() tea.Cmd {
							yn("Connect as SYSDBA? (yes/no):", orig.SysDBA, func(b bool) { c.SysDBA = b }, save)
							return nil
						})
						return nil
					}
					if v != "" { // a typed password replaces an env var setting
						c.Password, c.PasswordEnv = v, ""
						return askFlags()
					}
					m.ask("Password env var (empty = none):", orig.PasswordEnv, false, func(v string) tea.Cmd {
						c.PasswordEnv = strings.TrimSpace(v)
						return askFlags()
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

// connectKind describes a connect string: TNS alias, Easy Connect or descriptor.
func connectKind(s string) string {
	switch {
	case strings.HasPrefix(strings.TrimSpace(s), "("):
		return "connect descriptor"
	case strings.ContainsAny(s, ":/"):
		return "Easy Connect"
	}
	return "TNS alias, from $TNS_ADMIN/tnsnames.ora"
}

// passwordSource says where the password comes from, never the password.
func passwordSource(c config.Connection) string {
	switch {
	case c.PasswordEnv != "":
		state := "not set"
		if _, ok := os.LookupEnv(c.PasswordEnv); ok {
			state = "set"
		}
		s := fmt.Sprintf("environment variable %s (%s)", c.PasswordEnv, state)
		if c.Password != "" {
			s += "; falls back to the password stored in the config file"
		}
		return s
	case c.Password != "":
		return "stored in the config file (hidden)"
	}
	return "none"
}

// connView shows the selected connection's settings.
func (m *Model) connView() string {
	c := m.cfg.Connections[m.connSel]
	var open []string
	for i, t := range m.tabs {
		if t.conn != nil && t.conn.Name == c.Name {
			open = append(open, fmt.Sprintf("tab %d", i+1))
		}
	}
	openIn := "not connected"
	if len(open) > 0 {
		openIn = strings.Join(open, ", ")
	}
	rows := [][2]string{
		{"Name", c.Name},
		{"User", c.User},
		{"Connect string", c.ConnectString + "  " + hintText.Render("("+connectKind(c.ConnectString)+")")},
		{"Password", passwordSource(c)},
		{"Read-only", yesNo(c.ReadOnly)},
		{"SYSDBA", yesNo(c.SysDBA)},
		{"Open in", openIn},
		{"Config file", m.cfg.Path()},
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, headerStyle.Render(fmt.Sprintf("%-14s", r[0]))+sepStyle.Render(" │ ")+r[1])
	}
	lines = append(lines, "", hintText.Render("e in the connections pane edits this connection."))
	return box("Connection "+c.Name+" — any key closes", strings.Join(lines, "\n"), m.w, m.h, true)
}

// ---- sessions (F10): save and load all SQL tabs ----

func (m *Model) openSessions() {
	if m.hist == nil {
		m.setStatus("Sessions are not available (no history database)", true)
		return
	}
	m.mode = modeSessions
	m.sessSel = 0
	m.reloadSessions()
}

func (m *Model) reloadSessions() {
	list, err := m.hist.Sessions()
	if err != nil {
		m.setStatus("Sessions: "+err.Error(), true)
	}
	m.sessList = list
	m.sessSel = min(m.sessSel, max(len(list)-1, 0))
}

func (m *Model) updateSessions(key string) tea.Cmd {
	switch key {
	case "esc", "f10", "ctrl+c", "q":
		m.mode = modeNormal
	case "up", "ctrl+p", "k":
		m.sessSel = max(m.sessSel-1, 0)
	case "down", "ctrl+n", "j":
		m.sessSel = min(m.sessSel+1, max(len(m.sessList)-1, 0))
	case "s", "ctrl+s":
		m.askSaveSession(modeSessions)
	case "enter":
		if len(m.sessList) > 0 {
			return m.askLoadSession(m.sessList[m.sessSel].Name)
		}
	case "d", "delete":
		if len(m.sessList) == 0 {
			return nil
		}
		name := m.sessList[m.sessSel].Name
		m.ask(fmt.Sprintf("Delete session %q? (y/N)", name), "", false, func(v string) tea.Cmd {
			m.mode = modeSessions
			if strings.EqualFold(strings.TrimSpace(v), "y") {
				if err := m.hist.DeleteSession(name); err != nil {
					m.setStatus(err.Error(), true)
				} else {
					m.setStatus("Deleted session "+name, false)
					if name == m.sessionName { // no longer a current session
						m.sessionName = ""
						m.layout()
					}
				}
				m.reloadSessions()
			}
			return nil
		})
	}
	return nil
}

// askSaveSession asks for a name and saves every tab under it; an empty
// name cancels and returns to mode back.
func (m *Model) askSaveSession(back mode) {
	def := m.sessionName
	if def == "" {
		def = time.Now().Format("2006-01-02 15:04")
	}
	m.ask("Save session as:", def, false, func(v string) tea.Cmd {
		name := strings.TrimSpace(v)
		if name == "" {
			m.mode = back
			return nil
		}
		save := func() tea.Cmd {
			if err := m.saveSession(name); err != nil {
				m.setStatus("Saving session: "+err.Error(), true)
			} else {
				m.setStatus(fmt.Sprintf("Session %q saved: %d tab(s)", name, len(m.tabs)), false)
			}
			return nil
		}
		if ok, _ := m.hist.SessionExists(name); ok {
			m.ask(fmt.Sprintf("Session %q exists. Overwrite? (y/N)", name), "", false, func(v string) tea.Cmd {
				if strings.EqualFold(strings.TrimSpace(v), "y") {
					return save()
				}
				m.setStatus("Session not saved", false)
				return nil
			})
			return nil
		}
		return save()
	})
}

// saveSession stores every tab: its connection, SQL text and cursor.
func (m *Model) saveSession(name string) error {
	var tabs []history.SessionTab
	for _, t := range m.tabs {
		st := history.SessionTab{SQL: t.ed.Text(), Row: t.ed.CursorRow(), Col: t.ed.CursorCol()}
		switch {
		case t.conn != nil:
			st.Connection = t.conn.Name
		case t.connecting:
			st.Connection = t.connectingTo
		}
		tabs = append(tabs, st)
	}
	if err := m.hist.SaveSession(name, m.tabIndex(m.tab), tabs); err != nil {
		return err
	}
	m.sessionName = name
	m.layout() // the top line with the session name may have appeared
	return nil
}

// askLoadSession loads a session, asking first when that would roll back
// open transactions or cancel running statements.
func (m *Model) askLoadSession(name string) tea.Cmd {
	var tx, running int
	for _, t := range m.tabs {
		if t.conn != nil && t.conn.InTransaction() {
			tx++
		}
		if t.running {
			running++
		}
	}
	if running > 0 {
		m.setStatus("A statement is running; wait for it or cancel it (Ctrl-C) before loading a session", true)
		return nil
	}
	if tx > 0 {
		m.ask(fmt.Sprintf("%d open transaction(s) will be rolled back. Load session %q? (y/N)", tx, name), "", false, func(v string) tea.Cmd {
			if strings.EqualFold(strings.TrimSpace(v), "y") {
				return m.loadSession(name)
			}
			m.mode = modeSessions
			return nil
		})
		return nil
	}
	return m.loadSession(name)
}

// loadSession replaces all tabs with the saved ones and reconnects them.
func (m *Model) loadSession(name string) tea.Cmd {
	se, err := m.hist.LoadSession(name)
	if err != nil {
		m.setStatus("Loading session: "+err.Error(), true)
		return nil
	}
	if len(se.Tabs) == 0 {
		m.setStatus("Session "+name+" has no tabs", true)
		return nil
	}
	m.CloseAll() // rolls back open transactions
	m.tabs = nil
	var cmds []tea.Cmd
	var missing []string
	for _, st := range se.Tabs {
		t := m.newTab()
		m.tab = t
		t.ed.SetText(st.SQL) // cursor at the top-left corner, scrolled to the top
		if st.Connection == "" {
			continue
		}
		if i := m.connIndex(st.Connection); i >= 0 {
			cmds = append(cmds, m.connect(i))
		} else {
			missing = append(missing, st.Connection)
			m.setStatus(fmt.Sprintf("Connection %q is not in the config; this tab is not connected", st.Connection), true)
		}
	}
	m.tab = m.tabs[min(max(se.Active, 0), len(m.tabs)-1)]
	m.mode = modeNormal
	m.focusMain()
	m.sessionName = name
	m.layout()
	msg := fmt.Sprintf("Session %q loaded: %d tab(s)", name, len(m.tabs))
	if len(missing) > 0 {
		msg += "; not in the config: " + strings.Join(missing, ", ")
	}
	m.setStatus(msg, len(missing) > 0)
	return tea.Batch(cmds...)
}

func (m *Model) sessionsView() string {
	iw := m.w - 2
	var lines []string
	for i, s := range m.sessList {
		conns := s.Connections
		if conns == "" {
			conns = "not connected"
		}
		line := fmt.Sprintf(" %-24s %s  %2d tab(s)  %s", ansi.Truncate(s.Name, 24, "…"), s.SavedAt.Format("2006-01-02 15:04"), s.Tabs, conns)
		line = ansi.Truncate(line, iw, "…")
		if i == m.sessSel {
			line = connSelStyle.Render(line + strings.Repeat(" ", max(iw-ansi.StringWidth(line), 0)))
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, hintText.Render(" No saved sessions. Press s to save the open tabs."))
	}
	title := fmt.Sprintf("Sessions (%d)", len(m.sessList))
	if m.sessionName != "" {
		title += "  current: " + m.sessionName
	}
	b := box(title, strings.Join(lines, "\n"), m.w, m.h-1, true)
	hint := " " + hintKey.Render("s / ^S") + hintText.Render(" save open tabs  ") + hintKey.Render("Enter") + hintText.Render(" load (replaces the open tabs)  ") +
		hintKey.Render("d") + hintText.Render(" delete  ") + hintKey.Render("Esc") + hintText.Render(" close")
	return b + "\n" + ansi.Truncate(hint, m.w, "")
}

// ---- saving the SQL panel to a file (^K D) ----

// askSaveSQL asks for a file name (offering the tab's last one) and saves
// the SQL panel's text to it, asking before replacing another existing file.
func (m *Model) askSaveSQL() {
	def := m.file
	if def == "" {
		def = "query.sql"
	}
	m.askFile("Save SQL to file:", def, func(v string) tea.Cmd {
		name := strings.TrimSpace(v)
		if name == "" {
			return nil
		}
		path := expandHome(name)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if _, err := os.Stat(path); err == nil && path != m.file {
			m.ask(path+" exists. Overwrite? (y/N)", "", false, func(v string) tea.Cmd {
				if strings.EqualFold(strings.TrimSpace(v), "y") {
					m.saveSQL(path)
				} else {
					m.setStatus("Not saved", false)
				}
				return nil
			})
			return nil
		}
		m.saveSQL(path)
		return nil
	})
}

// saveSQL writes the SQL panel's text to path (with a final newline).
func (m *Model) saveSQL(path string) {
	text := m.ed.Text()
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		m.setStatus("Saving SQL: "+err.Error(), true)
		return
	}
	m.file = path
	m.setStatus(fmt.Sprintf("Saved %d line(s) to %s", len(m.ed.Lines()), path), false)
}

// ---- mouse selection and the clipboard ----

// dragState is a left-button drag that started in the editor or results.
type dragState struct {
	active   bool
	moved    bool
	pane     focus
	row, col int // start: buffer position (editor) or row/line index (results)
}

// dragTo extends the selection to the pointer at screen cell x, y.
func (m *Model) dragTo(x, y int) tea.Cmd {
	if !m.drag.active || m.mode != modeNormal {
		return nil
	}
	cw := m.connWidth()
	sqlH, _ := m.paneHeights()
	y -= m.tabBarH()
	switch m.drag.pane {
	case focusEditor:
		r, c := m.ed.PosAt(max(x-cw-1, 0), min(max(y-1, 0), max(sqlH-3, 0)))
		if r == m.drag.row && c == m.drag.col && !m.drag.moved {
			return nil
		}
		m.drag.moved = true
		m.ed.SetBlock(m.drag.row, m.drag.col, r, c)
		m.ed.SetCursor(r, c)
		m.mouseBlock = true
	case focusResults:
		i, ok := m.res.indexAt(y - sqlH - 1)
		if !ok {
			return nil
		}
		m.drag.moved = true
		m.res.rangeOn, m.res.rangeA, m.res.rangeB = true, m.drag.row, i
		if !m.res.showText() {
			m.res.row = i
			m.res.clampRow()
		}
	}
	return nil
}

// dragEnd finishes a drag: the selected text is copied to the clipboard.
func (m *Model) dragEnd() tea.Cmd {
	d := m.drag
	m.drag = dragState{}
	if !d.active || !d.moved {
		return nil
	}
	var text, what string
	switch d.pane {
	case focusEditor:
		text = m.ed.BlockText()
		what = fmt.Sprintf("%d character(s)", len([]rune(text)))
	case focusResults:
		var n int
		text, n = m.res.rangeText()
		what = fmt.Sprintf("%d row(s)", n)
		if m.res.showText() {
			what = fmt.Sprintf("%d line(s)", n)
		}
	}
	if text == "" {
		return nil
	}
	m.setStatus("Copied "+what+" to the clipboard", false)
	return copyToClipboard(text)
}

// systemCopy puts text on the desktop clipboard with wl-copy, xclip or xsel
// when available. Tests replace it.
var systemCopy = func(text string) {
	var args []string
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && hasCommand("wl-copy"):
		args = []string{"wl-copy"}
	case os.Getenv("DISPLAY") != "" && hasCommand("xclip"):
		args = []string{"xclip", "-selection", "clipboard"}
	case os.Getenv("DISPLAY") != "" && hasCommand("xsel"):
		args = []string{"xsel", "--clipboard", "--input"}
	default:
		return
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// copyToClipboard copies text with the terminal's OSC 52 sequence (works in
// most terminals, also over SSH and in tmux) and with a desktop tool.
func copyToClipboard(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		systemCopy(text)
		return nil
	})
}

// maxLoadSize is the largest file ^K I loads into a SQL panel.
const maxLoadSize = 10 << 20

// askLoadSQL asks for a file and replaces the SQL panel's text with it
// (^_ undoes). The file becomes the tab's file, so ^K D saves back to it.
func (m *Model) askLoadSQL() {
	m.askFile("Load file into this SQL panel:", m.file, func(v string) tea.Cmd {
		name := strings.TrimSpace(v)
		if name == "" {
			return nil
		}
		path := expandHome(name)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		text, err := readTextFile(path)
		if err != nil {
			m.setStatus("Loading "+name+": "+err.Error(), true)
			return nil
		}
		m.focusEditor()
		m.ed.ClosePopup()
		m.ed.SetText(strings.TrimSuffix(text, "\n")) // cursor at the top-left corner
		m.mouseBlock = false
		m.file = path
		m.setStatus(fmt.Sprintf("Loaded %d line(s) from %s (^_ undoes)", len(m.ed.Lines()), path), false)
		return nil
	})
}

// readTextFile reads a text file, refusing directories, binary files and
// files larger than maxLoadSize.
func readTextFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("no such file")
		}
		return "", err
	}
	if st.IsDir() {
		return "", errors.New("it is a directory")
	}
	if st.Size() > maxLoadSize {
		return "", fmt.Errorf("file is larger than %d MB", maxLoadSize>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", errors.New("not a text file")
	}
	return string(b), nil
}

// ---- file-name completion in prompts ----

// completeFileName completes the prompt input as a path: one match is
// completed fully (directories get a "/"), several are completed to their
// common prefix and listed in the status line; Tab again cycles through them.
func (m *Model) completeFileName() {
	p := &m.prompt
	if len(p.cycle) > 0 {
		p.input = []rune(p.cycleDir + p.cycle[p.cycleIdx%len(p.cycle)])
		p.cycleIdx++
		p.cur = len(p.input)
		return
	}
	in := string(p.input)
	dir, matches := matchFiles(in)
	switch len(matches) {
	case 0:
		m.setStatus("No matching files", true)
	case 1:
		p.input = []rune(dir + matches[0])
		m.setStatus("", false)
	default:
		common := dir + commonPrefix(matches)
		m.setStatus(fileList(matches), false)
		p.cycleDir, p.cycle, p.cycleIdx = dir, matches, 0
		if common != in {
			p.input = []rune(common) // the next Tab starts cycling
		} else {
			p.input = []rune(dir + matches[0])
			p.cycleIdx = 1
		}
	}
	p.cur = len(p.input)
}

// matchFiles lists the entries matching the last path element of in. It
// returns the directory part of in (as typed, e.g. "~/sql/") and the
// matching names, sorted, directories with a trailing "/".
func matchFiles(in string) (string, []string) {
	dir, base := "", in
	if i := strings.LastIndex(in, "/"); i >= 0 {
		dir, base = in[:i+1], in[i+1:]
	}
	listDir := expandHome(dir)
	if dir == "~" || listDir == "" {
		listDir = "."
	}
	ents, err := os.ReadDir(listDir)
	if err != nil {
		return dir, nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 { // follow links to directories
			if st, err := os.Stat(filepath.Join(listDir, name)); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return dir, out
}

func commonPrefix(names []string) string {
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// fileList shows matches in the status line (Tab cycles through them).
func fileList(names []string) string {
	const show = 12
	s := strings.Join(names[:min(len(names), show)], "  ")
	if len(names) > show {
		s += fmt.Sprintf("  … (%d more)", len(names)-show)
	}
	return s + "   (Tab: next)"
}

// blockFromOtherTab copies (or moves) the most recently marked block of
// another tab to the cursor, like JOE's ^K C / ^K M across windows. The
// copied text becomes the marked block.
func (m *Model) blockFromOtherTab(move bool) {
	var src *tab
	for _, t := range m.tabs {
		if t != m.tab && t.ed.HasBlock() && (src == nil || t.ed.BlockStamp() > src.ed.BlockStamp()) {
			src = t
		}
	}
	if src == nil {
		m.setStatus("No block (mark one with ^K B and ^K K)", true)
		return
	}
	text := src.ed.BlockText()
	m.ed.InsertBlock(text)
	m.mouseBlock = false
	if move {
		src.ed.DeleteBlock()
	} else {
		src.ed.ClearBlock() // one block at a time: it is now the copy
	}
	verb := "Copied"
	if move {
		verb = "Moved"
	}
	m.setStatus(fmt.Sprintf("%s %d line(s) from tab %d", verb, strings.Count(text, "\n")+1, m.tabIndex(src)+1), false)
}
