package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// stk sync parks uncommitted changes for the whole run. When they would not
// go back cleanly onto what the sync did, it undoes that instead and puts them
// back exactly as they were; stk restack is where they are reconciled, through
// stk continue.

// pushToRemoteTrunk lands a commit on the remote's main without moving the
// local one, as a colleague's merge would.
func pushToRemoteTrunk(r *repo, rel, content, message string) {
	r.t.Helper()
	current := r.git("branch", "--show-current")
	r.git("stash", "-q", "--include-untracked")
	r.git("switch", "-q", "-c", "publisher", "origin/main")
	r.commit(rel, content, message)
	r.git("push", "-q", "origin", "publisher:main")
	r.git("switch", "-q", current)
	r.git("branch", "-qD", "publisher")
	r.git("stash", "pop", "-q")
}

// conflictingSyncRepo is a stack whose bottom branch a, checked out, has an
// uncommitted edit to the first line of f.txt, which the remote trunk has
// edited too. a itself never touches f.txt, so restacking it is clean; only
// the uncommitted edit conflicts.
func conflictingSyncRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepoWithRemote(t)
	r.commit("f.txt", lines(5, nil), "five lines")
	r.git("push", "-q", "origin", "main")
	r.stk("create", "a")
	r.commit("a.txt", "a\n", "a")
	r.write("f.txt", lines(5, map[int]string{1: "MINE"}))
	pushToRemoteTrunk(r, "f.txt", lines(5, map[int]string{1: "THEIRS"}), "theirs")
	return r
}

func journalExists(r *repo) bool {
	_, err := os.Stat(filepath.Join(r.Root, ".git", "stk", "operations", "current.json"))
	return err == nil
}

func TestSyncCarriesUncommittedChangesThroughARestack(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "a")
	r.commit("a.txt", "a\n", "a")
	r.write("a.txt", "a\nwork in progress\n")
	pushToRemoteTrunk(r, "remote.txt", "remote\n", "remote work")

	out := r.stk("sync", "--no-cleanup")
	requireContains(t, out, "Stashed uncommitted changes")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.log("a")[1], "remote work", "a restacked onto the new trunk")
	requireEqual(t, r.fileContent("a.txt"), "a\nwork in progress\n", "changes came back")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "nothing in the stash list")
}

func TestSyncUndoesTheRestackWhenChangesWouldConflict(t *testing.T) {
	r := conflictingSyncRepo(t)
	tipOfA := r.sha("a")
	mine := r.fileContent("f.txt")

	res := r.stkAt(r.Root, "", "sync", "--no-cleanup")
	requireEqual(t, res.Code, 1, "sync reports the conflict")
	all := res.All()
	requireContains(t, all, "conflict with the restacked branches")
	requireContains(t, all, "The restack was undone")
	requireContains(t, all, "stk restack")
	requireContains(t, all, "stk continue")

	requireEqual(t, r.sha("a"), tipOfA, "a put back where it was")
	requireEqual(t, r.git("branch", "--show-current"), "a", "still on a")
	requireEqual(t, r.fileContent("f.txt"), mine, "changes back exactly as they were")
	requireNotContains(t, r.fileContent("f.txt"), "<<<<<<<")
	requireEqual(t, r.git("diff", "--name-only", "--diff-filter=U"), "", "no conflict left in the index")
	requireEqual(t, r.sha("main"), r.sha("origin/main"), "the rest of the sync stands")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "nothing in the stash list")
	requireEqual(t, journalExists(r), false, "no operation left in progress")
}

