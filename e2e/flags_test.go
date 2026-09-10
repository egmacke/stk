package e2e

import (
	"strings"
	"testing"
)

func TestSyncDryRunChangesNothing(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "merged")
	r.commit("m.txt", "m\n", "merged work")
	r.git("push", "-q", "origin", "merged:main")
	r.stk("checkout", "main")
	r.git("fetch", "-q", "origin")

	trunkBefore := r.sha("main")
	out := r.stk("--no-interactive", "--dry-run", "sync", "--cleanup")
	requireContains(t, out, "would fetch origin")
	requireContains(t, out, "would fast-forward main")
	requireContains(t, out, "fully contained in main")
	requireContains(t, out, "No changes have been made.")

	requireEqual(t, r.sha("main"), trunkBefore, "trunk moved during a dry run")
	if !r.branchExists("merged") {
		t.Fatal("dry run deleted a branch")
	}
}

func TestRestackFromTrunkCoversEveryStack(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "one")
	r.commit("one.txt", "1\n", "one")
	r.stk("checkout", "main")
	r.stk("create", "two")
	r.commit("two.txt", "2\n", "two")
	r.stk("checkout", "main")
	r.git("commit", "-q", "--allow-empty", "-m", "trunk moved")

	out := r.stk("restack")
	requireContains(t, out, "Restacking every stack...")
	for _, name := range []string{"one", "two"} {
		requireContains(t, strings.Join(r.log(name), "\n"), "trunk moved")
	}
}

func TestRestackRefusesUntrackedBranch(t *testing.T) {
	r := newRepo(t)
	r.git("switch", "-q", "-c", "loose")
	out := r.stkFail("restack")
	requireContains(t, out, `branch "loose" is not tracked by stk`)
}

func TestQuietSuppressesProgressButNotData(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")

	// Progress lines vanish...
	res := r.stkAt(r.Root, "", "--quiet", "create", "child")
	requireEqual(t, res.Code, 0, "exit code")
	requireEqual(t, res.Stdout, "", "quiet create printed progress")

	// ...but requested data does not.
	requireContains(t, r.stk("--quiet", "stack"), "api")
	requireEqual(t, strings.TrimSpace(r.stk("--quiet", "parent", "api")), "main", "parent output")
}

func TestVerboseLogsGitInvocations(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	res := r.stkAt(r.Root, "", "--verbose", "stack")
	requireContains(t, res.Stderr, "+ git ")
	requireContains(t, res.Stderr, "for-each-ref")
}

func TestStackLegend(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	out := r.stk("stack", "--legend")
	requireContains(t, out, "commits not pushed")
	requireContains(t, out, "requires restack")
	requireContains(t, out, "checked out in another worktree")
}

func TestNoColorAndColorDefaults(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	// Without a terminal there must be no escape sequences either way.
	requireNotContains(t, r.stk("stack"), "\x1b[")
	requireNotContains(t, r.stkAt(r.Root, "", "--no-color", "stack").Stdout, "\x1b[")
}

func TestConflictingScopeFlagsAreRejected(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	requireContains(t, r.stkFail("restack", "--up", "--only"), "not both")
	requireContains(t, r.stkFail("sync", "--cleanup", "--no-cleanup"), "not both")
	requireContains(t, r.stkFail("--interactive", "--no-interactive", "stack"), "not both")
}
