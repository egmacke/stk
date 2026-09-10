package operations

import (
	"errors"
	"fmt"

	"stk/internal/stack"
)

// DraftChoice says which of the pull requests a submit opens should be drafts.
//
// The three forms are mutually exclusive: All is the blunt one, From is the cut
// line a stack usually has — ready at the bottom, still being written at the
// top — and Branches names an arbitrary set.
type DraftChoice struct {
	// All makes every pull request this run opens a draft.
	All bool
	// From is a branch: it and everything above it open as drafts.
	From string
	// Branches names exactly the branches whose pull requests are drafts.
	Branches []string
}

// Empty reports whether the user expressed no preference, which is when stk may
// ask for a cut line.
func (d DraftChoice) Empty() bool {
	return !d.All && d.From == "" && len(d.Branches) == 0
}

// Validate rejects combinations that would leave stk guessing which wins.
func (d DraftChoice) Validate() error {
	given := 0
	if d.All {
		given++
	}
	if d.From != "" {
		given++
	}
	if len(d.Branches) > 0 {
		given++
	}
	if given > 1 {
		return errors.New("use one of --draft, --draft-from or --draft-branch, not several")
	}
	return nil
}

// draftSet resolves the choice against the graph, returning the branch ids
// whose pull requests should open as drafts.
func draftSet(g *stack.Graph, plan []*stack.Branch, d DraftChoice) (map[string]bool, error) {
	out := map[string]bool{}
	switch {
	case d.All:
		for _, b := range plan {
			out[b.ID] = true
		}
	case d.From != "":
		from, ok := g.Resolve(d.From)
		if !ok {
			return nil, fmt.Errorf("branch %q does not exist", d.From)
		}
		if from.IsTrunk {
			// Trunk is the cut line below everything, so nothing is ready.
			for _, b := range plan {
				out[b.ID] = true
			}
			return out, nil
		}
		// The named branch and everything stacked above it.
		for _, b := range stack.Subtree(from) {
			out[b.ID] = true
		}
	default:
		for _, name := range d.Branches {
			b, ok := g.Resolve(name)
			if !ok {
				return nil, fmt.Errorf("branch %q does not exist", name)
			}
			if b.IsTrunk {
				return nil, fmt.Errorf("%s is the trunk branch and has no pull request", name)
			}
			out[b.ID] = true
		}
	}
	return out, nil
}

// draftCandidates lists the branches offered as the ready/draft cut line,
// bottom first, with trunk at the head so that "nothing is ready yet" can be
// chosen too.
func draftCandidates(g *stack.Graph, plan []*stack.Branch) []*stack.Branch {
	out := []*stack.Branch{g.Trunk}
	return append(out, plan...)
}
