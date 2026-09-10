package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ErrNoCLI is returned when the GitHub CLI is not installed.
var ErrNoCLI = errors.New("the GitHub CLI (gh) is not installed")

// ErrNotAuthenticated is returned when gh holds no credentials for the host.
var ErrNotAuthenticated = errors.New("the GitHub CLI is not logged in")

// GH runs the GitHub CLI against one repository.
type GH struct {
	// Repo is passed to every call, so stk always acts on the repository
	// behind the configured remote rather than whatever gh would infer.
	Repo Repo
	// Dir is the working directory gh runs in.
	Dir string

	Verbose bool
	Log     io.Writer
}

// Available reports whether gh can be run at all.
func (g *GH) Available() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("%w\n\nInstall it from https://cli.github.com, then run:\n\n    gh auth login", ErrNoCLI)
	}
	return nil
}

// Authenticated reports whether gh holds credentials for this repository's
// host.
//
// Callers check it before publishing anything, so a run cannot push a stack of
// branches and only then discover it cannot open a pull request. gh's own
// message is relayed rather than paraphrased: it knows which of the several
// ways of logging in went wrong.
func (g *GH) Authenticated() error {
	host := g.host()
	if _, err := g.exec("auth", "status", "--hostname", host); err != nil {
		return fmt.Errorf("%w for %s\n\n%s\n\nLog in with:\n\n    gh auth login --hostname %s",
			ErrNotAuthenticated, host, indent(err.Error()), host)
	}
	return nil
}

func (g *GH) host() string {
	if g.Repo.Host == "" {
		return "github.com"
	}
	return g.Repo.Host
}

// indent offsets a relayed message so it reads as quoted rather than as stk's
// own words.
func indent(msg string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(msg), "\n") {
		out = append(out, "    "+line)
	}
	return strings.Join(out, "\n")
}

// run invokes "gh <group> <verb> --repo <repo> <args...>".
func (g *GH) run(group, verb string, args ...string) (string, error) {
	full := append([]string{group, verb, "--repo", g.Repo.String()}, args...)
	return g.exec(full...)
}

