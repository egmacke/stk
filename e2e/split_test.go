package e2e

import (
	"strings"
	"testing"
)

// buildBranchWithCommits makes one tracked branch carrying several commits.
func buildBranchWithCommits(r *repo, name string, subjects ...string) {
	r.t.Helper()
	r.stk("create", name)
	for _, s := range subjects {
		r.commit(strings.ReplaceAll(s, " ", "-")+".txt", s+"\n", s)
	}
}

func TestSplitByFlags(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two", "three", "four")
	tip := r.sha("big")

	out := r.stk("split", "--at", "big~2", "--name", "lower")
	requireContains(t, out, "Created lower at")
	requireContains(t, out, "big now sits on lower")
	requireContains(t, out, "big split into 2 branch(es)")

	// Nothing was rewritten: the tip is the same commit it was.
	requireEqual(t, r.sha("big"), tip, "the split branch keeps its tip")
	requireEqual(t, r.sha("lower"), r.sha("big~2"), "the new branch ends at the split point")
	requireEqual(t, r.parentOf("big"), "lower", "big sits on the new branch")
	requireEqual(t, r.parentOf("lower"), "main", "the new branch took big's old parent")
	requireEqual(t, r.baseOf("big"), r.sha("lower"), "big's base is the split point")
	requireEqual(t, r.baseOf("lower"), r.sha("main"), "the new branch kept big's old base")
	requireEqual(t, strings.Join(r.log("lower")[:2], ","), "two,one", "the lower branch has the first commits")
}

func TestSplitIntoThree(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two", "three", "four")

	out := r.stk("split", "--at", "big~3", "--at", "big~1", "--name", "first", "--name", "second")
	requireContains(t, out, "big split into 3 branch(es)")

	requireEqual(t, stripStatus(r.stackText()),
		"main\n└─ first\n   └─ second\n      └─ big  ←", "three branches in a line")
	requireEqual(t, strings.Join(r.log("first")[:1], ","), "one", "first holds one commit")
	requireEqual(t, strings.Join(r.log("second")[:3], ","), "three,two,one", "second holds up to three")
	requireEqual(t, r.baseOf("second"), r.sha("first"), "second's base is first's tip")
	requireEqual(t, r.baseOf("big"), r.sha("second"), "big's base is second's tip")
}

func TestSplitKeepsChildrenOnTheSplitBranch(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two")
	r.stk("create", "above")
	r.commit("above.txt", "above\n", "above")
	r.stk("checkout", "big")

	r.stk("split", "--at", "big~1", "--name", "lower")
	requireEqual(t, r.parentOf("above"), "big", "the child stayed on the branch it was on")
	requireEqual(t, stripStatus(r.stackText()),
		"main\n└─ lower\n   └─ big  ←\n      └─ above", "the child rides on top")
}

func TestSplitInteractively(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two", "three")

	// Split after the first and second commits, accepting the suggested name
	// for the first branch and typing the second.
	res := r.stkAt(r.Root, "1,2\n\nmiddle\n", "--interactive", "split")
	requireEqual(t, res.Code, 0, "split succeeded")
	requireContains(t, res.All(), "Commits on big, oldest first:")
	requireContains(t, res.All(), "Split after which commits?")
	requireContains(t, res.All(), "[big-1]")

	requireEqual(t, stripStatus(r.stackText()),
		"main\n└─ big-1\n   └─ middle\n      └─ big  ←", "named as answered")
	requireEqual(t, strings.Join(r.log("big-1")[:1], ","), "one", "first segment")
	requireEqual(t, strings.Join(r.log("middle")[:2], ","), "two,one", "second segment")
}

func TestSplitDismissedChangesNothing(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two")

	res := r.stkAt(r.Root, "\n", "--interactive", "split")
	requireEqual(t, res.Code, 0, "dismissing is not a failure")
	requireEqual(t, stripStatus(r.stackText()), "main\n└─ big  ←", "nothing changed")
}

func TestSplitDryRunChangesNothing(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two", "three")

	out := r.stk("split", "--at", "big~1", "--name", "lower", "--dry-run")
	requireContains(t, out, "(dry-run) Splitting big into 2 branch(es)")
	requireContains(t, out, "  lower")
	requireContains(t, out, "No changes have been made.")
	requireEqual(t, r.branchExists("lower"), false, "no branch was created")
	requireEqual(t, r.parentOf("big"), "main", "big still sits on trunk")
}

func TestSplitRefusesBadPoints(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two")

	requireContains(t, r.stkFail("split", "--at", "big", "--name", "x"),
		"is the tip of big; splitting there would leave nothing above it")
	requireContains(t, r.stkFail("split", "--at", "main", "--name", "x"),
		"is not one of the commits big adds to its parent")
	requireContains(t, r.stkFail("split", "--at", "big~1"), "need 1 name(s), got 0")
	requireContains(t, r.stkFail("split", "--name", "x"), "--name needs a --at")
	requireContains(t, r.stkFail("split", "--at", "big~1", "--name", "main"), "already exists")
	requireContains(t, r.stkFail("split", "--at", "big~1", "--at", "big~1", "--name", "a", "--name", "b"),
		"named twice")
	requireEqual(t, r.branchExists("x"), false, "nothing was created")
}

func TestSplitNeedsSomethingToSplit(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "single")
	r.commit("one.txt", "one\n", "one")

	requireContains(t, r.stkFail("split", "--at", "single", "--name", "x"),
		"has 1 commit(s) of its own; there is nothing to split")

	r.stk("checkout", "main")
	requireContains(t, r.stkFail("split"), "is the trunk branch")

	r.git("switch", "-q", "-c", "loose")
	requireContains(t, r.stkFail("split"), "is not tracked by stk")
}

func TestSplitWithoutATerminalNeedsFlags(t *testing.T) {
	r := newRepo(t)
	buildBranchWithCommits(r, "big", "one", "two")

	out := r.stkFail("--no-interactive", "split")
	requireContains(t, out, "stk cannot ask")
	requireContains(t, out, "--at <commit> --name <branch>")
}
