package e2e

import (
	"strings"
	"testing"
)

func TestDeleteReparentsTheBranchesAbove(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b", "c")
	r.stk("checkout", "main")
	baseID := r.git("config", "--local", "--get", "branch.b.stk-id")

	out := r.stk("--no-interactive", "delete", "--yes", "b")
	requireContains(t, out, "c is reparented onto a")
	requireContains(t, out, "Reparented c onto a")
	requireContains(t, out, "Deleted b")
	requireEqual(t, r.branchExists("b"), false, "b is gone")
	requireEqual(t, r.branchExists("c"), true, "c survives")
	requireEqual(t, r.parentOf("c"), "a", "c hangs off a")
	requireEqual(t, r.tracked("b"), false, "metadata is gone")
	requireNotContains(t, r.git("for-each-ref", "refs/stk/base"), baseID)
}

func TestDeleteNotesThatNothingIsLostWhenAChildHoldsTheCommits(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "delete", "--yes", "a")
	requireContains(t, out, "nothing that main or another branch lacks")
	requireNotContains(t, out, "Recover a deleted branch with")
}

func TestDeleteStepsOffTheBranchItRemoves(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	tip := r.sha("b")

	out := r.stk("--no-interactive", "delete", "--yes")
	requireContains(t, out, "1 commit(s) kept nowhere else")
	requireContains(t, out, "Switched to a")
	requireContains(t, out, "Deleted b")
	requireEqual(t, r.currentBranch(), "a", "stk stepped down to the parent")
	requireEqual(t, r.branchExists("b"), false, "b is gone")

	// The commits are recoverable, and stk said how.
	requireContains(t, out, "Recover a deleted branch with:")
	requireContains(t, out, "git branch b "+tip[:7])
	r.git("branch", "b", tip)
	requireEqual(t, r.log("b")[0], "b", "the branch came back")
}

func TestDeleteDeclinedLeavesEverythingAlone(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("checkout", "main")

	res := r.stkAt(r.Root, "n\n", "--interactive", "delete", "a")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "Delete 1 branch(es)?")
	requireContains(t, res.All(), "Leaving them in place.")
	requireEqual(t, r.branchExists("a"), true, "the branch is still there")
}

func TestDeleteRefusesWithoutAnAnswer(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("checkout", "main")

	out := r.stkFail("--no-interactive", "delete", "a")
	requireContains(t, out, "stk delete --yes")
	requireEqual(t, r.branchExists("a"), true, "nothing was deleted")
}

func TestDeleteRefusesTrunk(t *testing.T) {
	r := newRepo(t)
	out := r.stkFail("--no-interactive", "delete", "--yes", "main")
	requireContains(t, out, "main is the trunk branch")
}

func TestDeleteRefusesABranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("checkout", "main")
	path := r.addWorktree("wt-a", "a")

	out := r.stkFail("--no-interactive", "delete", "--yes", "a")
	requireContains(t, out, "a is checked out in:")
	requireContains(t, out, path)
	requireEqual(t, r.branchExists("a"), true, "the held branch survives")
}

func TestDeleteDryRunChangesNothing(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "--dry-run", "delete", "--yes", "a")
	requireContains(t, out, "(dry-run) Deleting:")
	requireContains(t, out, "No changes have been made.")
	requireEqual(t, r.branchExists("a"), true, "dry run deleted a branch")
}

func TestDeleteAsksAboutTheRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "a\n", "api work")
	r.git("push", "-q", "-u", "origin", "api")
	r.stk("checkout", "main")

	res := r.stkAt(r.Root, "y\ny\n", "--interactive", "delete", "api")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "These branches also exist on the remote:")
	requireContains(t, res.All(), "origin/api")
	requireContains(t, res.All(), "GitHub closes any open pull request")
	requireContains(t, res.All(), "Deleted origin/api")
	requireEqual(t, r.branchExists("api"), false, "the local branch is gone")
	requireNotContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestDeleteKeepsTheRemoteBranchWhenDeclined(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "a\n", "api work")
	r.git("push", "-q", "-u", "origin", "api")
	r.stk("checkout", "main")

	res := r.stkAt(r.Root, "y\nn\n", "--interactive", "delete", "api")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "Leaving the remote branches in place.")
	requireEqual(t, r.branchExists("api"), false, "the local branch is gone")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestDeleteYesAlsoRemovesTheRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "a\n", "api work")
	r.git("push", "-q", "-u", "origin", "api")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "delete", "--yes", "api")
	requireContains(t, out, "Deleted origin/api")
	requireNotContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestDeleteNoRemoteLeavesTheRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "a\n", "api work")
	r.git("push", "-q", "-u", "origin", "api")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "delete", "--yes", "--no-remote", "api")
	requireNotContains(t, out, "origin/api")
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestDeleteRemovesSeveralBranchesTopDown(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b", "c")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "delete", "--yes", "a", "c", "b")
	for _, name := range []string{"a", "b", "c"} {
		requireEqual(t, r.branchExists(name), false, name+" is gone")
	}
	// Nothing that survives holds any of them, so each gets its own way back.
	requireContains(t, out, "Recover a deleted branch with:")
	requireEqual(t, strings.Count(out, "    git branch "), 3, "one recovery line per branch")
	for _, name := range []string{"a", "b", "c"} {
		requireContains(t, out, "git branch "+name+" ")
	}
}
