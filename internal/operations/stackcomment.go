package operations

import (
	"fmt"
	"strings"

	"stk/internal/forge"
	"stk/internal/stack"
)

// stackCommentMarker identifies the one comment stk maintains on a pull
// request. It is an HTML comment, so it is invisible on the page, and it is
// what lets stk rewrite its own note without ever touching a comment somebody
// else wrote.
const stackCommentMarker = "<!-- stk:stack -->"

// stackEntry is one pull request in the rendered order of the stack.
type stackEntry struct {
	Branch string
	PR     *forge.PullRequest
}

// renderStackComment writes the note that goes on every pull request of a
// stack: the whole chain, bottom first, with the reader's own pull request
// marked.
func renderStackComment(entries []stackEntry, current int) string {
	var b strings.Builder
	b.WriteString(stackCommentMarker)
	b.WriteString("\n### Stack\n\n")
	for i, e := range entries {
		line := fmt.Sprintf("%d. #%d `%s`", i+1, e.PR.Number, e.Branch)
		if e.PR.Number == current {
			line += " ← this pull request"
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\nEach pull request is based on the one before it, so merge them in the order\n")
	b.WriteString("listed, starting with the first.\n\nMaintained by `stk submit`.")
	return b.String()
}

// findStackComment returns the comment stk maintains, or nil.
func findStackComment(comments []forge.Comment) *forge.Comment {
	for i := range comments {
		if strings.Contains(comments[i].Body, stackCommentMarker) {
			return &comments[i]
		}
	}
	return nil
}

// syncStackComments keeps one comment per pull request listing the whole
// stack.
//
// It is written once and then edited in place: the marker identifies stk's own
// comment, an unchanged body is left alone entirely, and a stack of one pull
// request gets no comment at all — though an existing one is still corrected,
// so a note stk wrote never lies about the shape of the stack.
func syncStackComments(env *Env, gh *forge.GH, g *stack.Graph, target *stack.Branch, prs *pullRequestCache) (int, error) {
	chain, err := PlanBranches(g, target, ScopeStack)
	if err != nil {
		return 0, err
	}
	var entries []stackEntry
	for _, b := range chain {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return 0, err
		}
		if pr != nil {
			entries = append(entries, stackEntry{Branch: b.Name, PR: pr})
		}
	}
	if len(entries) == 0 {
		return 0, nil
	}

	changed := 0
	for _, e := range entries {
		body := renderStackComment(entries, e.PR.Number)
		if env.DryRun {
			if len(entries) < 2 {
				continue
			}
			env.Out.Printf("(dry-run) would write the stack comment on #%d", e.PR.Number)
			changed++
			continue
		}
		comments, err := gh.Comments(e.PR.Number)
		if err != nil {
			return changed, err
		}
		existing := findStackComment(comments)
		switch {
		case existing == nil && len(entries) < 2:
			// A single pull request is not a stack worth annotating.
		case existing == nil:
			if err := gh.AddComment(e.PR.Number, body); err != nil {
				return changed, err
			}
			env.Out.OK("Commented the stack on #%d", e.PR.Number)
			changed++
		case existing.Body == body:
			// Already says exactly this; leave the timeline alone.
		default:
			if err := gh.UpdateComment(existing.ID, body); err != nil {
				return changed, err
			}
			env.Out.OK("Updated the stack comment on #%d", e.PR.Number)
			changed++
		}
	}
	return changed, nil
}

// pullRequestCache remembers the open pull request of each branch, so one
// submit asks gh about a branch once however many times it comes up.
type pullRequestCache struct {
	byBranch map[string]*forge.PullRequest
}

func newPullRequestCache() *pullRequestCache {
	return &pullRequestCache{byBranch: map[string]*forge.PullRequest{}}
}

// open returns the branch's open pull request, or nil when it has none.
func (c *pullRequestCache) open(gh *forge.GH, branch string) (*forge.PullRequest, error) {
	if pr, asked := c.byBranch[branch]; asked {
		return pr, nil
	}
	pr, err := gh.OpenPullRequest(branch)
	if err != nil {
		return nil, err
	}
	c.byBranch[branch] = pr
	return pr, nil
}

// record stores a pull request stk has just opened.
func (c *pullRequestCache) record(branch string, pr *forge.PullRequest) {
	c.byBranch[branch] = pr
}
