package e2e

import (
	"strings"
	"testing"
)

// aliased lists every short form stk ships, with the command it resolves to.
var aliased = map[string]string{
	"c":    "create",
	"co":   "checkout",
	"r":    "restack",
	"tr":   "track",
	"utr":  "untrack",
	"rn":   "rename",
	"cont": "continue",
	"ab":   "abort",
	"ss":   "submit",
}

// notAliased lists short names stk deliberately does not claim. git does not
// know them either, so they must come back as git's own "not a git command".
var notAliased = []string{
	"s", "sy", "u", "d", "t", "b", "i", "p", "ch", "st", "sh", "dr", "in", "cr", "rs", "ct", "ut", "mo",
}

// gitOwned lists short names that are real git commands. stk must not shadow
// them; running them bare reaches git and gets git's own usage error.
var gitOwned = []string{"mv"}

func TestAliasesResolveToTheirCommands(t *testing.T) {
	r := newRepo(t)
	// Each alias reaches the same help text as its full name.
	for alias, full := range aliased {
		short := r.stkAt(r.Root, "", "help", alias)
		long := r.stkAt(r.Root, "", "help", full)
		requireEqual(t, short.Code, 0, "help "+alias)
		if short.Stdout != long.Stdout {
			t.Fatalf("stk %s and stk %s describe different commands", alias, full)
		}
		requireContains(t, short.Stdout, "Aliases:")
	}
}

func TestUnaliasedNamesStillReachGit(t *testing.T) {
	r := newRepo(t)
	for _, name := range notAliased {
		res := r.stkAt(r.Root, "", name)
		if res.Code == 0 {
			t.Fatalf("stk %s was handled by stk; it must pass through to git", name)
		}
		requireContains(t, res.All(), "is not a git command")
	}
	for _, name := range gitOwned {
		res := r.stkAt(r.Root, "", name)
		requireContains(t, res.All(), "usage: git "+name)
	}
}

func TestAliasesDoTheWork(t *testing.T) {
	r := newRepo(t)

	r.stk("c", "api")
	requireEqual(t, r.currentBranch(), "api", "stk c created and checked out")
	r.commit("api.txt", "api\n", "api")

	r.stk("c", "service")
	r.commit("service.txt", "service\n", "service")

	r.stk("co", "api")
	requireEqual(t, r.currentBranch(), "api", "stk co switched")

	r.amend("api.txt", "api amended\n", "api amended")
	r.stk("r")
	requireEqual(t, r.log("service")[1], "api amended", "stk r restacked the stack")

	r.git("switch", "-q", "-c", "legacy")
	r.commit("legacy.txt", "l\n", "legacy")
	r.stk("tr", "legacy", "--parent", "api")
	requireEqual(t, r.parentOf("legacy"), "api", "stk tr tracked the branch")
	r.stk("utr", "legacy")
	if r.tracked("legacy") {
		t.Fatal("stk utr did not untrack")
	}

	r.stk("co", "api")
	r.stk("rn", "backend")
	requireEqual(t, r.currentBranch(), "backend", "stk rn renamed")
}

func TestContinueAndAbortAliases(t *testing.T) {
	r := newRepo(t)
	conflictingStack(r)
	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten\n", "a rewritten")

	res := r.stkAt(r.Root, "", "restack")
	requireEqual(t, res.Code, 1, "conflict exit code")
	r.write("shared.txt", "resolved\n")
	r.git("add", "shared.txt")
	r.stk("cont")
	requireEqual(t, r.log("b")[1], "a rewritten", "stk cont finished the operation")

	// And the abort alias on a fresh conflict.
	r.stk("checkout", "a")
	r.amend("shared.txt", "a rewritten again\n", "a rewritten again")
	bTip := r.sha("b")
	res = r.stkAt(r.Root, "", "restack")
	requireEqual(t, res.Code, 1, "second conflict exit code")
	r.stk("ab")
	requireEqual(t, r.sha("b"), bTip, "stk ab restored the branch")
}

func TestMoveHasNoAliasSoGitMvStillWorks(t *testing.T) {
	r := newRepo(t)
	r.commit("old.txt", "content\n", "add old")

	r.stk("mv", "old.txt", "new.txt")
	requireContains(t, r.git("status", "--porcelain"), "old.txt -> new.txt")
	if !strings.Contains(r.git("status", "--porcelain"), "new.txt") {
		t.Fatal("git mv did not run")
	}
}

func TestDoubleDashForcesPassthrough(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")

	// Without the escape, r is stk's restack.
	requireContains(t, r.stk("r"), "up to date")

	// With it, the same name reaches git, which has no such command.
	res := r.stkAt(r.Root, "", "--", "r")
	if res.Code == 0 {
		t.Fatal("stk -- r should have reached git")
	}
	requireContains(t, res.All(), "is not a git command")

	// The escape marker itself is not forwarded as an argument.
	res = r.stkAt(r.Root, "", "--", "status", "--porcelain", "--branch")
	requireEqual(t, res.Code, 0, "stk -- status")
	requireContains(t, res.Stdout, "## api")

	// Global flags still apply before the escape.
	res = r.stkAt(r.Root, "", "--cwd", r.Root, "--", "rev-parse", "--abbrev-ref", "HEAD")
	requireEqual(t, res.Code, 0, "stk --cwd ... -- rev-parse")
	requireEqual(t, strings.TrimSpace(res.Stdout), "api", "escaped git command ran in --cwd")
}

func TestAliasCompletion(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	// Completion resolves through the alias just as it does the full name.
	res := r.stkAt(r.Root, "", "__complete", "co", "")
	requireEqual(t, res.Code, 0, "completion exit code")
	requireContains(t, res.Stdout, "api")
	requireContains(t, res.Stdout, "service")
}

func TestAliasesAreDocumented(t *testing.T) {
	r := newRepo(t)
	out := r.stkAt(r.Root, "", "--help").Stdout
	requireContains(t, out, "Short forms:")
	for alias := range aliased {
		requireContains(t, out, alias)
	}
	requireContains(t, out, "stk -- r")
}