// exec runs gh verbatim, for the calls that take no --repo.
func (g *GH) exec(full ...string) (string, error) {
	if g.Verbose && g.Log != nil {
		fmt.Fprintf(g.Log, "+ gh %s\n", strings.Join(full, " "))
	}
	cmd := exec.Command("gh", full...)
	cmd.Dir = g.Dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// gh may need the terminal for a credential or two-factor prompt.
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("gh %s: %s", strings.Join(full, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// prFields are the pull request fields stk reads.
const prFields = "number,url,title,isDraft,state,baseRefName"

// OpenPullRequest returns the open pull request whose head is branch, or nil
// when there is none.
func (g *GH) OpenPullRequest(branch string) (*PullRequest, error) {
	return g.pullRequest(branch, "open")
}

// LatestPullRequest returns the most recent pull request for a branch
// whatever its state, so stk can tell "never proposed" from "already merged".
func (g *GH) LatestPullRequest(branch string) (*PullRequest, error) {
	return g.pullRequest(branch, "all")
}

func (g *GH) pullRequest(branch, state string) (*PullRequest, error) {
	out, err := g.run("pr", "list",
		"--head", branch,
		"--state", state,
		"--limit", "1",
		"--json", prFields)
	if err != nil {
		return nil, err
	}
	var prs []PullRequest
	if out == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("reading gh pr list output: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// RetargetPullRequest points an open pull request at another base branch.
//
// The base is the one thing stk maintains on a pull request it did not open:
// it is structural, not authored, and a stale base makes the diff show commits
// that belong to another review.
//
// This goes through the REST endpoint rather than "gh pr edit", which fetches
// organisation and team metadata over GraphQL and so demands a read:org scope
// that changing a base does not need.
func (g *GH) RetargetPullRequest(number int, base string) error {
	_, err := g.api("PATCH", g.apiPath("/pulls/%d", number), "-f", "base="+base)
	return err
}

// ClosePullRequest closes a pull request, saying why.
//
// The head branch is deliberately left on the remote: a closed pull request
// can be reopened, and stk does not delete branches it did not create.
func (g *GH) ClosePullRequest(number int, comment string) error {
	args := []string{fmt.Sprintf("%d", number)}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	_, err := g.run("pr", "close", args...)
	return err
}

// ReadyForReview takes a pull request out of draft, or with undo puts it back.
func (g *GH) ReadyForReview(number int, undo bool) error {
	args := []string{fmt.Sprintf("%d", number)}
	if undo {
		args = append(args, "--undo")
	}
	_, err := g.run("pr", "ready", args...)
	return err
}

// Comment is one comment on a pull request, reduced to what stk needs to find
// its own again and rewrite it.
type Comment struct {
	ID    int64  `json:"id"`
	Body  string `json:"body"`
	Login string `json:"login"`
}

// apiPath builds a REST path for this repository.
func (g *GH) apiPath(format string, args ...any) string {
	return fmt.Sprintf("/repos/%s/%s", g.Repo.Owner, g.Repo.Name) + fmt.Sprintf(format, args...)
}

// api runs gh api against the repository's host.
func (g *GH) api(method, path string, fields ...string) (string, error) {
	args := []string{"api"}
	if g.Repo.Host != "" && g.Repo.Host != "github.com" {
		args = append(args, "--hostname", g.Repo.Host)
	}
	if method != "GET" {
		args = append(args, "--method", method)
	}
	args = append(args, path)
	args = append(args, fields...)
	return g.exec(args...)
}

// Comments lists the comments on a pull request.
//
// The projection is done by gh, so stk depends on three documented fields and
// not on the shape of the whole payload, and --paginate means a comment does
// not hide on the second page of a busy pull request.
func (g *GH) Comments(number int) ([]Comment, error) {
	out, err := g.api("GET", g.apiPath("/issues/%d/comments", number),
		"--paginate", "--jq", `.[] | {id: .id, body: .body, login: .user.login}`)
	if err != nil {
		return nil, err
	}
	var comments []Comment
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var c Comment
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("reading gh api comment output: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

// AddComment posts a new comment on a pull request.
func (g *GH) AddComment(number int, body string) error {
	_, err := g.api("POST", g.apiPath("/issues/%d/comments", number), "-f", "body="+body)
	return err
}

// UpdateComment rewrites one existing comment, addressed by its own id so that
// no comment stk did not write can ever be overwritten.
func (g *GH) UpdateComment(id int64, body string) error {
	_, err := g.api("PATCH", g.apiPath("/issues/comments/%d", id), "-f", "body="+body)
	return err
}

// CreateOptions describes a pull request to open.
type CreateOptions struct {
	Head  string
	Base  string
	Title string
	Body  string
	Draft bool
}

// CreatePullRequest opens a pull request and returns it.
//
// gh prints the new pull request's URL, which carries its number; stk reads
// that rather than making a second call.
func (g *GH) CreatePullRequest(opts CreateOptions) (*PullRequest, error) {
	args := []string{
		"--head", opts.Head,
		"--base", opts.Base,
		"--title", opts.Title,
		"--body", opts.Body,
	}
	if opts.Draft {
		args = append(args, "--draft")
	}
	out, err := g.run("pr", "create", args...)
	if err != nil {
		return nil, err
	}
	pr := &PullRequest{
		URL:     lastURL(out),
		Title:   opts.Title,
		IsDraft: opts.Draft,
		State:   "OPEN",
	}
	pr.Number = numberFromURL(pr.URL)
	return pr, nil
}

// lastURL picks the pull request URL out of gh's output, which may carry a
// leading notice line.
func lastURL(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			out = line
		}
	}
	return strings.TrimSpace(out)
}

func numberFromURL(u string) int {
	idx := strings.LastIndex(u, "/")
	if idx < 0 {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(u[idx+1:], "%d", &n); err != nil {
		return 0
	}
	return n
}
