package e2e

import (
	"strings"
	"testing"
)

// looseBranch makes an ordinary git branch off main with one commit of its
// own, and returns to main.
func looseBranch(r *repo, name string) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", name, "main")
	r.commit(name+".txt", name+"\n", name+" work")
	r.git("checkout", "-q", "main")
}

func TestTriageTracksAndDeletesLocalOnlyBranches(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "keep")
	looseBranch(r, "spike")
	tip := r.sha("spike")

	// keep: track. spike: delete, then confirm losing its commit.
	res := r.stkAt(r.Root, "t\nd\ny\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	out := res.All()
	requireContains(t, out, "2 untracked branch(es):")
	requireContains(t, out, "[1/2] keep")
	requireContains(t, out, "not on the remote, with 1 commit(s) kept nowhere else")
	requireContains(t, out, "[t]rack, [d]elete, [S]kip, [q]uit")
	requireContains(t, out, "Tracking keep with parent main")
	requireContains(t, out, "1 commit(s) kept nowhere else. Delete it anyway?")
	requireContains(t, out, "Deleted spike")
	requireContains(t, out, "git branch spike "+tip[:7])
	requireContains(t, out, "Tracked 1, deleted 1, left 0 untracked.")

	requireEqual(t, r.tracked("keep"), true, "keep is tracked")
	requireEqual(t, r.parentOf("keep"), "main", "keep sits on trunk")
	requireEqual(t, r.branchExists("spike"), false, "spike is gone")
}

func TestTriageOnlyOffersTrackingForARemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	looseBranch(r, "api")
	r.git("push", "-q", "-u", "origin", "api")

	// "d" is not an answer here; stk asks again and the skip stands.
	res := r.stkAt(r.Root, "d\ns\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	out := res.All()
	requireContains(t, out, "on the remote as origin/api")
	requireNotContains(t, out, "[d]elete")
	requireContains(t, out, "Answer t, s, q.")
	requireContains(t, out, "Left api untracked")
	requireEqual(t, r.branchExists("api"), true, "api survives")
	requireEqual(t, r.tracked("api"), false, "api stays untracked")

	res = r.stkAt(r.Root, "t\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireEqual(t, r.tracked("api"), true, "api is tracked")
}

func TestTriageKeepsABranchWhenTheSecondQuestionIsDeclined(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "spike")

	res := r.stkAt(r.Root, "d\nn\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "Kept spike")
	requireEqual(t, r.branchExists("spike"), true, "spike survives")
}

func TestTriageDeletesAMergedBranchWithoutAskingTwice(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "old", "main")

	res := r.stkAt(r.Root, "d\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "nothing that main or another branch lacks")
	requireNotContains(t, res.All(), "Delete it anyway?")
	requireEqual(t, r.branchExists("old"), false, "old is gone")
}

func TestTriageStepsOffTheCurrentBranchToDeleteIt(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "old", "main")
	r.git("checkout", "-q", "old")

	res := r.stkAt(r.Root, "d\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireEqual(t, r.currentBranch(), "main", "stk moved to trunk")
	requireEqual(t, r.branchExists("old"), false, "old is gone")
}

func TestTriageNeverOffersToDeleteABranchInAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "old", "main")
	r.addWorktree("other", "old")

	res := r.stkAt(r.Root, "\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "checked out in")
	requireNotContains(t, res.All(), "[d]elete")
	requireContains(t, res.All(), "Left old untracked")
	requireEqual(t, r.branchExists("old"), true, "old survives")
}

func TestTriageQuitLeavesTheRestAlone(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "a")
	looseBranch(r, "b")

	res := r.stkAt(r.Root, "t\nq\n", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "Tracked 1, deleted 0, left 1 untracked.")
	requireEqual(t, r.tracked("a"), true, "a is tracked")
	requireEqual(t, r.tracked("b"), false, "b was never touched")
}

func TestTriageEndOfInputStopsQuietly(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "a")

	res := r.stkAt(r.Root, "", "--interactive", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "left 1 untracked")
	requireEqual(t, r.tracked("a"), false, "a is untouched")
}

func TestTriageTracksOntoTheNamedParent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "base")
	r.git("checkout", "-q", "-b", "loose", "base")
	r.commit("l.txt", "l\n", "loose work")
	r.git("checkout", "-q", "main")

	res := r.stkAt(r.Root, "t\n", "--interactive", "triage", "--parent", "base")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireEqual(t, r.parentOf("loose"), "base", "loose sits on base")
}

func TestTriageRefusesAnUntrackedParent(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "a")
	res := r.stkAt(r.Root, "", "--interactive", "triage", "--parent", "a")
	requireEqual(t, res.Code != 0, true, "exit code")
	requireContains(t, res.All(), "is not tracked by stk")
}

func TestTriageNeedsSomeoneToAsk(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "a")
	out := r.stkFail("--no-interactive", "triage")
	requireContains(t, out, "stk cannot ask here")
}

func TestTriageWithNothingToDo(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	out := r.stk("--no-interactive", "triage")
	requireContains(t, out, "Every local branch is already tracked.")
}

func TestTriageDryRunChangesNothing(t *testing.T) {
	r := newRepo(t)
	looseBranch(r, "a")
	r.git("branch", "old", "main")

	res := r.stkAt(r.Root, "t\nd\n", "--interactive", "--dry-run", "triage")
	requireEqual(t, res.Code, 0, "exit code\n"+res.All())
	requireContains(t, res.All(), "Would track 1, delete 1 and leave 0 untracked.")
	requireEqual(t, r.tracked("a"), false, "a stays untracked")
	requireEqual(t, r.branchExists("old"), true, "old survives")
	requireEqual(t, strings.Contains(res.All(), "Deleted old"), false, "nothing was deleted")
}
