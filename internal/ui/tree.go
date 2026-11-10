package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/db"
)

// The object tree (bottom of the connections panel) shows the current
// tab's schema: object groups, objects, and columns / package members.

// treeGroups is the order of the groups and their titles.
var treeGroups = []struct{ typ, title string }{
	{"TABLE", "Tables"}, {"VIEW", "Views"}, {"MATERIALIZED VIEW", "Materialized views"},
	{"PACKAGE", "Packages"}, {"PROCEDURE", "Procedures"}, {"FUNCTION", "Functions"},
	{"TYPE", "Types"}, {"SEQUENCE", "Sequences"}, {"SYNONYM", "Synonyms"},
	{"TRIGGER", "Triggers"}, {"INDEX", "Indexes"},
}

type treeNode struct {
	label    string // shown text (an object's or column's name)
	extra    string // dim text after the label (column type, member kind)
	objType  string // object type for objects and groups
	name     string // object name ("" for groups and children)
	parent   *treeNode
	children []*treeNode
	open     bool
	loading  bool
	loaded   bool
	invalid  bool
}

// expandable reports whether a node has (or can load) children.
func (n *treeNode) expandable() bool {
	if n.name == "" {
		return n.parent == nil // a group
	}
	switch n.objType {
	case "TABLE", "VIEW", "MATERIALIZED VIEW", "PACKAGE":
		return n.parent != nil && n.parent.parent == nil // an object, not a child
	}
	return false
}

// objTree is the tree of one connection's schema.
type objTree struct {
	groups  []*treeNode
	loading bool
	err     string
	sel     int // index into visible()
	top     int
	filter  string         // object name filter ("" = all objects)
	match   *regexp.Regexp // the compiled filter
}

// setFilter sets the object name filter: a case-insensitive part of the
// name, where * and % match any text. Groups with matches open; the first
// matching object is selected.
func (t *objTree) setFilter(f string) {
	f = strings.TrimSpace(f)
	t.filter, t.match = f, nil
	if f != "" {
		pat := regexp.QuoteMeta(f)
		pat = strings.NewReplacer(`\*`, ".*", "%", ".*").Replace(pat)
		t.match = regexp.MustCompile("(?i)" + pat)
		for _, g := range t.groups {
			g.open = t.count(g) > 0
		}
	}
	nodes, _ := t.visible()
	t.sel, t.top = 0, 0
	for i, n := range nodes {
		if n.name != "" { // the first object
			t.sel = i
			break
		}
	}
}

// selectNode selects n if it is shown (else keeps the selection in range).
func (t *objTree) selectNode(n *treeNode) {
	nodes, _ := t.visible()
	for i, x := range nodes {
		if x == n {
			t.sel = i
			return
		}
	}
	t.sel = min(max(t.sel, 0), max(len(nodes)-1, 0))
}

// shows reports whether an object passes the filter.
func (t *objTree) shows(n *treeNode) bool {
	return t.match == nil || n.name == "" || t.match.MatchString(n.name)
}

// count is the number of a group's objects that pass the filter.
func (t *objTree) count(g *treeNode) int {
	c := 0
	for _, n := range g.children {
		if t.shows(n) {
			c++
		}
	}
	return c
}

// visible returns the nodes shown, in order, with their depth.
func (t *objTree) visible() ([]*treeNode, []int) {
	var nodes []*treeNode
	var depth []int
	var walk func(ns []*treeNode, d int)
	walk = func(ns []*treeNode, d int) {
		for _, n := range ns {
			if d == 0 && t.match != nil && t.count(n) == 0 || d == 1 && !t.shows(n) {
				continue // filtered out (a group with no matching objects, or an object)
			}
			nodes = append(nodes, n)
			depth = append(depth, d)
			if n.open {
				walk(n.children, d+1)
			}
		}
	}
	walk(t.groups, 0)
	return nodes, depth
}

func (t *objTree) selected() *treeNode {
	nodes, _ := t.visible()
	if t.sel < 0 || t.sel >= len(nodes) {
		return nil
	}
	return nodes[t.sel]
}

// ---- loading ----

type treeObjectsMsg struct {
	conn string
	objs []db.SchemaObject
	err  error
}

type treeChildrenMsg struct {
	conn, objType, name string
	children            []*treeNode
	err                 error
}

