package ui

import (
	"testing"

	"github.com/egmacke/stk/internal/output"
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

func TestPickerJumpsToTheBestMatchWhileFiltering(t *testing.T) {
	g, byName := pickerGraph("c")
	m := &pickerModel{opts: PickOptions{Graph: g}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	m.filter = "d"
	m.rebuild()
	if got := m.rows[m.cursor].Branch; got != byName["d"] {
		t.Fatalf("cursor on %v after filtering to d, want d", got)
	}
}

func TestPickerKeepsItsPlaceWhenTheFilterIsCleared(t *testing.T) {
	g, byName := pickerGraph("c")
	m := &pickerModel{opts: PickOptions{Graph: g}, height: 20}
	m.rebuild()
	m.cursorToCurrent()
	m.filter = "d"
	m.rebuild()
	m.filter = ""
	m.rebuild()
	if got := m.rows[m.cursor].Branch; got != byName["d"] {
		t.Fatalf("cursor on %v after clearing the filter, want d", got)
	}
}

func TestBestMatchRanking(t *testing.T) {
	rows := func(names ...string) []Row {
		var out []Row
		for _, n := range names {
			out = append(out, Row{Branch: &stack.Branch{Name: n}, Selectable: true})
		}
		return out
	}
	for _, tc := range []struct {
		names  []string
		needle string
		want   string
	}{
		{[]string{"cool", "co"}, "co", "co"},                            // exact beats prefix
		{[]string{"fix/cool", "copy"}, "co", "copy"},                    // prefix beats segment
		{[]string{"discord", "feat/better-co"}, "co", "feat/better-co"}, // segment beats mid-word
		{[]string{"feat/better-checkout", "feat/co"}, "co", "feat/co"},  // later segment still counts
		{[]string{"feat/second", "fix/second"}, "second", "fix/second"}, // earlier match wins
		{[]string{"feat/api-long", "feat/api"}, "api", "feat/api"},      // then shorter name
		{[]string{"Feat/Better-CO"}, "co", "Feat/Better-CO"},            // case-insensitive
	} {
		r := rows(tc.names...)
		i := bestMatch(r, tc.needle)
		if i < 0 || r[i].Branch.Name != tc.want {
			t.Errorf("bestMatch(%v, %q) = %d, want %s", tc.names, tc.needle, i, tc.want)
		}
	}
	if i := bestMatch(rows("a", "b"), "z"); i != -1 {
		t.Errorf("bestMatch with no match = %d, want -1", i)
	}
	if i := bestMatch(rows("a"), ""); i != -1 {
		t.Errorf("bestMatch with no filter = %d, want -1", i)
	}
	heading := []Row{{Text: "untracked"}, {Branch: &stack.Branch{Name: "co"}, Selectable: true}}
	if i := bestMatch(heading, "co"); i != 1 {
		t.Errorf("bestMatch landed on a heading: %d", i)
	}
}

func TestEmphasiseKeepsTheWholeLineBold(t *testing.T) {
	was := output.ColorEnabled()
	defer output.EnableColor(was)

	output.EnableColor(false)
	if got := emphasise("main  x"); got != "main  x" {
		t.Fatalf("emphasise without colour = %q", got)
	}
	output.EnableColor(true)
	line := output.Cyan("a") + " " + output.Yellow("x")
	want := "\x1b[1m\x1b[36ma\x1b[0m\x1b[1m \x1b[33mx\x1b[0m\x1b[1m\x1b[0m"
	if got := emphasise(line); got != want {
		t.Fatalf("emphasise = %q, want %q", got, want)
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
