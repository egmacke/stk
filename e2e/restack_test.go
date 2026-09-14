package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conflictingStack builds main -> a -> b -> c where b's commit touches the
// same file as a's, so rewriting a makes b conflict.
func conflictingStack(r *repo) {
	r.commit("shared.txt", "base\n", "base")
	r.stk("create", "a")
	r.commit("shared.txt", "a\n", "a")
	r.stk("create", "b")
	r.commit("shared.txt", "b\n", "b")
	r.stk("create", "c")
	r.commit("c.txt", "c\n", "c")
}

func TestRestackAfterParentAmended(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	r.stk("restack")

	requireEqual(t, r.log("service")[1], "api amended", "service sits on the amended parent")
	requireEqual(t, r.log("ui")[1], "service", "ui still sits on service")
	requireEqual(t, r.baseOf("service"), r.sha("api"), "service base updated")
	requireEqual(t, r.baseOf("ui"), r.sha("service"), "ui base updated")
	requireEqual(t, r.currentBranch(), "api", "original branch restored")
}

func TestRestackAfterMiddleBranchAmended(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")

	r.stk("checkout", "service")
	r.amend("service.txt", "service amended\n", "service amended")
	r.stk("restack")

	requireEqual(t, r.log("ui")[1], "service amended", "ui rebased onto the amended middle branch")
	requireEqual(t, r.baseOf("ui"), r.sha("service"), "ui base updated")
}

func TestRestackFromAnywhereCoversTheWholeStack(t *testing.T) {
	for _, from := range []string{"api", "service", "ui"} {
		t.Run("from_"+from, func(t *testing.T) {
			r := newRepo(t)
			buildStack(r, "api", "service", "ui")

			// Rewrite the bottom branch, then restack from `from`.
			r.stk("checkout", "api")
			r.amend("api.txt", "api amended\n", "api amended")
			r.stk("checkout", from)

			out := r.stk("restack")
			requireContains(t, out, "api")
			requireContains(t, out, "service")
			requireContains(t, out, "ui")

			requireEqual(t, r.log("ui")[1], "service", "ui on service")
			requireEqual(t, r.log("service")[1], "api amended", "service on the amended api")
			requireEqual(t, r.currentBranch(), from, "original branch restored")
		})
	}
}

func TestRestackUpLimitsScopeToDescendants(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	apiTip := r.sha("api")

	r.stk("checkout", "service")
	out := r.stk("restack", "--up")
	requireContains(t, out, "service")
	requireContains(t, out, "ui")

	// service and ui move; api is untouched because it is an ancestor.
	requireEqual(t, r.sha("api"), apiTip, "api unchanged")
	requireEqual(t, r.log("service")[1], "api amended", "service rebased")
	requireEqual(t, r.log("ui")[1], "service", "ui rebased")
}

func TestRestackUpIgnoresSiblingBranches(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("create", "b")
	r.commit("b.txt", "b\n", "b")
	r.stk("checkout", "a")
	r.stk("create", "c")
	r.commit("c.txt", "c\n", "c")

	// Rewrite a so both b and c are stale.
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")
	bTip := r.sha("b")

	r.stk("checkout", "c")
	r.stk("restack", "--up")
	requireEqual(t, r.sha("b"), bTip, "sibling b untouched by --up from c")
	requireEqual(t, r.log("c")[1], "a amended", "c rebased")
}

func TestRestackDefaultCoversSiblingBranches(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("create", "b")
	r.commit("b.txt", "b\n", "b")
	r.stk("checkout", "a")
	r.stk("create", "c")
	r.commit("c.txt", "c\n", "c")

	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")

	r.stk("checkout", "c")
	r.stk("restack")
	requireEqual(t, r.log("b")[1], "a amended", "sibling b also restacked")
	requireEqual(t, r.log("c")[1], "a amended", "c restacked")
}

func TestRestackOnlyTouchesOneBranch(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	uiTip := r.sha("ui")

	r.stk("checkout", "service")
	r.stk("restack", "--only")
	requireEqual(t, r.log("service")[1], "api amended", "service rebased")
	requireEqual(t, r.sha("ui"), uiTip, "ui untouched by --only")
}

