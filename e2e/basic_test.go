package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialiseRepository(t *testing.T) {
	r := newRepoWithRemote(t)
	requireEqual(t, r.git("config", "--local", "--get", "stk.trunk"), "main", "trunk")
	requireEqual(t, r.git("config", "--local", "--get", "stk.remote"), "origin", "remote")
	requireEqual(t, r.git("config", "--local", "--get", "stk.version"), "1", "metadata version")

	// Initialisation must not add anything to the working tree.
	requireEqual(t, r.git("status", "--porcelain"), "", "working tree after init")
}

func TestAutomaticInitPromptAccepted(t *testing.T) {
	base := t.TempDir()
	r := &repo{t: t, Root: filepath.Join(base, "repo"), home: filepath.Join(base, "home")}
	mkdirAll(t, r.Root, r.home)
	r.gitAt(r.Root, "init", "-q", "-b", "main", ".")
	r.commit("README.md", "hi\n", "init")

	res := r.stkAt(r.Root, "y\n", "--interactive", "create", "feature/one")
	if res.Code != 0 {
		t.Fatalf("create failed: %s%s", res.Stdout, res.Stderr)
	}
	requireContains(t, res.All(), "This repository has not been initialised for stk.")
	requireContains(t, res.All(), "stk initialised")
	requireContains(t, res.All(), "Created feature/one from main")
	requireEqual(t, r.currentBranch(), "feature/one", "current branch")
}

func TestAutomaticInitPromptDeclined(t *testing.T) {
	base := t.TempDir()
	r := &repo{t: t, Root: filepath.Join(base, "repo"), home: filepath.Join(base, "home")}
	mkdirAll(t, r.Root, r.home)
	r.gitAt(r.Root, "init", "-q", "-b", "main", ".")
	r.commit("README.md", "hi\n", "init")

	res := r.stkAt(r.Root, "n\n", "--interactive", "create", "feature/one")
	requireEqual(t, res.Code, 1, "exit code")
	requireContains(t, res.All(), "repository is not initialised")
	if r.branchExists("feature/one") {
		t.Fatal("branch was created despite declining initialisation")
	}
}

func TestNonInteractiveRefusesToInitialise(t *testing.T) {
	base := t.TempDir()
	r := &repo{t: t, Root: filepath.Join(base, "repo"), home: filepath.Join(base, "home")}
	mkdirAll(t, r.Root, r.home)
	r.gitAt(r.Root, "init", "-q", "-b", "main", ".")
	r.commit("README.md", "hi\n", "init")

	out := r.stkFail("--no-interactive", "create", "foo")
	requireContains(t, out, "repository is not initialised")
	requireContains(t, out, "stk init")
	if r.tracked("foo") {
		t.Fatal("metadata was written without initialisation")
	}
}

func TestInitFlagInitialisesWithoutPrompting(t *testing.T) {
	base := t.TempDir()
	r := &repo{t: t, Root: filepath.Join(base, "repo"), home: filepath.Join(base, "home")}
	mkdirAll(t, r.Root, r.home)
	r.gitAt(r.Root, "init", "-q", "-b", "main", ".")
	r.commit("README.md", "hi\n", "init")

	out := r.stk("--no-interactive", "--init", "create", "foo")
	requireContains(t, out, "stk initialised")
	requireEqual(t, r.currentBranch(), "foo", "current branch")
}

func TestCreateChildFromCurrentBranch(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "api")
	requireEqual(t, r.currentBranch(), "api", "current branch")
	requireEqual(t, r.parentOf("api"), "main", "parent")
	requireEqual(t, r.baseOf("api"), r.sha("main"), "base ref")

	r.commit("api.txt", "api\n", "add api")
	r.stk("create", "service")
	requireEqual(t, r.parentOf("service"), "api", "parent")
	requireEqual(t, r.baseOf("service"), r.sha("api"), "base ref")
}

func TestCreateFromNamedParent(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "api")
	r.commit("api.txt", "api\n", "add api")
	r.stk("create", "other", "--from", "main")
	requireEqual(t, r.parentOf("other"), "main", "parent")
	requireEqual(t, r.baseOf("other"), r.sha("main"), "base ref")
	requireEqual(t, r.currentBranch(), "other", "current branch")
}

