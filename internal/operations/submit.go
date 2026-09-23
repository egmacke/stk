package operations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egmacke/stk/internal/config"
	"github.com/egmacke/stk/internal/forge"
	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// SubmitOptions configures stk submit.
type SubmitOptions struct {
	// Pull opens a pull request for each branch stk was asked to submit.
	Pull bool
	// Drafts says which of the pull requests this run opens are drafts.
	Drafts DraftChoice
	// NoPrompt takes the generated title and body instead of asking.
	NoPrompt bool
	// Stack submits the whole stack rather than one branch.
	Stack bool
	// NoComment leaves the stack comment on each pull request alone.
	NoComment bool
	// UpdateOnly refreshes the pull requests that already exist and never
	// opens one, so a restacked stack can be republished without proposing
	// work that is not ready to be looked at.
	UpdateOnly bool
	// NoLink leaves the stack on GitHub alone when stk.githubStacks is on.
	NoLink bool
	// Force replaces a diverged remote branch whatever it holds, instead of
	// taking a lease on the commit stk last published there. It is the only
	// way past a lease that keeps being declined, and it can overwrite work
	// stk has never seen.
	Force bool
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
		if env.Cfg.GitHubStacks && !opts.NoLink && target.Tracked {
			if err := requireStackExtension(gh); err != nil {
				return err
			}
		}
	}

	prs := newPullRequestCache()
	drafted := 0
	drafts := map[string]bool{}
	if opts.Pull {
		// What is already open decides everything that follows, and it is
		// read before the first push: a question about the whole stack should
		// not be asked halfway through publishing it, and a run that cannot
		// answer one should fail before it has published anything.
		missing, err := missingPullRequests(gh, plan, wanted, prs)
		if err != nil {
			return err
		}
		if len(missing) > 0 && !opts.UpdateOnly && !opts.NoPrompt && env.AskPullRequest == nil {
			return fmt.Errorf(
				"%s needs a title and body for its pull request, and stk cannot ask\n\n"+
					"Generate them instead:\n\n    stk submit --pull --no-prompt",
				missing[0].Name)
		}
		if opts.UpdateOnly {
			// Nothing will be created, so there is no draft state to decide.
			missing = nil
		}
		if drafts, err = resolveDrafts(env, g, plan, missing, opts); err != nil {
			return err
		}
	}
	pushed := 0
	linked := 0
	opened := 0
	refreshed := 0
	retargeted := 0
	for _, b := range plan {
		outcome, err := pushBranch(env, remote, b, opts.Force)
		if err != nil {
			return err
		}
		moved := false
		switch outcome {
		case git.PushCurrent:
			// Nothing to report beyond the line pushBranch printed.
		case git.PushLinked:
			linked++
		default:
			pushed++
			moved = true
		}
		if !opts.Pull || !wanted[b.ID] {
			continue
		}
		created, existed, moved2, err := ensurePullRequest(env, gh, g, b, opts, prs, drafts[b.ID], moved)
		if err != nil {
			return err
		}
		if moved2 {
			retargeted++
		}
		switch {
		case created:
			opened++
			if drafts[b.ID] {
				drafted++
			}
		case existed && moved:
			refreshed++
		}
	}

	if opts.Pull {
		// What this run learned about the pull requests goes into gh stack's
		// tracking, so gh stack view shows them without a sync of its own.
		mirrorGHStack(env, ghSyncOptions{PullRequests: prs.byBranch})
	}

	commented := 0
	stacked := false
	if opts.Pull && target.Tracked {
		// Last, so the record describes the stack as it now stands rather
		// than as it was when the run started. GitHub itself shows a linked
		// stack on every one of its pull requests, so with stk.githubStacks on
		// the comment would only say the same thing twice.
		switch {
		case env.Cfg.GitHubStacks:
			if !opts.NoLink {
				stacked, err = linkGitHubStack(env, gh, target, prs)
			}
		case !opts.NoComment:
			commented, err = syncStackComments(env, gh, g, target, prs)
		}
		if err != nil {
			return err
		}
	}

	env.Out.Printf("")
	if drafted > 0 && !env.DryRun {
		env.Out.Printf("Mark a draft ready for review with stk ready, or the whole stack with")
		env.Out.Printf("stk ready --stack.")
		env.Out.Printf("")
	}
	env.Out.Printf("%s", submitSummary(env, submitCounts{
		planned:    len(plan),
		pushed:     pushed,
		linked:     linked,
		opened:     opened,
		commented:  commented,
		refreshed:  refreshed,
		retargeted: retargeted,
		stacked:    stacked,
	}, opts))
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
	// Deepest ancestor first, so every base exists before it is needed. With
	// --update nothing is opened, so nothing needs a base and no branch the
	// user did not name gets published.
	var plan []*stack.Branch
	if !opts.UpdateOnly {
		for p := target.Parent; p != nil && !p.IsTrunk; p = p.Parent {
			plan = append([]*stack.Branch{p}, plan...)
		}
	}
	plan = append(plan, target)
	return plan, map[string]bool{target.ID: true}, nil
}

