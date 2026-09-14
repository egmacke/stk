package ui

import (
	"testing"

	"github.com/egmacke/stk/internal/stack"
)

// pickerGraph builds the same trunk -> a -> {b -> d, c} shape as tree(), as a
// graph the picker can be pointed at.
func pickerGraph(current string) (*stack.Graph, map[string]*stack.Branch) {
	trunk, _, byName := tree()
	g := &stack.Graph{Trunk: trunk, ByName: map[string]*stack.Branch{trunk.Name: trunk}}
	for name, b := range byName {
		g.ByName[name] = b
		g.Tracked = append(g.Tracked, b)
	}
	if current != "" {
		cur := g.ByName[current]
		cur.IsCurrent = true
		g.Current = cur
		g.CurrentName = current
	}
	return g, byName
}

func TestPickerOpensOnTheCurrentBranch(t *testing.T) {
	g, byName := pickerGraph("d")
	m := &pickerModel{opts: PickOptions{Graph: g}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	if got := m.rows[m.cursor].Branch; got != byName["d"] {
		t.Fatalf("cursor on %v, want d", got)
	}
}

func TestPickerFallsBackToTheFirstRowWithoutACurrentBranch(t *testing.T) {
	g, _ := pickerGraph("")
	m := &pickerModel{opts: PickOptions{Graph: g}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	if got := m.rows[m.cursor].Branch; got != g.Trunk {
		t.Fatalf("cursor on %v, want trunk", got)
	}
}

func TestPickerCursorStaysOnItsBranchWhileFiltering(t *testing.T) {
	g, byName := pickerGraph("c")
	m := &pickerModel{opts: PickOptions{Graph: g}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	// "c" still matches, so the cursor must not slide onto a neighbour.
	m.filter = "c"
	m.rebuild()
	if got := m.rows[m.cursor].Branch; got != byName["c"] {
		t.Fatalf("cursor on %v after filtering, want c", got)
	}
	// "b" does not match c, so the cursor falls onto the first match.
	m.filter = "b"
	m.rebuild()
	if got := m.rows[m.cursor].Branch; got == nil || !m.rows[m.cursor].Selectable {
		t.Fatalf("cursor on %v after filtering to b, want a selectable row", got)
	}
}

func TestPickerCandidatesOpenOnTheCurrentBranchWhenPresent(t *testing.T) {
	g, byName := pickerGraph("c")
	m := &pickerModel{opts: PickOptions{
		Graph:      g,
		Candidates: []*stack.Branch{byName["b"], byName["c"]},
	}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	if got := m.rows[m.cursor].Branch; got != byName["c"] {
		t.Fatalf("cursor on %v, want c", got)
	}
}
