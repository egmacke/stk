package operations

import (
	"errors"
	"fmt"
	"strings"

	"stk/internal/forge"
	"stk/internal/git"
	"stk/internal/stack"
)

// SubmitOptions configures stk submit.
type SubmitOptions struct {
	// Pull opens a pull request for each branch stk was asked to submit.
	Pull bool
	// Draft opens those pull requests as drafts.
	Draft bool
	// NoPrompt takes the generated title and body instead of asking.
	NoPrompt bool
	// Stack submits the whole stack rather than one branch.
	Stack bool
}

// PullRequestText is how the command layer collects a title and body. The
// defaults are stk's own suggestions, which the user may accept unchanged.
type PullRequestText func(b *stack.Branch, defaultTitle, defaultBody string) (title, body string, err error)

// Submit pushes branches to the configured remote and, when asked, opens a
// pull request for each one against its stack parent.
//
// Nothing is rewritten and nothing is checked out, so a dirty working tree and
// branches held by other worktrees are both fine.
func Submit(env *Env, g *stack.Graph, target *stack.Branch, opts SubmitOptions) error {
	repo := env.Repo
	// A paused operation means the stack is mid-rewrite; publishing it then
	// would push commits the user is about to abandon.
	if err := requireNoOperation(env); err != nil {
		return err
	}
	remote := env.Cfg.Remote
	if remote == "" {
		return errors.New("no default remote is configured\n\nRecord one with:\n\n    stk init --remote <name>")
	}
	if !repo.RemoteExists(remote) {
		return fmt.Errorf("remote %q is not configured", remote)
	}

	plan, wanted, err := submitPlan(g, target, opts)
	if err != nil {
		return err
	}

	var gh *forge.GH
	if opts.Pull {
		if gh, err = openForge(env, remote); err != nil {
			return err
		}
	}

	pushed := 0
	opened := 0
	for _, b := range plan {
		outcome, err := pushBranch(env, remote, b)
		if err != nil {
			return err
		}
		if outcome != git.PushCurrent {
			pushed++
		}
		if !opts.Pull || !wanted[b.ID] {
			continue
		}
		created, err := ensurePullRequest(env, gh, g, b, opts)
		if err != nil {
			return err
		}
		if created {
			opened++
		}
	}

	env.Out.Printf("")
	env.Out.Printf("%s", submitSummary(env, len(plan), pushed, opened, opts))
	return nil
}

// submitPlan returns the branches to push, parents before children, and the
// subset that should get a pull request.
//
// Ancestors are pushed but not proposed: a pull request needs its base branch
// to exist on the remote, and pushing the chain below the branch is the price
// of that. Proposing those ancestors as well is a different request, spelled
// --stack.
func submitPlan(g *stack.Graph, target *stack.Branch, opts SubmitOptions) ([]*stack.Branch, map[string]bool, error) {
	if target == nil {
		return nil, nil, errNoCurrentBranch
	}
	// Publishing is never repository-wide by accident: from trunk, "the whole
	// stack" would mean every branch in the repository.
	if target.IsTrunk {
		return nil, nil, fmt.Errorf("%s is the trunk branch; check out or name a branch stacked on it", target.Name)
	}
	if opts.Stack {
		if !target.Tracked {
			return nil, nil, fmt.Errorf(
				"branch %q is not tracked by stk, so it is not part of a stack\n\nSubmit it alone:\n\n    stk submit %s",
				target.Name, target.Name)
		}
		plan, err := PlanBranches(g, target, ScopeStack)
		if err != nil {
			return nil, nil, err
		}
		wanted := map[string]bool{}
		for _, b := range plan {
			wanted[b.ID] = true
		}
		return plan, wanted, nil
	}

	if opts.Pull && !target.Tracked {
		return nil, nil, fmt.Errorf(
			"branch %q is not tracked by stk, so stk cannot tell what to base a pull request on\n\nTrack it first:\n\n    stk track %s --parent <branch>",
			target.Name, target.Name)
	}
	// Deepest ancestor first, so every base exists before it is needed.
	var plan []*stack.Branch
	for p := target.Parent; p != nil && !p.IsTrunk; p = p.Parent {
		plan = append([]*stack.Branch{p}, plan...)
	}
	plan = append(plan, target)
	return plan, map[string]bool{target.ID: true}, nil
}

// openForge prepares the GitHub CLI for the repository behind the remote.
func openForge(env *Env, remote string) (*forge.GH, error) {
	url := env.Repo.RemoteURL(remote)
	repo, ok := forge.ParseRepo(url)
	if !ok {
		return nil, fmt.Errorf("cannot read %q as a repository URL, so stk cannot open a pull request\n\nPush without one:\n\n    stk submit", url)
	}
	if !repo.IsGitHub() {
		return nil, fmt.Errorf(
			"stk opens pull requests through the GitHub CLI, and %s is not a GitHub host\n\nPush without one:\n\n    stk submit",
			repo.Host)
	}
	gh := &forge.GH{Repo: repo, Dir: env.Repo.Root, Verbose: env.Repo.R.Verbose, Log: env.Repo.R.Log}
	if err := gh.Available(); err != nil {
		return nil, err
	}
	return gh, nil
}

