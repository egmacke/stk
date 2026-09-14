package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
)

func newRenameCmd() *cobra.Command {
	var yes, remote, noRemote bool
	cmd := &cobra.Command{
		Use:     "rename [old-name] [new-name]",
		Aliases: []string{"rn"},
		Short:   "Rename a branch, and its remote branch with it",
		Long: "Stack relationships are keyed by stable ids, so renaming a branch leaves\n" +
			"parents and children untouched.\n\n" +
			"When the branch has been published, stk offers to move the remote branch\n" +
			"with it: the new name is pushed and the old one deleted in a single push,\n" +
			"and the upstream link follows. That is the one push outside stk submit, so\n" +
			"it is always asked for and never assumed. GitHub closes any open pull\n" +
			"request whose head branch is deleted, so a pull request open for the old\n" +
			"name does not survive the move.\n\n" +
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
			return operations.Rename(a.Env, a.Graph, oldName, newName, operations.RenameOptions{
				Yes:      yes,
				Remote:   remote,
				NoRemote: noRemote,
			})
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer every question with yes, including the remote one")
	cmd.Flags().BoolVar(&remote, "remote", false, "rename the remote branch too, without asking")
	cmd.Flags().BoolVar(&noRemote, "no-remote", false, "leave the remote branch alone without asking")
	return cmd
}

func newMoveCmd() *cobra.Command {
	var onto string
	var stash autostashPref
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
			if err := stash.apply(a); err != nil {
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
	stash.register(cmd)
	_ = cmd.RegisterFlagCompletionFunc("onto", branchNameCompletion)
	return cmd
}
