package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
)

func newSyncCmd() *cobra.Command {
	var stackOnly, noRestack, noCleanup, cleanup, noPulls, allWorktrees bool
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
			"that survives, and their open pull requests are retargeted to match.\n\n" +
			"Sync works in the current worktree. A branch checked out in another\n" +
			"worktree is not restacked, and neither is anything above it; a trunk\n" +
			"checked out in another worktree is not moved, and the remote trunk is\n" +
			"restacked onto instead. --all-worktrees does both inside the worktree\n" +
			"that holds the branch, unless it has uncommitted changes or a git\n" +
			"operation in progress.\n\n" +
			"With stk.githubStacks on, sync also settles the stack on GitHub itself:\n" +
			"it links the pull requests of each stack that runs from trunk to a tip\n" +
			"without forking, which is how a stack linked outside this checkout — in\n" +
			"the browser, or by a colleague — is found and recorded here. Where the\n" +
			"graph forks there is no single stack to link, and stk submit, which is\n" +
			"given the branch, is where that choice is made. Before cleanup it reads\n" +
			"back each stack already linked on GitHub: pull requests merged there are\n" +
			"marked merged, and branches stacked on top there are created from the\n" +
			"remote and tracked. --no-pulls skips all of it.",
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
				StackOnly:    stackOnly,
				Restack:      !noRestack,
				Cleanup:      mode,
				NoPulls:      noPulls,
				AllWorktrees: allWorktrees,
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
	cmd.Flags().BoolVar(&allWorktrees, "all-worktrees", false, "also move trunk and restack branches checked out in other worktrees")
	stash.register(cmd)
	return cmd
}
