package cmd

import (
	"errors"

	"stk/internal/stack"
	"stk/internal/ui"
)

// promptText asks for a free-text value such as a new branch name.
//
// When stk may not ask, the caller's usage error stands: a missing argument
// stays an error rather than becoming a value stk invented.
func promptText(question, missing string) (string, error) {
	if !Interactive() {
		return "", errors.New(missing)
	}
	return ui.ReadLine(question)
}

// branchPrompt describes an interactive request for the name of a branch.
type branchPrompt struct {
	// Title is shown above the picker, and as the question when a name has
	// to be typed instead.
	Title string
	// Candidates restricts the choice; nil offers the whole graph.
	Candidates []*stack.Branch
	// Missing is the error to return when stk may not ask.
	Missing string
	// Empty is the error to return when no candidate qualifies.
	Empty string
}

// promptBranch asks the user to choose a branch.
//
// On a terminal that means the full-screen picker. With an answerable stdin
// but no terminal the name is typed instead, so --interactive still works
// over a pipe; without either, the caller's usage error stands.
func promptBranch(g *stack.Graph, p branchPrompt) (string, error) {
	if !Interactive() {
		return "", errors.New(p.Missing)
	}
	if p.Candidates != nil && len(p.Candidates) == 0 {
		return "", errors.New(p.Empty)
	}
	if !Selectable() {
		return ui.ReadLine(p.Title + ":")
	}
	b, err := ui.Pick(ui.PickOptions{
		Graph:      g,
		Title:      p.Title,
		Candidates: p.Candidates,
	})
	if err != nil {
		return "", err
	}
	return b.Name, nil
}

// parentCandidates lists the branches that may become the logical parent of
// b: trunk, plus every healthy tracked branch that is neither b itself nor
// stacked above it, since either would make a cycle.
func parentCandidates(g *stack.Graph, b *stack.Branch) []*stack.Branch {
	out := []*stack.Branch{g.Trunk}
	for _, c := range g.Tracked {
		if c == b || c.HasProblem() {
			continue
		}
		if b.Tracked && stack.IsDescendant(b, c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// cancelled reports whether the user dismissed a prompt. stk treats that as a
// quiet no-op rather than a failure, matching an escaped branch picker.
func cancelled(err error) bool { return errors.Is(err, ui.ErrCancelled) }
