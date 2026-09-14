package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

func newInfoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info [branch]",
		Short: "Show everything stk knows about a branch",
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
			if asJSON {
				return a.Out.JSON(output.Branch(b))
			}
			printInfo(a, b)
			return nil
		},
		ValidArgsFunction: branchNameCompletion,
	}
	cmd.Flags().BoolVarP(&asJSON, "json", "j", false, "print the branch as JSON")
	return cmd
}

func printInfo(a *app, b *stack.Branch) {
	// The label is padded before it is dimmed, since escape sequences count
	// towards len but not towards the column.
	row := func(label, value string) {
		a.Out.Raw("%s %s", output.Dim(fmt.Sprintf("%-13s", label+":")), value)
	}
	row("Branch", output.BranchName(b.Name))
	if b.IsTrunk {
		row("Role", "trunk")
	} else if b.Parent != nil {
		row("Parent", output.BranchName(b.Parent.Name))
	} else {
		row("Parent", "unknown")
	}
	var kids []string
	for _, c := range b.Children {
		kids = append(kids, output.BranchName(c.Name))
	}
	row("Children", orDash(strings.Join(kids, ", ")))
	a.Out.Raw("")
	if b.Base != "" {
		row("Base", git.ShortSHA(b.Base))
	}
	row("HEAD", git.ShortSHA(b.SHA))
	a.Out.Raw("")
	if b.Base != "" && b.SHA != "" {
		row("Commits", itoa(a.Repo.CountCommits(b.Base, b.SHA)))
	}
	if b.Upstream == "" {
		row("Upstream", output.Dim("none"))
		row("Published", output.Yellow("no"))
	} else {
		row("Upstream", b.Upstream)
		row("Published", output.Green("yes"))
		row("Unpushed", itoa(b.Ahead))
		row("Behind", itoa(b.Behind))
		if b.UpstreamGone {
			row("Note", output.Yellow("upstream branch no longer exists"))
		}
	}
	if b.PR != nil {
		pr := "#" + itoa(b.PR.Number)
		if b.PR.Merged {
			pr += " (merged)"
		}
		row("Pull request", output.Bold(pr))
		if b.PR.URL != "" {
			a.Out.Raw("%-13s %s", "", output.Dim(b.PR.URL))
		}
	}
	a.Out.Raw("")
	row("Worktree", orDash(b.Worktree))
	if b.IsCurrent {
		dirty := a.Graph.Dirty()
		value := output.Green(yesNo(dirty))
		if dirty {
			value = output.Yellow(yesNo(dirty))
		}
		row("Dirty", value)
	}
	a.Out.Raw("")
	if b.IsTrunk {
		row("Restack", "n/a")
	} else if b.NeedsRestack() {
		row("Restack", output.Yellow("required"))
	} else {
		row("Restack", output.Green("not required"))
	}
	if problems := output.Problems(b); len(problems) > 0 {
		row("Problems", output.Red(strings.Join(problems, ", ")))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(n int) string { return strconv.Itoa(n) }
