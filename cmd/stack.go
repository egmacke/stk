package cmd

import (
	"github.com/spf13/cobra"

	"stk/internal/ui"
)

func newStackCmd() *cobra.Command {
	var all, asJSON, legend bool
	cmd := &cobra.Command{
		Use:   "stack",
		Short: "Print the current stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if asJSON {
				return a.Out.JSON(buildStackJSON(a, all))
			}
			printTree(a, all)
			if legend {
				a.Out.Raw("")
				for _, line := range ui.Legend() {
					a.Out.Raw("%s", line)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "show every stack, plus untracked branches")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the stack as JSON")
	cmd.Flags().BoolVar(&legend, "legend", false, "explain the status markers")
	return cmd
}
