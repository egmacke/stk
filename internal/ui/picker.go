package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// PickOptions configures the interactive branch picker.
type PickOptions struct {
	Graph *stack.Graph
	// Title is shown above the list.
	Title string
	// Verb names what enter does, for the key hint. Defaults to "select".
	Verb string
	// Candidates restricts selection to a specific set, rendered flat. When
	// empty the whole stack graph is shown.
	Candidates []*stack.Branch
	// IncludeUntracked adds local branches that stk does not track.
	IncludeUntracked bool
	// ReadOnly disables selection; the picker becomes a viewer.
	ReadOnly bool
}

type pickerModel struct {
	opts     PickOptions
	filter   string
	rows     []Row
	lines    []string
	cursor   int
	offset   int
	height   int
	quitting bool
	chosen   *stack.Branch
	err      error
}

// Pick runs the interactive picker and returns the chosen branch.
func Pick(opts PickOptions) (*stack.Branch, error) {
	m := &pickerModel{opts: opts, height: 20}
	m.rebuild()
	if len(m.selectable()) == 0 && len(opts.Candidates) > 0 {
		return nil, fmt.Errorf("nothing to choose from")
	}
	m.cursorToCurrent()
	prog := tea.NewProgram(m)
	final, err := prog.Run()
	if err != nil {
		return nil, err
	}
	res := final.(*pickerModel)
	if res.err != nil {
		return nil, res.err
	}
	if res.chosen == nil {
		return nil, ErrCancelled
	}
	return res.chosen, nil
}

func (m *pickerModel) Init() tea.Cmd { return nil }

// rebuild recomputes the visible rows for the current filter.
//
// While a filter is typed the cursor jumps to the best match, so enter takes
// the branch the search most plausibly meant. With no filter it follows the
// branch it was on, so clearing a search does not lose the place.
func (m *pickerModel) rebuild() {
	g := m.opts.Graph
	var under *stack.Branch
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		under = m.rows[m.cursor].Branch
	}
	needle := strings.ToLower(m.filter)
	match := func(b *stack.Branch) bool {
		if needle == "" {
			return true
		}
		return strings.Contains(strings.ToLower(b.Name), needle)
	}

	var rows []Row
	if len(m.opts.Candidates) > 0 {
		for _, b := range m.opts.Candidates {
			if match(b) {
				rows = append(rows, Row{Branch: b, Prefix: "", Selectable: true})
			}
		}
	} else {
		rows = BuildTree(g.Trunk, g.Roots(), match)
		if m.opts.IncludeUntracked && len(g.Untracked) > 0 {
			flat := FlatRows(g.Untracked, match)
			if len(flat) > 0 {
				rows = append(rows, Row{Text: "", Selectable: false})
				rows = append(rows, Row{Text: "untracked", Selectable: false})
				rows = append(rows, flat...)
			}
		}
		if len(g.Orphans) > 0 {
			flat := FlatRows(g.Orphans, match)
			if len(flat) > 0 {
				rows = append(rows, Row{Text: "", Selectable: false})
				rows = append(rows, Row{Text: "orphaned (parent missing)", Selectable: false})
				rows = append(rows, flat...)
			}
		}
	}
	m.rows = rows
	m.lines = RenderRows(rows, g.Current, g.Dirty())
	m.cursor = 0
	if best := bestMatch(rows, needle); best >= 0 {
		m.cursor = best
	} else if under != nil {
		for i, r := range rows {
			if r.Selectable && r.Branch == under {
				m.cursor = i
				break
			}
		}
	}
	m.snapToSelectable(1)
}

// bestMatch returns the index of the selectable row whose branch name best
// matches needle, or -1 when needle is empty or nothing matches. Rows drawn
// only as the path to a match do not count.
//
// An exact name beats a prefix, a prefix beats a match at the start of a
// segment such as the part after "feat/", and any of those beats a match
// in the middle of a word. Ties go to the earlier match, then the shorter
// name, then the row drawn first.
func bestMatch(rows []Row, needle string) int {
	if needle == "" {
		return -1
	}
	best, bestScore, bestAt, bestLen := -1, 0, 0, 0
	for i, r := range rows {
		if !r.Selectable || r.Branch == nil {
			continue
		}
		name := strings.ToLower(r.Branch.Name)
		at := strings.Index(name, needle)
		if at < 0 {
			continue
		}
		score := 3
		switch {
		case name == needle:
			score = 0
		case at == 0:
			score = 1
		default:
			for j := at; j >= 0; j = nextIndex(name, needle, j) {
				if strings.ContainsRune("/-_.", rune(name[j-1])) {
					score, at = 2, j
					break
				}
			}
		}
		n := len(name)
		if best < 0 || score < bestScore ||
			(score == bestScore && (at < bestAt || (at == bestAt && n < bestLen))) {
			best, bestScore, bestAt, bestLen = i, score, at, n
		}
	}
	return best
}

