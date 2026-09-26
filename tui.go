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

type group struct {
	kind     Kind
	items    []Item
	selected []bool
	open     bool
}

type row struct{ g, i int }

type connectedMsg struct{ sess *session }
type errMsg struct{ err error }
type tickMsg time.Time

type model struct {
	screen    screen
	home      string
	addr      string
	opts      pullOptions
	ctx       context.Context
	cancel    context.CancelFunc
	input     textinput.Model
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
}

func newModel(code, addr, home string, opts pullOptions) model {
	ctx, cancel := context.WithCancel(context.Background())
	in := textinput.New()
	in.Placeholder = "7-guitar-orbit"
	in.Prompt = "  code › "
	in.CharLimit = 40
	in.Focus()
	m := model{
		screen: screenCode,
		home:   home,
		addr:   addr,
		opts:   opts,
		ctx:    ctx,
		cancel: cancel,
		input:  in,
		spin:   spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(styleAccent)),
		bar:    progress.New(progress.WithGradient("#7C3AED", "#34D399"), progress.WithoutPercentage()),
		width:  80,
		height: 24,
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
		return connectedMsg{sess}
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
		m.groups = buildGroups(msg.sess.items)
		m.vis = newVisual(msg.sess.sc.visual)
		m.screen = screenPaired
		return m, tickEvery(visFrame)
	case errMsg:
		m.err = msg.err
		m.screen = screenError
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

func buildGroups(items []Item) []group {
	var gs []group
	for _, k := range kindOrder {
		g := group{kind: k, open: k != KindRepos && k != KindConfig}
		for _, it := range items {
			if it.Kind == k {
				g.items = append(g.items, it)
				g.selected = append(g.selected, it.Default)
			}
		}
		if len(g.items) > 0 {
			gs = append(gs, g)
		}
	}
	return gs
}

func (m model) rows() []row {
	var rs []row
	for gi, g := range m.groups {
		rs = append(rs, row{gi, -1})
		if g.open {
			for i := range g.items {
				rs = append(rs, row{gi, i})
			}
		}
	}
	return rs
}

func (m model) listHeight() int { return max(3, m.height-7) }

func (m model) updateSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rs := m.rows()
	cur := rs[min(m.cursor, len(rs)-1)]
	switch msg.String() {
	case "q", "esc":
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
	case "right", "l", "tab":
		m.groups[cur.g].open = true
	case "left", "h":
		m.groups[cur.g].open = false
		for i, r := range m.rows() {
			if r.g == cur.g && r.i == -1 {
				m.cursor = i
			}
		}
	case " ", "x":
		g := &m.groups[cur.g]
		if cur.i >= 0 {
			g.selected[cur.i] = !g.selected[cur.i]
		} else {
			all := g.count() == len(g.items)
			for i := range g.selected {
				g.selected[i] = !all
			}
		}
	case "a", "n":
		for gi := range m.groups {
			for i := range m.groups[gi].selected {
				m.groups[gi].selected[i] = msg.String() == "a"
			}
		}
	case "enter":
		items := m.selectedItems()
		if len(items) == 0 {
			return m, nil
		}
		m.st = newRunState(items)
		m.lastTick = time.Now()
		m.screen = screenRunning
		go pull(m.ctx, m.sess, items, m.home, m.opts, m.st)
		return m, tea.Batch(tick(), m.spin.Tick)
	}
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	return m, nil
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

func (m model) selectedItems() []Item {
	var out []Item
	for _, g := range m.groups {
		for i, it := range g.items {
			if g.selected[i] {
				out = append(out, it)
			}
		}
	}
	return out
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
		b.WriteString(m.viewSelect())
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
			if g.open {
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
					size += it.Size
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
		if it.Warn != "" {
			detail = styleWarn.Render("⚠ "+it.Warn) + styleDim.Render(" · "+detail)
		} else {
			detail = styleDim.Render(ansi.Truncate(detail, detailW, "…"))
		}
		size := humanBytes(it.Size)
		if it.Clone != nil && it.Size == 0 {
			size = styleDim.Render("git")
		}
		fmt.Fprintf(&b, "%s    %s %s %s %s\n", pointer, checkbox(g.selected[r.i]),
			pad(ansi.Truncate(it.Title(), titleW, "…"), titleW), pad(ansi.Truncate(detail, detailW, "…"), detailW), padLeft(size, sizeW))
	}
	for i := len(rs) - m.offset; i < h; i++ {
		b.WriteString("\n")
	}

	items := m.selectedItems()
	var total int64
	clones := 0
	for _, it := range items {
		total += it.Size
		if it.Clone != nil {
			clones++
		}
	}
	summary := fmt.Sprintf("  %s selected · %s over the wire", plural(len(items), "item"), styleBold.Render(humanBytes(total)))
	if clones > 0 {
		summary += fmt.Sprintf(" · %d repos cloned from their remotes", clones)
	}
	if m.opts.overwrite {
		summary += styleWarn.Render(" · overwrite on")
	}
	b.WriteString("\n" + summary + "\n")
	b.WriteString(styleDim.Render("  ↑↓ move · space toggle · ←→ fold · a all · n none · enter pull · q quit"))
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
	lines := []string{fmt.Sprintf("  done in %s · %s transferred", end.Sub(st.started).Round(time.Second), humanBytes(st.doneBytes)), ""}
	if st.aborted != nil {
		lines = append(lines, "  "+bad("✗ "+st.aborted.Error()))
	}
	lines = append(lines, "  "+ok("✓ ")+plural(st.written, "file")+" written")
	if st.skipped > 0 {
		lines = append(lines, "  "+warn("• ")+fmt.Sprintf("%d already existed and were kept (use --overwrite to replace)", st.skipped))
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
		next = append(next, "fix the failures above and re-run hatch pull (existing files are kept)")
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
