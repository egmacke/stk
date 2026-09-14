package e2e

import (
	"strings"
	"testing"
)

// The mirror: with stk.githubStacks on, gh stack's local tracking file is a
// projection of stk's graph, rewritten after every change to it.

func TestGHStackMirrorIsOffByDefault(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	if r.ghStackExists() {
		t.Fatal("gh stack tracking written without opting in")
	}
}

func TestGHStackMirrorFollowsCreateTrackAndUntrack(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()

	out := r.stk("create", "api")
	requireContains(t, out, "gh stack tracking updated")
	r.commit("api.txt", "api\n", "api")
	r.stk("create", "service")
	r.commit("service.txt", "service\n", "service")

	f := r.ghStack()
	requireEqual(t, f.SchemaVersion, 1, "schema version")
	requireEqual(t, strings.Join(r.ghStackChains(), "|"), "main: api service", "one linear stack")
	requireEqual(t, f.Stacks[0].Trunk.Head, r.sha("main"), "trunk head")
	// gh stack's base is stk's base: the parent's tip the branch was cut from.
	requireEqual(t, f.Stacks[0].Branches[0].Base, r.baseOf("api"), "api base")
	requireEqual(t, f.Stacks[0].Branches[1].Base, r.baseOf("service"), "service base")

	// A branch made by plain git and then tracked joins the projection.
	r.git("branch", "loose", "main")
	r.stk("track", "loose", "--parent", "service")
	requireEqual(t, strings.Join(r.ghStackChains(), "|"), "main: api service loose", "tracked branch appended")

	r.stk("untrack", "loose")
	requireEqual(t, strings.Join(r.ghStackChains(), "|"), "main: api service", "untracked branch dropped")
}

func TestGHStackMirrorSplitsAForkIntoSubStacks(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	buildStack(r, "api", "service")
	r.stk("create", "other", "--from", "api")
	r.commit("other.txt", "other\n", "other")

	// gh stack is linear, so the fork at api ends the first stack and each
	// child starts one of its own with api as its trunk.
	chains := r.ghStackChains()
	requireEqual(t, len(chains), 3, "three stacks")
	requireEqual(t, chains[0], "main: api", "the shared bottom")
	requireEqual(t, chains[1], "api: other", "first fork")
	requireEqual(t, chains[2], "api: service", "second fork")
	requireEqual(t, r.ghStack().Stacks[1].Trunk.Head, r.sha("api"), "fork trunk head")
}

func TestGHStackMirrorFollowsRestackAndRename(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	buildStack(r, "api", "service")
	before := r.ghStack().Stacks[0].Branches[1].Base

	r.stk("checkout", "api")
	r.amend("api.txt", "api amended\n", "api amended")
	out := r.stk("restack")
	requireContains(t, out, "gh stack tracking updated")
	after := r.ghStack().Stacks[0].Branches[1].Base
	if before == after {
		t.Fatal("service's base did not follow the restack")
	}
	requireEqual(t, after, r.sha("api"), "service now based on the amended api")

	// gh stack keys by name, so a rename must reach it.
	r.stk("rename", "api", "core")
	requireEqual(t, strings.Join(r.ghStackChains(), "|"), "main: core service", "renamed in place")
}

func TestGHStackMirrorFollowsMoveAndSyncCleanup(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	buildStack(r, "api", "service", "ui")

	r.stk("move", "ui", "--onto", "api")
	chains := r.ghStackChains()
	requireEqual(t, strings.Join(chains, "|"), "main: api|api: service|api: ui", "fork after the move")

	// api merges into trunk; sync prunes it and the survivors re-root.
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--ff-only", "api")
	r.git("push", "-q", "origin", "main")
	r.stk("sync", "--cleanup")
	if r.branchExists("api") {
		t.Fatal("api was not pruned")
	}
	requireEqual(t, strings.Join(r.ghStackChains(), "|"), "main: service|main: ui", "pruned branch gone, children on trunk")
}

func TestGHStackMirrorAdoptsBranchesGHStackAdded(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	buildStack(r, "api")
	// The user reaches for gh stack add: a branch on top that stk has not
	// heard of, recorded only in gh stack's file.
	r.git("branch", "service", "api")
	r.git("checkout", "-q", "service")
	r.commit("service.txt", "service\n", "service")
	r.writeGHStack(`{
  "schemaVersion": 1,
  "repository": "",
  "stacks": [
    {
      "trunk": {"branch": "main", "head": "` + r.sha("main") + `"},
      "branches": [
        {"branch": "api", "base": "` + r.sha("main") + `"},
        {"branch": "service", "base": "` + r.sha("api") + `", "pullRequest": {"number": 7, "url": "https://github.com/example/repo/pull/7"}}
      ]
    }
  ]
}`)

	out := r.stk("sync", "--no-restack")
	requireContains(t, out, "Tracking service with parent api (from gh stack)")
	requireEqual(t, r.parentOf("service"), "api", "parent from gh stack's order")
	requireEqual(t, r.baseOf("service"), r.sha("api"), "base from gh stack's record")
	// And the pull request gh stack knew survives the rewrite.
	f := r.ghStack()
	requireEqual(t, f.Stacks[0].Branches[1].PullRequest.Number, 7, "pull request carried")
	requireContains(t, r.stk("stack"), "#7")
}

