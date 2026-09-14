package e2e

import (
	"path/filepath"
	"strconv"
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
	requireContains(t, out, "Opened pull request #1")
	requireContains(t, out, "https://github.com/example/repo/pull/1")

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
	requireContains(t, out, "Opened draft pull request #1")
	requireContains(t, r.ghCallLog(), "--base main --title api --body - api --draft")
}

func TestSubmitDraftFromCutsTheStack(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("ss", "-pn", "--draft-from", "service")
	requireContains(t, out, "Opened pull request #1 for api")
	requireContains(t, out, "Opened draft pull request #2 for service")
	requireContains(t, out, "Opened draft pull request #3 for ui")
	requireContains(t, out, "stk ready --stack")

	state := r.ghStubState()
	drafts := map[string]bool{}
	for _, pr := range state.PRs {
		drafts[pr.Head] = pr.Draft
	}
	requireEqual(t, drafts["api"], false, "api is ready")
	requireEqual(t, drafts["service"], true, "service is a draft")
	requireEqual(t, drafts["ui"], true, "ui is a draft")
}

func TestSubmitDraftBranchNamesIndividualDrafts(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("ss", "-n", "--draft-branch", "api", "--draft-branch", "ui")
	requireContains(t, out, "Opened draft pull request #1 for api")
	requireContains(t, out, "Opened pull request #2 for service")
	requireContains(t, out, "Opened draft pull request #3 for ui")

	drafts := map[string]bool{}
	for _, pr := range r.ghStubState().PRs {
		drafts[pr.Head] = pr.Draft
	}
	requireEqual(t, drafts["api"], true, "api is a draft")
	requireEqual(t, drafts["service"], false, "service is ready")
	requireEqual(t, drafts["ui"], true, "ui is a draft")
}

func TestSubmitRejectsConflictingDraftFlags(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	out := r.stkFail("ss", "-dn", "--draft-from", "service")
	requireContains(t, out, "not several")
	out = r.stkFail("ss", "-n", "--draft-from", "service", "--draft-branch", "api")
	requireContains(t, out, "not several")
	// A draft flag alone still means --pull.
	out = r.stk("ss", "-n", "--draft-from", "api")
	requireContains(t, out, "Opened draft pull request #1")
}

func TestSubmitDraftFlagsNameARealBranch(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	out := r.stkFail("ss", "-n", "--draft-from", "nope")
	requireContains(t, out, `branch "nope" does not exist`)
	requireNotContains(t, r.ghCallLog(), "pr create")
}

func TestSubmitLeavesAnOpenPullRequestAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()
	r.existingPullRequest(42, "api", "main", "Old title")

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

func TestSubmitCommentsTheStackOnEveryPullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "-spn")
	requireContains(t, out, "Commented the stack on #1")
	requireContains(t, out, "Commented the stack on #2")
	requireContains(t, out, "Commented the stack on #3")
	requireContains(t, out, "3 stack comment(s) written")

	// One comment per pull request, listing the whole chain bottom first.
	for number, branch := range map[int]string{1: "api", 2: "service", 3: "ui"} {
		comments := r.prComments(number)
		requireEqual(t, len(comments), 1, "one comment on #"+strconv.Itoa(number))
		body := comments[0]
		requireContains(t, body, "<!-- stk:stack -->")
		requireContains(t, body, "1. #1 `api`")
		requireContains(t, body, "2. #2 `service`")
		requireContains(t, body, "3. #3 `ui`")
		// Only the reader's own pull request is marked.
		requireContains(t, body, "`"+branch+"` ← this pull request")
		requireEqual(t, strings.Count(body, "← this pull request"), 1, "one marker on #"+strconv.Itoa(number))
	}
}

func TestSubmitLeavesAnUnchangedStackCommentAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("submit", "-spn")

	// Nothing about the stack has changed, so the comments must not be
	// rewritten and the timeline must not be disturbed.
	out := r.stk("submit", "-spn")
	requireNotContains(t, out, "stack comment")
	requireNotContains(t, r.ghCallLog(), "--method PATCH")
	requireEqual(t, len(r.prComments(1)), 1, "still one comment")
}