// openForge prepares the GitHub CLI for the repository behind the remote.
func openForge(env *Env, remote string) (*forge.GH, error) {
	url := env.Repo.RemoteURL(remote)
	repo, ok := forge.ParseRepo(url)
	if !ok {
		return nil, fmt.Errorf(
			"cannot read %q as a repository URL, so stk cannot reach a pull request\n\n"+
				"Pull requests need a GitHub remote; pushing and restacking do not",
			url)
	}
	if !repo.IsGitHub() {
		return nil, fmt.Errorf(
			"stk works with pull requests through the GitHub CLI, and %s is not a GitHub host\n\n"+
				"Pull requests need a GitHub remote; pushing and restacking do not",
			repo.Host)
	}
	gh := &forge.GH{Repo: repo, Dir: env.Repo.Root, Verbose: env.Repo.R.Verbose, Log: env.Repo.R.Log}
	if err := gh.Available(); err != nil {
		return nil, err
	}
	// Checked before the first push, not at the first pull request: a run that
	// published a stack of branches and only then found it could not propose
	// them would be a mess to unpick.
	if err := gh.Authenticated(); err != nil {
		return nil, err
	}
	return gh, nil
}

// pushBranch sends one branch to the remote, replacing a diverged remote
// branch only under a lease unless force says otherwise.
func pushBranch(env *Env, remote string, b *stack.Branch, force bool) (git.PushOutcome, error) {
	repo := env.Repo
	if b.SHA == "" {
		return git.PushCurrent, fmt.Errorf("branch %q has no commits", b.Name)
	}
	remoteSHA, exists := repo.RemoteBranchSHA(remote, b.Name)
	lease := ""
	forcing := false
	outcome := git.PushCreated
	switch {
	case !exists:
		outcome = git.PushCreated
	case remoteSHA == b.SHA:
		if b.HasUpstream() {
			env.Out.Skip("%s is already on %s", output.BranchName(b.Name), remote)
			return git.PushCurrent, nil
		}
		// The commit is published already and only the local upstream link is
		// missing, which costs no round trip to record.
		if env.DryRun {
			env.Out.Dry("would record %s/%s as the upstream of %s", remote, b.Name, output.BranchName(b.Name))
			return git.PushLinked, nil
		}
		if err := repo.SetUpstream(b.Name, remote); err != nil {
			return git.PushLinked, err
		}
		env.Out.OK("Recorded %s/%s as the upstream of %s", remote, b.Name, output.BranchName(b.Name))
		return git.PushLinked, nil
	case repo.IsAncestor(remoteSHA, b.SHA):
		outcome = git.PushUpdated
	case force:
		// Only a diverged branch reaches here, so --force changes nothing
		// about the cases above: it replaces the remote branch whatever it
		// holds, which is the one way past a lease that keeps being declined.
		outcome, forcing = git.PushForcedNoLease, true
	default:
		// Rewritten by a restack, most likely. The lease is what keeps this
		// from overwriting someone else's work.
		outcome = git.PushForced
		lease = remoteSHA
	}

	if env.DryRun {
		env.Out.Dry("would push %s to %s (%s)", output.BranchName(b.Name), remote, outcome)
		return outcome, nil
	}

	res := repo.Push(remote, b.Name, lease, forcing, !b.HasUpstream())
	if !res.OK() {
		echoGit(env, res)
		if lease != "" {
			return outcome, fmt.Errorf(
				"pushing %s was refused\n\n"+
					"%s/%s has moved since stk last saw it, so the force-with-lease was\n"+
					"declined and nothing was overwritten. Fetch and restack, then submit again:\n\n"+
					"    stk sync\n\n"+
					"Or replace it with what you have, whatever it holds:\n\n"+
					"    stk submit --force",
				b.Name, remote, b.Name)
		}
		return outcome, fmt.Errorf("pushing %s to %s failed", b.Name, remote)
	}
	env.Out.OK("Pushed %s to %s (%s)", output.BranchName(b.Name), remote, outcome)
	return outcome, nil
}

