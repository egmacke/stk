package e2e

import "testing"

// pushRemoteBranch publishes a branch from the second clone, as another person
// pushing a branch this repository has never seen would.
func (r *repo) pushRemoteBranch(clone, branch, message string) {
	r.t.Helper()
	r.runIn(clone, "", "git", "switch", "-q", "-c", branch)
	r.runIn(clone, "", "git", "commit", "-q", "--allow-empty", "-m", message)
	res := r.runIn(clone, "", "git", "push", "-q", "origin", branch)
	if res.Code != 0 {
		r.t.Fatalf("pushing %s to origin failed: %s%s", branch, res.Stdout, res.Stderr)
	}
}

func TestCheckoutFetchesAnUnseenRemoteBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.pushRemoteBranch(r.upstreamClone(), "colleague", "their work")

	out := r.stk("checkout", "colleague")
	requireContains(t, out, "Fetching origin")
	requireContains(t, out, "Created colleague from origin/colleague")
	requireContains(t, out, "Switched to colleague")
	requireEqual(t, r.currentBranch(), "colleague", "checked out branch")
	requireEqual(t, r.git("rev-parse", "--abbrev-ref", "colleague@{upstream}"),
		"origin/colleague", "upstream of the new branch")
}

func TestCheckoutTracksAFetchedBranchOntoTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	r.pushRemoteBranch(r.upstreamClone(), "colleague", "their work")

	out := r.stk("checkout", "colleague")
	requireContains(t, out, "Tracking colleague with parent main")
	requireEqual(t, r.tracked("colleague"), true, "a fetched branch is tracked")
	requireEqual(t, r.parentOf("colleague"), "main", "parent of the fetched branch")
}

func TestCheckoutTracksALooseLocalBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.git("switch", "-q", "-c", "loose")
	r.git("switch", "-q", "main")

	out := r.stk("checkout", "loose")
	requireContains(t, out, "Tracking loose with parent main")
	requireEqual(t, r.currentBranch(), "loose", "checked out branch")
	requireEqual(t, r.parentOf("loose"), "main", "parent of the loose branch")
}

func TestCheckoutNoTrackLeavesTheBranchAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	r.pushRemoteBranch(r.upstreamClone(), "colleague", "their work")

	out := r.stk("checkout", "-n", "colleague")
	requireNotContains(t, out, "Tracking colleague")
	requireEqual(t, r.currentBranch(), "colleague", "checked out branch")
	requireEqual(t, r.tracked("colleague"), false, "--no-track leaves the branch untracked")
}

func TestCheckoutTrackFromNamesAnotherParent(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("api.go", "package api\n", "api")
	r.git("switch", "-q", "-c", "loose")
	r.git("switch", "-q", "main")

	out := r.stk("checkout", "--track-from", "api", "loose")
	requireContains(t, out, "Tracking loose with parent api")
	requireEqual(t, r.parentOf("loose"), "api", "parent given by --track-from")
}

func TestCheckoutTrackFromRejectsAnUntrackableParent(t *testing.T) {
	r := newRepoWithRemote(t)
	r.git("switch", "-q", "-c", "other")
	r.git("switch", "-q", "-c", "loose")
	r.git("switch", "-q", "main")

	out := r.stkFail("checkout", "-tother", "loose")
	requireContains(t, out, `branch "other" is not tracked by stk`)
	requireEqual(t, r.currentBranch(), "main", "nothing is checked out when the parent is refused")
	requireEqual(t, r.tracked("loose"), false, "the branch stays untracked")
}

func TestCheckoutRejectsBothTrackFlags(t *testing.T) {
	r := newRepo(t)
	out := r.stkFail("checkout", "-n", "-tmain", "main")
	requireContains(t, out, "use either --no-track or --track-from, not both")
}

func TestCheckoutOfATrackedBranchDoesNotRetrack(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "api")
	r.commit("api.go", "package api\n", "api")
	r.stk("create", "web")
	r.commit("web.go", "package web\n", "web")

	out := r.stk("checkout", "api")
	requireNotContains(t, out, "Tracking api")
	requireEqual(t, r.parentOf("api"), "main", "parent is untouched")

	out = r.stk("checkout", "main")
	requireNotContains(t, out, "Tracking main")
	requireEqual(t, r.tracked("main"), false, "trunk is never tracked metadata")
}

func TestCheckoutOfAFetchedRemoteBranchSkipsTheFetch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.pushRemoteBranch(r.upstreamClone(), "colleague", "their work")
	r.git("fetch", "-q", "origin")

	out := r.stk("checkout", "colleague")
	requireNotContains(t, out, "Fetching origin")
	requireEqual(t, r.currentBranch(), "colleague", "checked out branch")
}

func TestCheckoutOfAnUnknownBranchNamesTheRemote(t *testing.T) {
	r := newRepoWithRemote(t)
	out := r.stkFail("checkout", "nope")
	requireContains(t, out, `branch "nope" does not exist locally or on origin`)
}

func TestCheckoutOfAnUnknownBranchWithoutARemote(t *testing.T) {
	r := newRepo(t)
	out := r.stkFail("checkout", "nope")
	requireContains(t, out, `branch "nope" does not exist`)
	requireNotContains(t, out, "or on")
}

func TestCheckoutDryRunDoesNotCreateTheBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.pushRemoteBranch(r.upstreamClone(), "colleague", "their work")

	out := r.stk("--dry-run", "checkout", "colleague")
	requireContains(t, out, "(dry-run) would fetch origin")
	requireContains(t, out, "(dry-run) would track colleague with parent main")
	requireEqual(t, r.branchExists("colleague"), false, "branch created under --dry-run")
	requireEqual(t, r.currentBranch(), "main", "current branch")
}

func TestCheckoutDryRunDoesNotTrackALooseBranch(t *testing.T) {
	r := newRepo(t)
	r.git("switch", "-q", "-c", "loose")
	r.git("switch", "-q", "main")

	out := r.stk("--dry-run", "checkout", "loose")
	requireContains(t, out, "(dry-run) would switch to loose")
	requireContains(t, out, "(dry-run) would track loose with parent main")
	requireEqual(t, r.currentBranch(), "main", "current branch")
	requireEqual(t, r.tracked("loose"), false, "tracked under --dry-run")
}