// tree returns the object tree of the active tab's connection, or nil.
func (m *Model) tree() *objTree {
	if m.conn == nil || m.trees == nil {
		return nil
	}
	return m.trees[m.conn.Name]
}

// loadTree loads the active connection's schema objects in the background
// (reload: again, keeping nothing).
func (m *Model) loadTree(reload bool) tea.Cmd {
	if m.conn == nil {
		return nil
	}
	if m.trees == nil {
		m.trees = map[string]*objTree{}
	}
	name := m.conn.Name
	t := m.trees[name]
	if t != nil && !reload && (t.loading || t.groups != nil) {
		return nil
	}
	if t == nil || reload {
		old := t
		t = &objTree{}
		if old != nil {
			t.sel, t.top, t.filter, t.match = old.sel, old.top, old.filter, old.match
		}
		m.trees[name] = t
	}
	t.loading = true
	conn := m.conn
	return func() tea.Msg {
		objs, err := conn.SchemaObjects(context.Background())
		return treeObjectsMsg{conn: name, objs: objs, err: err}
	}
}

func (m *Model) treeObjectsLoaded(msg treeObjectsMsg) {
	t := m.trees[msg.conn]
	if t == nil {
		return
	}
	t.loading = false
	if msg.err != nil {
		t.err = db.CleanError(msg.err)
		return
	}
	byType := map[string][]db.SchemaObject{}
	for _, o := range msg.objs {
		byType[o.Type] = append(byType[o.Type], o)
	}
	t.groups = []*treeNode{}
	for _, g := range treeGroups {
		objs := byType[g.typ]
		if len(objs) == 0 {
			continue
		}
		gn := &treeNode{label: g.title, extra: fmt.Sprint(len(objs)), objType: g.typ}
		for _, o := range objs {
			gn.children = append(gn.children, &treeNode{label: o.Name, objType: o.Type, name: o.Name, parent: gn, invalid: !o.Valid})
		}
		if t.match != nil {
			gn.open = t.count(gn) > 0
		}
		t.groups = append(t.groups, gn)
	}
	nodes, _ := t.visible()
	t.sel = min(t.sel, max(len(nodes)-1, 0))
}

// loadChildren loads the columns of a table/view or the members of a package.
func (m *Model) loadChildren(n *treeNode) tea.Cmd {
	if m.conn == nil || n.loading || n.loaded {
		return nil
	}
	n.loading = true
	conn, name, typ, obj := m.conn, m.conn.Name, n.objType, n.name
	return func() tea.Msg {
		var kids []*treeNode
		var err error
		if typ == "PACKAGE" {
			var ms []db.Member
			ms, err = conn.Members(context.Background(), "", obj)
			for _, mb := range ms {
				kind := "procedure"
				if mb.IsFunction {
					kind = "function"
				}
				kids = append(kids, &treeNode{label: mb.Name, extra: kind, objType: "MEMBER"})
			}
		} else {
			var cols []db.ColumnInfo
			cols, err = conn.ColumnInfos(context.Background(), obj)
			for _, c := range cols {
				extra := c.Type
				if c.NotNull {
					extra += " NOT NULL"
				}
				kids = append(kids, &treeNode{label: c.Name, extra: extra, objType: "COLUMN"})
			}
		}
		return treeChildrenMsg{conn: name, objType: typ, name: obj, children: kids, err: err}
	}
}

func (m *Model) treeChildrenLoaded(msg treeChildrenMsg) {
	t := m.trees[msg.conn]
	if t == nil {
		return
	}
	for _, g := range t.groups {
		for _, n := range g.children {
			if n.objType == msg.objType && n.name == msg.name {
				n.loading = false
				if msg.err != nil {
					m.setStatus(db.CleanError(msg.err), true)
					return
				}
				n.loaded = true
				for _, k := range msg.children {
					k.parent = n
				}
				n.children = msg.children
				if len(n.children) == 0 {
					n.children = []*treeNode{{label: "(none)", objType: "EMPTY", parent: n}}
				}
			}
		}
	}
}

// ---- keys ----

