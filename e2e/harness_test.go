// Package e2e drives the compiled stk binary against real temporary git
// repositories. Nothing here stubs git out: the point is to check the actual
// commit graph and the actual stk metadata after each operation.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var (
	stkBin    string
	stubGHBin string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "stk-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	stkBin = filepath.Join(dir, "stk")
	build := exec.Command("go", "build", "-o", stkBin, "..")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building stk: %v\n", err)
		os.Exit(1)
	}

	// The fake GitHub CLI is a real program, so the comment endpoints stk uses
	// can be answered with correct JSON and asserted on.
	stubGHBin = filepath.Join(dir, "gh")
	build = exec.Command("go", "build", "-o", stubGHBin, "./stubgh")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building the stub gh: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// repo is one temporary git repository under test.
type repo struct {
	t      *testing.T
	Root   string
	Origin string
	home   string
	// bin is prepended to PATH, so a test can put a stub gh in front of any
	// real one and assert on how stk called it.
	bin string
}

// env returns a hermetic environment: no user or system git config, a fixed
// identity, and no editor or pager that could block.
func (r *repo) env() []string {
	return append(os.Environ(),
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_CALLS="+r.ghCalls(),
		"GH_STATE="+r.ghState(),
		"GH_UNAUTHENTICATED="+r.ghUnauthenticated(),
		"HOME="+r.home,
		"XDG_CONFIG_HOME="+filepath.Join(r.home, "config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(r.home, "gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(r.home, "gitconfig-system"),
		"GIT_AUTHOR_NAME=stk test",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=stk test",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_EDITOR=true",
		"GIT_PAGER=cat",
		"EDITOR=true",
		"TERM=dumb",
		"NO_COLOR=1",
	)
}

type result struct {
	Stdout string
	Stderr string
	Code   int
}

// All returns stdout and stderr together, for assertions that do not care
// which stream a message used.
func (res result) All() string { return res.Stdout + res.Stderr }

func (r *repo) runIn(dir, stdin string, name string, args ...string) result {
	r.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = r.env()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			code = ee.ExitCode()
		} else {
			r.t.Fatalf("running %s %v: %v", name, args, err)
		}
	}
	return result{Stdout: out.String(), Stderr: errOut.String(), Code: code}
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// stkAt runs stk in a specific directory without asserting the exit code.
func (r *repo) stkAt(dir, stdin string, args ...string) result {
	r.t.Helper()
	full := append([]string{"--no-color"}, args...)
	return r.runIn(dir, stdin, stkBin, full...)
}

// stk runs stk in the repository root and fails the test on a non-zero exit.
func (r *repo) stk(args ...string) string {
	r.t.Helper()
	res := r.stkAt(r.Root, "", args...)
	if res.Code != 0 {
		r.t.Fatalf("stk %s failed (%d)\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), res.Code, res.Stdout, res.Stderr)
	}
	return res.Stdout
}

// stkFail runs stk expecting a non-zero exit and returns the combined output.
func (r *repo) stkFail(args ...string) string {
	r.t.Helper()
	res := r.stkAt(r.Root, "", args...)
	if res.Code == 0 {
		r.t.Fatalf("stk %s unexpectedly succeeded\nstdout:\n%s", strings.Join(args, " "), res.Stdout)
	}
	return res.All()
}

// git runs git in the repository root and fails the test on a non-zero exit.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	return r.gitAt(r.Root, args...)
}

func (r *repo) gitAt(dir string, args ...string) string {
	r.t.Helper()
	res := r.runIn(dir, "", "git", args...)
	if res.Code != 0 {
		r.t.Fatalf("git %s failed in %s (%d)\n%s%s",
			strings.Join(args, " "), dir, res.Code, res.Stdout, res.Stderr)
	}
	return strings.TrimRight(res.Stdout, "\n")
}

// write puts content in a file relative to the repository root.
func (r *repo) write(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.Root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit writes a file and commits it.
func (r *repo) commit(rel, content, message string) {
	r.t.Helper()
	r.write(rel, content)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", message)
}

// amend rewrites the tip commit with new file content.
func (r *repo) amend(rel, content, message string) {
	r.t.Helper()
	r.write(rel, content)
	r.git("add", "-A")
	r.git("commit", "-q", "--amend", "-m", message)
}

// fileContent reads a file relative to the repository root.
func (r *repo) fileContent(rel string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Root, rel))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

func (r *repo) sha(rev string) string {
	r.t.Helper()
	return r.git("rev-parse", rev)
}

