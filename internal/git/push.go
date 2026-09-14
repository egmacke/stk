package git

import (
	"fmt"
	"strings"
)

// PushOutcome says what a push did to the remote branch.
type PushOutcome string

const (
	// PushCreated means the branch did not exist on the remote.
	PushCreated PushOutcome = "created"
	// PushUpdated means the remote branch was fast-forwarded.
	PushUpdated PushOutcome = "updated"
	// PushForced means the remote branch had diverged and was replaced.
	PushForced PushOutcome = "forced"
	// PushCurrent means the remote already had this commit.
	PushCurrent PushOutcome = "current"
	// PushLinked means nothing was sent: the commit was already published and
	// only the local upstream link was missing.
	PushLinked PushOutcome = "upstream recorded"
)

// RemoteBranchSHA returns the commit the remote-tracking ref points at, and
// whether stk knows of the branch on that remote at all.
//
// It reads the local remote-tracking ref rather than asking the remote, so it
// is as current as the last fetch or push.
func (repo *Repo) RemoteBranchSHA(remote, branch string) (string, bool) {
	sha, err := repo.RevParse("refs/remotes/" + remote + "/" + branch)
	if err != nil {
		return "", false
	}
	return sha, true
}

// Push sends one branch to a remote under its own name.
//
// lease, when set, is the remote commit the caller believes it is replacing:
// git refuses the push if the remote has moved since, so a diverged branch is
// replaced only when nobody else has touched it. setUpstream records the
// remote branch as this branch's upstream.
//
// The branch is named explicitly, so pushing never depends on what this
// worktree has checked out.
func (repo *Repo) Push(remote, branch, lease string, setUpstream bool) Result {
	args := []string{"push"}
	if lease != "" {
		args = append(args, fmt.Sprintf("--force-with-lease=%s:%s", branch, lease))
	}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, branch)
	// Captured, with the real stdin attached: credential helpers must still
	// reach the terminal while stk keeps control of what is printed.
	return repo.R.Capture(args...)
}

// SetUpstream records a remote branch of the same name as this branch's
// upstream, without contacting the remote.
func (repo *Repo) SetUpstream(branch, remote string) error {
	return repo.R.Mutate("branch", "--set-upstream-to="+remote+"/"+branch, branch).Error()
}

// RemoteURL returns the URL configured for a remote.
//
// The configured value is read directly rather than through "git remote
// get-url", which applies url.*.insteadOf rewriting. That rewriting redirects
// transport — a mirror, a proxy, ssh in place of https — and says nothing about
// which repository this is. The GitHub CLI reads the configured value too, so
// reading it here keeps stk naming the same repository gh would.
func (repo *Repo) RemoteURL(remote string) string {
	res := repo.R.Run("config", "--get", "remote."+remote+".url")
	if !res.OK() {
		return ""
	}
	return res.Out()
}

// CommitSubjects lists the subject lines of the commits in from..to, oldest
// first.
func (repo *Repo) CommitSubjects(from, to string) []string {
	res := repo.R.Run("log", "--reverse", "--no-merges", "--format=%s", from+".."+to)
	if !res.OK() {
		return nil
	}
	return res.Lines()
}

// Commit is one commit, reduced to what stk shows a user.
type Commit struct {
	SHA     string
	Subject string
}

// Commits lists the commits in from..to, oldest first.
func (repo *Repo) Commits(from, to string) []Commit {
	res := repo.R.Run("log", "--reverse", "--format=%H%x00%s", from+".."+to)
	if !res.OK() {
		return nil
	}
	var out []Commit
	for _, line := range res.Lines() {
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		out = append(out, Commit{SHA: sha, Subject: subject})
	}
	return out
}

// CommitBody returns the message body, without the subject, of one commit.
func (repo *Repo) CommitBody(rev string) string {
	res := repo.R.Run("log", "--max-count=1", "--format=%b", rev)
	if !res.OK() {
		return ""
	}
	return res.Out()
}

// DeleteRemoteBranch removes a branch from a remote.
//
// A remote branch that is already gone is not an error: the caller asked for
// it to be absent, and it is.
func (repo *Repo) DeleteRemoteBranch(remote, branch string) Result {
	return repo.R.Capture("push", remote, "--delete", branch)
}

// RemoteRefMissing reports whether a delete was refused only because the
// remote branch was not there in the first place.
func RemoteRefMissing(res Result) bool {
	return strings.Contains(res.Stderr+res.Stdout, "remote ref does not exist")
}

// RenameRemoteBranch publishes a branch under a new name and removes the old
// one, in a single push so the two halves cannot drift apart.
//
// The new name is recorded as the branch's upstream; the delete refspec has no
// local counterpart, so it leaves no configuration behind.
func (repo *Repo) RenameRemoteBranch(remote, oldName, newName string) Result {
	return repo.R.Capture("push", "--set-upstream", remote, newName, ":"+oldName)
}
