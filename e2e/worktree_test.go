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

func TestRestackLeavesBranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	wt := r.addWorktree("wt-service", "service")

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	serviceTip := r.sha("service")
	uiTip := r.sha("ui")

	// stk works in the worktree it is run from: a branch held by another is
	// that worktree's, and what sits above it waits on it.
	out := r.stk("restack")
	requireContains(t, out, "service skipped: checked out in "+wt)
	requireContains(t, out, "Restack it by running stk restack from that worktree.")
	requireContains(t, out, "ui blocked")
	requireEqual(t, r.sha("service"), serviceTip, "service untouched")
	requireEqual(t, r.sha("ui"), uiTip, "ui untouched")
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), serviceTip, "linked worktree untouched")

	// From the worktree that holds it, it is restacked like any other.
	res := r.stkAt(wt, "", "restack")
	requireEqual(t, res.Code, 0, "restack from the owning worktree")
	if r.sha("service") == serviceTip {
		t.Fatal("service was not restacked from its own worktree")
	}
	if r.sha("ui") == uiTip {
		t.Fatal("ui was not restacked from service's worktree")
	}
}

func TestRestackPassesABranchHeldElsewhereThatNeedsNothing(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")
	r.addWorktree("wt-api", "api")

	// api is current, so being held elsewhere costs the branches above it
	// nothing.
	r.stk("checkout", "service")
	r.amend("service.txt", "service amended\n", "service amended")
	uiTip := r.sha("ui")

	out := r.stk("restack")
	requireNotContains(t, out, "skipped")
	if r.sha("ui") == uiTip {
		t.Fatal("ui was not restacked")
	}
}

// advanceRemoteTrunk pushes one commit to origin's main from another clone.
func advanceRemoteTrunk(r *repo, file, content string) {
	r.t.Helper()
	upstream := filepath.Join(filepath.Dir(r.Root), "upstream")
	r.runIn(filepath.Dir(r.Root), "", "git", "clone", "-q", r.Origin, upstream)
	if err := os.WriteFile(filepath.Join(upstream, file), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.runIn(upstream, "", "git", "add", "-A")
	r.runIn(upstream, "", "git", "commit", "-q", "-m", "remote work")
	r.runIn(upstream, "", "git", "push", "-q", "origin", "main")
}

func TestSyncLeavesBranchHeldByAnotherWorktree(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stk("checkout", "api")
	wt := r.addWorktree("wt-service", "service")
	advanceRemoteTrunk(r, "remote.txt", "remote\n")

	serviceTip := r.sha("service")
	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	requireContains(t, out, "service skipped: checked out in "+wt)
	requireEqual(t, r.git("merge-base", "--is-ancestor", "origin/main", "api"), "", "api restacked")
	requireEqual(t, r.sha("service"), serviceTip, "service untouched")
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), serviceTip, "linked worktree untouched")
}

func TestSyncLeavesTrunkCheckedOutInAnotherWorktree(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	// main lives in a linked worktree; the current one is on api.
	wt := r.addWorktree("wt-main", "main")
	mainTip := r.sha("main")
	advanceRemoteTrunk(r, "remote.txt", "remote\n")

	out := r.stk("--no-interactive", "sync", "--no-cleanup")
	requireContains(t, out, "main not moved: checked out in "+wt+"; working from origin/main instead")
	requireNotContains(t, out, "fast-forwarded")
	requireEqual(t, r.sha("main"), mainTip, "main left where it was")
	requireEqual(t, r.gitAt(wt, "rev-parse", "HEAD"), mainTip, "linked worktree untouched")
	// The stack still lands on what was fetched.
	requireEqual(t, r.parentOf("api"), "main", "parent unchanged")
	requireEqual(t, r.baseOf("api"), r.sha("origin/main"), "restacked onto the remote trunk")
}

func TestSyncContinuesOntoTheRemoteTrunkAfterAConflict(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.addWorktree("wt-main", "main")
	advanceRemoteTrunk(r, "api.txt", "remote\n")

	res := r.stkAt(r.Root, "", "--no-interactive", "sync", "--no-cleanup")
	requireEqual(t, res.Code, 1, "paused on the conflict")
	requireContains(t, res.All(), "Conflict encountered")

	r.write("api.txt", "resolved\n")
	r.git("add", "api.txt")
	r.stk("continue")
	// The journal carried the remote trunk across the pause.
	requireEqual(t, r.baseOf("api"), r.sha("origin/main"), "base after continue")
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

// A worktree branched off a freshly fetched origin/main while local main sat
// behind. Tracking it against the stale local main would record a base below
// commits that are already on trunk, and every later range from that base
// would claim them.
func TestTrackOntoTrunkUsesTheFresherRemoteTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	movedRemoteBranch(r, "main", "landed elsewhere")
	r.git("fetch", "-q", "origin")
	fresh := r.sha("refs/remotes/origin/main")
	requireEqual(t, r.sha("main") == fresh, false, "local main is stale")

	wt := filepath.Join(filepath.Dir(r.Root), "wt-api")
	r.git("worktree", "add", "-q", "-b", "api", wt, "origin/main")
	r.runIn(wt, "", "git", "commit", "-q", "--allow-empty", "-m", "api")
	r.stkAt(wt, "", "track", "api", "--parent", "main")

	requireEqual(t, r.baseOf("api"), fresh, "base is where api left origin/main")
}
