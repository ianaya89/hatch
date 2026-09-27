package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type screen int

const (
	screenCode screen = iota
	screenConnecting
	screenPaired
	screenSelect
	screenRunning
	screenDone
	screenError
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeAdd
	modeExclude
	modeDrill
)

type group struct {
	kind     Kind
	items    []Item
	selected []bool
	open     bool
}

type row struct{ g, i int }

type connectedMsg struct {
	sess    *session
	sel     *savedSelection
	filters map[string]itemFilter
	kids    map[string]childrenReply
}
type errMsg struct{ err error }
type tickMsg time.Time
type addedMsg struct {
	rep addReply
	err error
}
type kidsMsg struct {
	id    string
	rep   childrenReply
	err   error
	drill bool
}

type model struct {
	screen    screen
	mode      mode
	home      string
	addr      string
	opts      pullOptions
	ctx       context.Context
	cancel    context.CancelFunc
	input     textinput.Model
	prompt    textinput.Model
	inputErr  string
	spin      spinner.Model
	bar       progress.Model
	sess      *session
	groups    []group
	cursor    int
	offset    int
	width     int
	height    int
	st        *runState
	err       error
	speed     float64
	lastBytes int64
	lastTick  time.Time
	vis       visual

	filter   string
	filters  map[string]itemFilter
	kids     map[string]childrenReply
	custom   []string
	restored bool
	busy     bool
	status   string
	drillID  string
	drillCur int
	drillOff int
}

func newModel(code, addr, home string, opts pullOptions) model {
	ctx, cancel := context.WithCancel(context.Background())
	in := textinput.New()
	in.Placeholder = "7-guitar-orbit"
	in.Prompt = "  code › "
	in.CharLimit = 40
	in.Focus()
	m := model{
		screen:  screenCode,
		home:    home,
		addr:    addr,
		opts:    opts,
		ctx:     ctx,
		cancel:  cancel,
		input:   in,
		prompt:  textinput.New(),
		spin:    spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(styleAccent)),
		bar:     progress.New(progress.WithGradient("#7C3AED", "#34D399"), progress.WithoutPercentage()),
		width:   80,
		height:  24,
		filters: map[string]itemFilter{},
		kids:    map[string]childrenReply{},
	}
	if code != "" {
		m.input.SetValue(code)
		m.screen = screenConnecting
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.screen == screenConnecting {
		return tea.Batch(m.spin.Tick, m.connectCmd(m.input.Value()))
	}
	return textinput.Blink
}

func (m model) connectCmd(code string) tea.Cmd {
	return func() tea.Msg {
		sess, err := connect(m.ctx, code, m.addr)
		if err != nil {
			return errMsg{err}
		}
		var sel *savedSelection
		if !m.opts.fresh {
			sel = loadSelection(m.home, sess.peer.Host)
		}
		filters, kids := restoreSelection(sess, sel)
		return connectedMsg{sess: sess, sel: sel, filters: filters, kids: kids}
	}
}

func (m model) addCmd(paths []string) tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		rep, err := sess.addPaths(paths)
		return addedMsg{rep, err}
	}
}

func (m model) kidsCmd(id string, drill bool) tea.Cmd {
	sess, patterns := m.sess, m.filters[id].patterns
	return func() tea.Msg {
		rep, err := sess.children(id, patterns)
		return kidsMsg{id: id, rep: rep, err: err, drill: drill}
	}
}

