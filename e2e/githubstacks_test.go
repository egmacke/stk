package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// githubStacksRepo is a repository with a GitHub remote, the stub gh on PATH
// and stk.githubStacks turned on.
func githubStacksRepo(t *testing.T, branches ...string) *repo {
	t.Helper()
	r := newRepoWithRemote(t)
	buildStack(r, branches...)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()
	return r
}

// numbers writes pull request numbers as one space-separated line.
func numbers(list []int) string {
	var parts []string
	for _, n := range list {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, " ")
}

// prNumber finds the stub gh's pull request for a branch.
func prNumber(r *repo, head string) int {
	r.t.Helper()
	for _, pr := range r.ghStubState().PRs {
		if pr.Head == head {
			return pr.Number
		}
	}
	r.t.Fatalf("no pull request for %s", head)
	return 0
}

func TestGitHubStacksIsOffByDefault(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	out := r.stk("ss", "-pn")
	requireContains(t, out, "2 stack comment(s) written")
	requireNotContains(t, out, "stack on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")
	requireNotContains(t, r.ghCallLog(), "extension list")
}

func TestGitHubStacksLinksThePullRequests(t *testing.T) {
	r := githubStacksRepo(t, "api", "service", "ui")

	out := r.stk("ss", "-pn")
	requireContains(t, out, "3 pull request(s) opened")
	requireContains(t, out, "Linked #1, #2, #3 as a stack on GitHub")
	requireContains(t, out, "linked as a stack on GitHub.")
	// The stack replaces the comment rather than joining it.
	requireNotContains(t, out, "stack comment")
	for _, number := range []int{1, 2, 3} {
		requireEqual(t, len(r.prComments(number)), 0, "no comment on #"+strconv.Itoa(number))
	}

	calls := r.ghCallLog()
	// Bottom first, by number, onto trunk, through the configured remote.
	requireContains(t, calls, "stack link --base main --remote origin 1 2 3")
	requireNotContains(t, calls, "--method")

	state := r.ghStubState()
	requireEqual(t, len(state.Stacks), 1, "one stack on GitHub")
	requireEqual(t, state.Stacks[0].Base, "main", "stack base")
	requireEqual(t, numbers(state.Stacks[0].PRs), "1 2 3", "stack members")
}

func TestGitHubStacksRelinksFromASingleBranchSubmit(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn")

	// Like the comment, the link is about the stack and not the branch, so
	// submitting one branch still records the whole chain.
	r.stk("checkout", "api")
	r.commit("more.txt", "more\n", "more api work")
	out := r.stk("submit", "-pn")
	requireContains(t, out, "Pushed api to origin")
	requireContains(t, out, "Linked #1, #2 as a stack on GitHub")
	requireEqual(t, strings.Count(r.ghCallLog(), "stack link"), 2, "linked on both runs")
	requireEqual(t, len(r.ghStubState().Stacks), 1, "still one stack")
}

func TestGitHubStacksNeedsTheExtensionBeforePushingAnything(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.removeGHStackExtension()

	out := r.stkFail("ss", "-pn")
	requireContains(t, out, "the gh stack extension is not installed")
	requireContains(t, out, "gh extension install github/gh-stack")
	requireContains(t, out, "git config stk.githubStacks false")
	// Checked first, so nothing was published.
	heads := remoteHeads(r)
	requireNotContains(t, heads, "refs/heads/api")
	requireNotContains(t, heads, "refs/heads/service")
	requireNotContains(t, r.ghCallLog(), "pr create")

	// Pushing without --pull never asks about gh at all.
	out = r.stk("ss")
	requireContains(t, out, "Pushed api to origin")
	requireEqual(t, strings.Count(r.ghCallLog(), "extension list"), 1, "only the --pull run asked")
}

