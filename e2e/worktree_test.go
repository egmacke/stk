package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackMetadataIsSharedBetweenWorktrees(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")

	wt := r.addWorktree("wt-api", "api")
	res := r.stkAt(wt, "", "stack", "--all")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.Stdout, "api")
	requireContains(t, res.Stdout, "service")

	// Both worktrees agree on the graph. Only the current-branch arrow is
	// per-worktree, so it is stripped before comparing.
	requireEqual(t, stripArrow(stripStatus(strings.TrimRight(res.Stdout, "\n"))),
		stripArrow(stripStatus(r.stackText("--all"))), "stack graph differs between worktrees")

	// A branch created in the linked worktree is visible in the original.
	r.stkAt(wt, "", "create", "from-worktree")
	requireEqual(t, strings.TrimSpace(r.stk("parent", "from-worktree")), "api", "parent recorded repository-wide")
}

func TestCreateFromBranchCheckedOutElsewhere(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	// main stays checked out here so git cannot switch to it in the linked
	// worktree.
	r.stk("checkout", "main")
	wt := r.addWorktree("wt-feature", "api")

	// main is checked out in the original worktree, so git cannot switch to
	// it here; stk must branch from its ref instead.
	res := r.stkAt(wt, "", "create", "from-main", "--from", "main")
	if res.Code != 0 {
		t.Fatalf("create --from main failed in a linked worktree:\n%s%s", res.Stdout, res.Stderr)
	}
	requireContains(t, res.Stdout, "Created from-main from main")
	requireContains(t, res.Stdout, "Switched to from-main")
	requireEqual(t, strings.TrimSpace(r.stk("parent", "from-main")), "main", "parent")
	requireEqual(t, r.baseOf("from-main"), r.sha("main"), "base is main's tip")
	requireEqual(t, r.gitAt(wt, "symbolic-ref", "--short", "HEAD"), "from-main", "worktree switched")
}

func TestCheckoutRefusesBranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	wt := r.addWorktree("wt-service", "service")

	out := r.stkFail("checkout", "service")
	requireContains(t, out, "already checked out in")
	requireContains(t, out, wt)
	requireContains(t, out, "Git does not allow this branch to be checked out here")
}

func TestStackMarksBranchesHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	r.addWorktree("wt-service", "service")

	out := r.stackText()
	line := lineContaining(t, out, "service")
	requireContains(t, line, "@")
}

func TestRestackRewritesBranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	wt := r.addWorktree("wt-service", "service")

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	serviceTip := r.sha("service")
	uiTip := r.sha("ui")

	out := r.stk("restack")
	requireContains(t, out, wt)
	if r.sha("service") == serviceTip {
		t.Fatal("service was not restacked")
	}
	// The branch above it was rebased on the rewritten parent, not blocked.
	if r.sha("ui") == uiTip {
		t.Fatal("ui was not restacked")
	}
	// The linked worktree moved with its ref rather than being left behind it.
	requireEqual(t, r.gitAt(wt, "symbolic-ref", "--short", "HEAD"), "service", "still on service")
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), r.sha("service"), "linked worktree HEAD")
	if status := r.gitAt(wt, "status", "--porcelain"); status != "" {
		t.Fatalf("linked worktree left dirty:\n%s", status)
	}
}

func TestRestackSkipsBranchHeldByADirtyWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	wt := r.addWorktree("wt-service", "service")
	if err := os.WriteFile(filepath.Join(wt, "service.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	serviceTip := r.sha("service")
	uiTip := r.sha("ui")

	out := r.stk("restack")
	requireContains(t, out, "skipped: checked out with uncommitted changes in "+wt)
	// A child of a skipped parent must not be rebased against uncertain state.
	requireContains(t, out, "ui blocked")
	requireEqual(t, r.sha("service"), serviceTip, "service untouched")
	requireEqual(t, r.sha("ui"), uiTip, "ui untouched")
	requireEqual(t, strings.TrimSpace(r.gitAt(wt, "status", "--porcelain")), "M service.txt", "changes left alone")
}

func TestRestackUndoesAConflictingRebaseInAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	wt := r.addWorktree("wt-b", "b")

	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")
	bTip := r.sha("b")
	cTip := r.sha("c")

	out := r.stk("restack")
	requireContains(t, out, "b blocked: conflict while rebasing in "+wt)
	requireContains(t, out, "Resolve it by running stk restack from that worktree.")
	requireContains(t, out, "c blocked")
	requireEqual(t, r.sha("b"), bTip, "b left where it was")
	requireEqual(t, r.sha("c"), cTip, "c left where it was")
	// The other worktree is not left mid-rebase, and no journal survives here.
	requireEqual(t, r.gitAt(wt, "status", "--porcelain"), "", "linked worktree left mid-rebase")
	requireEqual(t, r.gitAt(wt, "symbolic-ref", "--short", "HEAD"), "b", "still on b")
	requireEqual(t, r.stkAt(r.Root, "", "restack").Code, 0, "restack again")

	// The conflict is still there to resolve, in the worktree that owns it.
	res := r.stkAt(wt, "", "restack")
	requireEqual(t, res.Code, 1, "restack from the owning worktree")
	requireContains(t, res.All(), "Conflict encountered")
	r.stkAt(wt, "", "abort")
}

func TestSyncRestacksThroughAnotherWorktree(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	wt := r.addWorktree("wt-service", "service")

	// Advance the remote trunk, so the whole stack needs replaying.
	upstream := filepath.Join(filepath.Dir(r.Root), "upstream")
	r.runIn(filepath.Dir(r.Root), "", "git", "clone", "-q", r.Origin, upstream)
	r.runIn(upstream, "", "git", "commit", "-q", "--allow-empty", "-m", "remote work")
	r.runIn(upstream, "", "git", "push", "-q", "origin", "main")

	serviceTip := r.sha("service")
	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	requireContains(t, out, wt)
	if r.sha("service") == serviceTip {
		t.Fatal("service was not restacked through its own worktree")
	}
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), r.sha("service"), "linked worktree moved with the ref")
	if status := r.gitAt(wt, "status", "--porcelain"); status != "" {
		t.Fatalf("linked worktree left dirty:\n%s", status)
	}
}

func TestTrunkCheckedOutInAnotherWorktreeStillSyncs(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")

	// main lives in a linked worktree; the current one is on api.
	wt := r.addWorktree("wt-main", "main")

	// Advance the remote trunk.
	upstream := filepath.Join(filepath.Dir(r.Root), "upstream")
	r.runIn(filepath.Dir(r.Root), "", "git", "clone", "-q", r.Origin, upstream)
	r.runIn(upstream, "", "git", "commit", "-q", "--allow-empty", "-m", "remote work")
	r.runIn(upstream, "", "git", "push", "-q", "origin", "main")

	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	requireContains(t, out, "fast-forwarded by 1 commit")
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), r.sha("origin/main"), "linked worktree moved with the ref")
	if status := r.gitAt(wt, "status", "--porcelain"); status != "" {
		t.Fatalf("linked worktree left dirty:\n%s", status)
	}
}

func TestOnlyTheOwningWorktreeMayContinueOrAbort(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	wt := r.addWorktree("wt-other", "main")

	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")
	r.stkAt(r.Root, "", "restack")

	res := r.stkAt(wt, "", "continue")
	requireEqual(t, res.Code, 1, "continue from the wrong worktree")
	requireContains(t, res.All(), "belongs to another worktree")
	requireContains(t, res.All(), r.Root)

	res = r.stkAt(wt, "", "abort")
	requireEqual(t, res.Code, 1, "abort from the wrong worktree")
	requireContains(t, res.All(), "belongs to another worktree")

	r.stk("abort")
}

func TestSyncNeverDeletesACheckedOutBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")
	r.addWorktree("wt-merged", "merged")

	out := r.stk("--no-interactive", "sync", "--cleanup", "--no-restack")
	requireContains(t, out, "Finished, but checked out in another worktree:")
	requireContains(t, out, "merged")
	if !r.branchExists("merged") {
		t.Fatal("a checked-out branch was deleted")
	}
}

// stripArrow removes the per-worktree current-branch marker.
func stripArrow(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, strings.TrimRight(strings.ReplaceAll(line, "←", ""), " "))
	}
	return strings.Join(out, "\n")
}

func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, text)
	return ""
}
