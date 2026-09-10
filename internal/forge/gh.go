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
//
// Authentication is deliberately not checked here: gh's own error names the
// host and the command to fix it, and stk should not paraphrase it.
func (g *GH) Available() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("%w\n\nInstall it from https://cli.github.com, then run:\n\n    gh auth login", ErrNoCLI)
	}
	return nil
}

// run invokes "gh <group> <verb> --repo <repo> <args...>".
func (g *GH) run(group, verb string, args ...string) (string, error) {
	full := append([]string{group, verb, "--repo", g.Repo.String()}, args...)
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

// OpenPullRequest returns the open pull request whose head is branch, or nil
// when there is none.
func (g *GH) OpenPullRequest(branch string) (*PullRequest, error) {
	out, err := g.run("pr", "list",
		"--head", branch,
		"--state", "open",
		"--limit", "1",
		"--json", "number,url,title,isDraft,state")
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
