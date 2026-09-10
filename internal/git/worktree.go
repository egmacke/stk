package git

import (
	"path/filepath"
	"strings"
)

// Worktree is one entry of "git worktree list --porcelain".
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short branch name, empty when detached or bare
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// Worktrees lists every worktree of the repository in one git invocation.
func (repo *Repo) Worktrees() ([]Worktree, error) {
	res := repo.R.Run("worktree", "list", "--porcelain")
	if !res.OK() {
		return nil, res.Error()
	}
	var out []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.Clean(value)}
		case "HEAD":
			if cur != nil {
				cur.Head = value
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()
	return out, nil
}

// BranchWorktrees maps each checked-out branch name to the worktree holding it.
func (repo *Repo) BranchWorktrees() (map[string]string, error) {
	list, err := repo.Worktrees()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, wt := range list {
		if wt.Branch != "" {
			out[wt.Branch] = wt.Path
		}
	}
	return out, nil
}
