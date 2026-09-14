package cmd

import (
	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
)

func newReadyCmd() *cobra.Command {
	var wholeStack, undo bool
	cmd := &cobra.Command{
		Use:   "ready [branch]",
		Short: "Take a pull request out of draft, or put it back",
		Long: "Marks the branch's pull request ready for review through the GitHub CLI,\n" +
			"and with --undo turns it back into a draft.\n\n" +
			"It is a command of its own because stk submit never edits a pull request\n" +
			"that is already open. Draft state is a decision about review, so stk\n" +
			"changes it only when asked.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: branchNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			// A paused operation leaves HEAD detached, so report it before
			// trying to work out which branch was meant.
			if err := operations.RequireNoOperation(a.Repo); err != nil {
				return err
			}
			target, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			return operations.Ready(a.Env, a.Graph, target, operations.ReadyOptions{
				Stack: wholeStack,
				Undo:  undo,
			})
		},
	}
	cmd.Flags().BoolVarP(&wholeStack, "stack", "s", false, "cover every branch in the stack")
	cmd.Flags().BoolVar(&undo, "undo", false, "turn the pull request back into a draft")
	return cmd
}
