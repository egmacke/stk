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

// WorktreeRunner returns a runner rooted at another worktree of this
// repository, or this worktree's own runner when dir is empty.
func (repo *Repo) WorktreeRunner(dir string) *Runner {
	if dir == "" {
		return repo.R
	}
	return repo.R.WithDir(dir)
}

// WorktreeClean reports whether the worktree at dir has no staged or unstaged
// changes. Untracked files do not count as dirty, exactly as in IsClean.
func (repo *Repo) WorktreeClean(dir string) (bool, error) {
	res := repo.WorktreeRunner(dir).Run("status", "--porcelain", "--untracked-files=no")
	if !res.OK() {
		return false, res.Error()
	}
	return strings.TrimSpace(res.Stdout) == "", nil
}

// WorktreeBusy reports whether the worktree at dir is part-way through a git
// operation of its own: a rebase, a merge, a cherry-pick, a revert or a
// bisect. stk never rewrites a branch out from under one of those.
func (repo *Repo) WorktreeBusy(dir string) bool {
	if repo.RebaseInProgressIn(dir) {
		return true
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
		if repo.gitPathExists(dir, name) {
			return true
		}
	}
	return false
}

// AbortRebaseIn ends a rebase paused in another worktree, putting its branch,
// index and files back where they were.
func (repo *Repo) AbortRebaseIn(dir string) error {
	return repo.WorktreeRunner(dir).Capture("rebase", "--abort").Error()
}

// ResetHardIn moves the branch another worktree has checked out to a commit,
// taking that worktree's index and files with it. Callers must have proved
// there is nothing there to lose.
func (repo *Repo) ResetHardIn(dir, sha string) error {
	return repo.WorktreeRunner(dir).Mutate("reset", "--hard", "--quiet", sha).Error()
}
