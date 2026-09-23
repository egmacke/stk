package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
	"github.com/egmacke/stk/internal/ui"
)

func newTriageCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "triage [--parent <branch>]",
		Short: "Go through every untracked branch, tracking or deleting each one",
		Long: "Walks the local branches stk does not track, one at a time, and asks what\n" +
			"to do with each.\n\n" +
			"A branch the remote also has can be tracked or left alone. A branch that\n" +
			"exists only here can be deleted as well; one that would take commits kept\n" +
			"nowhere else with it is asked about a second time.\n\n" +
			"Tracked branches go onto trunk unless --parent names another. The remote\n" +
			"is judged by the last fetch; nothing is fetched and nothing is pushed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			opts := operations.TriageOptions{Parent: parent}
			if Interactive() {
				opts.Ask = askTriage
			}
			return operations.Triage(a.Env, a.Graph, opts)
		},
	}
	cmd.Flags().StringVarP(&parent, "parent", "p", "", "track branches onto `branch` instead of trunk")
	_ = cmd.RegisterFlagCompletionFunc("parent", branchNameCompletion)
	return cmd
}

// askTriage offers the answers that make sense for one branch. Skipping is
// the default, so an absent-minded return changes nothing.
func askTriage(q operations.TriageQuestion) (operations.TriageAction, error) {
	choices := []ui.Choice{{Key: "t", Label: "track"}}
	if q.CanDelete {
		choices = append(choices, ui.Choice{Key: "d", Label: "delete"})
	}
	choices = append(choices,
		ui.Choice{Key: "s", Label: "skip", Default: true},
		ui.Choice{Key: "q", Label: "quit"},
	)
	question := fmt.Sprintf("Track %s onto %s?", q.Branch.Name, q.Parent)
	if q.CanDelete {
		question = fmt.Sprintf("Track %s onto %s, or delete it?", q.Branch.Name, q.Parent)
	}
	key, err := ui.Choose(question, choices)
	if cancelled(err) {
		// Nobody is there to answer the rest, but what was done stands.
		return operations.TriageQuit, nil
	}
	if err != nil {
		return operations.TriageQuit, err
	}
	switch key {
	case "t":
		return operations.TriageTrack, nil
	case "d":
		return operations.TriageDelete, nil
	case "q":
		return operations.TriageQuit, nil
	}
	return operations.TriageSkip, nil
}