func TestCreateRejectsUntrackedParent(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "legacy", "main")
	out := r.stkFail("create", "child", "--from", "legacy")
	requireContains(t, out, `branch "legacy" is not tracked by stk`)
	requireContains(t, out, "stk track legacy --parent")
	if r.branchExists("child") {
		t.Fatal("branch was created despite an invalid parent")
	}
}

func TestCreateWithoutCheckout(t *testing.T) {
	r := newRepo(t)
	r.stk("create", "api")
	r.commit("api.txt", "api\n", "add api")
	r.stk("create", "sibling", "--from", "main", "--no-checkout")
	requireEqual(t, r.currentBranch(), "api", "current branch should not move")
	requireEqual(t, r.parentOf("sibling"), "main", "parent")
}

func TestMultipleStackLevels(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")

	want := strings.Join([]string{
		"main",
		"└─ api",
		"   └─ service",
		"      └─ ui  ←",
	}, "\n")
	requireEqual(t, stripStatus(r.stackText()), want, "stack rendering")
}

func TestBranchingStackGraph(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.stk("create", "admin")
	r.commit("admin.txt", "admin\n", "admin")
	r.stk("checkout", "api")
	r.stk("create", "student")
	r.commit("student.txt", "student\n", "student")

	want := strings.Join([]string{
		"main",
		"└─ api",
		"   ├─ admin",
		"   └─ student  ←",
	}, "\n")
	requireEqual(t, stripStatus(r.stackText()), want, "stack rendering")
	requireEqual(t, strings.TrimSpace(r.stk("children", "api")), "admin\nstudent", "children")
}

func TestUnpushedCommitDetection(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.commit("b.txt", "2\n", "two")

	// No upstream yet.
	requireContains(t, r.stackText(), "↑?")

	r.git("push", "-q", "-u", "origin", "api")
	requireNotContains(t, r.stackText(), "↑?")

	r.commit("c.txt", "3\n", "three")
	r.commit("d.txt", "4\n", "four")
	requireContains(t, r.stackText(), "↑2")

	info := r.stk("info", "api")
	requireContains(t, info, "Upstream:     origin/api")
	requireContains(t, info, "Unpushed:     2")
	requireContains(t, info, "Published:    yes")
}

func TestDirtyWorktreeIsDistinctFromUnpushed(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")
	r.write("a.txt", "changed\n")

	out := r.stackText()
	// A dirty tree is marked, but the branch is not reported as unpushed.
	requireContains(t, out, "*")
	requireNotContains(t, out, "↑1")
}

func TestRenameKeepsStackIntact(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	apiID := r.git("config", "--local", "--get", "branch.api.stk-id")

	r.stk("rename", "api", "backend")
	requireEqual(t, r.git("config", "--local", "--get", "branch.backend.stk-id"), apiID, "stable id survives rename")
	requireEqual(t, r.parentOf("service"), "backend", "child still points at the renamed parent")
	requireEqual(t, r.parentOf("backend"), "main", "parent of renamed branch")
}

func TestRenameCurrentBranch(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.stk("rename", "backend")
	requireEqual(t, r.currentBranch(), "backend", "current branch")
	requireEqual(t, r.parentOf("backend"), "main", "parent")
}

func TestRenameNeverTouchesTheRemote(t *testing.T) {
	r := newRepoWithRemote(t)
	r.stk("create", "api")
	r.commit("a.txt", "1\n", "one")
	r.git("push", "-q", "-u", "origin", "api")

	out := r.stk("rename", "api", "backend")
	requireContains(t, out, "Remote branch remains:")
	requireContains(t, out, "origin/api")
	requireContains(t, out, "git push -u origin backend")
	requireContains(t, out, "git push origin --delete api")

	// The remote branch really is still there.
	requireContains(t, r.git("ls-remote", "--heads", "origin"), "refs/heads/api")
}

func TestRenameRejectsExistingName(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.git("branch", "taken", "main")
	out := r.stkFail("rename", "api", "taken")
	requireContains(t, out, `branch "taken" already exists`)
}

