package operations

import (
	"errors"
	"fmt"

	"stk/internal/stack"
)

// ReadyOptions configures stk ready.
type ReadyOptions struct {
	// Stack covers every branch of the stack rather than one branch.
	Stack bool
	// Undo puts the pull requests back into draft instead.
	Undo bool
}

// Ready takes pull requests out of draft, or with Undo puts them back.
//
// It is a separate command because stk submit deliberately never edits a pull
// request that is already open: draft state is a decision about review, and
// changing it is something the user asks for in its own right.
func Ready(env *Env, g *stack.Graph, target *stack.Branch, opts ReadyOptions) error {
	if err := requireNoOperation(env); err != nil {
		return err
	}
	remote := env.Cfg.Remote
	if remote == "" {
		return errors.New("no default remote is configured\n\nRecord one with:\n\n    stk init --remote <name>")
	}
	if target == nil {
		return errNoCurrentBranch
	}
	if target.IsTrunk {
		return fmt.Errorf("%s is the trunk branch and has no pull request", target.Name)
	}

	plan := []*stack.Branch{target}
	if opts.Stack {
		if !target.Tracked {
			return fmt.Errorf("branch %q is not tracked by stk, so it is not part of a stack", target.Name)
		}
		var err error
		if plan, err = PlanBranches(g, target, ScopeStack); err != nil {
			return err
		}
	}

	gh, err := openForge(env, remote)
	if err != nil {
		return err
	}
	prs := newPullRequestCache()

	changed := 0
	for _, b := range plan {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return err
		}
		if pr == nil {
			env.Out.Skip("no pull request is open for %s", b.Name)
			continue
		}
		if pr.IsDraft == opts.Undo {
			// Already in the state that was asked for, which is the opposite
			// of what --undo names.
			env.Out.Skip("%s is already %s", pr, readyState(!opts.Undo))
			continue
		}
		if env.DryRun {
			env.Out.Printf("(dry-run) would mark %s %s", pr, readyState(!opts.Undo))
			changed++
			continue
		}
		if err := gh.ReadyForReview(pr.Number, opts.Undo); err != nil {
			return err
		}
		env.Out.OK("%s is %s", pr, readyState(!opts.Undo))
		env.Out.Printf("    %s", pr.URL)
		changed++
	}

	env.Out.Printf("")
	if changed == 0 {
		env.Out.Printf("Nothing to change.")
		return nil
	}
	verb := "marked"
	if env.DryRun {
		verb = "would be marked"
	}
	env.Out.Printf("%d pull request(s) %s %s.", changed, verb, readyState(!opts.Undo))
	return nil
}

// readyState names a draft state the way a person would say it.
func readyState(ready bool) string {
	if ready {
		return "ready for review"
	}
	return "a draft"
}
