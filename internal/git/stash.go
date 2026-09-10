package git

import "fmt"

// StashCreate builds a stash commit from the index and the working tree
// without touching either, and returns its object id. It returns "" when
// there is nothing to stash.
//
// Untracked files are left where they are, matching both IsClean and git's
// own --autostash: a stash the user did not ask for should never make files
// disappear from a directory listing.
//
// Callers must not reach here during a dry run; the commit is written to the
// object database even though no ref moves.
func (repo *Repo) StashCreate(message string) (string, error) {
	res := repo.R.Run("stash", "create", message)
	if !res.OK() {
		return "", res.Error()
	}
	return res.Out(), nil
}

// StashApply restores a stash commit into the working tree, leaving the stash
// commit itself alone so a failed apply loses nothing.
//
// Staged changes come back unstaged, as they do from git's own autostash.
// --quiet is deliberately not passed: it would suppress the CONFLICT lines
// naming the files, and a conflict the caller leaves in the tree must be
// visible.
func (repo *Repo) StashApply(sha string) Result {
	return repo.R.Capture("stash", "apply", sha)
}

// StashStore adds an existing stash commit to the stash reflog, so it shows up
// in "git stash list" and survives garbage collection.
func (repo *Repo) StashStore(sha, message string) error {
	return repo.R.Mutate("stash", "store", "-m", message, sha).Error()
}

// ResetHard discards every tracked modification in the working tree and index.
// Callers must have preserved the content elsewhere first.
func (repo *Repo) ResetHard() error {
	return repo.R.Mutate("reset", "--hard", "--quiet").Error()
}

// StashListRef names an entry of the stash reflog.
func StashListRef(n int) string { return fmt.Sprintf("stash@{%d}", n) }
