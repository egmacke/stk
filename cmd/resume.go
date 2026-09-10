package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/operations"
)

func newContinueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "continue",
		Short: "Resume a restack that stopped on a conflict",
		Long:  "Continues with the scope the operation began with, not just the branch that conflicted.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			err = operations.Continue(a.Env)
			if errors.Is(err, operations.ErrConflict) {
				return errSilent{err}
			}
			return err
		},
	}
}

func newAbortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abort",
		Short: "Abandon the in-flight stk operation and restore the original branches",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			return operations.Abort(a.Env)
		},
	}
}
