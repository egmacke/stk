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

// linkGitHubStack records the shape of the stack on GitHub itself, through
// gh stack link, in place of the stack comment.
//
// Only pull requests are linked, bottom first, and only up to the first branch
// without one: a link across that gap would make gh retarget the pull request
// above it onto the branch below, which changes what its reviewer is looking
// at. A stack of one pull request is not linked, as it is not commented on.
func linkGitHubStack(env *Env, gh *forge.GH, target *stack.Branch, prs *pullRequestCache) (bool, error) {
	chain := linkChain(target)
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
	env.Out.OK("Linked %s as a stack on GitHub", list)
	return true, nil
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
