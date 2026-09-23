package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/operations"
	"github.com/egmacke/stk/internal/stack"
	"github.com/egmacke/stk/internal/ui"
)

func newSubmitCmd() *cobra.Command {
	var pull, draft, noPrompt, wholeStack, noComment, updateOnly, noLink, force bool
	var draftFrom string
	var draftBranches []string
	cmd := &cobra.Command{
		Use:     "submit [branch]",
		Aliases: []string{"s", "ss"},
		Short:   "Push a branch to the remote and optionally open a pull request",
		Long: "Pushes the branch to the configured remote, creating it there when it does\n" +
			"not exist yet. A branch a restack has rewritten is pushed with\n" +
			"--force-with-lease, so it is replaced only while nobody else has touched\n" +
			"it.\n\n" +
			"--force is what gets past that lease when it is in the way: the remote\n" +
			"branch is replaced with what you have, whatever it holds, and any commit\n" +
			"on it that is not in yours is lost. It is also the one switch that asks\n" +
			"nothing, so it implies --no-prompt. A branch that fast-forwards is pushed\n" +
			"the same way either way; --force only ever changes a diverged one.\n\n" +
			"With --pull stk also opens a pull request through the GitHub CLI, based on\n" +
			"the branch's stack parent rather than trunk. Ancestors that are not on the\n" +
			"remote yet are pushed first, because a pull request cannot be based on a\n" +
			"branch that is not there. A pull request that is already open is left\n" +
			"exactly as it is.\n\n" +
			"Each pull request of the stack carries one stk comment listing the whole\n" +
			"chain in order, rewritten in place as the stack changes. With\n" +
			"stk.githubStacks = true the pull requests are instead linked as a stack on\n" +
			"GitHub itself, through the gh stack extension, and no comment is written.\n\n" +
			"With --no-prompt there is nobody to ask, so stk writes the title itself:\n" +
			"the branch name, or, with stk.prTitle = commit, the subject of the\n" +
			"branch's first commit. The prompt offers that subject either way.\n\n" +
			"stk s is this command; stk ss is stk submit --stack, which refreshes every\n" +
			"branch of the stack at once. Every other flag still applies, so stk ss -pn\n" +
			"proposes the whole stack without asking anything.\n\n" +
			"With --update stk refreshes only the pull requests that already exist and\n" +
			"opens none, so a restacked stack can be republished without proposing work\n" +
			"that is not ready to be looked at.\n\n" +
			"Draft state is decided per branch. With none of the draft flags given, and\n" +
			"more than one pull request to open, stk asks where the stack stops being\n" +
			"ready: the branch you pick and everything below it open for review, and\n" +
			"everything above it opens as a draft.\n\n" +
			"stk submit never merges and never deletes anything.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: branchNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.CalledAs() == "ss" {
				// The short form exists to say --stack; anything else on the
				// line still applies.
				wholeStack = true
			}
			if force {
				// --force is the switch for a run that must go through
				// without being held up, and a question it stops on would
				// hold it up exactly as a declined lease does.
				noPrompt = true
			}
			drafts := operations.DraftChoice{All: draft, From: draftFrom, Branches: draftBranches}
			if err := drafts.Validate(); err != nil {
				return err
			}
			if updateOnly && !drafts.Empty() {
				return errors.New("--update opens no pull request, so there is no draft state to set")
			}
			if !drafts.Empty() || updateOnly {
				// Every draft flag is about a pull request, and so is
				// --update, so asking for either is asking for --pull.
				pull = true
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
			if pull && !noPrompt && !updateOnly && Interactive() {
				// Whether the questions are actually needed depends on what
				// is already open, which only the operation knows; it fails
				// before publishing anything if it needs an answer stk cannot
				// give.
				a.Env.AskPullRequest = askPullRequest
				a.Env.AskDraftCutLine = askDraftCutLine(a)
			}
			return operations.Submit(a.Env, a.Graph, target, operations.SubmitOptions{
				Pull:       pull,
				Drafts:     drafts,
				NoPrompt:   noPrompt,
				Stack:      wholeStack,
				NoComment:  noComment,
				UpdateOnly: updateOnly,
				NoLink:     noLink,
				Force:      force,
			})
		},
	}
	cmd.Flags().BoolVarP(&pull, "pull", "p", false, "open a pull request for the branch")
	cmd.Flags().BoolVarP(&draft, "draft", "d", false, "open every new pull request as a draft (implies --pull)")
	cmd.Flags().StringVar(&draftFrom, "draft-from", "", "open `branch` and everything above it as drafts (implies --pull)")
	cmd.Flags().StringArrayVar(&draftBranches, "draft-branch", nil, "open this `branch` as a draft; repeatable (implies --pull)")
	cmd.Flags().BoolVarP(&noPrompt, "no-prompt", "n", false, "do not ask for a title, body or draft state; use the generated ones (see stk.prTitle)")
	cmd.Flags().BoolVarP(&updateOnly, "update", "u", false, "refresh the pull requests that already exist; never open one (implies --pull)")
	cmd.Flags().BoolVarP(&wholeStack, "stack", "s", false, "submit every branch in the stack, each onto its parent")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace a diverged remote branch whatever it holds, and ask nothing (implies --no-prompt)")
	cmd.Flags().BoolVar(&noComment, "no-comment", false, "do not write or update the stack comment on the pull requests")
	cmd.Flags().BoolVar(&noLink, "no-link", false, "do not link the pull requests as a stack on GitHub (with stk.githubStacks)")
	_ = cmd.RegisterFlagCompletionFunc("draft-from", branchNameCompletion)
	_ = cmd.RegisterFlagCompletionFunc("draft-branch", branchNameCompletion)
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

// askDraftCutLine asks where the stack stops being ready for review.
//
// The candidates run bottom to top with trunk at the head, so choosing trunk
// says nothing is ready yet and choosing the topmost branch says everything
// is. Dismissing the question opens no drafts, which is what stk did before
// there was a question to ask.
func askDraftCutLine(a *app) func([]*stack.Branch) (*stack.Branch, error) {
	return func(candidates []*stack.Branch) (*stack.Branch, error) {
		name, err := promptBranch(a.Graph, branchPrompt{
			Title:      "Ready for review up to (everything above it opens as a draft)",
			Candidates: candidates,
			Missing:    "no draft choice given",
			Empty:      "no branch to choose from",
		})
		if cancelled(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, b := range candidates {
			if b.Name == name {
				return b, nil
			}
		}
		return nil, fmt.Errorf("%q is not one of the branches being submitted", name)
	}
}