func TestSubmitUpdatesTheStackCommentWhenTheStackGrows(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("submit", "-spn")
	requireNotContains(t, r.prComments(1)[0], "`ui`")

	// A third branch joins the stack.
	r.stk("create", "ui")
	r.commit("ui.txt", "ui\n", "ui")
	out := r.stk("submit", "-spn")
	requireContains(t, out, "Commented the stack on #3")
	requireContains(t, out, "Updated the stack comment on #1")
	requireContains(t, out, "Updated the stack comment on #2")

	for _, number := range []int{1, 2, 3} {
		comments := r.prComments(number)
		requireEqual(t, len(comments), 1, "one comment on #"+strconv.Itoa(number))
		requireContains(t, comments[0], "3. #3 `ui`")
	}
}

func TestSubmitDoesNotCommentOnASinglePullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "-pn")
	requireContains(t, out, "Opened pull request #1")
	requireNotContains(t, out, "stack comment")
	requireEqual(t, len(r.prComments(1)), 0, "no comment on a stack of one")
}

func TestSubmitNoCommentSkipsTheStackComment(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("submit", "-spn", "--no-comment")
	requireContains(t, out, "2 pull request(s) opened")
	requireNotContains(t, out, "stack comment")
	requireEqual(t, len(r.prComments(1)), 0, "no comment written")
	requireEqual(t, len(r.prComments(2)), 0, "no comment written")
}

func TestSubmitCommentsTheStackFromASingleBranchSubmit(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("submit", "-spn")

	// Submitting one branch still refreshes the whole stack's note, because
	// the note is about the stack and not about the branch.
	r.stk("checkout", "api")
	r.commit("more.txt", "more\n", "more api work")
	out := r.stk("submit", "-pn")
	requireContains(t, out, "Pushed api to origin")
	requireNotContains(t, out, "stack comment")
	requireContains(t, r.prComments(2)[0], "1. #1 `api`")
}

func TestSubmitNeverTouchesSomeoneElsesComment(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("submit", "-spn")

	// A reviewer comments after stk did.
	r.appendPRComment(1, "Looks good, one nit about naming.")

	r.stk("create", "ui")
	r.commit("ui.txt", "ui\n", "ui")
	r.stk("submit", "-spn")

	comments := r.prComments(1)
	requireEqual(t, len(comments), 2, "the review comment survived")
	requireContains(t, comments[0], "3. #3 `ui`")
	requireEqual(t, comments[1], "Looks good, one nit about naming.", "review comment unchanged")
}

