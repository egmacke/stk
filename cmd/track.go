package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newTrackCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:     "track [branch] [--parent <branch>]",
		Aliases: []string{"tr"},
		Short:   "Bring an existing branch into the stack graph",
		Long: "The parent must be trunk or a branch stk already tracks. The initial base\n" +
			"is the merge base of the two branches. stk never infers a parent on its own,\n" +
			"but it will ask for one when --parent is omitted.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			b, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			if parent == "" {
				parent, err = promptBranch(a.Graph, branchPrompt{
					Title:      fmt.Sprintf("Parent of %s", b.Name),
					Candidates: parentCandidates(a.Graph, b),
					Missing:    "--parent is required; stk will not guess a stack parent",
					Empty:      fmt.Sprintf("no branch can be the parent of %s", b.Name),
				})
				if cancelled(err) {
					return nil
				}
				if err != nil {
					return err
				}
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
		Use:     "untrack [branch]",
		Aliases: []string{"utr"},
		Short:   "Remove stack metadata, leaving the git branch alone",
		Args:    cobra.MaximumNArgs(1),
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