func TestGitHubStacksReportsARepositoryWithoutStacks(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.disableGHStacks()

	out := r.stkFail("ss", "-pn")
	requireContains(t, out, "stacked pull requests are not available for this repository")
	// gh's own words, quoted.
	requireContains(t, out, "    ✗ Stacked pull requests are not available for this repository")
	requireContains(t, out, "only the link")
	requireContains(t, out, "git config stk.githubStacks false")
	// Everything before the link stands.
	requireContains(t, remoteHeads(r), "refs/heads/service")
	requireEqual(t, len(r.ghStubState().PRs), 2, "pull requests opened")
	requireEqual(t, len(r.ghStubState().Stacks), 0, "no stack recorded")
}

func TestGitHubStacksDoesNotLinkASinglePullRequest(t *testing.T) {
	r := githubStacksRepo(t, "api")

	out := r.stk("submit", "-pn")
	requireContains(t, out, "Opened pull request #1")
	requireNotContains(t, out, "stack on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")
}

func TestGitHubStacksNoLinkLeavesTheStackAlone(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	// Without the link there is nothing to need the extension for.
	r.removeGHStackExtension()

	out := r.stk("ss", "-pn", "--no-link")
	requireContains(t, out, "2 pull request(s) opened")
	requireNotContains(t, out, "stack on GitHub")
	// Native mode: no comment either, --no-link is not a way back to it.
	requireNotContains(t, out, "stack comment")
	requireNotContains(t, r.ghCallLog(), "stack link")
	requireNotContains(t, r.ghCallLog(), "extension list")
	requireEqual(t, len(r.prComments(1)), 0, "no comment written")
}

func TestGitHubStacksStopsBelowABranchWithoutAPullRequest(t *testing.T) {
	r := githubStacksRepo(t, "api")
	// service adds nothing to api, so it is pushed but not proposed.
	r.stk("create", "service")
	r.stk("create", "ui")
	r.commit("ui.txt", "ui\n", "ui")

	out := r.stk("ss", "-pn")
	requireContains(t, out, "service adds no commits to api; no pull request opened")
	requireContains(t, out, "Opened pull request #2 for ui onto service")
	// Linking #1 and #2 across the gap would have gh retarget #2 onto api.
	requireContains(t, out, "service has no pull request, so the stack on GitHub stops below it")
	requireNotContains(t, out, "Linked")
	requireNotContains(t, r.ghCallLog(), "stack link")
}

func TestGitHubStacksLinksOnlyTheLinearChain(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	// A sibling of service: GitHub stacks are linear, so it is not part of
	// service's chain.
	r.stk("create", "other", "--from", "api")
	r.commit("other.txt", "other\n", "other")
	r.stk("checkout", "service")

	out := r.stk("ss", "-pn")
	requireContains(t, out, "3 pull request(s) opened")
	api, service, other := prNumber(r, "api"), prNumber(r, "service"), prNumber(r, "other")
	want := "stack link --base main --remote origin " + strconv.Itoa(api) + " " + strconv.Itoa(service)
	requireContains(t, r.ghCallLog(), want+"\n")
	requireNotContains(t, r.ghCallLog(), " "+strconv.Itoa(other)+"\n")
	requireEqual(t, len(r.ghStubState().Stacks), 1, "one stack")
	requireEqual(t, len(r.ghStubState().Stacks[0].PRs), 2, "two pull requests in it")
}

func TestGitHubStacksDryRunLinksNothing(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn")

	out := r.stk("ss", "-pn", "--dry-run")
	requireContains(t, out, "(dry-run) would link #1, #2 as a stack on GitHub")
	requireContains(t, out, "would be linked as a stack on GitHub")
	requireContains(t, out, "nothing has been published")
	requireEqual(t, strings.Count(r.ghCallLog(), "stack link"), 1, "only the real run linked")
}

