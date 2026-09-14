package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"stk/internal/output"
	"stk/internal/stack"
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
// The cursor follows the branch it was on when that branch survives the
// filter, so typing does not silently move the selection onto a neighbour.
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
	if under != nil {
		for i, r := range rows {
			if r.Selectable && r.Branch == under {
				m.cursor = i
				break
			}
		}
	}
	m.snapToSelectable(1)
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
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			if !m.opts.ReadOnly && m.cursor < len(m.rows) && m.rows[m.cursor].Branch != nil {
				m.chosen = m.rows[m.cursor].Branch
			}
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			m.move(-1)
		case tea.KeyDown:
			m.move(1)
		case tea.KeyPgUp:
			for i := 0; i < 10; i++ {
				m.move(-1)
			}
		case tea.KeyPgDown:
			for i := 0; i < 10; i++ {
				m.move(1)
			}
		case tea.KeyBackspace:
			if m.filter != "" {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
				m.rebuild()
			}
		case tea.KeyRunes, tea.KeySpace:
			switch msg.String() {
			case "ctrl+p":
				m.move(-1)
			case "ctrl+n":
				m.move(1)
			default:
				m.filter += msg.String()
				m.rebuild()
			}
		case tea.KeyCtrlP:
			m.move(-1)
		case tea.KeyCtrlN:
			m.move(1)
		case tea.KeyCtrlU:
			m.filter = ""
			m.rebuild()
		}
	}
	return m, nil
}

func (m *pickerModel) View() string {
	if m.quitting {
		return ""
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
		marker := "  "
		if i == m.cursor {
			marker = output.Cyan(output.Bold("> "))
		}
		fmt.Fprintf(&b, "%s%s\n", marker, m.lines[i])
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
	return b.String()
}