func tick() tea.Cmd { return tickEvery(150 * time.Millisecond) }

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = max(20, min(60, m.width-24))
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case connectedMsg:
		if len(msg.sess.items) == 0 {
			msg.sess.sc.Close()
			m.err = fmt.Errorf("%s has nothing to offer", msg.sess.peer.Host)
			m.screen = screenError
			return m, nil
		}
		m.sess = msg.sess
		m.filters, m.kids = msg.filters, msg.kids
		m.groups = buildGroups(msg.sess.items, msg.sel)
		if msg.sel != nil {
			m.custom = msg.sel.Custom
			m.restored = true
		}
		m.vis = newVisual(msg.sess.sc.visual)
		m.screen = screenPaired
		return m, tickEvery(visFrame)
	case errMsg:
		m.err = msg.err
		m.screen = screenError
		return m, nil
	case addedMsg:
		m.busy = false
		if msg.err != nil {
			m.status = styleErr.Render("✗ " + msg.err.Error())
			return m, nil
		}
		for _, it := range msg.rep.Items {
			m.addItem(it)
		}
		m.status = ""
		if len(msg.rep.Items) > 0 {
			m.status = styleOK.Render(fmt.Sprintf("✓ added %s", msg.rep.Items[0].Title()))
		}
		if len(msg.rep.Errors) > 0 {
			m.status = styleErr.Render("✗ " + strings.Join(msg.rep.Errors, "; "))
		}
		return m, nil
	case kidsMsg:
		m.busy = false
		if msg.err != nil {
			m.status = styleErr.Render("✗ " + msg.err.Error())
			return m, nil
		}
		m.kids[msg.id] = msg.rep
		if msg.drill {
			m.mode, m.drillID, m.drillCur, m.drillOff = modeDrill, msg.id, 0, 0
		}
		return m, nil
	case tickMsg:
		if m.screen == screenPaired {
			return m, tickEvery(visFrame)
		}
		if m.screen != screenRunning {
			return m, nil
		}
		m.updateSpeed(time.Time(msg))
		m.st.mu.Lock()
		done := m.st.finished
		m.st.mu.Unlock()
		if done {
			m.screen = screenDone
			return m, nil
		}
		return m, tick()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		switch m.screen {
		case screenCode:
			return m.updateCode(msg)
		case screenPaired:
			switch msg.String() {
			case "enter", " ", "y":
				m.screen = screenSelect
			case "q", "n", "esc":
				return m.quit()
			}
			return m, nil
		case screenSelect:
			switch m.mode {
			case modeFilter, modeAdd, modeExclude:
				return m.updatePrompt(msg)
			case modeDrill:
				return m.updateDrill(msg)
			}
			return m.updateSelect(msg)
		case screenRunning:
			if msg.String() == "q" {
				return m.quit()
			}
		case screenDone, screenError, screenConnecting:
			if s := msg.String(); s == "q" || s == "enter" || s == "esc" {
				return m.quit()
			}
		}
	}
	return m, nil
}

func (m model) quit() (tea.Model, tea.Cmd) {
	m.cancel()
	if m.sess != nil && (m.st == nil || !m.st.isFinished()) {
		m.sess.sc.Close()
	}
	return m, tea.Quit
}

func (st *runState) isFinished() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.finished
}

func (m model) updateCode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.quit()
	case "enter":
		if _, _, err := parseCode(m.input.Value()); err != nil {
			m.inputErr = err.Error()
			return m, nil
		}
		m.screen = screenConnecting
		return m, tea.Batch(m.spin.Tick, m.connectCmd(m.input.Value()))
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.inputErr = ""
	return m, cmd
}

func buildGroups(items []Item, sel *savedSelection) []group {
	var gs []group
	for _, k := range kindOrder {
		g := group{kind: k, open: k != KindRepos && k != KindConfig}
		for _, it := range items {
			if it.Kind == k {
				on := it.Default
				if sel != nil {
					on = sel.apply(it)
				}
				g.items = append(g.items, it)
				g.selected = append(g.selected, on)
			}
		}
		if len(g.items) > 0 {
			gs = append(gs, g)
		}
	}
	return gs
}

func (m *model) addItem(it Item) {
	for gi := range m.groups {
		for _, existing := range m.groups[gi].items {
			if existing.ID == it.ID {
				return
			}
		}
	}
	if !contains(m.custom, it.Label) {
		m.custom = append(m.custom, it.Label)
	}
	if len(m.groups) == 0 || m.groups[0].kind != KindCustom {
		m.groups = append([]group{{kind: KindCustom, open: true}}, m.groups...)
	}
	m.groups[0].items = append(m.groups[0].items, it)
	m.groups[0].selected = append(m.groups[0].selected, true)
	m.groups[0].open = true
}