func TestDoctorChecksTheStackExtensionWhenGitHubStacksIsOn(t *testing.T) {
	r := githubStacksRepo(t, "api")

	out := r.stk("doctor")
	requireContains(t, out, "GitHub stacks: gh stack is installed")
	requireContains(t, out, "No problems found.")

	// Missing extension is a warning: pushing and restacking are unaffected.
	r.removeGHStackExtension()
	res := r.stkAt(r.Root, "", "doctor")
	requireEqual(t, res.Code, 0, "doctor exit code")
	requireContains(t, res.Stdout, "stk.githubStacks is on but the gh stack extension is not installed")
	requireContains(t, res.Stdout, "gh extension install github/gh-stack")
}

func TestDoctorIgnoresTheStackExtensionWhenGitHubStacksIsOff(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api")
	r.stubGH()

	out := r.stk("doctor")
	requireNotContains(t, out, "GitHub stacks")
	requireNotContains(t, r.ghCallLog(), "extension list")
}

// stk sync settles the stack on GitHub too: gh stack's local tracking only
// knows what happened in this checkout, so a stack linked anywhere else is
// found by asking GitHub, which is what the link itself does.

func TestSyncAdoptsAStackLinkedOnGitHub(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	// Published without the link, so this checkout has no record of a stack.
	r.stk("ss", "-pn", "--no-link")
	requireEqual(t, r.ghStackNumbers()[0], 0, "no stack recorded yet")

	// Somebody links the two pull requests on GitHub itself.
	r.linkOnGitHub(7, "main", 1, 2)

	out := r.stk("sync", "--no-restack")
	requireContains(t, out, "#1, #2 are a stack on GitHub")
	requireEqual(t, r.ghStackNumbers()[0], 7, "the stack on GitHub is now recorded here")
	// Adopted, not duplicated: GitHub still holds the one stack.
	requireEqual(t, len(r.ghStubState().Stacks), 1, "one stack on GitHub")
	requireEqual(t, numbers(r.ghStubState().Stacks[0].PRs), "1 2", "its pull requests")
}

func TestSyncLinksAStackThatWasNeverLinked(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn", "--no-link")

	out := r.stk("sync", "--no-restack")
	requireContains(t, out, "#1, #2 are a stack on GitHub")
	requireContains(t, r.ghCallLog(), "stack link --base main --remote origin 1 2")
	requireEqual(t, len(r.ghStubState().Stacks), 1, "one stack on GitHub")
}

func TestSyncDoesNotAskAboutStacksWhenGitHubStacksIsOff(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()
	r.stk("ss", "-pn")

	out := r.stk("sync", "--no-restack")
	requireNotContains(t, out, "stack on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")
}

func TestSyncNoPullsAsksGitHubNothing(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn", "--no-link")
	r.truncateGHCallLog()

	out := r.stk("sync", "--no-restack", "--no-pulls")
	requireNotContains(t, out, "stack on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")
}

func TestSyncDryRunLinksNothing(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn", "--no-link")
	r.truncateGHCallLog()

	out := r.stk("sync", "--no-restack", "--dry-run")
	requireContains(t, out, "would check the stacks on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")
}

func TestSyncLeavesAForkedGraphToSubmit(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("create", "other", "--from", "api")
	r.commit("other.txt", "other\n", "other")
	r.stk("ss", "-pn", "--no-link")
	r.stk("checkout", "other")
	r.stk("ss", "-pn", "--no-link")
	r.truncateGHCallLog()

	// gh stack link is additive, so linking api+service and then api+other
	// would make one stack of all three. stk sync says nothing and leaves the
	// choice to stk submit, which is given the branch.
	out := r.stk("sync", "--no-restack")
	requireNotContains(t, out, "are a stack on GitHub")
	requireNotContains(t, r.ghCallLog(), "stack link")

	// Named explicitly, one side of the fork is linked and the other is not.
	out = r.stk("sync", "--no-restack", "--stack")
	requireContains(t, out, "are a stack on GitHub")
	requireEqual(t, len(r.ghStubState().Stacks), 1, "one stack on GitHub")
	requireEqual(t, numbers(r.ghStubState().Stacks[0].PRs), numbers([]int{prNumber(r, "api"), prNumber(r, "other")}), "the chain through the current branch")
}
