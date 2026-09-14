package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"stk/internal/operations"
	"stk/internal/stack"
	"stk/internal/ui"
)

func newCheckoutCmd() *cobra.Command {
	var stash autostashPref
	cmd := &cobra.Command{
		Use:     "checkout [branch]",
		Aliases: []string{"co"},
		Short:   "Switch branches, interactively when no name is given",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if err := stash.apply(a); err != nil {
				return err
			}
			if len(args) == 1 {
				b, ok := a.Graph.Resolve(args[0])
				if !ok {
					return checkoutRemote(a, args[0])
				}
				return switchTo(a, b)
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
			return switchTo(a, chosen)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	stash.register(cmd)
	return cmd
}

// checkoutRemote handles a name that is no local branch. Somebody else may
// have pushed it, so stk looks on the remote before declaring it unknown.
func checkoutRemote(a *app, name string) error {
	found, err := operations.CheckoutRemote(a.Env, name)
	if err != nil {
		return err
	}
	if found {
		return offerToTrack(a, name)
	}
	if remote := a.Cfg.Remote; remote != "" && a.Repo.RemoteExists(remote) {
		return fmt.Errorf("branch %q does not exist locally or on %s", name, remote)
	}
	return fmt.Errorf("branch %q does not exist", name)
}

// offerToTrack asks whether a branch stk has just brought down from the remote
// belongs in the stack, since the person who pushed it stacked it somewhere stk
// cannot see.
//
// Declining is free: the branch stays an ordinary git branch, and stk track
// says the same thing later.
func offerToTrack(a *app, name string) error {
	if globals.dryRun {
		return nil
	}
	// The graph was built before the branch existed.
	if err := a.reload(); err != nil {
		return err
	}
	b, ok := a.Graph.Resolve(name)
	if !ok {
		return nil
	}
	a.Out.Printf("")
	if !Interactive() {
		a.Out.Printf("%s is not tracked by stk. To stack on it:", name)
		a.Out.Printf("")
		a.Out.Printf("    stk track %s --parent <branch>", name)
		return nil
	}
	yes, err := ui.Confirm(fmt.Sprintf("Track %s in the stack?", name), true)
	if cancelled(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !yes {
		return nil
	}
	parent, err := promptBranch(a.Graph, branchPrompt{
		Title:      fmt.Sprintf("Parent of %s", name),
		Candidates: parentCandidates(a.Graph, b),
		Missing:    "--parent is required; stk will not guess a stack parent",
		Empty:      fmt.Sprintf("no branch can be the parent of %s", name),
	})
	if cancelled(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return operations.Track(a.Env, a.Graph, name, parent)
}

// switchTo checks out a branch, explaining git's worktree restriction rather
// than letting a raw git error through.
func switchTo(a *app, b *stack.Branch) error {
	if b.IsCurrent {
		a.Out.Printf("Already on %s.", b.Name)
		return nil
	}
	if b.CheckedOutElsewhere() {
		return fmt.Errorf("%s is already checked out in:\n\n    %s\n\nGit does not allow this branch to be checked out here", b.Name, b.Worktree)
	}
	if globals.dryRun {
		a.Out.Printf("(dry-run) would switch to %s", b.Name)
		return nil
	}
	// operations.Switch reports the switch itself, because with --autostash
	// there are two more lines to interleave with it.
	return operations.Switch(a.Env, b.Name)
}