func (m *Model) updateTree(key string) tea.Cmd {
	t := m.tree()
	switch key {
	case "esc":
		m.focusMain()
		return nil
	case "tab": // on to the results
		m.cyclePanel(1)
		return nil
	}
	if t == nil {
		return nil
	}
	nodes, _ := t.visible()
	n := t.selected()
	page := max(m.treeHeight()-3, 1)
	switch key {
	case "up", "ctrl+p", "k":
		t.sel--
	case "down", "ctrl+n", "j":
		t.sel++
	case "pgup", "ctrl+u":
		t.sel -= page
	case "pgdown", "ctrl+v":
		t.sel += page
	case "home", "g":
		t.sel = 0
	case "end", "G":
		t.sel = len(nodes) - 1
	case "enter", "right", "l", "space":
		if n == nil {
			break
		}
		if n.expandable() {
			if key == "right" && n.open {
				break
			}
			n.open = !n.open || key == "right"
			if n.open && n.name != "" {
				return m.loadChildren(n)
			}
		} else if key == "enter" {
			m.treeInsert(n)
		}
	case "left", "h":
		if n == nil {
			break
		}
		if n.open {
			n.open = false
		} else if n.parent != nil { // go to the parent
			for i, x := range nodes {
				if x == n.parent {
					t.sel = i
				}
			}
		}
	case "i":
		if n != nil {
			m.treeInsert(n)
		}
	case "d":
		if obj := treeObject(n); obj != nil {
			return m.execute([]string{"desc " + quoteIdent(obj.name)})
		}
	case "s": // select * in a new SQL tab, run at once
		if obj := treeObject(n); obj != nil {
			switch obj.objType {
			case "TABLE", "VIEW", "MATERIALIZED VIEW", "SYNONYM":
				return m.openQueryTab("select * from " + quoteIdent(obj.name) + ";")
			}
			m.setStatus("s queries tables, views and synonyms", true)
		}
	case "e": // select rowid + the columns in a new SQL tab, ready for editing
		if obj := treeObject(n); obj != nil {
			if obj.objType != "TABLE" {
				m.setStatus("e edits the rows of a table (views: s)", true)
				break
			}
			return m.editTableTab(obj.name)
		}
	case "/", "f":
		m.askTreeFilter()
		return nil
	case "x": // remove the filter
		if t.filter == "" {
			m.setStatus("No object filter set (/ sets one)", false)
			break
		}
		sel := n
		t.setFilter("")
		t.selectNode(sel)
		m.setStatus("Object filter removed", false)
	case "c": // collapse the whole tree
		group := n
		for group != nil && group.parent != nil {
			group = group.parent
		}
		for _, g := range t.groups {
			g.open = false
			for _, o := range g.children {
				o.open = false
			}
		}
		t.top = 0
		t.selectNode(group)
	case "r":
		m.setStatus("Reloading the object tree...", false)
		return m.loadTree(true)
	case "ctrl+d": // DDL of the object in a new tab
		obj := treeObject(n)
		if obj == nil {
			m.setStatus("Ctrl-D shows the DDL of an object: select one", true)
			return nil
		}
		return m.fetchDDL(obj.objType, obj.name)
	}
	nodes, _ = t.visible()
	t.sel = min(max(t.sel, 0), max(len(nodes)-1, 0))
	return nil
}

// treeObject returns the object a node belongs to (itself, or its parent
// for columns and members), or nil for groups.
func treeObject(n *treeNode) *treeNode {
	switch {
	case n == nil:
		return nil
	case n.name != "":
		return n
	case n.parent != nil && n.parent.name != "":
		return n.parent
	}
	return nil
}

// treeInsert inserts the node's name at the editor cursor: an object's
// name, a column's name, or package.member.
func (m *Model) treeInsert(n *treeNode) {
	text := ""
	switch {
	case n.objType == "MEMBER":
		text = quoteIdent(n.parent.name) + "." + quoteIdent(n.label)
	case n.objType == "COLUMN":
		text = quoteIdent(n.label)
	case n.name != "":
		text = quoteIdent(n.name)
	default:
		return
	}
	m.focusEditor()
	m.ed.InsertText(text)
	m.setStatus("Inserted "+text, false)
}

// quoteIdent quotes a name that isn't a plain upper-case identifier.
func quoteIdent(s string) string {
	for i, r := range s {
		ok := r >= 'A' && r <= 'Z' || r == '_' || r == '$' || r == '#' || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return `"` + s + `"`
		}
	}
	return s
}

// ---- layout & drawing ----

