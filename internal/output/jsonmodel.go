package output

import "github.com/egmacke/stk/internal/stack"

// BranchJSON is the stable machine-readable shape of one branch.
type BranchJSON struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Parent       string   `json:"parent,omitempty"`
	Children     []string `json:"children"`
	SHA          string   `json:"sha"`
	Base         string   `json:"base,omitempty"`
	Trunk        bool     `json:"trunk"`
	Tracked      bool     `json:"tracked"`
	Current      bool     `json:"current"`
	NeedsRestack bool     `json:"needsRestack"`
	Upstream     string   `json:"upstream,omitempty"`
	Published    bool     `json:"published"`
	Ahead        int      `json:"ahead"`
	Behind       int      `json:"behind"`
	UpstreamGone bool     `json:"upstreamGone,omitempty"`
	Worktree     string   `json:"worktree,omitempty"`
	Problems     []string `json:"problems,omitempty"`
	// PR is what gh stack's tracking knows about the branch's pull request,
	// present only when the repository keeps that tracking in step with stk.
	PR *PullRequestJSON `json:"pr,omitempty"`
}

// PullRequestJSON is the pull request recorded for a branch.
type PullRequestJSON struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	Merged bool   `json:"merged"`
}

// StackJSON is the document produced by stk stack --json and stk show --json.
type StackJSON struct {
	Trunk     string       `json:"trunk"`
	Remote    string       `json:"remote,omitempty"`
	Current   string       `json:"current,omitempty"`
	Dirty     bool         `json:"dirty"`
	Branches  []BranchJSON `json:"branches"`
	Untracked []string     `json:"untracked,omitempty"`
}

// Branch converts a graph node to its JSON shape.
func Branch(b *stack.Branch) BranchJSON {
	out := BranchJSON{
		ID:           b.ID,
		Name:         b.Name,
		SHA:          b.SHA,
		Base:         b.Base,
		Trunk:        b.IsTrunk,
		Tracked:      b.Tracked,
		Current:      b.IsCurrent,
		NeedsRestack: b.NeedsRestack(),
		Upstream:     b.Upstream,
		Published:    b.Upstream != "",
		Ahead:        b.Ahead,
		Behind:       b.Behind,
		UpstreamGone: b.UpstreamGone,
		Worktree:     b.Worktree,
		Children:     []string{},
	}
	if b.Parent != nil {
		out.Parent = b.Parent.Name
	}
	if b.PR != nil {
		out.PR = &PullRequestJSON{Number: b.PR.Number, URL: b.PR.URL, Merged: b.PR.Merged}
	}
	for _, c := range b.Children {
		out.Children = append(out.Children, c.Name)
	}
	out.Problems = Problems(b)
	return out
}

// Problems lists the metadata faults recorded for a branch.
func Problems(b *stack.Branch) []string {
	var out []string
	if b.Orphaned {
		out = append(out, "orphaned")
	}
	if b.InCycle {
		out = append(out, "cycle")
	}
	if b.DuplicateID {
		out = append(out, "duplicate-id")
	}
	if b.BaseMissing {
		out = append(out, "missing-base-ref")
	}
	return out
}
