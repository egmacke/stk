package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// upstreamClone returns a second clone used to publish commits as if another
// person had pushed them.
func (r *repo) upstreamClone() string {
	r.t.Helper()
	path := filepath.Join(filepath.Dir(r.Root), "upstream")
	r.runIn(filepath.Dir(r.Root), "", "git", "clone", "-q", r.Origin, path)
	return path
}

func (r *repo) pushRemoteCommit(clone, message string) {
	r.t.Helper()
	r.runIn(clone, "", "git", "commit", "-q", "--allow-empty", "-m", message)
	res := r.runIn(clone, "", "git", "push", "-q", "origin", "main")
	if res.Code != 0 {
		r.t.Fatalf("pushing to origin failed: %s%s", res.Stdout, res.Stderr)
	}
}

func TestSyncFastForwardsTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	clone := r.upstreamClone()
	r.pushRemoteCommit(clone, "remote one")
	r.pushRemoteCommit(clone, "remote two")

	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	requireContains(t, out, "Fetching origin")
	requireContains(t, out, "main fast-forwarded by 2 commit(s)")
	requireEqual(t, r.sha("main"), r.sha("origin/main"), "trunk matches the remote")

	// The stack was rebased onto the new trunk.
	requireContains(t, strings.Join(r.log("api"), "\n"), "remote two")
	requireEqual(t, r.log("service")[1], "api", "service still sits on api")
	requireEqual(t, r.baseOf("api"), r.sha("main"), "api base is the new trunk tip")
}

func TestSyncStopsOnDivergedTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	clone := r.upstreamClone()
	r.pushRemoteCommit(clone, "remote work")

	// Add a local-only commit to trunk so the two have diverged.
	r.git("checkout", "-q", "main")
	r.git("commit", "-q", "--allow-empty", "-m", "local only")
	localTip := r.sha("main")

	out := r.stkFail("--no-interactive", "sync")
	requireContains(t, out, "has diverged from origin/main")
	requireContains(t, out, "will not overwrite")
	requireEqual(t, r.sha("main"), localTip, "diverged trunk left untouched")
}

func TestSyncDetectsMergedBranches(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "sync", "--no-restack")
	requireContains(t, out, "The following branches add nothing to main:")
	requireContains(t, out, "merged")
	requireContains(t, out, "re-run with --cleanup")
	if !r.branchExists("merged") {
		t.Fatal("non-interactive sync deleted a branch without --cleanup")
	}
}

func TestSyncCleanupPromptDeclined(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")

	res := r.stkAt(r.Root, "n\n", "--interactive", "sync", "--no-restack")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "Remove these 1 local branches?")
	requireContains(t, res.All(), "Leaving them in place.")
	if !r.branchExists("merged") {
		t.Fatal("branch deleted after declining cleanup")
	}
}

func TestSyncCleanupPromptAccepted(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")

	res := r.stkAt(r.Root, "y\n", "--interactive", "sync", "--no-restack")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.All(), "Removed merged")
	if r.branchExists("merged") {
		t.Fatal("branch survived accepted cleanup")
	}
	if r.tracked("merged") {
		t.Fatal("metadata survived cleanup")
	}
	if strings.Contains(r.git("for-each-ref", "refs/stk/base"), "merged") {
		t.Fatal("base ref survived cleanup")
	}
}

func TestSyncNoCleanupSkipsDetection(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")

	out := r.stk("--no-interactive", "sync", "--no-cleanup", "--no-restack")
	requireNotContains(t, out, "fully contained")
	if !r.branchExists("merged") {
		t.Fatal("--no-cleanup deleted a branch")
	}
}

func TestSyncPrunesMergedBranchAndReparentsDescendant(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "a")
	r.commit("a.txt", "a\n", "a")
	r.stk("create", "b")
	r.commit("b.txt", "b\n", "b")
	// a lands on trunk; b does not.
	r.git("push", "-q", "origin", "a:main")
	r.stk("checkout", "b")

	out := r.stk("--no-interactive", "sync", "--cleanup")
	requireContains(t, out, "Reparented b onto main")
	requireContains(t, out, "Removed a")
	if r.branchExists("a") {
		t.Fatal("merged branch survived cleanup")
	}
	requireEqual(t, r.parentOf("b"), "main", "descendant reparented onto trunk")
	requireEqual(t, r.baseOf("b"), r.sha("main"), "descendant base updated by the restack")
	requireEqual(t, r.log("b")[0], "b", "b keeps its own commit")
	requireEqual(t, r.log("b")[1], "a", "b now sits directly on trunk")
}

