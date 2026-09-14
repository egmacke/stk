package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newSyncCmd() *cobra.Command {
	var stackOnly, noRestack, noCleanup, cleanup, noPulls bool
	var stash autostashPref
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch, update trunk, prune finished branches and restack",
		Long: "Fetches the configured remote, fast-forwards trunk when git can prove that\n" +
			"is safe, offers to delete the branches that are finished, and restacks what\n" +
			"remains. stk sync never pushes, and never touches a remote branch.\n\n" +
			"A branch is finished when trunk already contains it, when its pull request\n" +
			"landed, or when its pull request was closed or its remote branch deleted.\n" +
			"The last two carry no proof that the commits live on anywhere else, so they\n" +
			"are listed with what they would take with them and asked about separately.\n\n" +
			"Branches left above a deleted one are reparented onto the nearest ancestor\n" +
			"that survives, and their open pull requests are retargeted to match.",
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
				NoPulls:   noPulls,
			})
			if errors.Is(err, operations.ErrConflict) {
				return errSilent{err}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&stackOnly, "stack", "s", false, "only restack the stack containing the current branch")
	cmd.Flags().BoolVar(&noRestack, "no-restack", false, "fetch, update trunk and clean up without rewriting branches")
	cmd.Flags().BoolVar(&noCleanup, "no-cleanup", false, "never delete a branch")
	cmd.Flags().BoolVar(&cleanup, "cleanup", false, "delete finished branches without prompting")
	cmd.Flags().BoolVar(&noPulls, "no-pulls", false, "work from git alone; never ask the forge about pull requests")
	stash.register(cmd)
	return cmd
}
