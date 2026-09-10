package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newRenameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rename [old-name] [new-name]",
		Aliases: []string{"rn"},
		Short:   "Rename a branch without breaking the stack",
		Long: "Stack relationships are keyed by stable ids, so renaming a branch leaves\n" +
			"parents and children untouched. The remote branch is never renamed or\n" +
			"deleted.\n\n" +
			"With one name the current branch is renamed to it; with none stk asks for\n" +
			"the new name.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			oldName, newName := a.Graph.CurrentName, ""
			switch len(args) {
			case 1:
				newName = args[0]
			case 2:
				oldName, newName = args[0], args[1]
			}
			if oldName == "" {
				return errors.New("HEAD is detached; name the branch to rename")
			}
			if newName == "" {
				newName, err = promptText(
					fmt.Sprintf("New name for %s:", oldName),
					"no new branch name given\n\nRun:\n\n    stk rename <new-name>")
				if cancelled(err) {
					return nil
				}
				if err != nil {
					return err
				}
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
		Use:   "move [branch] [--onto <new-parent>]",
		Short: "Re-parent a branch and restack everything above it",
		Long: "Records a new logical parent for the branch and rebases it, and everything\n" +
			"stacked above it, onto that parent. Without --onto stk asks which branch to\n" +
			"move it onto.",
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
			if onto == "" {
				onto, err = promptBranch(a.Graph, branchPrompt{
					Title:      fmt.Sprintf("Move %s onto", b.Name),
					Candidates: parentCandidates(a.Graph, b),
					Missing:    "--onto is required",
					Empty:      fmt.Sprintf("every other branch is stacked above %s, so there is nowhere to move it", b.Name),
				})
				if cancelled(err) {
					return nil
				}
				if err != nil {
					return err
				}
			}
			return operations.Move(a.Env, a.Graph, b.Name, onto)
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().StringVarP(&onto, "onto", "o", "", "new parent `branch`")
	_ = cmd.RegisterFlagCompletionFunc("onto", branchNameCompletion)
	return cmd
}