func TestSyncRestacksEveryStack(t *testing.T) {
	r := newRepoWithRemote(t)
	// Two independent stacks off trunk.
	r.stk("create", "one/api")
	r.commit("one-api.txt", "1\n", "one api")
	r.stk("create", "one/ui")
	r.commit("one-ui.txt", "1\n", "one ui")
	r.stk("checkout", "main")
	r.stk("create", "two/api")
	r.commit("two-api.txt", "2\n", "two api")
	r.stk("create", "two/ui")
	r.commit("two-ui.txt", "2\n", "two ui")
	r.stk("checkout", "main")

	clone := r.upstreamClone()
	r.pushRemoteCommit(clone, "remote work")

	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	for _, name := range []string{"one/api", "one/ui", "two/api", "two/ui"} {
		requireContains(t, out, name)
	}
	for _, name := range []string{"one/api", "two/api"} {
		requireContains(t, strings.Join(r.log(name), "\n"), "remote work")
	}
	requireEqual(t, r.log("one/ui")[1], "one api", "one/ui still on one/api")
	requireEqual(t, r.log("two/ui")[1], "two api", "two/ui still on two/api")
}

func TestSyncStackLimitsRestackToTheCurrentStack(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "one")
	r.commit("one.txt", "1\n", "one")
	r.stk("checkout", "main")
	r.stk("create", "two")
	r.commit("two.txt", "2\n", "two")

	clone := r.upstreamClone()
	r.pushRemoteCommit(clone, "remote work")

	oneTip := r.sha("one")
	r.stk("checkout", "two")
	r.stk("--no-interactive", "sync", "--stack", "--no-cleanup")

	requireEqual(t, r.sha("one"), oneTip, "the other stack was left alone")
	requireContains(t, strings.Join(r.log("two"), "\n"), "remote work")
}

func TestSyncNoRestackLeavesBranchesAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	clone := r.upstreamClone()
	r.pushRemoteCommit(clone, "remote work")
	tip := r.sha("api")

	r.stk("--no-interactive", "sync", "--no-restack", "--no-cleanup")
	requireEqual(t, r.sha("main"), r.sha("origin/main"), "trunk still updated")
	requireEqual(t, r.sha("api"), tip, "feature branch untouched")
}

func TestSyncNeverPushes(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "a\n", "a")

	r.stk("--no-interactive", "sync", "--no-cleanup")
	requireNotContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestSyncCleansUpASquashMergedBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.git("push", "-q", "origin", "api", "service")

	// A squash merge: the content lands on trunk under a commit of its own,
	// so api is no ancestor of main and ancestry alone cannot see the merge.
	r.stk("checkout", "main")
	r.git("merge", "-q", "--squash", "api")
	r.git("commit", "-q", "-m", "Add api (#1)")
	r.git("push", "-q", "origin", "main")
	r.git("checkout", "-q", "service")
	requireEqual(t, r.runIn(r.Root, "", "git", "merge-base", "--is-ancestor", "api", "main").Code, 1,
		"api is deliberately not an ancestor of main")

	// One sync is enough: the branch is gone and service sits on trunk.
	out := r.stk("--no-interactive", "sync", "--cleanup")
	requireContains(t, out, "add nothing to main")
	requireContains(t, out, "Removed api")
	requireContains(t, out, "Reparented service onto main")
	requireEqual(t, r.branchExists("api"), false, "the squash-merged branch is gone")
	requireEqual(t, r.parentOf("service"), "main", "service was reparented")
	requireEqual(t, r.log("service")[0], "service", "service kept its own commit")
	requireEqual(t, r.log("service")[1], "Add api (#1)", "service sits on the squashed commit")
}

func TestSyncRefusesDirtyWorktreeWithNoAutostash(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.write("api.txt", "dirty\n")
	out := r.stkFail("--no-interactive", "sync", "--no-autostash")
	requireContains(t, out, "uncommitted changes")
}
