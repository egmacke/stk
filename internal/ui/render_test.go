package ui

import (
	"strings"
	"testing"

	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// tree builds a small graph by hand: trunk -> a -> {b -> d, c}.
//
// Every branch is given a clean upstream so Status contributes no markers and
// the tests can compare tree drawing on its own.
func tree() (*stack.Branch, []*stack.Branch, map[string]*stack.Branch) {
	trunk := &stack.Branch{Name: "main", IsTrunk: true, ID: stack.TrunkID, Upstream: "origin/main"}
	mk := func(name string, parent *stack.Branch) *stack.Branch {
		b := &stack.Branch{
			Name:     name,
			ID:       name,
			Tracked:  true,
			Parent:   parent,
			Upstream: "origin/" + name,
		}
		parent.Children = append(parent.Children, b)
		return b
	}
	a := mk("a", trunk)
	b := mk("b", a)
	c := mk("c", a)
	d := mk("d", b)
	return trunk, trunk.Children, map[string]*stack.Branch{"a": a, "b": b, "c": c, "d": d}
}

func TestBuildTreeDrawsBranchingStacks(t *testing.T) {
	trunk, roots, _ := tree()
	got := strings.Join(RenderRows(BuildTree(trunk, roots, nil), nil, false), "\n")
	want := strings.Join([]string{
		"main",
		"└─ a",
		"   ├─ b",
		"   │  └─ d",
		"   └─ c",
	}, "\n")
	if got != want {
		t.Fatalf("tree rendering:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildTreeKeepsThePathToAMatch(t *testing.T) {
	trunk, roots, _ := tree()
	// Filtering to "d" must still draw its ancestors, or the match would be
	// impossible to locate.
	visible := func(b *stack.Branch) bool { return b.Name == "d" }
	got := strings.Join(RenderRows(BuildTree(trunk, roots, visible), nil, false), "\n")
	want := strings.Join([]string{
		"main",
		"└─ a",
		"   └─ b",
		"      └─ d",
	}, "\n")
	if got != want {
		t.Fatalf("filtered rendering:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatusMarkers(t *testing.T) {
	b := &stack.Branch{Name: "x", Tracked: true}
	if got := Status(b, false); got != output.SymAhead+"?" {
		t.Errorf("unpublished branch: got %q", got)
	}

	b.Upstream = "origin/x"
	b.Ahead = 3
	b.Behind = 1
	got := Status(b, false)
	if !strings.Contains(got, "↑3") || !strings.Contains(got, "↓1") {
		t.Errorf("ahead/behind: got %q", got)
	}

	b.Worktree = "/elsewhere"
	if !strings.Contains(Status(b, false), output.SymWorktree) {
		t.Error("expected the worktree marker")
	}

	b.IsCurrent = true
	if strings.Contains(Status(b, false), output.SymWorktree) {
		t.Error("the current worktree must not be flagged as another worktree")
	}
	if !strings.Contains(Status(b, true), output.SymDirty) {
		t.Error("expected the dirty marker")
	}

	b.Orphaned = true
	if !strings.Contains(Status(b, false), output.SymProblem) {
		t.Error("expected the problem marker")
	}
}

func TestCurrentBranchArrow(t *testing.T) {
	trunk, roots, byName := tree()
	lines := RenderRows(BuildTree(trunk, roots, nil), byName["c"], false)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "c") && strings.Contains(line, output.SymCurrent) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the current-branch arrow on c:\n%s", strings.Join(lines, "\n"))
	}
}