// connBoxHeight is the outer height of the connections box at the top of
// the left column; the object tree gets the rest.
func (m *Model) connBoxHeight() int {
	want := len(m.cfg.Connections) + 2
	return min(max(want, 4), max(m.paneH()/2, 4))
}

func (m *Model) treeHeight() int { return m.paneH() - m.connBoxHeight() }

var (
	treeGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	treeDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	treeBadStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// treeView draws the object tree box.
func (m *Model) treeView(w, h int) string {
	focused := m.focus == focusTree
	title := "Objects"
	if m.conn == nil {
		return box(title, hintText.Render("not connected"), w, h, focused)
	}
	title += " · " + m.conn.Name
	t := m.tree()
	switch {
	case t == nil || (t.loading && t.groups == nil):
		return box(title, hintText.Render("loading…"), w, h, focused)
	case t.err != "":
		return box(title, treeBadStyle.Render(t.err), w, h, focused)
	case len(t.groups) == 0:
		return box(title, hintText.Render("no objects"), w, h, focused)
	}
	nodes, depth := t.visible()
	vis := max(h-2, 1)
	filterLine := ""
	if t.filter != "" || m.mode == modePrompt && m.prompt.onChange != nil {
		vis = max(vis-1, 1) // the last line shows the filter
		n := 0
		for _, g := range t.groups {
			n += t.count(g)
		}
		word := "matches"
		if n == 1 {
			word = "match"
		}
		filterLine = ansi.Truncate(treeGroupStyle.Render("/ ")+t.filter+treeDimStyle.Render(fmt.Sprintf("  %d %s", n, word)), w-2, "…")
	}
	if len(nodes) == 0 {
		return box(title, strings.Repeat("\n", vis-1)+hintText.Render("no match")+"\n"+filterLine, w, h, focused)
	}
	t.sel = min(max(t.sel, 0), len(nodes)-1)
	if t.sel < t.top {
		t.top = t.sel
	}
	if t.sel >= t.top+vis {
		t.top = t.sel - vis + 1
	}
	iw := w - 2
	var lines []string
	for i := t.top; i < len(nodes) && i < t.top+vis; i++ {
		n := nodes[i]
		mark := "  "
		if n.expandable() {
			mark = "▸ "
			if n.open {
				mark = "▾ "
			}
			if n.loading {
				mark = "… "
			}
		}
		label := n.label
		if n.invalid {
			label += " ✗"
		}
		extra := n.extra
		if n.parent == nil && t.match != nil { // a group: matching / all objects
			extra = fmt.Sprintf("%d/%d", t.count(n), len(n.children))
		}
		plain := strings.Repeat("  ", depth[i]) + mark + label
		if extra != "" {
			plain += "  " + extra
		}
		plain = ansi.Truncate(plain, iw, "…")
		var line string
		switch {
		case i == t.sel && focused:
			line = connSelStyle.Render(plain + strings.Repeat(" ", max(iw-ansi.StringWidth(plain), 0)))
		case i == t.sel:
			line = selDimStyle.Render(plain + strings.Repeat(" ", max(iw-ansi.StringWidth(plain), 0)))
		default:
			head := strings.Repeat("  ", depth[i]) + mark
			body := label
			switch {
			case n.parent == nil:
				body = treeGroupStyle.Render(label)
			case n.invalid:
				body = treeBadStyle.Render(label)
			}
			line = ansi.Truncate(head+body, iw, "…")
			if extra != "" && ansi.StringWidth(line) < iw {
				line += ansi.Truncate(treeDimStyle.Render("  "+extra), iw-ansi.StringWidth(line), "…")
			}
		}
		lines = append(lines, line)
	}
	if filterLine != "" {
		for len(lines) < vis {
			lines = append(lines, "")
		}
		lines = append(lines, filterLine)
	}
	return box(title, strings.Join(lines, "\n"), w, h, focused)
}

// treeClick selects the node at content line y (0 = first visible) and
// toggles it on a double click.
func (m *Model) treeClick(y int, double bool) tea.Cmd {
	m.focus = focusTree
	t := m.tree()
	if t == nil {
		return nil
	}
	nodes, _ := t.visible()
	i := t.top + y
	if y < 0 || i >= len(nodes) {
		return nil
	}
	t.sel = i
	if double {
		return m.updateTree("enter")
	}
	return nil
}

// ---- DDL of an object (Ctrl-D) ----

type ddlMsg struct {
	conn, objType, name, text string
	err                       error
}

// fetchDDL reads an object's DDL in the background; it opens in a new tab.
func (m *Model) fetchDDL(objType, name string) tea.Cmd {
	if m.conn == nil {
		return nil
	}
	m.setStatus(fmt.Sprintf("Reading the DDL of %s %s...", strings.ToLower(objType), name), false)
	conn := m.conn
	return func() tea.Msg {
		text, err := conn.DDL(context.Background(), objType, name)
		return ddlMsg{conn: conn.Name, objType: objType, name: name, text: text, err: err}
	}
}

// ddlLoaded opens a new tab on the same connection with the DDL.
func (m *Model) ddlLoaded(msg ddlMsg) tea.Cmd {
	if msg.err != nil {
		m.setStatus(fmt.Sprintf("DDL of %s: %s", msg.name, db.CleanError(msg.err)), true)
		return nil
	}
	cmd := m.openTab() // connects a new session to the same connection
	m.ed.SetText(strings.TrimSuffix(msg.text, "\n"))
	m.ddlName = msg.objType + " " + msg.name
	m.setStatus(fmt.Sprintf("DDL of %s %s (from DBMS_METADATA)", strings.ToLower(msg.objType), msg.name), false)
	return cmd
}

// askTreeFilter edits the object tree's name filter on the bottom line; the
// tree follows as you type. Enter keeps it, an empty filter shows all
// objects, Esc goes back to the previous filter.
func (m *Model) askTreeFilter() {
	t := m.tree()
	if t == nil || t.groups == nil {
		m.setStatus("The object tree is not loaded yet", true)
		return
	}
	old := t.filter
	m.ask("Filter objects (part of the name, * = any text; empty = all):", old, false, func(v string) tea.Cmd {
		t.setFilter(v)
		if t.filter == "" {
			m.setStatus("Object filter cleared", false)
		} else {
			m.setStatus("Object filter: "+t.filter, false)
		}
		return nil
	})
	m.prompt.onChange = func(v string) { t.setFilter(v) }
	m.prompt.onCancel = func() { t.setFilter(old) }
}

// ---- s / e: a new SQL tab with a query of the object ----

// openQueryTab opens a new SQL tab on the current connection with sql and
// runs it as soon as the tab's session is connected.
func (m *Model) openQueryTab(sql string) tea.Cmd {
	if m.conn == nil {
		m.setStatus("Not connected", true)
		return nil
	}
	if !m.showSQL || !m.showResults { // the new tab shows both
		m.showSQL, m.showResults = true, true
	}
	cmd := m.openTab()
	m.ed.SetText(sql)
	m.ed.SetCursor(0, 0)
	m.runOnConnect = true
	return cmd
}

type editColsMsg struct {
	tab   int
	table string
	cols  []string
	err   error
}

// editTableTab reads the table's columns, then opens a tab with
// "select rowid, col1, ... from table" (editable: u, i, d) and runs it.
func (m *Model) editTableTab(table string) tea.Cmd {
	if m.conn == nil {
		m.setStatus("Not connected", true)
		return nil
	}
	conn, id := m.conn, m.id
	m.setStatus("Reading the columns of "+table+"...", false)
	return func() tea.Msg {
		infos, err := conn.ColumnInfos(context.Background(), table)
		msg := editColsMsg{tab: id, table: table, err: err}
		for _, c := range infos {
			msg.cols = append(msg.cols, c.Name)
		}
		return msg
	}
}

func (m *Model) editColsLoaded(msg editColsMsg) tea.Cmd {
	if msg.err != nil {
		m.setStatus(db.CleanError(msg.err), true)
		return nil
	}
	if len(msg.cols) == 0 {
		m.setStatus("No columns found for "+msg.table, true)
		return nil
	}
	return m.openQueryTab(editTableSQL(msg.table, msg.cols))
}

// editTableSQL is the query e opens: the ROWID first, then every column.
func editTableSQL(table string, cols []string) string {
	var b strings.Builder
	b.WriteString("select rowid")
	for _, c := range cols {
		b.WriteString(",\n       " + quoteIdent(c))
	}
	b.WriteString("\n  from " + quoteIdent(table) + ";")
	return b.String()
}
