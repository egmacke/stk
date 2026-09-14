package operations

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/egmacke/stk/internal/forge"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// stackCommentMarker identifies the one comment stk maintains on a pull
// request. It is an HTML comment, so it is invisible on the page, and it is
// what lets stk rewrite its own note without ever touching a comment somebody
// else wrote.
const stackCommentMarker = "<!-- stk:stack -->"

// stackEntry is one pull request in the rendered order of the stack.
//
// Landed says the branch has left the stack: the pull request stays in the
// list, because a review is easier to follow when the part of the stack that
// has already gone in is still named.
type stackEntry struct {
	Branch  string
	Number  int
	Current bool
	Landed  string // "merged", "closed", or empty while the branch is live
}

// entryLine matches a line stk wrote itself, which is how the merged part of
// a stack survives: by the time a pull request lands, its branch is gone from
// the local graph, and stk's own comment is the only record of where it sat.
var entryLine = regexp.MustCompile("^[0-9]+\\. #([0-9]+) `([^`]+)`")

// parseStackComment reads the entries out of a body stk wrote earlier.
func parseStackComment(body string) []stackEntry {
	var out []stackEntry
	for _, line := range strings.Split(body, "\n") {
		m := entryLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		number, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, stackEntry{Number: number, Branch: m[2]})
	}
	return out
}

// mergeStackHistory keeps the shape recorded in prior and folds the live stack
// into it.
//
// Stacks grow at the top and land at the bottom, so the recorded order is
// authoritative and anything new goes on the end. An entry whose branch is no
// longer in the stack is kept only when its pull request has actually finished
// — a still-open pull request that has left the stack is somebody else's
// business.
func mergeStackHistory(prior, live []stackEntry, state func(int) string) []stackEntry {
	liveByNumber := map[int]stackEntry{}
	for _, e := range live {
		liveByNumber[e.Number] = e
	}

	var out []stackEntry
	seen := map[int]bool{}
	for _, p := range prior {
		if seen[p.Number] {
			continue
		}
		seen[p.Number] = true
		if e, ok := liveByNumber[p.Number]; ok {
			out = append(out, e)
			continue
		}
		switch landed := state(p.Number); landed {
		case "merged", "closed":
			p.Landed = landed
			out = append(out, p)
		default:
			// Open but out of the stack: not this stack's to describe.
		}
	}
	for _, e := range live {
		if !seen[e.Number] {
			seen[e.Number] = true
			out = append(out, e)
		}
	}
	return out
}

// renderStackComment writes the note that goes on every pull request of a
// stack: the whole chain, bottom first, with the reader's own pull request
// marked and the parts that have landed still named.
func renderStackComment(entries []stackEntry, current int) string {
	var b strings.Builder
	b.WriteString(stackCommentMarker)
	b.WriteString("\n### Stack\n\n")
	landed := 0
	for i, e := range entries {
		line := fmt.Sprintf("%d. #%d `%s`", i+1, e.Number, e.Branch)
		switch {
		case e.Landed != "":
			line += " — " + e.Landed
			landed++
		case e.Number == current:
			line += " ← this pull request"
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\nEach pull request is based on the one before it, so merge them in the order\n")
	b.WriteString("listed, starting with the first.\n")
	if landed > 0 {
		b.WriteString("\nThe ones already in are kept in the list, so the shape of the stack still\nreads the same as it did.\n")
	}
	b.WriteString("\nMaintained by `stk submit`.")
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
// stack, the landed part included.
//
// It is written once and then edited in place: the marker identifies stk's own
// comment, an unchanged body is left alone entirely, and a stack of one live
// pull request gets no comment at all — though an existing one is still
// corrected, so a note stk wrote never lies about the shape of the stack.
func syncStackComments(env *Env, gh *forge.GH, g *stack.Graph, target *stack.Branch, prs *pullRequestCache) (int, error) {
	chain, err := PlanBranches(g, target, ScopeStack)
	if err != nil {
		return 0, err
	}
	var live []stackEntry
	for _, b := range chain {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return 0, err
		}
		if pr != nil {
			live = append(live, stackEntry{Branch: b.Name, Number: pr.Number})
		}
	}
	if len(live) == 0 {
		return 0, nil
	}
	if env.DryRun {
		if len(live) < 2 {
			return 0, nil
		}
		for _, e := range live {
			env.Out.Dry("would write the stack comment on %s", output.Bold(fmt.Sprintf("#%d", e.Number)))
		}
		return len(live), nil
	}

	// Read every comment first: stk's own note is where the landed part of the
	// stack is recorded, and the most complete record wins.
	existing := map[int]*forge.Comment{}
	var prior []stackEntry
	for _, e := range live {
		comments, err := gh.Comments(e.Number)
		if err != nil {
			return 0, err
		}
		if c := findStackComment(comments); c != nil {
			existing[e.Number] = c
			if recorded := parseStackComment(c.Body); len(recorded) > len(prior) {
				prior = recorded
			}
		}
	}

	entries := mergeStackHistory(prior, live, landedState(gh))
	if len(entries) < 2 && len(existing) == 0 {
		// Not a stack worth annotating, and nothing already says otherwise.
		return 0, nil
	}

	changed := 0
	for _, e := range live {
		body := renderStackComment(entries, e.Number)
		switch c := existing[e.Number]; {
		case c == nil:
			if err := gh.AddComment(e.Number, body); err != nil {
				return changed, err
			}
			env.Out.OK("Commented the stack on %s", output.Bold(fmt.Sprintf("#%d", e.Number)))
			changed++
		case c.Body == body:
			// Already says exactly this; leave the timeline alone.
		default:
			if err := gh.UpdateComment(c.ID, body); err != nil {
				return changed, err
			}
			env.Out.OK("Updated the stack comment on %s", output.Bold(fmt.Sprintf("#%d", e.Number)))
			changed++
		}
	}
	return changed, nil
}

// landedState reports how a pull request finished, asking the forge once per
// number and remembering the answer.
func landedState(gh *forge.GH) func(int) string {
	cache := map[int]string{}
	return func(number int) string {
		if state, asked := cache[number]; asked {
			return state
		}
		state := ""
		if pr, err := gh.PullRequestByNumber(number); err == nil && pr != nil {
			switch pr.State {
			case "MERGED":
				state = "merged"
			case "CLOSED":
				state = "closed"
			}
		}
		cache[number] = state
		return state
	}
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