func (m model) matches(it Item) bool {
	if m.filter == "" {
		return true
	}
	q := strings.ToLower(m.filter)
	return strings.Contains(strings.ToLower(it.Title()), q) || strings.Contains(strings.ToLower(it.Detail), q)
}

func (m model) rows() []row {
	var rs []row
	for gi, g := range m.groups {
		var items []row
		for i, it := range g.items {
			if m.matches(it) {
				items = append(items, row{gi, i})
			}
		}
		if m.filter != "" && len(items) == 0 {
			continue
		}
		rs = append(rs, row{gi, -1})
		if g.open || m.filter != "" {
			rs = append(rs, items...)
		}
	}
	return rs
}

func (m model) listHeight() int { return max(3, m.height-9) }

// itemSize reflects excludes once the peer has reported filtered children.
func (m model) itemSize(it Item) int64 {
	f, ok := m.filters[it.ID]
	kids, cached := m.kids[it.ID]
	if !ok || f.empty() || !cached {
		return it.Size
	}
	return f.size(kids)
}

func (m model) currentRow() (row, bool) {
	rs := m.rows()
	if len(rs) == 0 {
		return row{}, false
	}
	return rs[min(m.cursor, len(rs)-1)], true
}

func (m *model) startPrompt(md mode, placeholder, value string) tea.Cmd {
	m.mode = md
	m.prompt = textinput.New()
	m.prompt.Placeholder = placeholder
	m.prompt.CharLimit = 200
	m.prompt.SetValue(value)
	m.prompt.Focus()
	m.status = ""
	return textinput.Blink
}

func (m model) updateSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rs := m.rows()
	cur, ok := m.currentRow()
	switch msg.String() {
	case "q":
		return m.quit()
	case "esc":
		if m.filter != "" {
			m.filter, m.cursor, m.offset = "", 0, 0
			return m, nil
		}
		return m.quit()
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(rs)-1, m.cursor+1)
	case "pgup":
		m.cursor = max(0, m.cursor-m.listHeight())
	case "pgdown":
		m.cursor = min(len(rs)-1, m.cursor+m.listHeight())
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(rs) - 1
	case "/":
		return m, m.startPrompt(modeFilter, "type to filter", m.filter)
	case "+":
		return m, m.startPrompt(modeAdd, "~/Documents/**/*.pdf or ~/Desktop/project", "")
	case "-":
		if ok && cur.i >= 0 {
			return m, m.startPrompt(modeExclude, "*.log, /cache, node_modules", "")
		}
	case "right", "l", "tab":
		if !ok {
			break
		}
		if cur.i < 0 {
			m.groups[cur.g].open = true
			break
		}
		if !m.busy {
			m.busy = true
			m.status = styleDim.Render("listing…")
			return m, m.kidsCmd(m.groups[cur.g].items[cur.i].ID, true)
		}
	case "left", "h":
		if !ok {
			break
		}
		m.groups[cur.g].open = false
		for i, r := range m.rows() {
			if r.g == cur.g && r.i == -1 {
				m.cursor = i
			}
		}
	case " ", "x":
		if !ok {
			break
		}
		g := &m.groups[cur.g]
		if cur.i >= 0 {
			g.selected[cur.i] = !g.selected[cur.i]
			break
		}
		var visible []int
		all := true
		for i, it := range g.items {
			if m.matches(it) {
				visible = append(visible, i)
				all = all && g.selected[i]
			}
		}
		for _, i := range visible {
			g.selected[i] = !all
		}
	case "a", "n":
		for gi := range m.groups {
			for i, it := range m.groups[gi].items {
				if m.matches(it) {
					m.groups[gi].selected[i] = msg.String() == "a"
				}
			}
		}
	case "enter":
		return m.startPull()
	}
	m.scrollTo(len(rs))
	return m, nil
}