func TestGHStackMirrorLeavesAnUnattachableBranchAlone(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	buildStack(r, "api")
	r.git("branch", "mystery", "main")
	r.git("branch", "above", "mystery")
	r.writeGHStack(`{"schemaVersion": 1, "repository": "", "stacks": [
    {"trunk": {"branch": "nowhere"}, "branches": [{"branch": "mystery"}, {"branch": "above"}]}
  ]}`)

	out := r.stk("sync", "--no-restack")
	requireContains(t, out, "mystery is in gh stack's tracking, but nowhere below it is not tracked by stk; left alone")
	if r.tracked("mystery") || r.tracked("above") {
		t.Fatal("a branch was adopted under a parent stk cannot resolve")
	}
	// The foreign stack holds nothing stk tracks, so it is kept as it was.
	chains := r.ghStackChains()
	requireEqual(t, strings.Join(chains, "|"), "main: api|nowhere: mystery above", "foreign stack preserved")
}

func TestGHStackMirrorRecordsPullRequestsOnSubmit(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")

	r.stk("ss", "-pn")
	f := r.ghStack()
	requireEqual(t, f.Stacks[0].Branches[0].PullRequest.Number, 1, "api's pull request")
	requireEqual(t, f.Stacks[0].Branches[1].PullRequest.Number, 2, "service's pull request")
	requireEqual(t, f.Stacks[0].Branches[1].PullRequest.URL, "https://github.com/example/repo/pull/2", "url recorded")

	// Visible wherever stk shows a branch.
	out := r.stk("stack")
	requireContains(t, out, "#1")
	requireContains(t, out, "#2")
	requireContains(t, r.stk("info", "service"), "Pull request: #2")
	requireContains(t, r.stk("stack", "--json"), `"pr": {`)
	requireContains(t, r.stk("stack", "--json"), `"number": 2`)
}

func TestGHStackMirrorSyncRefreshesPullRequestState(t *testing.T) {
	r := githubStacksRepo(t, "api", "service")
	r.stk("ss", "-pn")
	r.mergePullRequest(1)

	out := r.stk("sync", "--no-restack", "--no-cleanup")
	requireContains(t, out, "gh stack tracking: 1 marked merged")
	requireEqual(t, r.ghStack().Stacks[0].Branches[0].PullRequest.Merged, true, "api merged")
	requireContains(t, r.stk("stack"), "#1 merged")
	requireContains(t, r.stk("info", "api"), "#1 (merged)")
	requireContains(t, r.ghCallLog(), "pr view --repo example/repo 1 --json state")
}

func TestGHStackMirrorSyncDiscoversAPullRequestOpenedElsewhere(t *testing.T) {
	r := githubStacksRepo(t, "api")
	r.seedPullRequest(41, "api", "main")

	out := r.stk("sync", "--no-restack", "--no-cleanup")
	requireContains(t, out, "gh stack tracking: 1 pull request(s) recorded")
	requireEqual(t, r.ghStack().Stacks[0].Branches[0].PullRequest.Number, 41, "found through gh pr list")
}

func TestGHStackMirrorDoctorReportsDrift(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	r.stubGH()
	buildStack(r, "api", "service")
	requireContains(t, r.stk("doctor"), "gh stack tracking: in step with stk")

	// gh stack learns of a branch stk does not track ...
	r.git("branch", "extra", "service")
	r.writeGHStack(`{"schemaVersion": 1, "repository": "", "stacks": [
    {"trunk": {"branch": "main"}, "branches": [{"branch": "api"}, {"branch": "service"}, {"branch": "extra"}]}
  ]}`)
	res := r.stkAt(r.Root, "", "doctor")
	requireEqual(t, res.Code, 0, "drift is a warning")
	requireContains(t, res.Stdout, "tracked only by gh stack: extra")

	// ... and the next sync settles it in both directions.
	r.stk("sync", "--no-restack")
	requireContains(t, r.stk("doctor"), "gh stack tracking: in step with stk")
	requireEqual(t, r.parentOf("extra"), "service", "extra adopted")

	// stk learns of a branch gh stack does not have.
	r.writeGHStack(`{"schemaVersion": 1, "repository": "", "stacks": [
    {"trunk": {"branch": "main"}, "branches": [{"branch": "api"}]}
  ]}`)
	requireContains(t, r.stkAt(r.Root, "", "doctor").Stdout, "tracked only by stk: extra, service")
}

