package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"stk/internal/operations"
	"stk/internal/stack"
	"stk/internal/ui"
)

func newSubmitCmd() *cobra.Command {
	var pull, draft, noPrompt, wholeStack, noComment bool
	cmd := &cobra.Command{
		Use:     "submit [branch]",
		Aliases: []string{"ss"},
		Short:   "Push a branch to the remote and optionally open a pull request",
		Long: "Pushes the branch to the configured remote, creating it there when it does\n" +
			"not exist yet. A branch a restack has rewritten is pushed with\n" +
			"--force-with-lease, so it is replaced only while nobody else has touched\n" +
			"it.\n\n" +
			"With --pull stk also opens a pull request through the GitHub CLI, based on\n" +
			"the branch's stack parent rather than trunk. Ancestors that are not on the\n" +
			"remote yet are pushed first, because a pull request cannot be based on a\n" +
			"branch that is not there. A pull request that is already open is left\n" +
			"exactly as it is.\n\n" +
			"Each pull request of the stack carries one stk comment listing the whole\n" +
			"chain in order, rewritten in place as the stack changes.\n\n" +
			"stk ss is stk submit --stack: it refreshes every branch of the stack at\n" +
			"once. Every other flag still applies, so stk ss -pn proposes the whole\n" +
			"stack without asking anything.\n\n" +
			"stk submit never merges and never deletes anything.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: branchNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.CalledAs() == "ss" {
				// The short form exists to say --stack; anything else on the
				// line still applies.
				wholeStack = true
			}
			if draft {
				// A draft is a kind of pull request, so asking for one is
				// asking for the other.
				pull = true
			}
			if noPrompt && !pull {
				// --no-prompt only shapes a pull request; on its own it says
				// nothing about pushing.
				noPrompt = false
			}
			a, err := open()
			if err != nil {
				return err
			}
			// A paused operation is reported before anything else: it leaves
			// the worktree mid-rebase with HEAD detached, so every other
			// diagnosis would be misleading.
			if err := operations.RequireNoOperation(a.Repo); err != nil {
				return err
			}
			target, err := a.resolveBranchArg(args)
			if err != nil {
				return err
			}
			if pull && !noPrompt {
				if !Interactive() {
					return errors.New("a pull request needs a title and body, and stk cannot ask\n\n" +
						"Generate them instead:\n\n    stk submit --pull --no-prompt")
				}
				a.Env.AskPullRequest = askPullRequest
			}
			return operations.Submit(a.Env, a.Graph, target, operations.SubmitOptions{
				Pull:      pull,
				Draft:     draft,
				NoPrompt:  noPrompt,
				Stack:     wholeStack,
				NoComment: noComment,
			})
		},
	}
	cmd.Flags().BoolVarP(&pull, "pull", "p", false, "open a pull request for the branch")
	cmd.Flags().BoolVarP(&draft, "draft", "d", false, "open the pull request as a draft (implies --pull)")
	cmd.Flags().BoolVarP(&noPrompt, "no-prompt", "n", false, "do not ask for a title or body; use the generated ones")
	cmd.Flags().BoolVarP(&wholeStack, "stack", "s", false, "submit every branch in the stack, each onto its parent")
	cmd.Flags().BoolVar(&noComment, "no-comment", false, "do not write or update the stack comment on the pull requests")
	return cmd
}

// askPullRequest collects a title and body, offering stk's suggestions.
//
// Dismissing either question cancels the whole submission rather than opening
// a pull request the user did not describe.
func askPullRequest(b *stack.Branch, defaultTitle, defaultBody string) (string, string, error) {
	fmt.Fprintln(os.Stderr)
	title, err := ui.ReadLineDefault(fmt.Sprintf("Title for the pull request for %s:", b.Name), defaultTitle)
	if err != nil {
		return "", "", err
	}
	if title == "" {
		return "", "", ui.ErrCancelled
	}
	body, err := ui.ReadParagraph("Body (one line per paragraph, blank line to finish, or accept):", defaultBody)
	if err != nil {
		return "", "", err
	}
	return title, body, nil
}
