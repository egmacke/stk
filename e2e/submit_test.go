package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// remoteHeads lists the branches origin holds, read from the origin
// repository itself so it does not depend on the remote's configured URL.
func remoteHeads(r *repo) string {
	r.t.Helper()
	return r.gitAt(r.Origin, "for-each-ref", "--format=%(refname)", "refs/heads")
}

func upstreamOf(r *repo, branch string) string {
	r.t.Helper()
	res := r.runIn(r.Root, "", "git", "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if res.Code != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

func TestSubmitPushesTheBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")

	out := r.stk("submit")
	requireContains(t, out, "Pushed api to origin (created)")
	requireContains(t, remoteHeads(r), "refs/heads/api")
	requireEqual(t, upstreamOf(r, "api"), "origin/api", "upstream recorded")

	// A second submit has nothing to do.
	out = r.stk("submit")
	requireContains(t, out, "already on origin")
	requireContains(t, out, "Nothing to push")
}

func TestSubmitRecordsAMissingUpstreamWithoutPushing(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stk("submit")
	published := r.sha("api")

	// The commit is on the remote, but the branch has forgotten where it went.
	r.git("branch", "--unset-upstream", "api")
	requireEqual(t, upstreamOf(r, "api"), "", "upstream cleared")

	out := r.stk("submit")
	requireContains(t, out, "Recorded origin/api as the upstream of api")
	requireContains(t, out, "1 upstream(s) recorded")
	requireNotContains(t, out, "Pushed api")
	requireEqual(t, upstreamOf(r, "api"), "origin/api", "upstream recorded")
	requireEqual(t, r.sha("refs/remotes/origin/api"), published, "the remote was left alone")
}

func TestSubmitPushesTheAncestorChain(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")

	out := r.stk("submit")
	requireContains(t, out, "Pushed api to origin")
	requireContains(t, out, "Pushed service to origin")
	requireContains(t, out, "Pushed ui to origin")
	heads := remoteHeads(r)
	for _, name := range []string{"api", "service", "ui"} {
		requireContains(t, heads, "refs/heads/"+name)
	}
}

func TestSubmitForcesWithLeaseAfterRestack(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stk("submit", "--stack")

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	r.stk("restack")

	out := r.stk("submit", "--stack")
	requireContains(t, out, "Pushed api to origin (forced)")
	requireContains(t, out, "Pushed service to origin (forced)")
	requireEqual(t, r.sha("refs/remotes/origin/service"), r.sha("service"), "remote caught up")
}

func TestSubmitRefusesWhenTheRemoteMovedUnderIt(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stk("submit")

	// Someone else pushes to the same branch; stk's remote-tracking ref still
	// points at the commit it published.
	other := filepath.Join(filepath.Dir(r.Root), "other")
	r.runIn(filepath.Dir(r.Root), "", "git", "clone", "-q", r.Origin, other)
	r.runIn(other, "", "git", "checkout", "-q", "api")
	r.runIn(other, "", "git", "commit", "-q", "--allow-empty", "-m", "someone else")
	r.runIn(other, "", "git", "push", "-q", "origin", "api")

	r.amend("api.txt", "api amended\n", "api amended")
	out := r.stkFail("submit")
	requireContains(t, out, "has moved since stk last saw it")
	requireContains(t, out, "nothing was overwritten")
	requireEqual(t, r.sha("refs/remotes/origin/api"), r.sha("refs/remotes/origin/api"), "local view unchanged")
}

func TestSubmitPullOpensAPullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "--pull", "--no-prompt")
	requireContains(t, out, "Pushed api to origin")
	requireContains(t, out, "Pushed service to origin")
	requireContains(t, out, "Opened pull request #7")
	requireContains(t, out, "https://github.com/example/repo/pull/7")

	calls := r.ghCallLog()
	requireContains(t, calls, "pr list --repo example/repo --head service")
	// Based on the stack parent, not trunk, and titled after the branch.
	requireContains(t, calls, "pr create --repo example/repo --head service --base api --title service")
	requireNotContains(t, calls, "--head api --base main")
	requireNotContains(t, calls, "--draft")
}

func TestSubmitDraftImpliesPull(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "-d", "-n")
	requireContains(t, out, "Opened draft pull request #7")
	requireContains(t, r.ghCallLog(), "--base main --title api --body - api --draft")
}

