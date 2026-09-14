// Package forge is the only part of stk that knows a code-hosting service
// exists, and it reaches one through the GitHub CLI rather than an API of its
// own: gh already solves authentication, enterprise hosts and two-factor
// prompts, and stk has no business storing a token.
//
// Everything else in stk keeps working with no forge at all; only stk submit
// --pull comes here.
package forge

import (
	"fmt"
	"net/url"
	"strings"
)

// Repo names a repository on a GitHub host in the form gh understands:
// OWNER/NAME, or HOST/OWNER/NAME for anything but github.com.
type Repo struct {
	Host  string
	Owner string
	Name  string
}

// String renders the repository as gh's --repo argument.
func (r Repo) String() string {
	if r.Host == "" || r.Host == "github.com" {
		return r.Owner + "/" + r.Name
	}
	return r.Host + "/" + r.Owner + "/" + r.Name
}

// ParseRepo reads a git remote URL as a GitHub repository.
//
// ok is false for a URL stk cannot read that way, which includes every
// non-GitHub forge; the caller reports that pull requests are not available
// rather than guessing.
func ParseRepo(remoteURL string) (Repo, bool) {
	raw := strings.TrimSpace(remoteURL)
	if raw == "" {
		return Repo{}, false
	}
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return Repo{}, false
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, ":"):
		// scp-like syntax: git@github.com:owner/name.git
		hostPart, rest, _ := strings.Cut(raw, ":")
		if at := strings.LastIndex(hostPart, "@"); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		host, path = hostPart, rest
	default:
		return Repo{}, false
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, false
	}
	if host == "" {
		return Repo{}, false
	}
	return Repo{Host: host, Owner: owner, Name: name}, true
}

// IsGitHub reports whether the host looks like GitHub or GitHub Enterprise.
//
// Enterprise installations use arbitrary hostnames, so this cannot be
// certain; it is used only to phrase the error when gh is unlikely to help.
func (r Repo) IsGitHub() bool {
	return r.Host == "github.com" || strings.Contains(r.Host, "github")
}

// PullRequest is the part of a pull request stk reports on.
type PullRequest struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	IsDraft bool   `json:"isDraft"`
	State   string `json:"state"`
	// Base is the branch the pull request is opened against, which stk keeps
	// pointing at the stack parent.
	Base string `json:"baseRefName"`
}

// IsMerged reports whether the pull request has already landed.
func (pr *PullRequest) IsMerged() bool { return pr.State == "MERGED" }

func (pr *PullRequest) String() string { return fmt.Sprintf("#%d", pr.Number) }