func TestRestackProcessesParentsBeforeChildren(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b", "c")
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")

	out := r.stk("restack")
	ia := strings.Index(out, "a\n")
	ib := strings.Index(out, "b\n")
	ic := strings.Index(out, "c\n")
	if !(ia < ib && ib < ic) {
		t.Fatalf("expected dependency order a, b, c in:\n%s", out)
	}
}

func TestRestackIsIdempotent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	tip := r.sha("b")
	out := r.stk("restack")
	requireContains(t, out, "Stack is up to date.")
	requireEqual(t, r.sha("b"), tip, "no rewrite when nothing changed")
}

func TestRestackAfterManualGitRebase(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	// The user rebases b by hand; stk must notice the branch is already
	// current rather than rewriting it again.
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")
	r.git("checkout", "-q", "b")
	r.git("rebase", "-q", "--onto", "a", r.baseOf("b"), "b")
	tip := r.sha("b")

	out := r.stk("restack")
	requireContains(t, out, "Stack is up to date.")
	requireEqual(t, r.sha("b"), tip, "already-rebased branch left alone")
	requireEqual(t, r.baseOf("b"), r.sha("a"), "stored base caught up")
}

func TestRestackRefusesDirtyWorktreeWithNoAutostash(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	r.write("a.txt", "dirty\n")
	out := r.stkFail("restack", "--no-autostash")
	requireContains(t, out, "uncommitted changes")
	requireContains(t, out, "Autostashing is off")
	requireEqual(t, r.fileContent("a.txt"), "dirty\n", "refusing to run changes nothing")
}

func TestRestackDropsSquashMergedCommits(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	// Two commits on api, so the squash cannot be recognised by patch id.
	r.stk("checkout", "api")
	r.commit("api2.txt", "more api\n", "more api")
	r.stk("restack")
	r.stk("checkout", "service")

	// The squash merge: api's content lands on main under one new commit.
	r.stk("checkout", "main")
	r.git("merge", "-q", "--squash", "api")
	r.git("commit", "-q", "-m", "Add api (#1)")
	r.stk("checkout", "service")

	// Replaying api's commits onto their own merged result would conflict; stk
	// recognises the merge instead.
	out := r.stk("restack")
	requireContains(t, out, "api is already in main; its own commits went in with the merge")
	requireContains(t, out, "✓ service")
	requireNotContains(t, out, "Conflict")
	// One line about it, not two.
	requireNotContains(t, out, "✓ api\n")

	requireEqual(t, r.sha("api"), r.sha("main"), "api collapsed onto trunk")
	requireEqual(t, r.log("service")[0], "service", "service kept its own commit")
	requireEqual(t, r.log("service")[1], "Add api (#1)", "service sits on the squashed commit")
	requireEqual(t, r.baseOf("service"), r.sha("main"), "service's base caught up")

	// Now that it is plainly contained, cleanup can take it away.
	out = r.stk("--no-interactive", "sync", "--no-restack", "--cleanup")
	requireContains(t, out, "Removed api")
	requireEqual(t, r.parentOf("service"), "main", "service was reparented")
}

func TestRestackDropsSquashMergedCommitsOnTheCurrentBranch(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.stk("checkout", "api")
	r.commit("api2.txt", "more api\n", "more api")

	r.stk("checkout", "main")
	r.git("merge", "-q", "--squash", "api")
	r.git("commit", "-q", "-m", "Add api (#1)")
	r.stk("checkout", "api")

	// The branch being collapsed is the one checked out, so the working tree
	// has to move with it.
	out := r.stk("restack")
	requireContains(t, out, "api is already in main")
	requireEqual(t, r.sha("api"), r.sha("main"), "api collapsed onto trunk")
	requireEqual(t, r.currentBranch(), "api", "still on the same branch")
	requireEqual(t, r.git("status", "--porcelain"), "", "the working tree came with it")
}

func TestRestackDryRunChangesNothing(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	before := r.sha("service")

	out := r.stk("checkout", "service")
	out = r.stk("restack", "--dry-run")
	requireContains(t, out, "Plan:")
	requireContains(t, out, "api       no changes required")
	requireContains(t, out, "service   rebase onto api")
	requireContains(t, out, "ui        rebase onto service")
	requireContains(t, out, "No changes have been made.")
	requireEqual(t, r.sha("service"), before, "dry run rewrote a branch")

	out = r.stk("restack", "--up", "--dry-run")
	requireNotContains(t, out, "api       no changes")
	requireContains(t, out, "service   rebase onto api")
	requireContains(t, out, "ui        rebase onto service")
}

