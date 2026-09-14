// Package ui renders the stack for humans and hosts the interactive picker.
package ui

import (
	"fmt"
	"strings"

	"stk/internal/output"
	"stk/internal/stack"
)

// Row is one line of a rendered stack tree.
type Row struct {
	Branch *stack.Branch
	// Prefix is the tree drawing that precedes the branch name.
	Prefix string
	// Selectable is false for headings and blank separators.
	Selectable bool
	// Text replaces the branch name for non-branch rows.
	Text string
}

// Label is the drawn portion of the row before status markers.
func (r Row) Label() string {
	if r.Branch == nil {
		return r.Text
	}
	return r.Prefix + r.Branch.Name
}

// Visible decides whether a branch appears in a rendered tree.
type Visible func(*stack.Branch) bool

// BuildTree lays out trunk and the branches above it as tree rows.
//
// A branch is drawn when visible reports true for it or for any of its
// descendants, so filtering never hides the path to a match.
func BuildTree(trunk *stack.Branch, roots []*stack.Branch, visible Visible) []Row {
	if visible == nil {
		visible = func(*stack.Branch) bool { return true }
	}
	keep := map[*stack.Branch]bool{}
	var mark func(b *stack.Branch) bool
	mark = func(b *stack.Branch) bool {
		show := visible(b)
		for _, c := range b.Children {
			if mark(c) {
				show = true
			}
		}
		keep[b] = show
		return show
	}
	var shown []*stack.Branch
	for _, r := range roots {
		if mark(r) {
			shown = append(shown, r)
		}
	}
	if trunk == nil {
		var rows []Row
		for i, r := range shown {
			rows = append(rows, subtreeRows(r, "", i == len(shown)-1, keep)...)
		}
		return rows
	}
	rows := []Row{{Branch: trunk, Prefix: "", Selectable: true}}
	for i, r := range shown {
		rows = append(rows, subtreeRows(r, "", i == len(shown)-1, keep)...)
	}
	return rows
}

func subtreeRows(b *stack.Branch, prefix string, last bool, keep map[*stack.Branch]bool) []Row {
	connector := "├─ "
	childPrefix := prefix + "│  "
	if last {
		connector = "└─ "
		childPrefix = prefix + "   "
	}
	rows := []Row{{Branch: b, Prefix: prefix + connector, Selectable: true}}
	var kids []*stack.Branch
	for _, c := range b.Children {
		if keep[c] {
			kids = append(kids, c)
		}
	}
	for i, c := range kids {
		rows = append(rows, subtreeRows(c, childPrefix, i == len(kids)-1, keep)...)
	}
	return rows
}

// FlatRows renders branches as a flat list, used for untracked branches which
// have no place in the graph.
func FlatRows(branches []*stack.Branch, visible Visible) []Row {
	var rows []Row
	for _, b := range branches {
		if visible != nil && !visible(b) {
			continue
		}
		rows = append(rows, Row{Branch: b, Prefix: "   ", Selectable: true})
	}
	return rows
}

// Status is the marker column for a branch: publication state first, then
// anything that needs the user's attention.
func Status(b *stack.Branch, dirty bool) string {
	if b == nil {
		return ""
	}
	var parts []string
	switch {
	case !b.Tracked && !b.IsTrunk && b.Upstream == "":
		// Untracked and unpublished; the ↑? marker would be noise.
	case b.Upstream == "":
		parts = append(parts, output.SymAhead+"?")
	default:
		if b.UpstreamGone {
			parts = append(parts, output.SymAhead+"!")
		}
		if b.Ahead > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", output.SymAhead, b.Ahead))
		}
		if b.Behind > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", output.SymBehind, b.Behind))
		}
	}
	if b.NeedsRestack() {
		parts = append(parts, output.SymRestack)
	}
	if b.HasProblem() {
		parts = append(parts, output.SymProblem)
	}
	if b.CheckedOutElsewhere() {
		parts = append(parts, output.SymWorktree)
	}
	if b.IsCurrent && dirty {
		parts = append(parts, output.SymDirty)
	}
	return strings.Join(parts, " ")
}

// RenderRows formats rows into aligned text lines. current marks the branch to
// annotate with the current-branch arrow.
func RenderRows(rows []Row, current *stack.Branch, dirty bool) []string {
	width := 0
	for _, r := range rows {
		if n := len([]rune(r.Label())); n > width {
			width = n
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Branch == nil {
			out = append(out, output.Dim(r.Text))
			continue
		}
		status := Status(r.Branch, dirty)
		if r.Branch.HasProblem() {
			status = output.Red(status)
		} else if status != "" {
			status = output.Yellow(status)
		}
		if r.Branch == current {
			if status == "" {
				status = output.Cyan(output.SymCurrent)
			} else {
				status = status + " " + output.Cyan(output.SymCurrent)
			}
		}
		label := r.Label()
		pad := width - len([]rune(label))
		if pad < 0 {
			pad = 0
		}
		name := r.Prefix + styleName(r.Branch, current)
		line := name
		if status != "" {
			line = name + strings.Repeat(" ", pad) + "  " + status
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

func styleName(b *stack.Branch, current *stack.Branch) string {
	switch {
	case b == current:
		return output.Cyan(output.Bold(b.Name))
	case b.IsTrunk:
		return output.Bold(b.Name)
	case !b.Tracked:
		return output.Dim(b.Name)
	}
	return b.Name
}

// Legend explains the status markers.
func Legend() []string {
	return []string{
		output.SymAhead + "N  commits not pushed",
		output.SymBehind + "N  commits behind upstream",
		output.SymAhead + "?  no upstream branch",
		output.SymAhead + "!  upstream branch is gone",
		output.SymRestack + "   requires restack",
		output.SymWorktree + "   checked out in another worktree",
		output.SymDirty + "   uncommitted changes",
		output.SymProblem + "   metadata needs repair",
		output.SymCurrent + "   current branch",
	}
}