func (m *model) scrollTo(n int) {
	h := m.listHeight()
	m.cursor = max(0, min(m.cursor, n-1))
	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.mode == modeFilter {
			m.filter = ""
		}
		back := modeList
		if m.mode == modeExclude && m.drillID != "" {
			back = modeDrill
		}
		m.mode = back
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.prompt.Value())
		switch m.mode {
		case modeFilter:
			m.filter, m.mode, m.cursor, m.offset = val, modeList, 0, 0
			return m, nil
		case modeAdd:
			m.mode = modeList
			if val == "" || m.busy {
				return m, nil
			}
			m.busy = true
			m.status = styleDim.Render("resolving " + val + " on " + m.sess.peer.Host + "…")
			return m, m.addCmd(splitList(val))
		case modeExclude:
			id := m.excludeTarget()
			back := modeList
			if m.drillID != "" {
				back = modeDrill
			}
			m.mode = back
			if id == "" || val == "" {
				return m, nil
			}
			f := m.filters[id]
			if f.hidden == nil {
				f.hidden = map[string]bool{}
			}
			for _, p := range splitList(val) {
				if !contains(f.patterns, p) {
					f.patterns = append(f.patterns, p)
				}
			}
			m.filters[id] = f
			m.busy = true
			return m, m.kidsCmd(id, back == modeDrill)
		}
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	if m.mode == modeFilter {
		m.filter, m.cursor, m.offset = m.prompt.Value(), 0, 0
	}
	return m, cmd
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (m model) excludeTarget() string {
	if m.drillID != "" {
		return m.drillID
	}
	if cur, ok := m.currentRow(); ok && cur.i >= 0 {
		return m.groups[cur.g].items[cur.i].ID
	}
	return ""
}

func (m model) updateDrill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	kids := m.kids[m.drillID].Children
	h := m.listHeight() - 2
	switch msg.String() {
	case "esc", "left", "h", "q":
		m.mode, m.drillID = modeList, ""
		return m, nil
	case "up", "k":
		m.drillCur = max(0, m.drillCur-1)
	case "down", "j":
		m.drillCur = min(len(kids)-1, m.drillCur+1)
	case "pgup":
		m.drillCur = max(0, m.drillCur-h)
	case "pgdown":
		m.drillCur = min(len(kids)-1, m.drillCur+h)
	case " ", "x":
		if len(kids) == 0 {
			break
		}
		f := m.filters[m.drillID]
		if f.hidden == nil {
			f.hidden = map[string]bool{}
		}
		name := kids[m.drillCur].Name
		f.hidden[name] = !f.hidden[name]
		m.filters[m.drillID] = f
	case "a", "n":
		f := m.filters[m.drillID]
		f.hidden = map[string]bool{}
		if msg.String() == "n" {
			for _, c := range kids {
				f.hidden[c.Name] = true
			}
		}
		m.filters[m.drillID] = f
	case "-":
		return m, m.startPrompt(modeExclude, "*.log, /cache, node_modules", "")
	case "r":
		f := m.filters[m.drillID]
		f.patterns = nil
		m.filters[m.drillID] = f
		m.busy = true
		return m, m.kidsCmd(m.drillID, true)
	}
	if m.drillCur < m.drillOff {
		m.drillOff = m.drillCur
	} else if m.drillCur >= m.drillOff+h {
		m.drillOff = m.drillCur - h + 1
	}
	return m, nil
}

func (m model) startPull() (tea.Model, tea.Cmd) {
	var items []Item
	selected := map[string]bool{}
	var all []Item
	for _, g := range m.groups {
		for i, it := range g.items {
			all = append(all, it)
			selected[it.ID] = g.selected[i]
			if g.selected[i] {
				it.Size = m.itemSize(it)
				items = append(items, it)
			}
		}
	}
	if len(items) == 0 {
		return m, nil
	}
	m.opts.excludes = map[string][]string{}
	for id, f := range m.filters {
		if ex := f.excludes(); len(ex) > 0 {
			m.opts.excludes[id] = ex
		}
	}
	selectionFrom(all, selected, m.custom, m.filters).save(m.home, m.sess.peer.Host)
	m.st = newRunState(items)
	m.lastTick = time.Now()
	m.screen = screenRunning
	go pull(m.ctx, m.sess, items, m.home, m.opts, m.st)
	return m, tea.Batch(tick(), m.spin.Tick)
}