// parentOf reads the recorded stack parent of a branch.
func (r *repo) parentOf(branch string) string {
	r.t.Helper()
	return strings.TrimSpace(r.stk("parent", branch))
}

// baseOf reads the protected base ref of a branch.
func (r *repo) baseOf(branch string) string {
	r.t.Helper()
	id := r.git("config", "--local", "--get", "branch."+branch+".stk-id")
	return r.git("rev-parse", "refs/stk/base/"+id)
}

// tracked reports whether stk has metadata for a branch.
func (r *repo) tracked(branch string) bool {
	r.t.Helper()
	res := r.runIn(r.Root, "", "git", "config", "--local", "--get", "branch."+branch+".stk-id")
	return res.Code == 0
}

func (r *repo) branchExists(branch string) bool {
	r.t.Helper()
	res := r.runIn(r.Root, "", "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return res.Code == 0
}

func (r *repo) currentBranch() string {
	r.t.Helper()
	return r.git("symbolic-ref", "--short", "HEAD")
}

// log returns the first-parent commit subjects of a branch, newest first.
func (r *repo) log(branch string) []string {
	r.t.Helper()
	out := r.git("log", "--format=%s", branch)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// ghCalls is the file the stub gh appends every invocation to.
func (r *repo) ghCalls() string { return filepath.Join(r.bin, "gh-calls.log") }

// ghState is the stub gh's pull request and comment store.
func (r *repo) ghState() string { return filepath.Join(r.bin, "gh-state.json") }

// ghUnauthenticated is the file whose presence makes the stub gh refuse.
func (r *repo) ghUnauthenticated() string { return filepath.Join(r.bin, "gh-logged-out") }

// stubGH puts the fake GitHub CLI on PATH.
func (r *repo) stubGH() {
	r.t.Helper()
	if err := os.Symlink(stubGHBin, filepath.Join(r.bin, "gh")); err != nil {
		r.t.Fatal(err)
	}
}

// logOutGH makes the stub gh report that nobody is logged in.
func (r *repo) logOutGH() {
	r.t.Helper()
	if err := os.WriteFile(r.ghUnauthenticated(), nil, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// ghCallLog returns every stub gh invocation, one per line.
func (r *repo) ghCallLog() string {
	r.t.Helper()
	data, err := os.ReadFile(r.ghCalls())
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		r.t.Fatal(err)
	}
	return string(data)
}

// ghStubState is the stub gh's store, decoded.
type ghStubState struct {
	PRs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Base   string `json:"baseRefName"`
		Head   string `json:"headRefName"`
		Draft  bool   `json:"isDraft"`
		State  string `json:"state"`
	} `json:"prs"`
	Comments map[string][]struct {
		ID    int64  `json:"id"`
		Body  string `json:"body"`
		Login string `json:"login"`
	} `json:"comments"`
}

func (r *repo) ghStubState() ghStubState {
	r.t.Helper()
	var state ghStubState
	data, err := os.ReadFile(r.ghState())
	if err != nil {
		if os.IsNotExist(err) {
			return state
		}
		r.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		r.t.Fatal(err)
	}
	return state
}

// prComments returns the comments the stub gh holds for one pull request.
func (r *repo) prComments(number int) []string {
	r.t.Helper()
	var out []string
	for _, c := range r.ghStubState().Comments[strconv.Itoa(number)] {
		out = append(out, c.Body)
	}
	return out
}

// appendPRComment adds a comment to the stub gh's store, as another person
// commenting on the pull request would.
func (r *repo) appendPRComment(number int, body string) {
	r.t.Helper()
	var raw map[string]any
	data, err := os.ReadFile(r.ghState())
	if err != nil {
		r.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		r.t.Fatal(err)
	}
	comments, _ := raw["comments"].(map[string]any)
	if comments == nil {
		comments = map[string]any{}
		raw["comments"] = comments
	}
	key := strconv.Itoa(number)
	list, _ := comments[key].([]any)
	next, _ := raw["nextComment"].(float64)
	if next == 0 {
		next = 1001
	}
	comments[key] = append(list, map[string]any{"id": next, "body": body, "login": "reviewer"})
	raw["nextComment"] = next + 1
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.ghState(), out, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// mergedPullRequest seeds the stub gh with a pull request that has landed.
func (r *repo) mergedPullRequest(number int, head, base, title string) {
	r.t.Helper()
	r.existingPullRequest(number, head, base, title)
	r.setPullRequestState(number, "MERGED")
}

// setPullRequestState rewrites one pull request's state in the stub's store.
func (r *repo) setPullRequestState(number int, state string) {
	r.t.Helper()
	var raw map[string]any
	data, err := os.ReadFile(r.ghState())
	if err != nil {
		r.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		r.t.Fatal(err)
	}
	prs, _ := raw["prs"].([]any)
	for _, entry := range prs {
		pr, _ := entry.(map[string]any)
		if n, ok := pr["number"].(float64); ok && int(n) == number {
			pr["state"] = state
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.ghState(), out, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// pullRequestBase reads a pull request's base branch from the stub's store.
func (r *repo) pullRequestBase(number int) string {
	r.t.Helper()
	for _, pr := range r.ghStubState().PRs {
		if pr.Number == number {
			return pr.Base
		}
	}
	return ""
}

// existingPullRequest seeds the stub gh with a pull request stk did not open.
func (r *repo) existingPullRequest(number int, head, base, title string) {
	r.t.Helper()
	state := struct {
		NextPR      int              `json:"nextPr"`
		NextComment int64            `json:"nextComment"`
		PRs         []map[string]any `json:"prs"`
	}{
		NextPR:      number + 1,
		NextComment: 1001,
		PRs: []map[string]any{{
			"number":      number,
			"url":         fmt.Sprintf("https://github.com/example/repo/pull/%d", number),
			"title":       title,
			"baseRefName": base,
			"headRefName": head,
			"state":       "OPEN",
		}},
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.ghState(), data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// ghRemoteURL is the repository stk derives a gh --repo argument from.
const ghRemoteURL = "https://github.com/example/repo.git"

// useGitHubURL makes the remote look like a GitHub repository while every git
// transfer still goes to the local origin.
//
// The configured URL is the GitHub one, which is what stk reads to name the
// repository for gh; url.<local>.insteadOf redirects the actual fetches and
// pushes, exactly as a user with a local mirror would.
func (r *repo) useGitHubURL() {
	r.t.Helper()
	r.git("config", "url."+r.Origin+".insteadOf", ghRemoteURL)
	r.git("remote", "set-url", "origin", ghRemoteURL)
}

// tempBase returns a temporary directory with every symlink resolved.
//
// macOS puts temporary directories under /var/folders, which is itself a
// symlink to /private/var/folders. Git reports the resolved path — for a
// worktree, for the top level, for everything — so a test that built an
// expected path out of t.TempDir() would be comparing the two spellings of one
// directory and would fail on macOS alone.
func tempBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// newRepo creates a repository with one commit on main, initialised for stk.
func newRepo(t *testing.T) *repo {
	t.Helper()
	base := tempBase(t)
	r := &repo{
		t:    t,
		Root: filepath.Join(base, "repo"),
		home: filepath.Join(base, "home"),
		bin:  filepath.Join(base, "bin"),
	}
	mkdirAll(t, r.Root, r.home, r.bin)
	r.gitAt(r.Root, "init", "-q", "-b", "main", ".")
	r.commit("README.md", "hello\n", "init")
	r.stk("--no-interactive", "init")
	return r
}

// newRepoWithRemote creates a bare origin and a clone initialised for stk.
func newRepoWithRemote(t *testing.T) *repo {
	t.Helper()
	base := tempBase(t)
	r := &repo{
		t:      t,
		Root:   filepath.Join(base, "repo"),
		Origin: filepath.Join(base, "origin.git"),
		home:   filepath.Join(base, "home"),
		bin:    filepath.Join(base, "bin"),
	}
	mkdirAll(t, r.Origin, r.home, r.bin)
	r.gitAt(r.Origin, "init", "-q", "-b", "main", "--bare", ".")
	r.runIn(base, "", "git", "clone", "-q", r.Origin, r.Root)
	r.commit("README.md", "hello\n", "init")
	r.git("push", "-q", "-u", "origin", "main")
	r.stk("--no-interactive", "init")
	return r
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// addWorktree creates a linked worktree checked out at branch.
func (r *repo) addWorktree(name, branch string) string {
	r.t.Helper()
	path := filepath.Join(filepath.Dir(r.Root), name)
	r.git("worktree", "add", "-q", path, branch)
	return path
}

// stackText normalises the rendered stack for comparison.
func (r *repo) stackText(args ...string) string {
	r.t.Helper()
	out := r.stk(append([]string{"stack"}, args...)...)
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return strings.Join(lines, "\n")
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}

func requireNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected output not to contain %q, got:\n%s", needle, haystack)
	}
}

func requireEqual[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}
