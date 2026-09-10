package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// lines returns numbered file content, long enough that an edit near the top
// and one near the bottom land in different diff hunks.
func lines(n int, replace map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if text, ok := replace[i]; ok {
			fmt.Fprintf(&b, "%s\n", text)
			continue
		}
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

// stashCount is the number of entries in the git stash list.
func stashCount(r *repo) int {
	r.t.Helper()
	out := r.git("stash", "list")
	if strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

// parkedStashes counts the refs stk uses to hold changes it has stashed.
func parkedStashes(r *repo) int {
	r.t.Helper()
	out := r.git("for-each-ref", "--format=%(refname)", "refs/stk/autostash")
	if strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

func TestRestackAutostashRestoresChanges(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")
	r.write("README.md", "hello\nwork in progress\n")

	out := r.stk("restack", "--autostash")
	requireContains(t, out, "Stashed uncommitted changes")
	requireContains(t, out, "Restored stashed changes")

	requireEqual(t, r.log("b")[1], "a amended", "b sits on the amended parent")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")
	requireEqual(t, stashCount(r), 0, "no stash left in the stash list")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
}

func TestAutostashConfigAppliesAndIsOverridable(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	r.stk("checkout", "a")
	r.amend("a.txt", "a amended\n", "a amended")
	r.git("config", "--local", "stk.autostash", "true")
	r.write("README.md", "hello\nwork in progress\n")

	out := r.stkFail("restack", "--no-autostash")
	requireContains(t, out, "uncommitted changes")

	out = r.stk("restack")
	requireContains(t, out, "Stashed uncommitted changes")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")

	out = r.stkFail("restack", "--autostash", "--no-autostash")
	requireContains(t, out, "not both")
}

func TestRestackAutostashSurvivesConflictAndAbort(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a amended\n", "a amended")
	r.write("side.txt", "parked\n")
	r.git("add", "side.txt")
	r.git("commit", "-q", "-m", "add side")
	r.write("side.txt", "parked and edited\n")
	tipOfB := r.sha("b")

	out := r.stkAt(r.Root, "", "restack", "--autostash")
	requireEqual(t, out.Code, 1, "restack stops on the conflict")
	requireContains(t, out.All(), "stk continue")
	// The parked changes are the journal's until the operation finishes.
	requireEqual(t, parkedStashes(r), 1, "stash held for the paused operation")

	out2 := r.stk("abort")
	requireContains(t, out2, "Restored stashed changes")
	requireEqual(t, r.fileContent("side.txt"), "parked and edited\n", "changes came back after abort")
	requireEqual(t, r.sha("b"), tipOfB, "abort restored b")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "no stash left in the stash list")
}

func TestRestackAutostashSurvivesConflictAndContinue(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a amended\n", "a amended")
	r.write("side.txt", "parked\n")
	r.git("add", "side.txt")
	r.git("commit", "-q", "-m", "add side")
	r.write("side.txt", "parked and edited\n")

	res := r.stkAt(r.Root, "", "restack", "--autostash")
	requireEqual(t, res.Code, 1, "restack stops on the conflict")

	r.write("shared.txt", "resolved\n")
	r.git("add", "shared.txt")
	out := r.stk("continue")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.fileContent("side.txt"), "parked and edited\n", "changes came back after continue")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
}

func TestRestackAutostashKeepsChangesWhenRestoreConflicts(t *testing.T) {
	r := newRepo(t)
	r.commit("f.txt", lines(5, nil), "five lines")
	r.stk("create", "a")
	r.commit("a.txt", "a\n", "a")
	r.stk("create", "b")
	r.commit("b.txt", "b\n", "b")
	// Amending a to touch f.txt means the restack of b rewrites the very line
	// the working tree has edited, so the parked changes cannot go back.
	r.stk("checkout", "a")
	r.write("f.txt", lines(5, map[int]string{1: "THEIRS"}))
	r.git("add", "-A")
	r.git("commit", "-q", "--amend", "-m", "a plus the first line")
	r.stk("checkout", "b")
	r.write("f.txt", lines(5, map[int]string{1: "MINE"}))

	res := r.stkAt(r.Root, "", "restack", "--autostash")
	requireEqual(t, res.Code, 0, "the restack itself succeeded")
	// The conflict is git's to explain and the user's to resolve.
	requireContains(t, res.All(), "CONFLICT")
	requireContains(t, res.All(), "did not reapply cleanly")
	requireContains(t, res.All(), "git stash pop")
	requireContains(t, r.fileContent("f.txt"), "MINE")
	// Nothing rests on that resolution: the changes are in the stash too.
	requireEqual(t, stashCount(r), 1, "the stash is kept as well")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
}

func TestCheckoutAutostashCarriesChangesAcross(t *testing.T) {
	r := newRepo(t)
	r.commit("f.txt", lines(20, nil), "twenty lines")
	r.stk("create", "top")
	r.commit("f.txt", lines(20, map[int]string{1: "ONE"}), "top edits the first line")
	r.stk("checkout", "main")
	// Git refuses to carry this across on its own: the file differs between
	// the branches and the working tree has edited it.
	r.write("f.txt", lines(20, map[int]string{20: "TWENTY"}))

	out := r.stkFail("checkout", "top")
	requireContains(t, out, "would be overwritten")
	requireEqual(t, r.currentBranch(), "main", "the refused checkout changed nothing")

	out = r.stk("checkout", "top", "--autostash")
	requireContains(t, out, "Switched to top")
	requireContains(t, out, "Carried your uncommitted changes across")
	requireEqual(t, r.currentBranch(), "top", "switched")
	requireEqual(t, r.fileContent("f.txt"), lines(20, map[int]string{1: "ONE", 20: "TWENTY"}),
		"both the branch's commit and the local edit are present")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "no stash left in the stash list")
}