func (g group) count() int {
	n := 0
	for _, s := range g.selected {
		if s {
			n++
		}
	}
	return n
}

func (m *model) updateSpeed(now time.Time) {
	m.st.mu.Lock()
	done := m.st.doneBytes
	m.st.mu.Unlock()
	dt := now.Sub(m.lastTick).Seconds()
	if dt <= 0 {
		return
	}
	inst := float64(done-m.lastBytes) / dt
	m.speed = 0.75*m.speed + 0.25*inst
	m.lastBytes, m.lastTick = done, now
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString("\n " + styleTitle.Render(" hatch ") + "  " + m.headerText() + "\n\n")
	switch m.screen {
	case screenCode:
		b.WriteString("  Enter the code shown by " + styleBold.Render("hatch serve") + " on the other machine.\n\n")
		b.WriteString(m.input.View() + "\n")
		if m.inputErr != "" {
			b.WriteString("\n  " + styleErr.Render(m.inputErr) + "\n")
		}
		b.WriteString("\n" + styleDim.Render("  enter connect · esc quit"))
	case screenConnecting:
		b.WriteString("  " + m.spin.View() + " finding peer and pairing…\n")
	case screenError:
		b.WriteString("  " + styleErr.Render("✗ "+m.err.Error()) + "\n\n" + styleDim.Render("  q quit"))
	case screenPaired:
		b.WriteString("  " + styleOK.Render("✓ paired") + styleDim.Render(" — both machines derived the same key and draw it:") + "\n\n")
		b.WriteString(m.vis.render(time.Now().UnixMilli()) + "\n")
		b.WriteString("      " + styleDim.Render(m.vis.caption()) + "\n\n")
		b.WriteString("  Same cloud, color and spin as on " + styleBold.Render(m.sess.peer.Host) + "?\n\n")
		b.WriteString(styleDim.Render("  enter yes, continue · q no, abort"))
	case screenSelect:
		if m.mode == modeDrill || (m.mode == modeExclude && m.drillID != "") {
			b.WriteString(m.viewDrill())
		} else {
			b.WriteString(m.viewSelect())
		}
	case screenRunning:
		b.WriteString(m.viewRunning())
	case screenDone:
		b.WriteString(strings.Join(summaryLines(m.st, true), "\n") + "\n\n" + styleDim.Render("  q quit"))
	}
	return b.String()
}

func routeBadge(route string) string {
	if route == "thunderbolt" {
		return styleWarn.Render("⚡ thunderbolt")
	}
	return styleAccent.Render(route)
}

func (m model) headerText() string {
	if m.sess == nil {
		return styleDim.Render("pull")
	}
	txt := styleDim.Render("from ") + styleBold.Render(m.sess.peer.Host) + styleDim.Render(" · ") + routeBadge(m.sess.route) + styleDim.Render(" "+m.sess.addr)
	if m.st != nil {
		m.st.mu.Lock()
		z := m.st.compressing
		m.st.mu.Unlock()
		txt += styleDim.Render(map[bool]string{true: " · zstd", false: " · raw"}[z])
	}
	if m.restored && m.screen == screenSelect {
		txt += styleAccent.Render(" · last selection restored")
	}
	return txt
}

func checkbox(on bool) string {
	if on {
		return styleAccent.Render("[x]")
	}
	return styleDim.Render("[ ]")
}

