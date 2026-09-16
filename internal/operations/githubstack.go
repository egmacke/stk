package operations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egmacke/stk/internal/config"
	"github.com/egmacke/stk/internal/forge"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// requireStackExtension refuses to start a run that will need gh stack link
// before anything has been pushed, for the same reason the login is checked
// first: a stack published and then found impossible to link is a mess to
// unpick by hand.
func requireStackExtension(gh *forge.GH) error {
	err := gh.HasStackExtension()
	if errors.Is(err, forge.ErrNoStackExtension) {
		return fmt.Errorf("%w\n\nOr go back to the stack comment:\n\n    git config %s false",
			err, config.KeyGitHubStacks)
	}
	return err
}

// linkChain returns the run of branches a GitHub stack can hold: from the
// bottom of the stack up through target, and above target only while each
// branch has exactly one child.
//
// GitHub stacks are strictly linear, so where the graph forks the chain ends
// rather than picking a side; the branches on the other side belong to a stack
// of their own.
func linkChain(target *stack.Branch) []*stack.Branch {
	var chain []*stack.Branch
	for b := target; b != nil && !b.IsTrunk; b = b.Parent {
		chain = append([]*stack.Branch{b}, chain...)
	}
	for b := target; len(b.Children) == 1; {
		b = b.Children[0]
		chain = append(chain, b)
	}
	return chain
}

// linearChains decomposes the graph into the linear runs a GitHub stack can
// hold, parents before children.
//
// From each branch on trunk a run goes upward while every branch has exactly
// one child. Where a branch forks the run ends there and each child begins a
// run of its own, because a GitHub stack is strictly linear: this is the one
// decomposition in which every branch is in exactly one run and every run's
// parent relationships are the graph's.
func linearChains(g *stack.Graph) [][]*stack.Branch {
	var out [][]*stack.Branch
	var walk func(start *stack.Branch)
	walk = func(start *stack.Branch) {
		var chain []*stack.Branch
		cur := start
		for {
			chain = append(chain, cur)
			if len(cur.Children) != 1 {
				break
			}
			cur = cur.Children[0]
		}
		out = append(out, chain)
		if len(cur.Children) > 1 {
			for _, c := range cur.Children {
				walk(c)
			}
		}
	}
	for _, root := range g.Roots() {
		walk(root)
	}
	return out
}

// unforkedChains lists the stacks stk can settle on GitHub without being told
// which one is meant: those running from trunk to a tip with no fork anywhere
// along the way.
//
// Where the graph forks there is no single answer. gh stack link is additive,
// so linking each side in turn would gather both into one stack that is the
// shape of neither, and stk submit — which is given the branch — is where that
// choice belongs.
func unforkedChains(g *stack.Graph) [][]*stack.Branch {
	var out [][]*stack.Branch
	for _, chain := range linearChains(g) {
		bottom, top := chain[0], chain[len(chain)-1]
		if len(top.Children) == 0 && bottom.Parent != nil && bottom.Parent.IsTrunk {
			out = append(out, chain)
		}
	}
	return out
}

// linkGitHubStack records the shape of the stack through target on GitHub
// itself, through gh stack link, in place of the stack comment.
func linkGitHubStack(env *Env, gh *forge.GH, target *stack.Branch, prs *pullRequestCache) (bool, error) {
	return linkPullRequestChain(env, gh, linkChain(target), prs, "Linked %s as a stack on GitHub")
}

