package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
	"github.com/egmacke/stk/internal/output"
)

func newRestackCmd() *cobra.Command {
	var up, only, rebaseMerges bool
	var stash autostashPref
	cmd := &cobra.Command{
		Use:     "restack",
		Aliases: []string{"r"},
		Short:   "Rebase branches onto their parents so the stack is consistent",
		Long: "By default stk restacks the entire stack containing the current branch,\n" +
			"starting at the lowest branch above trunk and processing every descendant\n" +
			"in dependency order.\n\n" +
			"  stk restack          the whole current stack\n" +
			"  stk restack --up     the current branch and its descendants\n" +
			"  stk restack --only   the current branch alone",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if up && only {
				return errors.New("use either --up or --only, not both")
			}
			a, err := open()
			if err != nil {
				return err
			}
			if err := stash.apply(a); err != nil {
				return err
			}
			// A paused operation must be reported before anything else: it
			// leaves the worktree mid-rebase, so every other diagnosis would
			// be misleading.
			if err := operations.RequireNoOperation(a.Repo); err != nil {
				return err
			}
			target := a.Graph.Current
			if target == nil {
				return errors.New("HEAD is detached; check out a branch first")
			}
			if !target.IsTrunk {
				if err := a.requireTracked(target); err != nil {
					return err
				}
			}

			opts := operations.RestackOptions{
				Scope:        operations.ScopeStack,
				RebaseMerges: rebaseMerges,
			}
			switch {
			case up:
				opts.Scope = operations.ScopeUp
				opts.Heading = fmt.Sprintf("Restacking from %s...", target.Name)
				opts.DoneMessage = "Upstack is up to date."
			case only:
				opts.Scope = operations.ScopeOnly
				opts.Heading = fmt.Sprintf("Restacking %s...", target.Name)
				opts.DoneMessage = "Branch is up to date."
			default:
				opts.DoneMessage = "Stack is up to date."
				opts.Heading = fmt.Sprintf("Restacking the stack containing %s...", target.Name)
			}
			if target.IsTrunk && !only {
				// From trunk there is no single current stack, so every stack
				// above trunk is in scope.
				opts.Heading = "Restacking every stack..."
				opts.DoneMessage = "All stacks are up to date."
			}

			if globals.dryRun {
				a.Out.Printf("%s", output.Heading("Current stack:"))
				a.Out.Printf("")
				printTree(a, target.IsTrunk)
				a.Out.Printf("")
			}
			_, err = operations.Restack(a.Env, a.Graph, target, opts)
			if errors.Is(err, operations.ErrConflict) {
				return errSilent{err}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&up, "up", "u", false, "restack the current branch and its descendants only")
	cmd.Flags().BoolVarP(&only, "only", "o", false, "restack the current branch only")
	cmd.Flags().BoolVar(&rebaseMerges, "rebase-merges", false, "preserve merge commits instead of refusing to flatten them")
	stash.register(cmd)
	return cmd
}

// errSilent marks an error whose message has already been printed.
type errSilent struct{ err error }

func (e errSilent) Error() string { return e.err.Error() }
func (e errSilent) Unwrap() error { return e.err }