// nextIndex returns where needle next occurs in s after position from, or -1.
func nextIndex(s, needle string, from int) int {
	i := strings.Index(s[from+1:], needle)
	if i < 0 {
		return -1
	}
	return from + 1 + i
}

// emphasise renders a whole picker line bold. The line already carries its
// own styling, and every reset in it would end the bold early, so bold is
// switched back on after each one.
func emphasise(line string) string {
	if !output.ColorEnabled() {
		return line
	}
	const bold, reset = "\x1b[1m", "\x1b[0m"
	return bold + strings.ReplaceAll(line, reset, reset+bold) + reset
}

// cursorToCurrent opens the picker on the branch the user is standing on.
//
// Starting on trunk would make enter a checkout of master or main, which is
// almost never what was wanted; starting on the current branch makes it a
// no-op, so the dangerous key is the harmless one until the cursor is moved.
func (m *pickerModel) cursorToCurrent() {
	if m.opts.Graph == nil || m.opts.Graph.Current == nil {
		return
	}
	for i, r := range m.rows {
		if r.Selectable && r.Branch == m.opts.Graph.Current {
			m.cursor = i
			return
		}
	}
}

func (m *pickerModel) selectable() []int {
	var out []int
	for i, r := range m.rows {
		if r.Selectable {
			out = append(out, i)
		}
	}
	return out
}

// snapToSelectable moves the cursor onto a selectable row, searching in the
// given direction first.
func (m *pickerModel) snapToSelectable(dir int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].Selectable {
			m.cursor = i
			return
		}
	}
	for i := range m.rows {
		if m.rows[i].Selectable {
			m.cursor = i
			return
		}
	}
}

func (m *pickerModel) move(delta int) {
	i := m.cursor
	for {
		i += delta
		if i < 0 || i >= len(m.rows) {
			return
		}
		if m.rows[i].Selectable {
			m.cursor = i
			return
		}
	}
}

func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if !m.opts.ReadOnly && m.cursor < len(m.rows) && m.rows[m.cursor].Branch != nil {
				m.chosen = m.rows[m.cursor].Branch
			}
			m.quitting = true
			return m, tea.Quit
		case "up":
			m.move(-1)
		case "down":
			m.move(1)
		case "pgup":
			for i := 0; i < 10; i++ {
				m.move(-1)
			}
		case "pgdown":
			for i := 0; i < 10; i++ {
				m.move(1)
			}
		case "backspace":
			if m.filter != "" {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
				m.rebuild()
			}
		case "ctrl+p":
			m.move(-1)
		case "ctrl+n":
			m.move(1)
		case "ctrl+u":
			m.filter = ""
			m.rebuild()
		default:
			// Text is non-empty only for printable keys, which keeps
			// modifier chords and function keys out of the filter.
			if msg.Text != "" {
				m.filter += msg.Text
				m.rebuild()
			}
		}
	}
	return m, nil
}

func (m *pickerModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	var b strings.Builder
	title := m.opts.Title
	if title == "" {
		title = "Search"
	}
	fmt.Fprintf(&b, "%s %s\n\n", output.Bold(title+":"), output.Cyan(m.filter))

	visible := m.height - 8
	if visible < 5 {
		visible = 5
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	if m.offset > len(m.lines)-visible {
		m.offset = len(m.lines) - visible
	}
	if m.offset < 0 {
		m.offset = 0
	}
	end := m.offset + visible
	if end > len(m.lines) {
		end = len(m.lines)
	}
	for i := m.offset; i < end; i++ {
		marker, line := "  ", m.lines[i]
		if i == m.cursor {
			marker = output.Cyan(output.Bold("> "))
			line = emphasise(line)
		}
		fmt.Fprintf(&b, "%s%s\n", marker, line)
	}
	if len(m.lines) == 0 {
		b.WriteString(output.Dim("  no matching branches") + "\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", strings.Join(Legend()[:4], "   "))
	if m.opts.ReadOnly {
		fmt.Fprintf(&b, "%s %s\n", output.Bold("esc"), output.Dim("quit"))
	} else {
		verb := m.opts.Verb
		if verb == "" {
			verb = "select"
		}
		fmt.Fprintf(&b, "%s %s   %s %s\n",
			output.Bold("enter"), output.Dim(verb), output.Bold("esc"), output.Dim("cancel"))
	}
	return tea.NewView(b.String())
}