func TestNavigation(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service", "ui")

	r.stk("down")
	requireEqual(t, r.currentBranch(), "service", "after down")
	r.stk("down", "2")
	requireEqual(t, r.currentBranch(), "main", "after down 2")
	r.stk("up")
	requireEqual(t, r.currentBranch(), "api", "after up")
	r.stk("up", "2")
	requireEqual(t, r.currentBranch(), "ui", "after up 2")
	r.stk("bottom")
	requireEqual(t, r.currentBranch(), "api", "after bottom")
	r.stk("top")
	requireEqual(t, r.currentBranch(), "ui", "after top")
}

func TestUpWithSeveralChildrenNeedsAChoice(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.stk("create", "admin")
	r.stk("checkout", "api")
	r.stk("create", "student")
	r.stk("checkout", "api")

	out := r.stkFail("--no-interactive", "up")
	requireContains(t, out, "admin")
	requireContains(t, out, "student")
}

func TestTrackAndUntrack(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	r.git("switch", "-q", "-c", "legacy")
	r.commit("legacy.txt", "legacy\n", "legacy")

	mergeBase := r.git("merge-base", "api", "legacy")
	r.stk("track", "legacy", "--parent", "api")
	requireEqual(t, r.parentOf("legacy"), "api", "parent")
	requireEqual(t, r.baseOf("legacy"), mergeBase, "base is the merge base")

	r.stk("untrack", "legacy")
	if r.tracked("legacy") {
		t.Fatal("metadata survived untrack")
	}
	if !r.branchExists("legacy") {
		t.Fatal("untrack deleted the git branch")
	}
}

func TestTrackRefusesWithoutParent(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "legacy", "main")
	out := r.stkFail("track", "legacy")
	requireContains(t, out, "--parent is required")
}

func TestTrackRefusesCycle(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	out := r.stkFail("track", "api", "--parent", "service")
	requireContains(t, out, "cycle")
}

func TestUntrackWithChildrenRequiresAChoice(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	out := r.stkFail("untrack", "api")
	requireContains(t, out, "--recursive")
	requireContains(t, out, "--reparent")

	r.stk("untrack", "api", "--reparent", "main")
	requireEqual(t, r.parentOf("service"), "main", "child reparented")
	if r.tracked("api") {
		t.Fatal("api still tracked")
	}
	if !r.branchExists("api") {
		t.Fatal("untrack deleted the git branch")
	}
}

func TestMoveBranchOntoAnotherParent(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a")
	r.stk("create", "b")
	r.commit("b.txt", "b\n", "b")
	r.stk("checkout", "main")
	r.stk("create", "c")
	r.commit("c.txt", "c\n", "c")

	r.stk("checkout", "b")
	r.stk("move", "--onto", "c")
	requireEqual(t, r.parentOf("b"), "c", "new parent")
	// b's commit now sits on top of c.
	requireEqual(t, r.log("b")[0], "b", "tip commit")
	requireContains(t, strings.Join(r.log("b"), "\n"), "c")
}

func TestMoveRejectsCycle(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "a", "b")
	out := r.stkFail("move", "a", "--onto", "b")
	requireContains(t, out, "cycle")
}

func TestJSONOutputIsStable(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")

	var doc struct {
		Trunk    string `json:"trunk"`
		Current  string `json:"current"`
		Branches []struct {
			Name         string   `json:"name"`
			Parent       string   `json:"parent"`
			Children     []string `json:"children"`
			Tracked      bool     `json:"tracked"`
			NeedsRestack bool     `json:"needsRestack"`
			Published    bool     `json:"published"`
		} `json:"branches"`
	}
	if err := json.Unmarshal([]byte(r.stk("stack", "--json")), &doc); err != nil {
		t.Fatalf("stack --json is not valid JSON: %v", err)
	}
	requireEqual(t, doc.Trunk, "main", "trunk")
	requireEqual(t, doc.Current, "service", "current")
	requireEqual(t, len(doc.Branches), 3, "branch count")

	var info struct {
		Name      string `json:"name"`
		Parent    string `json:"parent"`
		Published bool   `json:"published"`
	}
	if err := json.Unmarshal([]byte(r.stk("info", "--json")), &info); err != nil {
		t.Fatalf("info --json is not valid JSON: %v", err)
	}
	requireEqual(t, info.Name, "service", "info name")
	requireEqual(t, info.Parent, "api", "info parent")
	requireEqual(t, info.Published, false, "info published")
}

