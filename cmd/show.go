package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/output"
	"stk/internal/stack"
	"stk/internal/ui"
)

func newShowCmd() *cobra.Command {
	var asJSON, noSelect bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Browse the whole stack graph interactively",
		Long: "Opens the same viewer as stk checkout with no arguments, without implying\n" +
			"that checking a branch out is the goal. Press enter to check out the\n" +
			"highlighted branch.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if asJSON {
				return a.Out.JSON(buildStackJSON(a, true))
			}
			if !Selectable() {
				// A command with no terminal must not open a selector.
				printTree(a, true)
				return nil
			}
			chosen, err := ui.Pick(ui.PickOptions{
				Graph:            a.Graph,
				Title:            "Search",
				IncludeUntracked: true,
				ReadOnly:         noSelect,
			})
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			if noSelect || chosen == nil {
				return nil
			}
			return switchTo(a, chosen)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the stack graph as JSON")
	cmd.Flags().BoolVar(&noSelect, "no-select", false, "browse without offering to check out")
	return cmd
}

// printTree renders the stack as text. all includes every stack plus branches
// stk does not track.
func printTree(a *app, all bool) {
	g := a.Graph
	roots := g.Roots()
	if !all && g.Current != nil && !g.Current.IsTrunk {
		if root := g.Root(g.Current); root != nil {
			roots = []*stack.Branch{root}
		}
	}
	rows := ui.BuildTree(g.Trunk, roots, nil)
	if all {
		if len(g.Untracked) > 0 {
			rows = append(rows, ui.Row{Text: ""}, ui.Row{Text: "untracked"})
			rows = append(rows, ui.FlatRows(g.Untracked, nil)...)
		}
		if len(g.Orphans) > 0 {
			rows = append(rows, ui.Row{Text: ""}, ui.Row{Text: "orphaned (parent missing)"})
			rows = append(rows, ui.FlatRows(g.Orphans, nil)...)
		}
	}
	for _, line := range ui.RenderRows(rows, g.Current, g.Dirty()) {
		a.Out.Raw("%s", line)
	}
}

func buildStackJSON(a *app, all bool) output.StackJSON {
	g := a.Graph
	doc := output.StackJSON{
		Trunk:   g.Trunk.Name,
		Remote:  a.Cfg.Remote,
		Current: g.CurrentName,
		Dirty:   g.Dirty(),
	}
	var include []*stack.Branch
	if all {
		include = append([]*stack.Branch{g.Trunk}, g.Tracked...)
	} else {
		include = append(include, g.Trunk)
		if g.Current != nil && !g.Current.IsTrunk {
			if root := g.Root(g.Current); root != nil {
				include = append(include, stack.Subtree(root)...)
			}
		}
	}
	for _, b := range include {
		doc.Branches = append(doc.Branches, output.Branch(b))
	}
	if all {
		for _, b := range g.Untracked {
			doc.Untracked = append(doc.Untracked, b.Name)
		}
	}
	return doc
}
