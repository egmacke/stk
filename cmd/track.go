package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newTrackCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "track [branch] --parent <branch>",
		Short: "Bring an existing branch into the stack graph",
		Long: "The parent must be trunk or a branch stk already tracks. The initial base\n" +
			"is the merge base of the two branches. stk never infers a parent on its own.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if parent == "" {
				return errors.New("--parent is required; stk will not guess a stack parent")
			}
			a, err := open()
			if err != nil {
				return err
			}
			b, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			return operations.Track(a.Env, a.Graph, b.Name, parent)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().StringVar(&parent, "parent", "", "logical parent `branch`")
	_ = cmd.RegisterFlagCompletionFunc("parent", branchNameCompletion)
	return cmd
}

func newUntrackCmd() *cobra.Command {
	var recursive bool
	var reparent string
	cmd := &cobra.Command{
		Use:   "untrack [branch]",
		Short: "Remove stack metadata, leaving the git branch alone",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			b, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			return operations.Untrack(a.Env, a.Graph, b.Name, operations.UntrackOptions{
				Recursive: recursive,
				Reparent:  reparent,
			})
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().BoolVar(&recursive, "recursive", false, "also untrack every branch stacked above it")
	cmd.Flags().StringVar(&reparent, "reparent", "", "move its children onto `branch` first")
	_ = cmd.RegisterFlagCompletionFunc("reparent", branchNameCompletion)
	return cmd
}
