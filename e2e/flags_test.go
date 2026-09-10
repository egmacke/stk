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

func TestShorthandFlagsBundle(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	r.stubGH()
	r.useGitHubURL()

	// -a -l -j as one token: every stack, with a legend, as JSON.
	out := r.stk("stack", "-alj")
	requireContains(t, out, `"trunk": "main"`)
	requireContains(t, out, `"name": "service"`)

	// -s -p -n as one token: the whole stack, with generated pull requests.
	out = r.stk("submit", "-spn", "--dry-run")
	requireContains(t, out, "(dry-run) would push api to origin")
	requireContains(t, out, "(dry-run) would push service to origin")
	requireContains(t, out, "would open a pull request for api onto main")
	requireContains(t, out, "would open a pull request for service onto api")

	// A bundle still reaches the flags' own validation.
	requireContains(t, r.stkFail("restack", "-uo"), "use either --up or --only, not both")

	// A shorthand that takes a value may carry it in the same token.
	out = r.stk("create", "bundled", "-fmain", "--dry-run")
	requireContains(t, out, "Would create bundled from main")
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

func TestShorthandFlags(t *testing.T) {
	r := newRepo(t)

	// Value-taking flags.
	r.stk("create", "api", "-f", "main")
	r.commit("api.txt", "api\n", "api")
	r.git("switch", "-q", "-c", "loose")
	r.commit("loose.txt", "loose\n", "loose work")
	r.stk("track", "loose", "-p", "api")
	requireEqual(t, r.parentOf("loose"), "api", "parent set with -p")
	r.stk("move", "loose", "-o", "main")
	requireEqual(t, r.parentOf("loose"), "main", "parent set with -o")
	r.stk("untrack", "loose", "-r")
	if r.tracked("loose") {
		t.Fatal("-r did not untrack")
	}

	// Boolean flags.
	requireContains(t, r.stk("stack", "-j"), `"branches"`)
	requireContains(t, r.stk("info", "api", "-j"), `"name"`)
	requireContains(t, r.stk("doctor", "-j"), `"checks"`)
	requireContains(t, r.stk("stack", "-a"), "loose")
	requireContains(t, r.stk("stack", "-l"), "requires restack")
	r.stk("checkout", "api")
	requireContains(t, r.stk("restack", "-o"), "api")
	requireContains(t, r.stk("restack", "-u"), "api")
	requireContains(t, r.stk("sync", "-s", "--no-cleanup"), "api")
}

// Global flags stay long-form only: they are stripped before an unknown
// command reaches git, and single letters collide with git's own options.
func TestGlobalFlagsHaveNoShorthand(t *testing.T) {
	r := newRepo(t)
	requireContains(t, r.stkFail("-q", "stack"), "unknown shorthand flag")
	// git still receives its own short flags untouched.
	requireContains(t, r.stkAt(r.Root, "", "log", "-1", "--format=%s").Stdout, "init")
}