// ensurePullRequest opens a pull request for a branch unless one is already
// open, and reports whether it created one and whether one was already there.
//
// An open pull request is never edited: someone may have rewritten the
// description in the browser.
func ensurePullRequest(env *Env, gh *forge.GH, g *stack.Graph, b *stack.Branch, opts SubmitOptions, prs *pullRequestCache, draft, moved bool) (created, existed, retargeted bool, err error) {
	if b.IsTrunk {
		return false, false, false, nil
	}
	base := g.Trunk.Name
	if b.Parent != nil && !b.Parent.IsTrunk {
		base = b.Parent.Name
	}
	if !env.DryRun {
		existing, err := prs.open(gh, b.Name)
		if err != nil {
			return false, false, false, err
		}
		if existing != nil {
			// The base is the one thing stk maintains on a pull request it did
			// not open. A re-parent, a fold or a merged branch below leaves it
			// pointing at the wrong branch, and the diff then shows commits
			// that belong to another review.
			if existing.Base != "" && existing.Base != base {
				// A failure here is loud but not fatal: the branch is pushed
				// either way, and abandoning the rest of the stack half
				// published would be the worse outcome.
				if err := gh.RetargetPullRequest(existing.Number, base); err != nil {
					env.Out.Fail("could not retarget %s onto %s: %v", existing, base, err)
				} else {
					env.Out.OK("Retargeted %s from %s onto %s", existing, existing.Base, base)
					retargeted = true
				}
			}
			// The push is what brought it up to date; say which happened.
			what := "is already open for"
			if moved {
				what = "was refreshed for"
			}
			env.Out.OK("Pull request %s %s %s", existing, what, output.BranchName(b.Name))
			env.Out.Printf("    %s", output.Dim(existing.URL))
			return false, true, retargeted, nil
		}
	}
	if opts.UpdateOnly {
		env.Out.Skip("%s has no pull request; --update opens none", output.BranchName(b.Name))
		return false, false, false, nil
	}
	if !env.DryRun {
		// No open pull request is not the same as never proposed: a merged one
		// means the branch has landed, and opening a second would propose the
		// same change twice.
		if latest, err := gh.LatestPullRequest(b.Name); err == nil && latest != nil {
			if latest.IsMerged() {
				env.Out.Skip("%s was merged as %s; not opening another", output.BranchName(b.Name), latest)
				env.Out.Printf("    Remove the branch with %s", output.Command("stk sync --cleanup"))
				return false, false, false, nil
			}
			if latest.State == "CLOSED" {
				env.Out.Printf("%s was closed for %s; opening a new one", latest, b.Name)
			}
		}
	}
	if b.Base != "" && env.Repo.CountCommits(b.Base, b.SHA) == 0 {
		env.Out.Skip("%s adds no commits to %s; no pull request opened", output.BranchName(b.Name), output.BranchName(base))
		return false, false, false, nil
	}

	title, body, err := pullRequestText(env, b, base, opts)
	if err != nil {
		return false, false, false, err
	}

	if env.DryRun {
		kind := "a pull request"
		if draft {
			kind = "a draft pull request"
		}
		env.Out.Dry("would open %s for %s onto %s", kind, output.BranchName(b.Name), output.BranchName(base))
		env.Out.Printf("    %s %s", output.Dim("title:"), title)
		// Counted, because the summary of a dry run is written in the
		// conditional too.
		return true, false, false, nil
	}
	pr, err := gh.CreatePullRequest(forge.CreateOptions{
		Head:  b.Name,
		Base:  base,
		Title: title,
		Body:  body,
		Draft: draft,
	})
	if err != nil {
		return false, false, false, err
	}
	kind := "Opened pull request"
	if draft {
		kind = "Opened draft pull request"
	}
	if pr.Number > 0 {
		env.Out.OK("%s %s for %s onto %s", kind, pr, output.BranchName(b.Name), output.BranchName(base))
	} else {
		env.Out.OK("%s for %s onto %s", kind, output.BranchName(b.Name), output.BranchName(base))
	}
	if pr.URL != "" {
		env.Out.Printf("    %s", output.Dim(pr.URL))
	}
	prs.record(b.Name, pr)
	return true, false, false, nil
}

// missingPullRequests lists the branches stk was asked to propose that have no
// open pull request yet, in stack order.
//
// A dry run asks too: it is the only way to say what a real run would open.
func missingPullRequests(gh *forge.GH, plan []*stack.Branch, wanted map[string]bool, prs *pullRequestCache) ([]*stack.Branch, error) {
	var missing []*stack.Branch
	for _, b := range plan {
		if !wanted[b.ID] {
			continue
		}
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return nil, err
		}
		if pr == nil {
			missing = append(missing, b)
		}
	}
	return missing, nil
}

