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
	requireEqual(t, r.tracked("colleague"), false, "a fetched branch is not tracked by stk")
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
	requireEqual(t, r.branchExists("colleague"), false, "branch created under --dry-run")
	requireEqual(t, r.currentBranch(), "main", "current branch")
}
