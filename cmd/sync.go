package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newSyncCmd() *cobra.Command {
	var stackOnly, noRestack, noCleanup, cleanup bool
	var stash autostashPref
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch, update trunk, prune merged branches and restack",
		Long: "Fetches the configured remote, fast-forwards trunk when git can prove that\n" +
			"is safe, offers to delete branches already contained in trunk, and restacks\n" +
			"what remains. stk sync never pushes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cleanup && noCleanup {
				return errors.New("use either --cleanup or --no-cleanup, not both")
			}
			a, err := open()
			if err != nil {
				return err
			}
			if err := stash.apply(a); err != nil {
				return err
			}
			mode := operations.CleanupAsk
			switch {
			case noCleanup:
				mode = operations.CleanupNever
			case cleanup:
				mode = operations.CleanupAlways
			}
			// Without a terminal stk still reports what could be pruned, but
			// deletes nothing unless --cleanup was given.
			err = operations.Sync(a.Env, operations.SyncOptions{
				StackOnly: stackOnly,
				Restack:   !noRestack,
				Cleanup:   mode,
			})
			if errors.Is(err, operations.ErrConflict) {
				return errSilent{err}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&stackOnly, "stack", "s", false, "only restack the stack containing the current branch")
	cmd.Flags().BoolVar(&noRestack, "no-restack", false, "fetch, update trunk and clean up without rewriting branches")
	cmd.Flags().BoolVar(&noCleanup, "no-cleanup", false, "never delete merged branches")
	cmd.Flags().BoolVar(&cleanup, "cleanup", false, "delete merged branches without prompting")
	stash.register(cmd)
	return cmd
}
