package e2e

import (
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

func TestRestackSkipsBranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	wt := r.addWorktree("wt-service", "service")

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	serviceTip := r.sha("service")
	uiTip := r.sha("ui")

	out := r.stk("restack")
	requireContains(t, out, "skipped: checked out in "+wt)
	requireContains(t, out, "Run stk restack from that worktree.")
	// A child of a skipped parent must not be rebased against uncertain state.
	requireContains(t, out, "ui blocked")
	requireEqual(t, r.sha("service"), serviceTip, "service untouched")
	requireEqual(t, r.sha("ui"), uiTip, "ui untouched")

	// Running from the owning worktree completes the work.
	res := r.stkAt(wt, "", "restack")
	requireEqual(t, res.Code, 0, "restack from owning worktree")
	if r.sha("service") == serviceTip {
		t.Fatal("service was not restacked from its own worktree")
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
	requireContains(t, out, "Safe to prune, but currently checked out:")
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
