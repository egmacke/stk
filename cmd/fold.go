package cmd

import (
	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
)

func newFoldCmd() *cobra.Command {
	var into string
	var wholeStack, yes, closePulls bool
	cmd := &cobra.Command{
		Use:   "fold [branch]",
		Short: "Collapse stacked branches into one",
		Long: "Folds a branch into the one below it: the lower branch ends up with both\n" +
			"sets of commits, anything stacked above is reparented onto it, and the\n" +
			"folded branch is deleted.\n\n" +
			"Nothing is squashed and nothing is rebased. In a consistent stack the top\n" +
			"branch already contains every commit below it, so a fold only moves the\n" +
			"surviving branch's ref up to it — which is also why stk can prove the fold\n" +
			"loses nothing. A stack that needs a restack is refused rather than folded.\n\n" +
			"  stk fold                     the current branch into its parent\n" +
			"  stk fold --into <branch>     everything from here down into that branch\n" +
			"  stk fold --stack             the whole stack into its lowest branch\n\n" +
			"The remote is not touched. Publish the result with stk submit, which\n" +
			"force-pushes the surviving branch under a lease.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: branchNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			// A paused operation leaves HEAD detached, so report it before
			// working out which branch was meant.
			if err := operations.RequireNoOperation(a.Repo); err != nil {
				return err
			}
			target, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			return operations.Fold(a.Env, a.Graph, target, operations.FoldOptions{
				Into:       into,
				Stack:      wholeStack,
				Yes:        yes,
				ClosePulls: closePulls,
			})
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "fold everything from the branch down into this `branch`")
	cmd.Flags().BoolVarP(&wholeStack, "stack", "s", false, "fold the whole stack into its lowest branch")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask before deleting the folded branches")
	cmd.Flags().BoolVar(&closePulls, "close-pulls", false, "close the pull requests of the folded branches")
	_ = cmd.RegisterFlagCompletionFunc("into", branchNameCompletion)
	return cmd
}