// linkPullRequestChain links the pull requests of one linear run of branches
// as a stack on GitHub, and reports whether it had two to link.
//
// Only pull requests are linked, bottom first, and only up to the first branch
// without one: a link across that gap would make gh retarget the pull request
// above it onto the branch below, which changes what its reviewer is looking
// at. A stack of one pull request is not linked, as it is not commented on.
//
// done says how the result is announced. stk submit has just built the stack
// and says it linked it; stk sync is reconciling one that may well have been
// linked already, and says only what is true either way.
func linkPullRequestChain(env *Env, gh *forge.GH, chain []*stack.Branch, prs *pullRequestCache, done string) (bool, error) {
	var numbers []int
	var gap *stack.Branch
	for i, b := range chain {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return false, err
		}
		if pr != nil {
			numbers = append(numbers, pr.Number)
			continue
		}
		gap = b
		// Only worth mentioning when something above the gap is left out.
		for _, above := range chain[i+1:] {
			pr, err := prs.open(gh, above.Name)
			if err != nil {
				return false, err
			}
			if pr != nil {
				env.Out.Skip("%s has no pull request, so the stack on GitHub stops below it", output.BranchName(gap.Name))
				break
			}
		}
		break
	}
	if len(numbers) < 2 {
		return false, nil
	}

	list := prNumbers(numbers)
	if env.DryRun {
		env.Out.Dry("would link %s as a stack on GitHub", list)
		return true, nil
	}
	if err := gh.LinkStack(env.Cfg.Trunk, env.Cfg.Remote, numbers); err != nil {
		var ce *forge.CommandError
		if errors.As(err, &ce) && ce.ExitCode == forge.ExitStacksUnavailable {
			return false, fmt.Errorf(
				"stacked pull requests are not available for this repository\n\n%s\n\n"+
					"The branches were pushed and the pull requests opened; only the link\n"+
					"between them is missing. Go back to the stack comment with:\n\n    git config %s false",
				indent(ce.Message), config.KeyGitHubStacks)
		}
		return false, err
	}
	env.Out.OK(done, list)
	return true, nil
}

// syncGitHubStacks asks GitHub about the stacks stk tracks and brings them
// back into step, by linking each run of pull requests as a stack there.
//
// gh stack's local tracking records only what has happened on this machine, so
// a stack linked, extended or renumbered somewhere else — in the browser, by a
// colleague, or from another checkout — is invisible to stk until something
// asks GitHub itself. Linking is that question, and the same one stk submit
// --pull ends with: gh stack link is given pull request numbers rather than
// branch names, so it pushes nothing and opens nothing, a run that is already
// a stack on GitHub is left as it stands, and either way gh stack's tracking
// comes back to what GitHub holds.
//
// target names one stack to settle; nil means every stack in the graph.
// Nothing here is fatal. The rest of the command has already done its work,
// and a forge stk cannot reach only means the check waits for the next run.
func syncGitHubStacks(env *Env, g *stack.Graph, target *stack.Branch) {
	if !env.Cfg.GitHubStacks {
		return
	}
	remote := env.Cfg.Remote
	if remote == "" || !env.Repo.RemoteExists(remote) {
		return
	}
	var chains [][]*stack.Branch
	switch {
	case target == nil:
		chains = unforkedChains(g)
	case target.Tracked && !target.HasProblem():
		chains = [][]*stack.Branch{linkChain(target)}
	}
	// A run of one branch can never hold the two pull requests a stack needs,
	// so it is not worth a round trip to find that out.
	var worth [][]*stack.Branch
	for _, chain := range chains {
		if len(chain) > 1 {
			worth = append(worth, chain)
		}
	}
	if len(worth) == 0 {
		return
	}
	if env.DryRun {
		env.Out.Dry("would check the stacks on GitHub and link any that are not linked there yet")
		return
	}
	gh, err := openForge(env, remote)
	if err != nil {
		env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
		return
	}
	if err := requireStackExtension(gh); err != nil {
		env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
		return
	}
	prs := newPullRequestCache()
	for _, chain := range worth {
		if _, err := linkPullRequestChain(env, gh, chain, prs, "%s are a stack on GitHub"); err != nil {
			// One repository-wide answer: a link refused for one stack is
			// refused for every other, and the reason is worth reading once.
			env.Out.Warnf("Stacks on GitHub not updated: %v", firstLine(err))
			return
		}
	}
}

// prNumbers writes pull request numbers the way GitHub does: #1, #2, #3.
func prNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
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
