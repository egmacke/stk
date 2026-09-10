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
					return fmt.Errorf("branch %q does not exist", args[0])
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
