package e2e

import "testing"

func TestRenameMovesTheRemoteBranchWithIt(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")
	tip := r.sha("api")

	out := r.stk("--no-interactive", "rename", "--yes", "api", "backend")
	requireContains(t, out, "origin/api -> origin/backend")

	heads := r.git("ls-remote", "--heads", "origin")
	requireContains(t, heads, "refs/heads/backend")
	requireNotContains(t, heads, "refs/heads/api")
	requireEqual(t, r.sha("refs/remotes/origin/backend"), tip, "the remote branch carries the same commit")
	requireEqual(t, r.git("rev-parse", "--abbrev-ref", "backend@{upstream}"), "origin/backend", "upstream follows the rename")
	requireEqual(t, r.parentOf("backend"), "main", "the stack is untouched")
}

func TestRenameAsksBeforeMovingTheRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")

	res := r.stkAt(r.Root, "n\n", "--interactive", "rename", "api", "backend")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "Rename origin/api to origin/backend as well?")
	requireContains(t, res.All(), "GitHub closes any open pull request")
	requireContains(t, res.All(), "Remote branch remains:")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
	requireEqual(t, r.branchExists("backend"), true, "the local rename still happened")
}

func TestRenameAcceptedAtThePromptMovesTheRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")

	res := r.stkAt(r.Root, "y\n", "--interactive", "rename", "api", "backend")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "origin/api -> origin/backend")
	requireNotContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestRenameNoRemoteLeavesTheRemoteAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")

	out := r.stk("--no-interactive", "rename", "--no-remote", "--yes", "api", "backend")
	requireContains(t, out, "Remote branch remains:")
	requireContains(t, out, "git push -u origin backend")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestRenameRefusesToOverwriteAnExistingRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")
	// Somebody else already published a branch under the new name.
	r.git("push", "-q", "origin", "main:backend")

	out := r.stkFail("--no-interactive", "rename", "--remote", "api", "backend")
	requireContains(t, out, "origin/backend already exists")
	requireEqual(t, r.branchExists("api"), true, "nothing was renamed")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestRenameDryRunSaysWhatWouldMove(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")

	out := r.stk("--no-interactive", "--dry-run", "rename", "--yes", "api", "backend")
	requireContains(t, out, "Would rename api -> backend")
	requireContains(t, out, "Would rename origin/api -> origin/backend")
	requireEqual(t, r.branchExists("api"), true, "dry run renamed a branch")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}