// pushBranch sends one branch to the remote, replacing a diverged remote
// branch only under a lease.
func pushBranch(env *Env, remote string, b *stack.Branch) (git.PushOutcome, error) {
	repo := env.Repo
	if b.SHA == "" {
		return git.PushCurrent, fmt.Errorf("branch %q has no commits", b.Name)
	}
	remoteSHA, exists := repo.RemoteBranchSHA(remote, b.Name)
	lease := ""
	outcome := git.PushCreated
	switch {
	case !exists:
		outcome = git.PushCreated
	case remoteSHA == b.SHA:
		if b.HasUpstream() {
			env.Out.Skip("%s is already on %s", b.Name, remote)
			return git.PushCurrent, nil
		}
		// The commit is there but the branch has no upstream, so push anyway
		// to record one.
		outcome = git.PushUpdated
	case repo.IsAncestor(remoteSHA, b.SHA):
		outcome = git.PushUpdated
	default:
		// Rewritten by a restack, most likely. The lease is what keeps this
		// from overwriting someone else's work.
		outcome = git.PushForced
		lease = remoteSHA
	}

	if env.DryRun {
		env.Out.Printf("(dry-run) would push %s to %s (%s)", b.Name, remote, outcome)
		return outcome, nil
	}

	res := repo.Push(remote, b.Name, lease, !b.HasUpstream())
	if !res.OK() {
		echoGit(env, res)
		if lease != "" {
			return outcome, fmt.Errorf(
				"pushing %s was refused\n\n"+
					"%s/%s has moved since stk last saw it, so the force-with-lease was\n"+
					"declined and nothing was overwritten. Fetch and restack, then submit again:\n\n"+
					"    stk sync",
				b.Name, remote, b.Name)
		}
		return outcome, fmt.Errorf("pushing %s to %s failed", b.Name, remote)
	}
	env.Out.OK("Pushed %s to %s (%s)", b.Name, remote, outcome)
	return outcome, nil
}

// ensurePullRequest opens a pull request for a branch unless one is already
// open, which stk never edits: someone may have rewritten the description in
// the browser.
func ensurePullRequest(env *Env, gh *forge.GH, g *stack.Graph, b *stack.Branch, opts SubmitOptions) (bool, error) {
	if b.IsTrunk {
		return false, nil
	}
	base := g.Trunk.Name
	if b.Parent != nil && !b.Parent.IsTrunk {
		base = b.Parent.Name
	}
	if b.Base != "" && env.Repo.CountCommits(b.Base, b.SHA) == 0 {
		env.Out.Skip("%s adds no commits to %s; no pull request opened", b.Name, base)
		return false, nil
	}

	if !env.DryRun {
		existing, err := gh.OpenPullRequest(b.Name)
		if err != nil {
			return false, err
		}
		if existing != nil {
			env.Out.OK("Pull request %s is already open for %s", existing, b.Name)
			env.Out.Printf("    %s", existing.URL)
			return false, nil
		}
	}

	title, body, err := pullRequestText(env, b, base, opts)
	if err != nil {
		return false, err
	}

	if env.DryRun {
		env.Out.Printf("(dry-run) would open a pull request for %s onto %s", b.Name, base)
		env.Out.Printf("    title: %s", title)
		// Counted, because the summary of a dry run is written in the
		// conditional too.
		return true, nil
	}
	pr, err := gh.CreatePullRequest(forge.CreateOptions{
		Head:  b.Name,
		Base:  base,
		Title: title,
		Body:  body,
		Draft: opts.Draft,
	})
	if err != nil {
		return false, err
	}
	kind := "Opened pull request"
	if opts.Draft {
		kind = "Opened draft pull request"
	}
	if pr.Number > 0 {
		env.Out.OK("%s %s for %s onto %s", kind, pr, b.Name, base)
	} else {
		env.Out.OK("%s for %s onto %s", kind, b.Name, base)
	}
	if pr.URL != "" {
		env.Out.Printf("    %s", pr.URL)
	}
	return true, nil
}

// pullRequestText settles on the title and body: generated outright with
// --no-prompt, otherwise offered to the user to accept or replace.
func pullRequestText(env *Env, b *stack.Branch, base string, opts SubmitOptions) (string, string, error) {
	subjects := env.Repo.CommitSubjects(b.Base, b.SHA)
	body := bulletBody(subjects)
	if opts.NoPrompt {
		return b.Name, body, nil
	}
	if env.AskPullRequest == nil {
		return "", "", fmt.Errorf(
			"a title and body are needed for the pull request for %s\n\nGenerate them instead:\n\n    stk submit --pull --no-prompt",
			b.Name)
	}
	// The first commit's subject usually says it better than the branch name,
	// and it is only an offer.
	title := b.Name
	if len(subjects) > 0 {
		title = subjects[0]
	}
	return env.AskPullRequest(b, title, body)
}

// bulletBody lists the commits a branch adds, one per line.
func bulletBody(subjects []string) string {
	var b strings.Builder
	for _, s := range subjects {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return strings.TrimRight(b.String(), "\n")
}

// submitSummary closes the run. A dry run reports in the conditional, because
// it has published nothing.
func submitSummary(env *Env, planned, pushed, opened int, opts SubmitOptions) string {
	pushedVerb, openedVerb := "pushed", "opened"
	if env.DryRun {
		pushedVerb, openedVerb = "would be pushed", "would be opened"
	}
	var parts []string
	if pushed == 0 {
		parts = append(parts, "Nothing to push")
	} else {
		parts = append(parts, fmt.Sprintf("%d branch(es) %s", pushed, pushedVerb))
	}
	if opts.Pull && opened > 0 {
		parts = append(parts, fmt.Sprintf("%d pull request(s) %s", opened, openedVerb))
	}
	if planned > pushed && pushed > 0 {
		parts = append(parts, fmt.Sprintf("%d already up to date", planned-pushed))
	}
	if env.DryRun {
		parts = append(parts, "nothing has been published")
	}
	return strings.Join(parts, ", ") + "."
}