func TestRestackAfterAnUndoneSyncReconcilesThroughContinue(t *testing.T) {
	r := conflictingSyncRepo(t)
	r.stkAt(r.Root, "", "sync", "--no-cleanup")

	res := r.stkAt(r.Root, "", "restack")
	requireEqual(t, res.Code, 1, "restack pauses on the conflict")
	requireContains(t, res.All(), "CONFLICT")
	requireContains(t, res.All(), "Your uncommitted changes conflict with the restacked a")
	requireContains(t, res.All(), "stk continue")
	requireEqual(t, r.log("a")[1], "theirs", "a restacked onto the new trunk")
	requireContains(t, r.fileContent("f.txt"), "<<<<<<<")
	requireEqual(t, parkedStashes(r), 1, "changes still held until resolved")

	// Unresolved: continue refuses and keeps everything.
	res = r.stkAt(r.Root, "", "continue")
	requireEqual(t, res.Code, 1, "continue waits for the resolution")
	requireContains(t, res.All(), "unresolved conflicts")
	requireEqual(t, parkedStashes(r), 1, "changes still held")

	r.write("f.txt", lines(5, map[int]string{1: "BOTH"}))
	r.git("add", "f.txt")
	out := r.stk("continue")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.fileContent("f.txt"), lines(5, map[int]string{1: "BOTH"}), "resolution kept")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "nothing in the stash list")
	requireEqual(t, journalExists(r), false, "operation finished")
	r.stk("status")
}

func TestAbortAfterAConflictingRestoreGivesTheChangesBack(t *testing.T) {
	r := conflictingSyncRepo(t)
	r.stkAt(r.Root, "", "sync", "--no-cleanup")
	tipOfA := r.sha("a")
	mine := r.fileContent("f.txt")

	r.stkAt(r.Root, "", "restack")
	out := r.stk("abort")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.sha("a"), tipOfA, "restack undone")
	requireEqual(t, r.fileContent("f.txt"), mine, "changes back exactly as they were")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "nothing in the stash list")
	requireEqual(t, journalExists(r), false, "operation finished")
}

func TestSyncLeavesTrunkWhenChangesOnItWouldConflict(t *testing.T) {
	r := newRepoWithRemote(t)
	r.commit("f.txt", lines(5, nil), "five lines")
	r.git("push", "-q", "origin", "main")
	r.write("f.txt", lines(5, map[int]string{1: "MINE"}))
	pushToRemoteTrunk(r, "f.txt", lines(5, map[int]string{1: "THEIRS"}), "theirs")
	before := r.sha("main")
	mine := r.fileContent("f.txt")

	res := r.stkAt(r.Root, "", "sync", "--no-cleanup")
	requireEqual(t, res.Code, 1, "sync reports the conflict")
	requireContains(t, res.All(), "conflict with the commits the sync brought into main")
	requireEqual(t, r.sha("main"), before, "main put back where it was")
	requireEqual(t, r.fileContent("f.txt"), mine, "changes back exactly as they were")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, stashCount(r), 0, "nothing in the stash list")
}

func TestSyncPausedOnARebaseConflictKeepsChangesThroughContinue(t *testing.T) {
	r := newRepoWithRemote(t)
	r.commit("f.txt", lines(5, nil), "five lines")
	r.git("push", "-q", "origin", "main")
	r.stk("create", "a")
	r.commit("f.txt", lines(5, map[int]string{1: "A"}), "a edits the first line")
	r.write("README.md", "hello\nwork in progress\n")
	pushToRemoteTrunk(r, "f.txt", lines(5, map[int]string{1: "THEIRS"}), "theirs")

	// A conflict of the restack's own pauses the sync as before, holding the
	// changes for stk continue or stk abort.
	res := r.stkAt(r.Root, "", "sync", "--no-cleanup")
	requireEqual(t, res.Code, 1, "sync pauses on the rebase conflict")
	requireEqual(t, parkedStashes(r), 1, "changes held for the paused operation")

	r.write("f.txt", lines(5, map[int]string{1: "RESOLVED"}))
	r.git("add", "f.txt")
	out := r.stk("continue")
	requireContains(t, out, "Restored stashed changes")
	requireEqual(t, r.fileContent("README.md"), "hello\nwork in progress\n", "changes came back")
	requireEqual(t, parkedStashes(r), 0, "no stash ref left behind")
	requireEqual(t, journalExists(r), false, "operation finished")
}
