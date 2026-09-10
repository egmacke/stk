package e2e

import "testing"

// stkStdin runs stk with the given stdin and fails the test on a non-zero
// exit, so prompt answers can be piped in without a terminal.
func (r *repo) stkStdin(stdin string, args ...string) string {
	r.t.Helper()
	res := r.stkAt(r.Root, stdin, args...)
	if res.Code != 0 {
		r.t.Fatalf("stk %v failed (%d)\nstdout:\n%s\nstderr:\n%s", args, res.Code, res.Stdout, res.Stderr)
	}
	return res.All()
}

func TestCreatePromptsForBranchName(t *testing.T) {
	r := newRepo(t)
	out := r.stkStdin("api\n", "--interactive", "create")
	requireContains(t, out, "Name for the new branch:")
	requireContains(t, out, "Created api from main")
	requireEqual(t, r.currentBranch(), "api", "branch after prompted create")
	requireEqual(t, r.parentOf("api"), "main", "parent of prompted branch")
}

func TestCreateWithoutNameFailsNonInteractively(t *testing.T) {
	r := newRepo(t)
	out := r.stkFail("--no-interactive", "create")
	requireContains(t, out, "no branch name given")
	requireContains(t, out, "stk create <branch>")
}

func TestCreatePromptCancelledLeavesRepoAlone(t *testing.T) {
	r := newRepo(t)
	// Empty stdin: the prompt is dismissed, and dismissal is not a failure.
	res := r.stkAt(r.Root, "", "--interactive", "create")
	requireEqual(t, res.Code, 0, "exit code after a dismissed prompt")
	requireEqual(t, r.currentBranch(), "main", "branch after a dismissed prompt")
}

func TestRenamePromptsForNewName(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	out := r.stkStdin("api-v2\n", "--interactive", "rename")
	requireContains(t, out, "New name for api:")
	requireContains(t, out, "api -> api-v2")
	requireEqual(t, r.currentBranch(), "api-v2", "branch after prompted rename")
	requireEqual(t, r.parentOf("api-v2"), "main", "parent survives a prompted rename")
}

func TestRenameWithoutNameFailsNonInteractively(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	out := r.stkFail("--no-interactive", "rename")
	requireContains(t, out, "no new branch name given")
}

func TestTrackPromptsForParent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.git("switch", "-q", "-c", "loose")
	r.commit("loose.txt", "loose\n", "loose work")

	out := r.stkStdin("api\n", "--interactive", "track")
	requireContains(t, out, "Parent of loose:")
	requireContains(t, out, "Tracking loose with parent api")
	requireEqual(t, r.parentOf("loose"), "api", "parent chosen at the prompt")
}

func TestTrackWithoutParentFailsNonInteractively(t *testing.T) {
	r := newRepo(t)
	r.git("switch", "-q", "-c", "loose")
	out := r.stkFail("--no-interactive", "track")
	requireContains(t, out, "--parent is required")
}

func TestMovePromptsForNewParent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "web")
	r.stk("checkout", "web")

	out := r.stkStdin("main\n", "--interactive", "move")
	requireContains(t, out, "Move web onto:")
	requireContains(t, out, "Parent of web is now main")
	requireEqual(t, r.parentOf("web"), "main", "parent chosen at the prompt")
}

func TestMoveWithoutOntoFailsNonInteractively(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	out := r.stkFail("--no-interactive", "move")
	requireContains(t, out, "--onto is required")
}
