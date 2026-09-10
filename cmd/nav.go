package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"stk/internal/stack"
	"stk/internal/ui"
)

func newParentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "parent [branch]",
		Short: "Print the logical parent of a branch",
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
			if b.IsTrunk {
				return fmt.Errorf("%s is trunk and has no parent", b.Name)
			}
			if b.Parent == nil {
				return fmt.Errorf("%s has no resolvable parent", b.Name)
			}
			a.Out.Raw("%s", b.Parent.Name)
			return nil
		},
		ValidArgsFunction: branchNameCompletion,
	}
}

func newChildrenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "children [branch]",
		Short: "Print the branches stacked directly on a branch",
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
			for _, c := range b.Children {
				a.Out.Raw("%s", c.Name)
			}
			return nil
		},
		ValidArgsFunction: branchNameCompletion,
	}
}

// stepCount reads the optional numeric argument shared by up and down.
func stepCount(args []string) (int, error) {
	if len(args) == 0 {
		return 1, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("expected a positive number of steps, got %q", args[0])
	}
	return n, nil
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down [n]",
		Short: "Move one step (or n steps) toward trunk",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := stepCount(args)
			if err != nil {
				return err
			}
			a, err := open()
			if err != nil {
				return err
			}
			cur := a.Graph.Current
			if cur == nil {
				return errors.New("HEAD is detached")
			}
			if cur.IsTrunk {
				return fmt.Errorf("already on trunk (%s)", cur.Name)
			}
			target := cur
			for i := 0; i < n; i++ {
				if target.Parent == nil {
					return fmt.Errorf("%s has no resolvable parent", target.Name)
				}
				target = target.Parent
			}
			return switchTo(a, target)
		},
	}
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up [n]",
		Short: "Move one step (or n steps) away from trunk",
		Long:  "When a branch has several children, stk asks which one to follow.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := stepCount(args)
			if err != nil {
				return err
			}
			a, err := open()
			if err != nil {
				return err
			}
			target := a.Graph.Current
			if target == nil {
				return errors.New("HEAD is detached")
			}
			for i := 0; i < n; i++ {
				next, err := chooseChild(a, target)
				if err != nil {
					return err
				}
				target = next
			}
			return switchTo(a, target)
		},
	}
}

// chooseChild picks the single child, or asks when a branch forks.
func chooseChild(a *app, b *stack.Branch) (*stack.Branch, error) {
	switch len(b.Children) {
	case 0:
		return nil, fmt.Errorf("%s has no branches stacked on it", b.Name)
	case 1:
		return b.Children[0], nil
	}
	return choose(a, b.Children, fmt.Sprintf("Children of %s", b.Name))
}

func choose(a *app, candidates []*stack.Branch, title string) (*stack.Branch, error) {
	if !Selectable() {
		var names []string
		for _, c := range candidates {
			names = append(names, c.Name)
		}
		return nil, fmt.Errorf("several branches match and stk is not attached to a terminal:\n\n    %s\n\nName one explicitly with stk checkout", strings.Join(names, "\n    "))
	}
	chosen, err := ui.Pick(ui.PickOptions{Graph: a.Graph, Title: title, Verb: "checkout", Candidates: candidates})
	if err != nil {
		return nil, err
	}
	return chosen, nil
}

func newBottomCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bottom",
		Short: "Switch to the lowest branch of the current stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			cur := a.Graph.Current
			if cur == nil {
				return errors.New("HEAD is detached")
			}
			if cur.IsTrunk {
				return fmt.Errorf("already on trunk (%s); nothing is below it", cur.Name)
			}
			root := a.Graph.Root(cur)
			if root == nil {
				return fmt.Errorf("cannot determine the stack containing %s", cur.Name)
			}
			return switchTo(a, root)
		},
	}
}

func newTopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "top",
		Short: "Switch to the highest branch above the current one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			cur := a.Graph.Current
			if cur == nil {
				return errors.New("HEAD is detached")
			}
			leaves := stack.Leaves(cur)
			switch len(leaves) {
			case 0:
				return fmt.Errorf("%s has no branches stacked on it", cur.Name)
			case 1:
				return switchTo(a, leaves[0])
			}
			chosen, err := choose(a, leaves, "Leaves")
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			return switchTo(a, chosen)
		},
	}
}
