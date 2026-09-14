package cmd

import (
	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
)

func newCreateCmd() *cobra.Command {
	var from string
	var noCheckout bool
	var stash autostashPref
	cmd := &cobra.Command{
		Use:     "create [branch]",
		Aliases: []string{"c"},
		Short:   "Create a branch and record its place in the stack",
		Long: "Creates a branch whose logical parent is the current branch, or the branch\n" +
			"named by --from. When no name is given stk asks for one.\n\n" +
			"The branch is created from the parent's ref rather than by checking the\n" +
			"parent out, so --from works even when the parent is checked out in another\n" +
			"worktree.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if err := stash.apply(a); err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				name, err = promptText(
					"Name for the new branch:",
					"no branch name given\n\nRun:\n\n    stk create <branch>")
				if cancelled(err) {
					return nil
				}
				if err != nil {
					return err
				}
			}
			return operations.Create(a.Env, a.Graph, operations.CreateOptions{
				Name:     name,
				From:     from,
				Checkout: !noCheckout,
			})
		},
	}
	cmd.Flags().StringVarP(&from, "from", "f", "", "parent `branch` (defaults to the current branch)")
	cmd.Flags().BoolVar(&noCheckout, "no-checkout", false, "create the branch without switching to it")
	stash.register(cmd)
	_ = cmd.RegisterFlagCompletionFunc("from", branchNameCompletion)
	return cmd
}
