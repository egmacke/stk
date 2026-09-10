package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newRenameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rename [old-name] <new-name>",
		Aliases: []string{"rn"},
		Short:   "Rename a branch without breaking the stack",
		Long: "Stack relationships are keyed by stable ids, so renaming a branch leaves\n" +
			"parents and children untouched. The remote branch is never renamed or\n" +
			"deleted.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			oldName, newName := a.Graph.CurrentName, args[0]
			if len(args) == 2 {
				oldName, newName = args[0], args[1]
			}
			if oldName == "" {
				return errors.New("HEAD is detached; name the branch to rename")
			}
			return operations.Rename(a.Env, a.Graph, oldName, newName)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	return cmd
}

func newMoveCmd() *cobra.Command {
	var onto string
	cmd := &cobra.Command{
		Use:   "move [branch] --onto <new-parent>",
		Short: "Re-parent a branch and restack everything above it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if onto == "" {
				return errors.New("--onto is required")
			}
			a, err := open()
			if err != nil {
				return err
			}
			b, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			return operations.Move(a.Env, a.Graph, b.Name, onto)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().StringVar(&onto, "onto", "", "new parent `branch`")
	_ = cmd.RegisterFlagCompletionFunc("onto", branchNameCompletion)
	return cmd
}