func TestSubmitLeavesAnOpenPullRequestAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()
	r.existingPullRequest(`[{"number":42,"url":"https://github.com/example/repo/pull/42","title":"Old title","isDraft":false,"state":"OPEN"}]`)

	out := r.stk("submit", "-p", "-n")
	requireContains(t, out, "Pull request #42 is already open for api")
	requireContains(t, out, "https://github.com/example/repo/pull/42")
	requireNotContains(t, r.ghCallLog(), "pr create")
}

func TestSubmitStackOpensOnePullRequestPerBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "--stack", "--pull", "--no-prompt")
	requireContains(t, out, "3 branch(es) pushed")
	requireContains(t, out, "3 pull request(s) opened")

	calls := r.ghCallLog()
	requireContains(t, calls, "--head api --base main")
	requireContains(t, calls, "--head service --base api")
	requireContains(t, calls, "--head ui --base service")
}

func TestSubmitPromptsForTitleAndBody(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	// A typed title, one body line, then a blank line to finish.
	res := r.stkAt(r.Root, "My title\nWhy this change matters\n\n", "--interactive", "submit", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	requireContains(t, res.All(), "Title for the pull request for api:")
	requireContains(t, r.ghCallLog(), "--title My title --body Why this change matters")
}

func TestSubmitPromptDefaultsToTheFirstCommitSubject(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	// Empty answers accept both offers.
	res := r.stkAt(r.Root, "\n\n", "--interactive", "submit", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	requireContains(t, res.All(), "[api]")
	requireContains(t, r.ghCallLog(), "--title api --body - api")
}

func TestSubmitPullWithoutATerminalNeedsNoPrompt(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	out := r.stkFail("--no-interactive", "submit", "--pull")
	requireContains(t, out, "cannot ask")
	requireContains(t, out, "--no-prompt")
	requireNotContains(t, r.ghCallLog(), "pr create")
	requireNotContains(t, remoteHeads(r), "refs/heads/api")
}

func TestSubmitChecksGitHubLoginBeforePushingAnything(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.logOutGH()

	out := r.stkFail("submit", "--stack", "-p", "-n")
	requireContains(t, out, "not logged in for github.com")
	requireContains(t, out, "not logged into any GitHub hosts")
	requireContains(t, out, "gh auth login --hostname github.com")
	// The point of checking first: nothing was published.
	heads := remoteHeads(r)
	requireNotContains(t, heads, "refs/heads/api")
	requireNotContains(t, heads, "refs/heads/service")
	requireNotContains(t, r.ghCallLog(), "pr create")

	// Pushing without --pull does not care about gh at all.
	out = r.stk("submit", "--stack")
	requireContains(t, out, "Pushed api to origin")
}

func TestSubmitRefusesTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stk("checkout", "main")

	out := r.stkFail("submit")
	requireContains(t, out, "is the trunk branch")
	requireNotContains(t, remoteHeads(r), "refs/heads/api")
}

func TestSubmitNeedsARemote(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")

	out := r.stkFail("submit")
	requireContains(t, out, "no default remote is configured")
}

func TestSubmitRejectsANonGitHubRemote(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.git("remote", "set-url", "--push", "origin", r.Origin)
	r.git("remote", "set-url", "origin", "https://gitlab.com/example/repo.git")

	out := r.stkFail("submit", "-p", "-n")
	requireContains(t, out, "not a GitHub host")
	// Pushing is unaffected by where pull requests can be opened.
	out = r.stk("submit")
	requireContains(t, out, "Pushed api to origin")
}

func TestSubmitDryRunPushesNothing(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "--pull", "--no-prompt", "--dry-run")
	requireContains(t, out, "(dry-run) would push api to origin (created)")
	requireContains(t, out, "(dry-run) would open a pull request for api onto main")
	requireContains(t, out, "1 branch(es) would be pushed")
	requireContains(t, out, "1 pull request(s) would be opened")
	requireContains(t, out, "nothing has been published")
	requireNotContains(t, remoteHeads(r), "refs/heads/api")
	// A dry run still verifies it could log in, and asks gh for nothing else.
	requireContains(t, r.ghCallLog(), "auth status")
	requireNotContains(t, r.ghCallLog(), "pr list")
	requireNotContains(t, r.ghCallLog(), "pr create")
}

func TestSubmitRefusesWhileAnOperationIsPaused(t *testing.T) {
	r := newRepoWithRemote(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a amended\n", "a amended")
	r.stkAt(r.Root, "", "restack")

	out := r.stkFail("submit")
	requireContains(t, out, "operation is already in progress")
}