func (m model) viewSelect() string {
	var b strings.Builder
	rs := m.rows()
	h := m.listHeight()
	sizeW := 10
	titleW := max(16, min(44, m.width*2/5))
	detailW := max(0, m.width-titleW-sizeW-14)

	for idx := m.offset; idx < len(rs) && idx < m.offset+h; idx++ {
		r := rs[idx]
		g := m.groups[r.g]
		pointer := "  "
		if idx == m.cursor {
			pointer = styleCursor.Render("› ")
		}
		if r.i == -1 {
			caret := "▸"
			if g.open || m.filter != "" {
				caret = "▾"
			}
			n := g.count()
			box := styleDim.Render("[ ]")
			switch {
			case n == len(g.items):
				box = styleAccent.Render("[x]")
			case n > 0:
				box = styleAccent.Render("[-]")
			}
			var size int64
			clones := 0
			for i, it := range g.items {
				if g.selected[i] {
					size += m.itemSize(it)
					if it.Clone != nil {
						clones++
					}
				}
			}
			detail := fmt.Sprintf("%d/%d", n, len(g.items))
			if clones > 0 {
				detail += fmt.Sprintf(" · %d clone", clones)
			}
			title := ansi.Truncate(kindLabel[g.kind], titleW+2, "…")
			fmt.Fprintf(&b, "%s%s %s %s %s %s\n", pointer, styleDim.Render(caret), box,
				styleBold.Render(pad(title, titleW+2)), styleDim.Render(pad(detail, detailW)), padLeft(humanBytes(size), sizeW))
			continue
		}
		it := g.items[r.i]
		detail := it.Detail
		if f, ok := m.filters[it.ID]; ok && !f.empty() {
			detail = "✂ " + strings.Join(f.excludes(), " ") + " · " + detail
		}
		if it.Warn != "" {
			detail = styleWarn.Render("⚠ "+it.Warn) + styleDim.Render(" · "+detail)
		} else {
			detail = styleDim.Render(ansi.Truncate(detail, detailW, "…"))
		}
		size := humanBytes(m.itemSize(it))
		if it.Clone != nil && it.Size == 0 {
			size = styleDim.Render("git")
		}
		fmt.Fprintf(&b, "%s    %s %s %s %s\n", pointer, checkbox(g.selected[r.i]),
			pad(ansi.Truncate(it.Title(), titleW, "…"), titleW), pad(ansi.Truncate(detail, detailW, "…"), detailW), padLeft(size, sizeW))
	}
	for i := len(rs) - m.offset; i < h; i++ {
		b.WriteString("\n")
	}

	var total int64
	clones, count := 0, 0
	for _, g := range m.groups {
		for i, it := range g.items {
			if g.selected[i] {
				count++
				total += m.itemSize(it)
				if it.Clone != nil {
					clones++
				}
			}
		}
	}
	summary := fmt.Sprintf("  %s selected · up to %s over the wire", plural(count, "item"), styleBold.Render(humanBytes(total)))
	if clones > 0 {
		summary += fmt.Sprintf(" · %d repos cloned", clones)
	}
	switch {
	case m.opts.overwrite:
		summary += styleWarn.Render(" · overwrite")
	case m.opts.update:
		summary += styleWarn.Render(" · update newer")
	}
	if m.opts.dryRun {
		summary += styleWarn.Render(" · dry run")
	}
	b.WriteString("\n" + summary + "\n")
	b.WriteString(m.footer(
		"↑↓ move · space toggle · → look inside · ←→ fold · + add path · - exclude · / filter · a/n all/none · enter pull · q quit"))
	return b.String()
}

func (m model) footer(keys string) string {
	switch m.mode {
	case modeFilter:
		return "  " + styleAccent.Render("filter › ") + m.prompt.View() + styleDim.Render("   enter keep · esc clear")
	case modeAdd:
		return "  " + styleAccent.Render("add path or glob on "+m.sess.peer.Host+" › ") + m.prompt.View() + styleDim.Render("   comma-separate several · esc cancel")
	case modeExclude:
		return "  " + styleAccent.Render("exclude › ") + m.prompt.View() + styleDim.Render("   name anywhere · /anchored · ** any depth")
	}
	line := ""
	if m.status != "" {
		line = "  " + m.status + "\n"
	} else if m.filter != "" && m.mode == modeList {
		line = "  " + styleAccent.Render("filter: "+m.filter) + styleDim.Render(" (esc clears)") + "\n"
	}
	return line + styleDim.Render("  "+keys)
}