func TestCheckoutAutostashRollsBackOnConflict(t *testing.T) {
	r := newRepo(t)
	r.commit("f.txt", lines(20, nil), "twenty lines")
	r.stk("create", "top")
	r.commit("f.txt", lines(20, map[int]string{10: "THEIRS"}), "top edits the tenth line")
	r.stk("checkout", "main")
	mine := lines(20, map[int]string{10: "MINE"})
	r.write("f.txt", mine)

	out := r.stkFail("checkout", "top", "--autostash")
	requireContains(t, out, "conflict with top")
	requireContains(t, out, "Nothing was switched")
	requireEqual(t, r.currentBranch(), "main", "still on the original branch")
	requireEqual(t, r.fileContent("f.txt"), mine, "the local edit is untouched")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "no stash left in the stash list")
}

func TestNavigationAutostash(t *testing.T) {
	r := newRepo(t)
	r.commit("f.txt", lines(20, nil), "twenty lines")
	r.stk("create", "top")
	r.commit("f.txt", lines(20, map[int]string{1: "ONE"}), "top edits the first line")
	r.stk("checkout", "main")
	r.write("f.txt", lines(20, map[int]string{20: "TWENTY"}))

	out := r.stk("up", "--autostash")
	requireContains(t, out, "Carried your uncommitted changes across")
	requireEqual(t, r.currentBranch(), "top", "up switched with the changes")

	out = r.stk("down", "--autostash")
	requireContains(t, out, "Carried your uncommitted changes across")
	requireEqual(t, r.currentBranch(), "main", "down switched back")
	requireEqual(t, r.fileContent("f.txt"), lines(20, map[int]string{20: "TWENTY"}), "the edit survived both hops")
}

func TestMoveAutostash(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	r.write("README.md", "hello\nwork in progress\n")

	out := r.stk("move", "b", "--onto", "main", "--autostash")
	requireContains(t, out, "Stashed uncommitted changes")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.parentOf("b"), "main", "b was re-parented")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
}

func TestSyncAutostashUpdatesTrunkHeldHere(t *testing.T) {
	r := newRepoWithRemote(t)
	// Put a commit on the remote trunk without moving the local one, so sync
	// has a fast-forward to perform on the branch checked out here.
	r.git("switch", "-q", "-c", "publisher")
	r.git("commit", "-q", "--allow-empty", "-m", "remote work")
	r.git("push", "-q", "origin", "publisher:main")
	r.git("switch", "-q", "main")
	r.git("branch", "-qD", "publisher")
	r.write("README.md", "hello\nwork in progress\n")

	out := r.stk("sync", "--autostash", "--no-cleanup")
	requireContains(t, out, "Stashed uncommitted changes")
	requireContains(t, out, "fast-forwarded")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")
	requireEqual(t, r.log("main")[0], "remote work", "trunk moved")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
}

func TestDoctorReportsParkedStash(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	// Simulate an stk that died between parking changes and restoring them.
	r.write("README.md", "hello\nwork in progress\n")
	sha := strings.TrimSpace(r.git("stash", "create", "stk autostash: restack"))
	r.git("update-ref", "refs/stk/autostash/deadbeefdeadbeef", sha)

	out := r.stk("doctor")
	requireContains(t, out, "parked changes")
	requireContains(t, out, "never restored")
	requireContains(t, out, "git stash apply")
}
