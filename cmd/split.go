package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"stk/internal/git"
	"stk/internal/operations"
	"stk/internal/ui"
)

func newSplitCmd() *cobra.Command {
	var at, names []string
	cmd := &cobra.Command{
		Use:   "split [branch]",
		Short: "Split a branch into several stacked branches",
		Long: "Divides the branch's own commits into a stack, cutting after each split\n" +
			"point you choose.\n\n" +
			"The branch keeps its name and ends up on top, so its pull request keeps its\n" +
			"identity and simply shows fewer commits, and anything already stacked on it\n" +
			"stays where it is. The new branches appear below it, bottom first.\n\n" +
			"Nothing is rebased: the commits are already in a line, so splitting points\n" +
			"new branches at commits that are already there and rewrites the metadata.\n\n" +
			"With no --at, stk lists the commits and asks which ones end a segment.",
		Args:              cobra.MaximumNArgs(1),
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
			target, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			if Interactive() {
				a.Env.AskSplitPoints = askSplitPoints
			}
			err = operations.Split(a.Env, a.Graph, target, operations.SplitOptions{
				At:    at,
				Names: names,
			})
			// Dismissing a prompt changes nothing and is not a failure.
			if cancelled(err) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&at, "at", nil, "split after this `commit`; repeatable, oldest first")
	cmd.Flags().StringArrayVar(&names, "name", nil, "name for each new `branch`, bottom first; one per --at")
	return cmd
}

// askSplitPoints lists the commits and asks which ones end a segment, then
// asks what to call each new branch.
//
// The commits are numbered rather than picked from a full-screen list, so the
// same prompt works on a terminal and down a pipe.
func askSplitPoints(branch string, commits []git.Commit) ([]operations.SplitChoice, error) {
	out := os.Stderr
	fmt.Fprintf(out, "\nCommits on %s, oldest first:\n\n", branch)
	for i, c := range commits {
		fmt.Fprintf(out, "  %2d  %s  %s\n", i+1, git.ShortSHA(c.SHA), c.Subject)
	}
	fmt.Fprintln(out)

	answer, err := ui.ReadLine(fmt.Sprintf(
		"Split after which commits? (numbers, comma separated, 1-%d)", len(commits)-1))
	if cancelled(err) {
		// Choosing nothing is an answer: the caller says so and stops.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	indexes, err := parseSplitIndexes(answer, len(commits))
	if err != nil {
		return nil, err
	}

	choices := make([]operations.SplitChoice, 0, len(indexes))
	for n, i := range indexes {
		suggestion := fmt.Sprintf("%s-%d", branch, n+1)
		name, err := ui.ReadLineDefault(
			fmt.Sprintf("Name for the branch ending at %s:", git.ShortSHA(commits[i].SHA)), suggestion)
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, ui.ErrCancelled
		}
		choices = append(choices, operations.SplitChoice{SHA: commits[i].SHA, Name: name})
	}
	return choices, nil
}

// parseSplitIndexes reads "1,3" as zero-based positions in the commit list.
func parseSplitIndexes(answer string, count int) ([]int, error) {
	var out []int
	for _, field := range strings.Split(answer, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("%q is not a commit number", field)
		}
		if n < 1 || n >= count {
			return nil, fmt.Errorf("%d is not one of 1-%d; the tip cannot end a segment", n, count-1)
		}
		out = append(out, n-1)
	}
	return out, nil
}
