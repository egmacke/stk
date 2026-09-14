package cmd

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"stk/internal/git"
	"stk/internal/output"
	"stk/internal/stack"
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
	row := func(label, value string) {
		a.Out.Raw("%-13s %s", label+":", value)
	}
	row("Branch", b.Name)
	if b.IsTrunk {
		row("Role", "trunk")
	} else if b.Parent != nil {
		row("Parent", b.Parent.Name)
	} else {
		row("Parent", "unknown")
	}
	var kids []string
	for _, c := range b.Children {
		kids = append(kids, c.Name)
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
		row("Upstream", "none")
		row("Published", "no")
	} else {
		row("Upstream", b.Upstream)
		row("Published", "yes")
		row("Unpushed", itoa(b.Ahead))
		row("Behind", itoa(b.Behind))
		if b.UpstreamGone {
			row("Note", "upstream branch no longer exists")
		}
	}
	if b.PR != nil {
		pr := "#" + itoa(b.PR.Number)
		if b.PR.Merged {
			pr += " (merged)"
		}
		row("Pull request", pr)
		if b.PR.URL != "" {
			a.Out.Raw("%-13s %s", "", b.PR.URL)
		}
	}
	a.Out.Raw("")
	row("Worktree", orDash(b.Worktree))
	if b.IsCurrent {
		row("Dirty", yesNo(a.Graph.Dirty()))
	}
	a.Out.Raw("")
	if b.IsTrunk {
		row("Restack", "n/a")
	} else if b.NeedsRestack() {
		row("Restack", "required")
	} else {
		row("Restack", "not required")
	}
	if problems := output.Problems(b); len(problems) > 0 {
		row("Problems", strings.Join(problems, ", "))
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
