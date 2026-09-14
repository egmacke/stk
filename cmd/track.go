package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newTrackCmd() *cobra.Command {
	var parent, fromPR string
	cmd := &cobra.Command{
		Use:     "track [branch] [--parent <branch>] | track --from-pr <pull-request>",
		Aliases: []string{"tr"},
		Short:   "Bring an existing branch, or a stack on GitHub, into the stack graph",
		Long: "The parent must be trunk or a branch stk already tracks. The initial base\n" +
			"is the merge base of the two branches. stk never infers a parent on its own,\n" +
			"but it will ask for one when --parent is omitted.\n\n" +
			"With --from-pr, and stk.githubStacks = true, the whole stack a pull request\n" +
			"belongs to on GitHub is brought in through gh stack checkout: its branches\n" +
			"are fetched, checked out and tracked with the parents GitHub has for them.\n" +
			"The argument is a pull request number or URL, or a stack number.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromPR != "" && (len(args) > 0 || parent != "") {
				return errors.New("--from-pr names the whole stack; do not give a branch or --parent with it")
			}
			a, err := open()
			if err != nil {
				return err
			}
			if fromPR != "" {
				return operations.TrackFromPR(a.Env, fromPR)
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
	cmd.Flags().StringVarP(&parent, "parent", "p", "", "logical parent `branch`")
	cmd.Flags().StringVar(&fromPR, "from-pr", "", "track the stack this `pull-request` belongs to on GitHub (needs stk.githubStacks)")
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
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "also untrack every branch stacked above it")
	cmd.Flags().StringVarP(&reparent, "reparent", "p", "", "move its children onto `branch` first")
	_ = cmd.RegisterFlagCompletionFunc("reparent", branchNameCompletion)
	return cmd
}