func TestNoSelectorWithoutATerminal(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")

	// show falls back to printing rather than opening a picker.
	out := r.stk("show")
	requireContains(t, out, "api")

	// checkout with no argument has nothing to fall back to.
	requireContains(t, r.stkFail("checkout"), "not attached to a terminal")
}

func TestPassthroughToGit(t *testing.T) {
	r := newRepo(t)
	res := r.stkAt(r.Root, "", "status", "--porcelain", "--branch")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.Stdout, "## main")

	// An unknown git subcommand keeps git's own exit code.
	res = r.stkAt(r.Root, "", "no-such-git-command")
	if res.Code == 0 {
		t.Fatal("expected git's failure exit code to be preserved")
	}
}

func TestDoctorOnAHealthyRepository(t *testing.T) {
	r := newRepoWithRemote(t)
	buildStack(r, "api", "service")
	out := r.stk("doctor")
	requireContains(t, out, "No problems found.")
	requireNotContains(t, out, "✗")
}

func TestDoctorDetectsOrphanedMetadata(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	// Deleting the parent behind stk's back leaves the child orphaned.
	r.git("checkout", "-q", "main")
	r.git("branch", "-D", "api")

	res := r.stkAt(r.Root, "", "doctor")
	requireEqual(t, res.Code, 1, "doctor exit code")
	requireContains(t, res.Stdout, "parent ids resolve")
	requireContains(t, res.Stdout, "service")
}

func TestDoctorDetectsDuplicateIDs(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	// git branch -c copies the branch config, duplicating the stk id.
	r.git("branch", "-c", "api", "api-copy")

	res := r.stkAt(r.Root, "", "doctor")
	requireEqual(t, res.Code, 1, "doctor exit code")
	requireContains(t, res.Stdout, "branch ids unique")
}

func TestDuplicateIDBlocksRestack(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")
	r.git("branch", "-c", "api", "api-copy")

	out := r.stk("restack")
	requireContains(t, out, "blocked")
}

func TestVersionCommand(t *testing.T) {
	r := newRepo(t)
	out := r.stk("version")
	requireContains(t, out, "stk ")
	requireContains(t, out, "commit:")
	requireContains(t, out, "built:")
}

func TestCwdFlag(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api")
	elsewhere, err := os.MkdirTemp("", "stk-cwd-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(elsewhere)

	res := r.stkAt(elsewhere, "", "--cwd", r.Root, "stack")
	requireEqual(t, res.Code, 0, "exit code")
	requireContains(t, res.Stdout, "api")
}

// buildStack creates a chain of branches, each with one commit.
func buildStack(r *repo, names ...string) {
	r.t.Helper()
	for _, name := range names {
		r.stk("create", name)
		r.commit(name+".txt", name+"\n", name)
	}
}

// stripStatus removes the marker column so tree shape can be compared alone,
// keeping only the current-branch arrow. Markers are separated from the name
// by at least two spaces, which distinguishes them from tree indentation.
func stripStatus(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		idx := strings.LastIndex(line, "  ")
		if idx < 0 || strings.TrimSpace(line[:idx]) == "" {
			out = append(out, strings.TrimRight(line, " "))
			continue
		}
		trimmed := strings.TrimRight(line[:idx], " ")
		if strings.Contains(line[idx:], "←") {
			trimmed += "  ←"
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

func TestShellCompletion(t *testing.T) {
	r := newRepo(t)
	buildStack(r, "api", "service")

	for _, shell := range []string{"bash", "zsh", "fish"} {
		res := r.stkAt(r.Root, "", "completion", shell)
		if res.Code != 0 || len(res.Stdout) == 0 {
			t.Fatalf("completion %s failed (%d): %s", shell, res.Code, res.Stderr)
		}
	}

	// Dynamic branch completion for the flags the design calls out.
	for _, args := range [][]string{
		{"__complete", "checkout", ""},
		{"__complete", "create", "x", "--from", ""},
		{"__complete", "track", "--parent", ""},
		{"__complete", "move", "--onto", ""},
		{"__complete", "rename", ""},
	} {
		res := r.stkAt(r.Root, "", args...)
		requireEqual(t, res.Code, 0, "completion exit code")
		requireContains(t, res.Stdout, "api")
		requireContains(t, res.Stdout, "service")
		requireContains(t, res.Stdout, "main")
	}
}