func TestSubmitAsksWhereTheStackStopsBeingReady(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	// The cut line, then a title and body for each of the three branches.
	// api is the last branch ready, so the two above it open as drafts.
	stdin := "api\n" + strings.Repeat("\n\n", 3)
	res := r.stkAt(r.Root, stdin, "--interactive", "ss", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	requireContains(t, res.All(), "Ready for review up to")
	requireContains(t, res.All(), "Opened pull request #1 for api")
	requireContains(t, res.All(), "Opened draft pull request #2 for service")
	requireContains(t, res.All(), "Opened draft pull request #3 for ui")

	drafts := map[string]bool{}
	for _, pr := range r.ghStubState().PRs {
		drafts[pr.Head] = pr.Draft
	}
	requireEqual(t, drafts["api"], false, "api is ready")
	requireEqual(t, drafts["service"], true, "service is a draft")
	requireEqual(t, drafts["ui"], true, "ui is a draft")
}

func TestSubmitCutLineAtTheTopMeansNoDrafts(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	stdin := "service\n" + strings.Repeat("\n\n", 2)
	res := r.stkAt(r.Root, stdin, "--interactive", "ss", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	for _, pr := range r.ghStubState().PRs {
		requireEqual(t, pr.Draft, false, "nothing is a draft: "+pr.Head)
	}
}

func TestSubmitCutLineAtTrunkMeansEverythingIsADraft(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	stdin := "main\n" + strings.Repeat("\n\n", 2)
	res := r.stkAt(r.Root, stdin, "--interactive", "ss", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	for _, pr := range r.ghStubState().PRs {
		requireEqual(t, pr.Draft, true, "everything is a draft: "+pr.Head)
	}
}

func TestSubmitDoesNotAskAboutDraftsForASinglePullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()
	r.useGitHubURL()

	res := r.stkAt(r.Root, "\n\n", "--interactive", "submit", "-p")
	requireEqual(t, res.Code, 0, "submit succeeded")
	requireNotContains(t, res.All(), "Ready for review up to")
}

func TestSubmitNoPromptSkipsTheDraftQuestion(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	res := r.stkAt(r.Root, "", "--interactive", "ss", "-pn")
	requireEqual(t, res.Code, 0, "submit succeeded")
	requireNotContains(t, res.All(), "Ready for review up to")
	for _, pr := range r.ghStubState().PRs {
		requireEqual(t, pr.Draft, false, "nothing is a draft: "+pr.Head)
	}
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

	// ... but only when a pull request actually has to be described. With one
	// already open there is nothing to ask, so the run goes ahead.
	r.stk("submit", "--pull", "--no-prompt")
	out = r.stk("--no-interactive", "submit", "--pull")
	requireContains(t, out, "already open for api")
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

func TestSSIsSubmitStack(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service", "ui")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("ss")
	for _, name := range []string{"api", "service", "ui"} {
		requireContains(t, out, "Pushed "+name+" to origin")
	}
	requireContains(t, out, "3 branch(es) pushed")

	// Every other flag still applies through the short form.
	out = r.stk("ss", "-pn")
	requireContains(t, out, "3 pull request(s) opened")
	requireContains(t, out, "3 stack comment(s) written")
	requireContains(t, r.prComments(2)[0], "2. #2 `service` ← this pull request")
}

func TestSubmitRefusesTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stk("checkout", "main")

	out := r.stkFail("submit")
	requireContains(t, out, "is the trunk branch")
	requireNotContains(t, remoteHeads(r), "refs/heads/api")
}

func TestReadyMarksPullRequestsReadyAndBack(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("ss", "-dn")

	out := r.stk("ready")
	requireContains(t, out, "#2 is ready for review")
	requireContains(t, out, "1 pull request(s) marked ready for review")

	// Already ready: nothing to do, and gh is not asked to do it again.
	out = r.stk("ready")
	requireContains(t, out, "#2 is already ready for review")
	requireContains(t, out, "Nothing to change.")

	out = r.stk("ready", "--stack")
	requireContains(t, out, "#1 is ready for review")
	requireContains(t, out, "#2 is already ready for review")

	out = r.stk("ready", "--stack", "--undo")
	requireContains(t, out, "#1 is a draft")
	requireContains(t, out, "#2 is a draft")
	for _, pr := range r.ghStubState().PRs {
		requireEqual(t, pr.Draft, true, "back to a draft: "+pr.Head)
	}
}

func TestReadyReportsBranchesWithNoPullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("ready", "--stack")
	requireContains(t, out, "no pull request is open for api")
	requireContains(t, out, "no pull request is open for service")
	requireContains(t, out, "Nothing to change.")
	requireNotContains(t, r.ghCallLog(), "pr ready")
}

func TestReadyRefusesTrunk(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stk("checkout", "main")

	out := r.stkFail("ready")
	requireContains(t, out, "is the trunk branch")
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
	requireContains(t, out, "pushing and restacking do not")
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
	// A dry run verifies it could log in and reads what is already open, but
	// writes nothing at all.
	requireContains(t, r.ghCallLog(), "auth status")
	requireNotContains(t, r.ghCallLog(), "pr create")
	requireNotContains(t, r.ghCallLog(), "--method")
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