// resolveDrafts works out which pull requests open as drafts.
//
// With no preference given, and more than one pull request to open, stk asks
// for the cut line: a stack is usually ready at the bottom and still being
// written at the top, so one question settles every branch.
func resolveDrafts(env *Env, g *stack.Graph, plan, missing []*stack.Branch, opts SubmitOptions) (map[string]bool, error) {
	if err := opts.Drafts.Validate(); err != nil {
		return nil, err
	}
	choice := opts.Drafts
	if choice.Empty() && !opts.NoPrompt && env.AskDraftCutLine != nil && !env.DryRun {
		// Only a real choice is worth a question.
		if len(missing) > 1 {
			cut, err := env.AskDraftCutLine(draftCandidates(g, missing))
			if err != nil {
				return nil, err
			}
			if cut != nil {
				choice = DraftChoice{From: aboveCut(missing, cut)}
				if choice.From == "" {
					// The top branch is ready, so nothing is a draft.
					return map[string]bool{}, nil
				}
			}
		}
	}
	return draftSet(g, plan, choice)
}

// aboveCut names the first branch above the chosen one, which is where drafts
// begin. It is empty when the choice was the topmost branch.
func aboveCut(ordered []*stack.Branch, cut *stack.Branch) string {
	if cut.IsTrunk {
		return ordered[0].Name
	}
	for i, b := range ordered {
		if b.ID == cut.ID {
			if i+1 < len(ordered) {
				return ordered[i+1].Name
			}
			return ""
		}
	}
	return ""
}

// pullRequestText settles on the title and body: generated outright with
// --no-prompt, otherwise offered to the user to accept or replace.
func pullRequestText(env *Env, b *stack.Branch, base string, opts SubmitOptions) (string, string, error) {
	subjects := env.Repo.CommitSubjects(b.Base, b.SHA)
	body := bulletBody(subjects)
	if opts.NoPrompt {
		return generatedTitle(env.Cfg.PRTitle, b, subjects), body, nil
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

// generatedTitle names a pull request stk opens without being able to ask.
//
// The branch name is the default because it is what the whole stack is keyed
// on and it survives the commits being rewritten; stk.prTitle = commit takes
// the first commit's subject instead, which usually reads better. A branch
// that adds no commit has no subject to take, so it falls back to the name.
func generatedTitle(src config.PRTitleSource, b *stack.Branch, subjects []string) string {
	if src == config.PRTitleCommit && len(subjects) > 0 {
		return subjects[0]
	}
	return b.Name
}

// bulletBody lists the commits a branch adds, one per line.
func bulletBody(subjects []string) string {
	var b strings.Builder
	for _, s := range subjects {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return strings.TrimRight(b.String(), "\n")
}

// submitCounts is what one submit run did.
type submitCounts struct {
	planned    int
	pushed     int
	linked     int
	opened     int
	refreshed  int
	retargeted int
	commented  int
	stacked    bool
}

// submitSummary closes the run. A dry run reports in the conditional, because
// it has published nothing.
func submitSummary(env *Env, c submitCounts, opts SubmitOptions) string {
	pushedVerb, openedVerb, linkedVerb := "pushed", "opened", "recorded"
	if env.DryRun {
		pushedVerb, openedVerb, linkedVerb = "would be pushed", "would be opened", "would be recorded"
	}
	var parts []string
	if c.pushed == 0 {
		parts = append(parts, "Nothing to push")
	} else {
		parts = append(parts, fmt.Sprintf("%d branch(es) %s", c.pushed, pushedVerb))
	}
	if c.linked > 0 {
		parts = append(parts, fmt.Sprintf("%d upstream(s) %s", c.linked, linkedVerb))
	}
	if opts.Pull && c.opened > 0 {
		parts = append(parts, fmt.Sprintf("%d pull request(s) %s", c.opened, openedVerb))
	}
	if opts.Pull && c.refreshed > 0 {
		parts = append(parts, fmt.Sprintf("%d pull request(s) refreshed", c.refreshed))
	}
	if c.retargeted > 0 {
		parts = append(parts, fmt.Sprintf("%d retargeted", c.retargeted))
	}
	if c.commented > 0 {
		what := "stack comment(s) written"
		if env.DryRun {
			what = "stack comment(s) would be written"
		}
		parts = append(parts, fmt.Sprintf("%d %s", c.commented, what))
	}
	if c.stacked {
		what := "linked as a stack on GitHub"
		if env.DryRun {
			what = "would be linked as a stack on GitHub"
		}
		parts = append(parts, what)
	}
	if accounted := c.pushed + c.linked; c.planned > accounted && accounted > 0 {
		parts = append(parts, fmt.Sprintf("%d already up to date", c.planned-accounted))
	}
	if env.DryRun {
		parts = append(parts, "nothing has been published")
	}
	return strings.Join(parts, ", ") + "."
}
