package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newDeleteCmd() *cobra.Command {
	var yes, remote, noRemote bool
	cmd := &cobra.Command{
		Use:   "delete [branch...]",
		Short: "Delete branches and, if you want, their remote counterparts",
		Long: "Deletes local branches and offers to delete the matching remote branches\n" +
			"too. With no argument the current branch is deleted, and stk steps down to\n" +
			"the nearest branch that survives first.\n\n" +
			"Branches stacked above a deleted one are reparented onto the nearest\n" +
			"ancestor that stays, so the graph keeps its shape; they are left needing a\n" +
			"restack rather than rewritten here.\n\n" +
			"stk says how many commits each branch keeps that nothing else does, and\n" +
			"prints the git command that brings a deleted branch back. Deleting the\n" +
			"remote branch is asked separately, because GitHub closes any open pull\n" +
			"request whose head branch disappears.",
		Args:              cobra.ArbitraryArgs,
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
			names := args
			if len(names) == 0 {
				if a.Graph.CurrentName == "" {
					return errors.New("HEAD is detached; name the branch to delete")
				}
				names = []string{a.Graph.CurrentName}
			}
			return operations.Delete(a.Env, a.Graph, names, operations.DeleteOptions{
				Yes:      yes,
				Remote:   remote,
				NoRemote: noRemote,
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer every question with yes, including the remote one")
	cmd.Flags().BoolVar(&remote, "remote", false, "delete the remote branches without asking")
	cmd.Flags().BoolVar(&noRemote, "no-remote", false, "leave the remote branches alone without asking")
	return cmd
}
