package e2e

import (
	"strings"
	"testing"
)

func TestFoldIntoParent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "service")

	out := r.stk("fold", "--yes")
	requireContains(t, out, "Folding into api")
	requireContains(t, out, "Reparented ui onto api")
	requireContains(t, out, "Folded and deleted service")
	requireContains(t, out, "1 branch(es) folded into api")

	if r.branchExists("service") {
		t.Fatal("the folded branch is still there")
	}
	// api holds both sets of commits, ui now sits on api, and no commit was
	// lost or rewritten.
	requireEqual(t, r.log("api")[0], "service", "api ends at service's commit")
	requireEqual(t, r.log("api")[1], "api", "api still has its own commit")
	requireEqual(t, r.parentOf("ui"), "api", "ui was reparented")
	requireEqual(t, r.baseOf("ui"), r.sha("api"), "ui's base caught up")
	requireEqual(t, r.currentBranch(), "api", "left standing on the survivor")
}

func TestFoldIntoNamedBranch(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui", "polish")
	r.stk("checkout", "ui")

	out := r.stk("fold", "--into", "api", "--yes")
	requireContains(t, out, "Folded and deleted service")
	requireContains(t, out, "Folded and deleted ui")
	requireContains(t, out, "Reparented polish onto api")

	for _, gone := range []string{"service", "ui"} {
		if r.branchExists(gone) {
			t.Fatalf("%s is still there", gone)
		}
	}
	requireEqual(t, strings.Join(r.log("api")[:3], ","), "ui,service,api", "api holds the run's commits")
	requireEqual(t, r.parentOf("polish"), "api", "polish was reparented")
	requireEqual(t, r.log("polish")[0], "polish", "polish kept its own commit")
}

func TestFoldWholeStack(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "service")

	out := r.stk("fold", "--stack", "--yes")
	requireContains(t, out, "Folding into api")
	requireEqual(t, r.branchExists("service"), false, "service folded")
	requireEqual(t, r.branchExists("ui"), false, "ui folded")
	requireEqual(t, strings.Join(r.log("api")[:3], ","), "ui,service,api", "everything landed on api")
	requireEqual(t, stripStatus(r.stackText()), "main\n└─ api  ←", "one branch left")
}

func TestFoldRefusesAStackThatNeedsRestacking(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")

	out := r.stkFail("fold", "service", "--yes")
	requireContains(t, out, "does not contain")
	requireContains(t, out, "stk restack")
	// Nothing moved.
	requireEqual(t, r.branchExists("service"), true, "service survived the refusal")
	requireEqual(t, r.log("api")[0], "api amended", "api untouched")
}

func TestFoldRefusesTrunkAndBranchesOnIt(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")

	r.stk("checkout", "main")
	requireContains(t, r.stkFail("fold"), "is the trunk branch")

	r.stk("checkout", "api")
	requireContains(t, r.stkFail("fold"), "nothing below it to fold into")
	requireContains(t, r.stkFail("fold", "--into", "main"), "never folds a stack into it")
}

func TestFoldRefusesABranchNotAboveTheTarget(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "main")
	r.stk("create", "other")
	r.commit("other.txt", "other\n", "other")

	out := r.stkFail("fold", "--into", "api", "--yes")
	requireContains(t, out, "not stacked above")
	requireEqual(t, r.branchExists("other"), true, "nothing was deleted")
}

func TestFoldRefusesABranchingStack(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	r.stk("create", "sibling")
	r.commit("sibling.txt", "s\n", "sibling")

	out := r.stkFail("fold", "--stack", "--yes")
	requireContains(t, out, "branches, so there is no single branch")
	requireEqual(t, r.branchExists("service"), true, "nothing was deleted")
}

func TestFoldDryRunChangesNothing(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "service")

	out := r.stk("fold", "--dry-run")
	requireContains(t, out, "(dry-run) Folding into api")
	requireContains(t, out, "service   1 commit(s), deleted")
	requireNotContains(t, out, "2 commit(s), deleted")
	requireContains(t, out, "ui is reparented onto api")
	requireContains(t, out, "No changes have been made.")

	requireEqual(t, r.branchExists("service"), true, "service still there")
	requireEqual(t, r.parentOf("ui"), "service", "ui still on service")
}

func TestFoldAsksBeforeDeleting(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "service")

	// Answering no leaves the stack exactly as it was.
	res := r.stkAt(r.Root, "n\n", "--interactive", "fold")
	requireEqual(t, res.Code, 0, "declining is not a failure")
	requireContains(t, res.All(), "Fold 1 branch(es) into api?")
	requireContains(t, res.All(), "Leaving the stack as it is.")
	requireEqual(t, r.branchExists("service"), true, "service survived")

	// Answering yes does the work.
	res = r.stkAt(r.Root, "y\n", "--interactive", "fold")
	requireEqual(t, res.Code, 0, "fold succeeded")
	requireEqual(t, r.branchExists("service"), false, "service folded")
}

func TestFoldCarriesUncommittedChanges(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "service")
	r.write("README.md", "hello\nwork in progress\n")

	out := r.stk("fold", "--yes")
	requireContains(t, out, "Stashed uncommitted changes")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")
	requireEqual(t, r.currentBranch(), "api", "on the survivor")
}

func TestFoldRefusesABranchHeldInAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.addWorktree("elsewhere", "service")
	r.stk("checkout", "ui")

	out := r.stkFail("fold", "--into", "api", "--yes")
	requireContains(t, out, "service is checked out in")
	requireEqual(t, r.branchExists("service"), true, "nothing was deleted")
}

func TestFoldPrintsRecoveryCommands(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "service")
	folded := r.sha("service")

	out := r.stk("fold", "--yes")
	requireContains(t, out, "git branch service "+folded[:7])

	// The printed command really does bring the branch back.
	r.git("branch", "service", folded)
	requireEqual(t, r.sha("service"), folded, "recovered at the same commit")
}

func TestFoldClosesTheFoldedPullRequests(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("ss", "-pn")

	r.stk("checkout", "service")
	out := r.stk("fold", "--yes", "--close-pulls")
	requireContains(t, out, "Closed #2 (service)")
	requireNotContains(t, out, "still open")

	// Closed with a comment pointing at the pull request that absorbed it.
	comments := r.prComments(2)
	requireContains(t, comments[len(comments)-1], "Folded into #1")
	for _, pr := range r.ghStubState().PRs {
		if pr.Head == "service" {
			requireEqual(t, pr.State, "CLOSED", "service's pull request is closed")
		}
	}
}

func TestFoldWithoutClosePullsSaysSo(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "service")

	out := r.stk("fold", "--yes")
	requireContains(t, out, "are still open")
	requireContains(t, out, "--close-pulls")
}