func TestGHStackMirrorRefusesANewerSchema(t *testing.T) {
	r := newRepoWithRemote(t)
	r.enableGitHubStacks()
	r.writeGHStack(`{"schemaVersion": 2, "stacks": []}`)

	res := r.stkAt(r.Root, "", "create", "api")
	requireEqual(t, res.Code, 0, "create succeeded regardless")
	requireContains(t, res.All(), "Created api from main")
	requireContains(t, res.All(), "gh stack tracking not updated")
	requireContains(t, res.All(), "newer than stk understands")
	// The file it could not read is left exactly as it was.
	requireContains(t, r.fileContent(".git/gh-stack"), `"schemaVersion": 2`)
}

// stk track --from-pr

func TestTrackFromPRBringsTheStackIn(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()
	// A colleague's stack: two branches on origin with pull requests, linked
	// as a stack on GitHub. This clone has never seen the branches.
	r.remoteOnlyBranch("feat/api", "main")
	r.remoteOnlyBranch("feat/ui", "feat/api")
	r.seedPullRequest(10, "feat/api", "main")
	r.seedPullRequest(11, "feat/ui", "feat/api")
	r.seedRemoteStack(3, "main", 10, 11)

	out := r.stk("track", "--from-pr", "11")
	requireContains(t, out, "Running gh stack checkout 11...")
	requireContains(t, out, "Tracking feat/api with parent main (from gh stack)")
	requireContains(t, out, "Tracking feat/ui with parent feat/api (from gh stack)")
	requireContains(t, out, "2 branch(es) tracked from the stack on GitHub.")
	requireEqual(t, r.currentBranch(), "feat/ui", "gh stack checked out the pull request's branch")
	requireEqual(t, r.parentOf("feat/api"), "main", "bottom on trunk")
	requireEqual(t, r.parentOf("feat/ui"), "feat/api", "top on bottom")
	requireEqual(t, r.baseOf("feat/ui"), r.sha("feat/api"), "base from gh stack")
	requireContains(t, r.ghCallLog(), "stack checkout 11")

	f := r.ghStack()
	requireEqual(t, f.Stacks[0].Number, 3, "stack number kept")
	requireEqual(t, f.Stacks[0].Branches[1].PullRequest.Number, 11, "pull request kept")
	stackOut := r.stk("stack")
	requireContains(t, stackOut, "feat/ui")
	requireContains(t, stackOut, "#11")

	// Asking again finds everything already in place.
	out = r.stk("track", "--from-pr", "https://github.com/example/repo/pull/10")
	requireContains(t, out, "Every branch of that stack was already tracked.")
}

func TestTrackFromPRNeedsTheOptIn(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()

	out := r.stkFail("track", "--from-pr", "11")
	requireContains(t, out, "needs stk.githubStacks = true")
	requireContains(t, out, "git config stk.githubStacks true")
	requireNotContains(t, r.ghCallLog(), "stack checkout")
}

func TestTrackFromPRNeedsTheExtension(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()
	r.removeGHStackExtension()

	out := r.stkFail("track", "--from-pr", "11")
	requireContains(t, out, "the gh stack extension is not installed")
	requireNotContains(t, r.ghCallLog(), "stack checkout")
}

func TestTrackFromPRReportsAnUnknownPullRequest(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()

	out := r.stkFail("track", "--from-pr", "99")
	requireContains(t, out, `gh stack found no stack for "99"`)
}

func TestTrackFromPRRelaysACompositionConflict(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()
	buildStack(r, "feat/api")
	r.remoteOnlyBranch("feat/ui", "main")
	r.seedPullRequest(10, "feat/api", "main")
	r.seedPullRequest(11, "feat/ui", "feat/api")
	r.seedRemoteStack(3, "main", 10, 11)
	// Locally gh stack (through the mirror) has a stack of feat/api alone;
	// GitHub's has two branches. gh stack cannot ask over a pipe.

	out := r.stkFail("track", "--from-pr", "11")
	requireContains(t, out, "gh stack's local tracking disagrees with the stack on GitHub")
	requireContains(t, out, "gh stack unstack --local")
}

func TestTrackFromPRRejectsABranchArgument(t *testing.T) {
	r := newRepoWithRemote(t)
	out := r.stkFail("track", "api", "--from-pr", "11")
	requireContains(t, out, "--from-pr names the whole stack")
}

func TestTrackFromPRDryRunRunsNothing(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stubGH()
	r.useGitHubURL()
	r.enableGitHubStacks()

	out := r.stk("track", "--from-pr", "11", "--dry-run")
	requireContains(t, out, "(dry-run) would run gh stack checkout 11")
	requireNotContains(t, r.ghCallLog(), "stack checkout")
}

func TestInitSuggestsTheOptInWhenGHStackTrackingExists(t *testing.T) {
	r := newRepoWithRemote(t)
	r.writeGHStack(`{"schemaVersion": 1, "repository": "", "stacks": []}`)
	out := r.stk("init")
	requireContains(t, out, "This repository has gh stack tracking")
	requireContains(t, out, "git config stk.githubStacks true")
}
