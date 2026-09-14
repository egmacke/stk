package git

import (
	"fmt"
	"strconv"
	"strings"
)

// BranchInfo is the git-side state of one local branch, collected in bulk.
type BranchInfo struct {
	Name         string
	SHA          string
	Upstream     string // short name, e.g. origin/foo; empty when unset
	Ahead        int
	Behind       int
	UpstreamGone bool
	IsHead       bool // checked out in the current worktree
}

// HasUpstream reports whether the branch has a configured upstream.
func (b BranchInfo) HasUpstream() bool { return b.Upstream != "" }

const branchFormat = "%(refname:short)%00%(objectname)%00%(upstream:short)%00%(upstream:track,nobracket)%00%(HEAD)"

// Branches lists every local branch with its upstream relationship in a single
// git invocation, so cost does not grow with one subprocess per branch.
func (repo *Repo) Branches() ([]BranchInfo, error) {
	res := repo.R.Run("for-each-ref", "--format="+branchFormat, "refs/heads")
	if !res.OK() {
		return nil, res.Error()
	}
	var out []BranchInfo
	for _, line := range res.Lines() {
		parts := strings.SplitN(line, "\x00", 5)
		if len(parts) < 5 {
			continue
		}
		b := BranchInfo{
			Name:     parts[0],
			SHA:      parts[1],
			Upstream: parts[2],
			IsHead:   parts[4] == "*",
		}
		parseTrack(parts[3], &b)
		out = append(out, b)
	}
	return out, nil
}

// parseTrack reads the "ahead 2, behind 1" / "gone" form produced by
// %(upstream:track,nobracket).
func parseTrack(track string, b *BranchInfo) {
	track = strings.TrimSpace(track)
	if track == "" {
		return
	}
	if track == "gone" {
		b.UpstreamGone = true
		return
	}
	for _, part := range strings.Split(track, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "ahead":
			b.Ahead = n
		case "behind":
			b.Behind = n
		}
	}
}

// BranchExists reports whether a local branch of that name exists.
func (repo *Repo) BranchExists(name string) bool {
	return repo.R.Run("show-ref", "--verify", "--quiet", "refs/heads/"+name).OK()
}

// ValidBranchName reports whether git would accept the name for a branch.
func (repo *Repo) ValidBranchName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	return repo.R.Run("check-ref-format", "--branch", name).OK()
}

// CreateBranch creates a branch at start without checking it out.
func (repo *Repo) CreateBranch(name, start string) error {
	return repo.R.Mutate("branch", name, start).Error()
}

// CreateTrackingBranch creates a local branch at the remote's branch of the
// same name, with that remote-tracking ref as its upstream.
func (repo *Repo) CreateTrackingBranch(name, remote string) error {
	return repo.R.Mutate("branch", "--track", name, remote+"/"+name).Error()
}

// Switch checks out an existing branch.
func (repo *Repo) Switch(name string) error { return repo.TrySwitch(name).Error() }

// TrySwitch is Switch with git's own result, for callers that have to tell why
// a checkout was refused rather than only that it was.
func (repo *Repo) TrySwitch(name string) Result {
	return repo.R.Mutate("switch", name)
}

// SwitchDetach detaches HEAD at a commit, releasing any branch it holds.
func (repo *Repo) SwitchDetach(rev string) error {
	return repo.R.Mutate("switch", "--detach", rev).Error()
}

// RenameBranch renames a local branch, moving its git config with it.
func (repo *Repo) RenameBranch(from, to string) error {
	return repo.R.Mutate("branch", "-m", from, to).Error()
}

// DeleteBranch removes a local branch. force skips git's own merge check;
// callers must have proved the branch is safe to delete.
func (repo *Repo) DeleteBranch(name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	return repo.R.Mutate("branch", flag, name).Error()
}

// CurrentBranch returns the branch checked out in this worktree, or "" when
// HEAD is detached.
func (repo *Repo) CurrentBranch() string {
	res := repo.R.Run("symbolic-ref", "--quiet", "--short", "HEAD")
	if !res.OK() {
		return ""
	}
	return res.Out()
}

// IsClean reports whether the current worktree has no staged or unstaged
// changes. Untracked files do not count as dirty.
func (repo *Repo) IsClean() (bool, error) {
	res := repo.R.Run("status", "--porcelain", "--untracked-files=no")
	if !res.OK() {
		return false, res.Error()
	}
	return strings.TrimSpace(res.Stdout) == "", nil
}

// RebaseInProgress reports whether this worktree is mid-rebase.
func (repo *Repo) RebaseInProgress() bool {
	res := repo.R.Run("rev-parse", "--git-path", "rebase-merge")
	merge := res.Out()
	res2 := repo.R.Run("rev-parse", "--git-path", "rebase-apply")
	apply := res2.Out()
	return pathExists(repo.Dir, merge) || pathExists(repo.Dir, apply)
}

// Fetch updates remote-tracking refs for one remote.
func (repo *Repo) Fetch(remote string) error {
	return repo.R.Mutate("fetch", "--prune", remote).Error()
}

// RemoteExists reports whether a remote is configured.
func (repo *Repo) RemoteExists(name string) bool {
	res := repo.R.Run("remote")
	if !res.OK() {
		return false
	}
	for _, line := range res.Lines() {
		if line == name {
			return true
		}
	}
	return false
}

// Remotes lists configured remotes.
func (repo *Repo) Remotes() []string {
	res := repo.R.Run("remote")
	if !res.OK() {
		return nil
	}
	return res.Lines()
}

// Rebase replays child's commits from oldBase onto newBase.
func (repo *Repo) Rebase(newBase, oldBase, child string, rebaseMerges bool) Result {
	args := []string{"rebase"}
	if repo.SupportsNoUpdateRefs() {
		// stk owns refs/stk/*; git must not rewrite refs on its own.
		args = append(args, "--no-update-refs")
	}
	if rebaseMerges {
		args = append(args, "--rebase-merges")
	}
	args = append(args, "--onto", newBase, oldBase, child)
	// Captured rather than inherited: stk prints one line per branch, and
	// git's own output is surfaced only when something goes wrong.
	return repo.R.Capture(args...)
}

func (repo *Repo) String() string {
	return fmt.Sprintf("repo(root=%s common=%s)", repo.Root, repo.CommonDir)
}
