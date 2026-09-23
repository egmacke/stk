package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
	"github.com/egmacke/stk/internal/ui"
)

func newCheckoutCmd() *cobra.Command {
	var stash autostashPref
	var track trackPref
	cmd := &cobra.Command{
		Use:     "checkout [branch] [--no-track | --track-from <branch>]",
		Aliases: []string{"co"},
		Short:   "Switch branches, interactively when no name is given",
		Long: "A branch stk does not track is brought into the stack as it is checked out,\n" +
			"with trunk as its parent — including a branch only the remote had, which is\n" +
			"fetched first. --track-from names a different parent, and --no-track leaves\n" +
			"the branch an ordinary git branch.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if err := stash.apply(a); err != nil {
				return err
			}
			if err := track.validate(a); err != nil {
				return err
			}
			if len(args) == 1 {
				b, ok := a.Graph.Resolve(args[0])
				if !ok {
					return checkoutRemote(a, &track, args[0])
				}
				if err := switchTo(a, b); err != nil {
					return err
				}
				return track.apply(a, b.Name)
			}
			if !Selectable() {
				return errors.New("no branch given and stk is not attached to a terminal\n\nName the branch, or use stk stack to list them")
			}
			chosen, err := ui.Pick(ui.PickOptions{
				Graph:            a.Graph,
				Title:            "Search",
				Verb:             "checkout",
				IncludeUntracked: true,
			})
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			if err := switchTo(a, chosen); err != nil {
				return err
			}
			return track.apply(a, chosen.Name)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	stash.register(cmd)
	track.register(cmd)
	return cmd
}

// trackPref is the --no-track/--track-from pair, which decide where a branch
// stk does not yet track lands in the stack once it has been checked out.
//
// Trunk is the answer when neither is given: a branch you have just switched
// to is work that starts from trunk far more often than not, and stk track or
// stk move says otherwise later at no cost.
type trackPref struct {
	off  bool
	from string
}

func (p *trackPref) register(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&p.off, "no-track", "n", false,
		"leave an untracked branch out of the stack")
	cmd.Flags().StringVarP(&p.from, "track-from", "t", "",
		"stack an untracked branch on `branch` instead of trunk")
	_ = cmd.RegisterFlagCompletionFunc("track-from", branchNameCompletion)
}

// validate rejects a contradictory pair, and a parent that cannot hold a
// branch, before anything is checked out: a mistyped --track-from should not
// leave you on a branch the command then refused to track.
func (p *trackPref) validate(a *app) error {
	if p.off && p.from != "" {
		return errors.New("use either --no-track or --track-from, not both")
	}
	if p.from == "" {
		return nil
	}
	return operations.CanBeParent(a.Graph, p.from)
}

// apply brings the branch just checked out into the stack when stk does not
// already track it.
func (p *trackPref) apply(a *app, name string) error {
	if p.off {
		return nil
	}
	if _, ok := a.Graph.Resolve(name); !ok {
		// The graph was built before the branch was fetched.
		if err := a.reload(); err != nil {
			return err
		}
	}
	parent := p.from
	if parent == "" {
		parent = a.Cfg.Trunk
	}
	b, ok := a.Graph.Resolve(name)
	switch {
	case ok && (b.IsTrunk || b.Tracked):
		return nil
	case !ok && !globals.dryRun:
		// Nothing was checked out after all.
		return nil
	case globals.dryRun:
		// Under --dry-run the fetched branch does not exist to resolve, and a
		// branch that arrives from the remote is never already tracked.
		a.Out.Dry("would track %s with parent %s", output.BranchName(name), output.BranchName(parent))
		return nil
	}
	return operations.Track(a.Env, a.Graph, name, parent)
}

// checkoutRemote handles a name that is no local branch. Somebody else may
// have pushed it, so stk looks on the remote before declaring it unknown.
func checkoutRemote(a *app, track *trackPref, name string) error {
	found, err := operations.CheckoutRemote(a.Env, name)
	if err != nil {
		return err
	}
	if found {
		return track.apply(a, name)
	}
	if remote := a.Cfg.Remote; remote != "" && a.Repo.RemoteExists(remote) {
		return fmt.Errorf("branch %q does not exist locally or on %s", name, remote)
	}
	return fmt.Errorf("branch %q does not exist", name)
}

// switchTo checks out a branch, explaining git's worktree restriction rather
// than letting a raw git error through.
func switchTo(a *app, b *stack.Branch) error {
	if b.IsCurrent {
		a.Out.Printf("Already on %s.", output.BranchName(b.Name))
		return nil
	}
	if b.CheckedOutElsewhere() {
		return fmt.Errorf("%s is already checked out in:\n\n    %s\n\nGit does not allow this branch to be checked out here", b.Name, b.Worktree)
	}
	if globals.dryRun {
		a.Out.Dry("would switch to %s", output.BranchName(b.Name))
		return nil
	}
	// operations.Switch reports the switch itself, because with --autostash
	// there are two more lines to interleave with it.
	if err := operations.Switch(a.Env, b.Name); err != nil {
		return err
	}
	noteUpstreamGone(a, b)
	return nil
}