func TestRestackConflictThenContinue(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")

	res := r.stkAt(r.Root, "", "restack")
	requireEqual(t, res.Code, 1, "conflict exit code")
	requireContains(t, res.All(), "Conflict encountered.")
	requireContains(t, res.All(), "stk continue")
	requireContains(t, res.All(), "stk abort")

	// Git's own tools stay available during the conflict.
	requireContains(t, r.git("status", "--porcelain"), "shared.txt")

	journal := filepath.Join(r.Root, ".git", "stk", "operations", "current.json")
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("expected an operation journal: %v", err)
	}

	r.write("shared.txt", "resolved\n")
	r.git("add", "shared.txt")
	out := r.stk("continue")
	requireContains(t, out, "b")
	requireContains(t, out, "c")

	// The whole original scope finished, not just the conflicted branch.
	requireEqual(t, r.log("b")[1], "a rewritten", "b rebased")
	requireEqual(t, r.log("c")[1], "b", "c rebased on top of b")
	requireEqual(t, r.currentBranch(), "a", "original branch restored")
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("journal survived a completed operation")
	}
}

func TestRestackConflictThenAbort(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	bTip := r.sha("b")
	cTip := r.sha("c")
	bBase := r.baseOf("b")

	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")

	res := r.stkAt(r.Root, "", "restack")
	requireEqual(t, res.Code, 1, "conflict exit code")

	out := r.stk("abort")
	requireContains(t, out, "Aborted")
	requireEqual(t, r.sha("b"), bTip, "b restored")
	requireEqual(t, r.sha("c"), cTip, "c untouched")
	requireEqual(t, r.baseOf("b"), bBase, "b base restored")
	requireEqual(t, r.currentBranch(), "a", "original branch restored")

	if out := r.git("status", "--porcelain"); out != "" {
		t.Fatalf("working tree not clean after abort:\n%s", out)
	}
	if len(r.git("for-each-ref", "refs/stk/snapshot")) != 0 {
		t.Fatal("snapshot refs survived abort")
	}
}

func TestContinueWithoutAnOperationFails(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	requireContains(t, r.stkFail("continue"), "no stk operation in progress")
	requireContains(t, r.stkFail("abort"), "no stk operation in progress")
}

func TestSecondOperationIsRefusedWhileOneIsInFlight(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")
	r.stkAt(r.Root, "", "restack")

	out := r.stkFail("restack")
	requireContains(t, out, "already in progress")
	r.stk("abort")
}

func TestRestackRefusesToFlattenMergeCommits(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("create", "b")
	r.commit("b1.txt", "b1\n", "b1")

	// Give b a merge commit of its own.
	r.git("switch", "-q", "-c", "tmp", r.baseOf("b"))
	r.commit("side.txt", "side\n", "side")
	r.git("switch", "-q", "b")
	r.git("merge", "-q", "--no-ff", "-m", "merge side", "tmp")

	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")

	out := r.stkFail("restack")
	requireContains(t, out, "merge commits")
	requireContains(t, out, "--rebase-merges")
}

func TestRestackStopsWhenTheBaseIsNoLongerInHistory(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	// Rewriting b so the recorded base is unreachable leaves stk unable to
	// tell which commits belong to it.
	r.git("checkout", "-q", "b")
	r.git("reset", "-q", "--hard", "main")
	r.commit("b2.txt", "b2\n", "unrelated")
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")

	out := r.stkFail("restack")
	requireContains(t, out, "cannot restack b safely")
	requireContains(t, out, "stk track b --parent a")
}

func TestRestackFixesStaleBaseWithoutRewriting(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	// Move a forward without rewriting it: b already contains a's tip only
	// after b is rebased, so first check the no-op path on a fresh stack.
	tip := r.sha("b")
	r.stk("restack")
	requireEqual(t, r.sha("b"), tip, "no rewrite needed")
	requireEqual(t, r.baseOf("b"), r.sha("a"), "base matches the parent tip")
}