func (m model) viewDrill() string {
	var b strings.Builder
	var it Item
	for _, g := range m.groups {
		for _, x := range g.items {
			if x.ID == m.drillID {
				it = x
			}
		}
	}
	rep := m.kids[m.drillID]
	f := m.filters[m.drillID]
	kept := m.itemSize(it)
	fmt.Fprintf(&b, "  %s  %s\n", styleBold.Render(it.Title()),
		styleDim.Render(fmt.Sprintf("%s of %s selected", humanBytes(kept), humanBytes(rep.Size))))
	if len(f.patterns) > 0 {
		b.WriteString("  " + styleAccent.Render("✂ "+strings.Join(f.patterns, "  ")) + styleDim.Render("  (r clears)") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	h := m.listHeight() - 2
	nameW := max(20, min(50, m.width/2))
	for i := m.drillOff; i < len(rep.Children) && i < m.drillOff+h; i++ {
		c := rep.Children[i]
		pointer := "  "
		if i == m.drillCur {
			pointer = styleCursor.Render("› ")
		}
		name := c.Name
		if c.Dir {
			name += "/"
		}
		files := ""
		if c.Dir {
			files = plural(c.Files, "file")
		}
		fmt.Fprintf(&b, "%s  %s %s %s  %s\n", pointer, checkbox(!f.hidden[c.Name]),
			pad(ansi.Truncate(name, nameW, "…"), nameW), padLeft(humanBytes(c.Size), 10), styleDim.Render(files))
	}
	if len(rep.Children) == 0 {
		b.WriteString(styleDim.Render("  nothing left after excludes") + "\n")
	}
	for i := len(rep.Children) - m.drillOff; i < h; i++ {
		b.WriteString("\n")
	}
	b.WriteString("\n" + m.footer("↑↓ move · space toggle · a/n all/none · - exclude pattern · r clear patterns · esc back"))
	return b.String()
}

func pad(s string, w int) string {
	if gap := w - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func padLeft(s string, w int) string {
	if gap := w - ansi.StringWidth(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

func (m model) viewRunning() string {
	st := m.st
	st.mu.Lock()
	defer st.mu.Unlock()
	var b strings.Builder
	pct := 0.0
	if st.totalBytes > 0 {
		pct = min(1, float64(st.doneBytes)/float64(st.totalBytes))
	}
	fmt.Fprintf(&b, "  %s %s\n", m.bar.ViewAs(pct), styleBold.Render(fmt.Sprintf("%3.0f%%", pct*100)))
	fmt.Fprintf(&b, "  %s / %s · %s/s · %s\n\n", humanBytes(st.doneBytes), humanBytes(st.totalBytes),
		humanBytes(int64(m.speed)), time.Since(st.started).Round(time.Second))

	current := ""
	if st.current != "" {
		current = st.byID[st.current].Title()
	}
	fmt.Fprintf(&b, "  %s %s %s\n\n", styleDim.Render("phase "+st.phase), m.spin.View(), current)

	h := max(1, m.height-12)
	start := max(0, len(st.log)-h)
	for _, id := range st.log[start:] {
		b.WriteString("  " + itemLine(st.byID[id], st.state[id], m.width-6) + "\n")
	}
	b.WriteString("\n" + styleDim.Render("  q abort"))
	return b.String()
}

func itemLine(it Item, s *itemState, width int) string {
	titleW := min(44, max(16, width/2))
	switch s.status {
	case statusFailed:
		msg := s.note
		if s.err != nil {
			msg = strings.TrimPrefix(msg+" · "+s.err.Error(), " · ")
		}
		return styleErr.Render("✗ ") + pad(ansi.Truncate(it.Title(), titleW, "…"), titleW) + " " + styleErr.Render(ansi.Truncate(msg, max(10, width-titleW-4), "…"))
	default:
		return styleOK.Render("✓ ") + pad(ansi.Truncate(it.Title(), titleW, "…"), titleW) + " " + styleDim.Render(ansi.Truncate(s.note, max(10, width-titleW-4), "…"))
	}
}

func summaryLines(st *runState, styled bool) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	ok, bad, warn, dim := styleOK.Render, styleErr.Render, styleWarn.Render, styleDim.Render
	if !styled {
		id := func(s ...string) string { return strings.Join(s, "") }
		ok, bad, warn, dim = id, id, id, id
	}
	end := st.finishedAt
	if end.IsZero() {
		end = time.Now()
	}
	lines := []string{fmt.Sprintf("  done in %s · %s checked", end.Sub(st.started).Round(time.Second), humanBytes(st.doneBytes)), ""}
	if st.aborted != nil {
		lines = append(lines, "  "+bad("✗ "+st.aborted.Error()))
	}
	if st.dryRun {
		return append(lines, dryRunLines(st, ok, warn, bad, dim)...)
	}
	lines = append(lines, "  "+ok("✓ ")+plural(st.written, "file")+" written")
	if st.uptodate > 0 {
		lines = append(lines, "  "+ok("✓ ")+fmt.Sprintf("%d already up to date (not transferred)", st.uptodate))
	}
	if st.skipped > 0 {
		lines = append(lines, "  "+warn("• ")+fmt.Sprintf("%d differ locally and were kept (--update takes newer, --overwrite replaces all)", st.skipped))
	}
	if st.cloned > 0 {
		lines = append(lines, "  "+ok("✓ ")+plural(st.cloned, "repo")+" cloned")
	}
	if st.failed > 0 {
		lines = append(lines, "  "+bad("✗ ")+fmt.Sprintf("%d failed", st.failed))
		for _, id := range st.log {
			s := st.state[id]
			if s.status == statusFailed {
				msg := ""
				if s.err != nil {
					msg = s.err.Error()
				}
				lines = append(lines, "      "+st.byID[id].Title()+"  "+dim(msg))
			}
		}
	}
	var next, later []string
	if st.masApps > 0 {
		next = append(next, fmt.Sprintf("sign in to the App Store first — %d apps in the Brewfile come from it (mas)", st.masApps))
	}
	if st.brewfile != "" {
		next = append(next, "brew bundle --file "+st.brewfile)
	}
	for _, h := range st.hints {
		if strings.HasPrefix(h, "chezmoi") {
			next = append(next, h)
		} else {
			later = append(later, h)
		}
	}
	if st.packages != "" {
		next = append(next, "sh "+st.packages+"   # cargo/go/npm/pipx/uv tools")
	}
	if st.staged {
		next = append(next, "env files of failed clones are in ~/"+stagingDir)
	}
	if st.failed > 0 {
		next = append(next, "fix the failures above and re-run hatch pull (only missing or changed files move)")
	}
	next = append(next, later...)
	if len(next) > 0 {
		lines = append(lines, "", "  next")
		for i, n := range next {
			lines = append(lines, fmt.Sprintf("    %s %s", dim(fmt.Sprintf("%2d.", i+1)), n))
		}
	}
	return lines
}

func dryRunLines(st *runState, ok, warn, bad, dim func(...string) string) []string {
	lines := []string{"  " + warn("dry run — nothing was written or cloned"), ""}
	lines = append(lines, "  "+ok("→ ")+fmt.Sprintf("would write %s (%s)", plural(st.planned, "file"), humanBytes(st.plannedSize)))
	if st.uptodate > 0 {
		lines = append(lines, "  "+ok("✓ ")+fmt.Sprintf("%d already up to date", st.uptodate))
	}
	if st.skipped > 0 {
		lines = append(lines, "  "+warn("• ")+fmt.Sprintf("%d differ locally and would be kept (--update / --overwrite to replace)", st.skipped))
	}
	if st.plannedRepo > 0 {
		lines = append(lines, "  "+ok("→ ")+fmt.Sprintf("would clone %s", plural(st.plannedRepo, "repo")))
	}
	if st.failed > 0 {
		lines = append(lines, "  "+bad("✗ ")+fmt.Sprintf("%d items could not be checked", st.failed))
	}
	return append(lines, "", "  "+dim("run the same pull without --dry-run to apply it (the selection is saved)"))
}
