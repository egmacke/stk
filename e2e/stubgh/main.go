// Command stubgh is a fake GitHub CLI for stk's end-to-end tests.
//
// It is installed on PATH as "gh" and answers the handful of calls stk makes,
// keeping pull requests and comments in a JSON file so a test can assert on
// what stk published and on what a second run does differently. Only the
// arguments stk actually passes are understood; anything else fails loudly, so
// a change in how stk calls gh cannot pass unnoticed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type pullRequest struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Base    string `json:"baseRefName"`
	Head    string `json:"headRefName"`
	IsDraft bool   `json:"isDraft"`
	State   string `json:"state"`
}

type comment struct {
	ID    int64  `json:"id"`
	Body  string `json:"body"`
	Login string `json:"login"`
}

// ghStack is one stack linked through gh stack link.
type ghStack struct {
	Number int    `json:"number"`
	Base   string `json:"base"`
	Remote string `json:"remote"`
	PRs    []int  `json:"prs"`
}

// remoteStack is a stack as GitHub holds it, which gh stack checkout can
// discover: a number, a base branch and pull requests bottom first.
type remoteStack struct {
	Number int    `json:"number"`
	Base   string `json:"base"`
	PRs    []int  `json:"prs"`
}

type state struct {
	NextPR       int                  `json:"nextPr"`
	NextStack    int                  `json:"nextStack"`
	NextComment  int64                `json:"nextComment"`
	PRs          []pullRequest        `json:"prs"`
	Comments     map[string][]comment `json:"comments"`
	Stacks       []ghStack            `json:"stacks"`
	RemoteStacks []remoteStack        `json:"remoteStacks"`
}

func statePath() string {
	if p := os.Getenv("GH_STATE"); p != "" {
		return p
	}
	fail("GH_STATE is not set")
	return ""
}

func load() *state {
	s := &state{NextPR: 1, NextComment: 1001, Comments: map[string][]comment{}}
	data, err := os.ReadFile(statePath())
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil {
		fail("unreadable state: %v", err)
	}
	if s.Comments == nil {
		s.Comments = map[string][]comment{}
	}
	if s.NextPR == 0 {
		s.NextPR = 1
	}
	if s.NextStack == 0 {
		s.NextStack = 1
	}
	if s.NextComment == 0 {
		s.NextComment = 1001
	}
	return s
}

func (s *state) save() {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(statePath(), append(data, '\n'), 0o644); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "stub gh: "+format+"\n", args...)
	os.Exit(1)
}

// flags reads the --name value pairs stk passes, and the bare switches.
func flags(args []string) (map[string]string, map[string]bool) {
	values := map[string]string{}
	switches := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if name, value, ok := strings.Cut(name, "="); ok {
			values[name] = value
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			values[name] = args[i+1]
			i++
			continue
		}
		switches[name] = true
	}
	return values, switches
}

func main() {
	args := os.Args[1:]
	if calls := os.Getenv("GH_CALLS"); calls != "" {
		f, err := os.OpenFile(calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(f, strings.Join(args, " "))
			f.Close()
		}
	}
	if len(args) < 2 {
		fail("expected a subcommand, got %q", strings.Join(args, " "))
	}

	switch args[0] + " " + args[1] {
	case "auth status":
		authStatus()
	case "pr list":
		listPRs(args[2:])
	case "pr create":
		createPR(args[2:])
	case "pr ready":
		readyPR(args[2:])
	case "pr close":
		closePR(args[2:])
	case "pr edit":
		editPR(args[2:])
	case "pr view":
		viewPR(args[2:])
	case "stack checkout":
		checkoutStack(args[2:])
	case "extension list":
		listExtensions()
	case "stack link":
		linkStack(args[2:])
	default:
		if args[0] == "api" {
			api(args[1:])
			return
		}
		fail("unexpected call: %s", strings.Join(args, " "))
	}
}

func authStatus() {
	if os.Getenv("GH_UNAUTHENTICATED") != "" {
		if _, err := os.Stat(os.Getenv("GH_UNAUTHENTICATED")); err == nil {
			fmt.Fprintln(os.Stderr, "gh: You are not logged into any GitHub hosts.")
			os.Exit(1)
		}
	}
	fmt.Println("Logged in to github.com account tester")
}

// flagged reports whether the file named by an environment variable exists,
// which is how a test flips one of the stub's behaviours.
func flagged(env string) bool {
	path := os.Getenv(env)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// listExtensions answers gh extension list in gh's tab-separated shape. The
// stack extension is installed unless a test says otherwise.
func listExtensions() {
	if flagged("GH_NO_STACK_EXTENSION") {
		return
	}
	fmt.Println("gh stack\tgithub/gh-stack\tv1.0.0")
}

// linkStack answers gh stack link <numbers...>: every argument stk passes is
// a pull request number, and pull requests already in a stack extend that
// stack rather than starting another.
func linkStack(args []string) {
	if flagged("GH_NO_STACKS") {
		fmt.Fprintln(os.Stderr, "✗ Stacked pull requests are not available for this repository")
		os.Exit(9)
	}
	values, _ := flags(args)
	if values["base"] == "" {
		fail("stack link without --base")
	}
	if values["remote"] == "" {
		fail("stack link without --remote")
	}
	var numbers []int
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			i++ // skip the value
			continue
		}
		n, err := strconv.Atoi(args[i])
		if err != nil {
			fail("stack link with a branch name %q; stk should pass pull request numbers", args[i])
		}
		numbers = append(numbers, n)
	}
	if len(numbers) < 2 {
		fail("stack link needs at least two pull requests")
	}
	s := load()
	known := map[int]bool{}
	for _, pr := range s.PRs {
		known[pr.Number] = true
	}
	for _, n := range numbers {
		if !known[n] {
			fail("no pull request %d", n)
		}
	}
	for i := range s.Stacks {
		for _, have := range s.Stacks[i].PRs {
			for _, n := range numbers {
				if have != n {
					continue
				}
				// Additive only, as the real thing is.
				for _, n := range numbers {
					if !contains(s.Stacks[i].PRs, n) {
						s.Stacks[i].PRs = append(s.Stacks[i].PRs, n)
					}
				}
				if s.Stacks[i].Number == 0 {
					s.Stacks[i].Number = s.NextStack
					s.NextStack++
				}
				s.save()
				recordLocalStack(s.Stacks[i].Number, heads(s, s.Stacks[i].PRs))
				fmt.Fprintf(os.Stderr, "Updated stack to %d PRs\n", len(s.Stacks[i].PRs))
				return
			}
		}
	}
	stack := ghStack{Number: s.NextStack, Base: values["base"], Remote: values["remote"], PRs: numbers}
	s.NextStack++
	s.Stacks = append(s.Stacks, stack)
	s.save()
	recordLocalStack(stack.Number, heads(s, numbers))
	fmt.Fprintf(os.Stderr, "Created stack with %d PRs\n", len(numbers))
}

// heads names the branches behind a list of pull requests.
func heads(s *state, numbers []int) []string {
	byNumber := map[int]pullRequest{}
	for _, pr := range s.PRs {
		byNumber[pr.Number] = pr
	}
	var out []string
	for _, n := range numbers {
		out = append(out, byNumber[n].Head)
	}
	return out
}

// recordLocalStack writes the stack's number into gh stack's local tracking,
// which is what the real gh stack link does once GitHub has answered: the
// stack is held on GitHub, and this is the only record of it in the checkout.
func recordLocalStack(number int, branches []string) {
	path := stackFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		// Nothing tracked here yet, so there is no stack to number.
		return
	}
	var local localStackFile
	if err := json.Unmarshal(data, &local); err != nil {
		fail("unreadable %s: %v", path, err)
	}
	wanted := map[string]bool{}
	for _, b := range branches {
		wanted[b] = true
	}
	for _, st := range local.Stacks {
		entries, _ := st["branches"].([]any)
		hit := false
		for _, e := range entries {
			m, _ := e.(map[string]any)
			if name, _ := m["branch"].(string); wanted[name] {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		st["number"] = number
		out, err := json.MarshalIndent(local, "", "  ")
		if err != nil {
			fail("%v", err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fail("%v", err)
		}
		return
	}
}

func contains(list []int, n int) bool {
	for _, have := range list {
		if have == n {
			return true
		}
	}
	return false
}

func listPRs(args []string) {
	values, _ := flags(args)
	head := values["head"]
	wantAll := values["state"] == "all"
	limit := 30
	if raw := values["limit"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			fail("bad --limit %q", raw)
		}
		limit = n
	}
	s := load()
	// Without --head this is the bulk listing stk uses to answer for a whole
	// stack at once. gh returns the newest first, so the stub does too.
	matched := []pullRequest{}
	for i := len(s.PRs) - 1; i >= 0; i-- {
		pr := s.PRs[i]
		if head != "" && pr.Head != head {
			continue
		}
		if !wantAll && pr.State != "OPEN" {
			continue
		}
		if len(matched) >= limit {
			break
		}
		matched = append(matched, pr)
	}
	out, _ := json.Marshal(matched)
	fmt.Println(string(out))
}

func editPR(args []string) {
	values, _ := flags(args)
	n := prNumber(args, values)
	base := values["base"]
	if base == "" {
		fail("only --base is understood by the stub: %s", strings.Join(args, " "))
	}
	s := load()
	for i := range s.PRs {
		if s.PRs[i].Number == n {
			s.PRs[i].Base = base
			s.save()
			return
		}
	}
	fail("no pull request %d", n)
}

func createPR(args []string) {
	values, switches := flags(args)
	s := load()
	for _, pr := range s.PRs {
		// GitHub refuses a second open pull request for the same head, but a
		// closed or merged one is no obstacle.
		if pr.Head == values["head"] && pr.State == "OPEN" {
			fail("a pull request for %s already exists", pr.Head)
		}
	}
	pr := pullRequest{
		Number:  s.NextPR,
		Title:   values["title"],
		Body:    values["body"],
		Base:    values["base"],
		Head:    values["head"],
		IsDraft: switches["draft"],
		State:   "OPEN",
	}
	pr.URL = fmt.Sprintf("https://github.com/example/repo/pull/%d", pr.Number)
	s.NextPR++
	s.PRs = append(s.PRs, pr)
	s.save()
	fmt.Println(pr.URL)
}

// viewPR answers gh pr view <n> --json state.
func viewPR(args []string) {
	values, _ := flags(args)
	number := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && values["repo"] != a && values["json"] != a {
			number = a
			break
		}
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		fail("bad pull request number %q", number)
	}
	for _, pr := range load().PRs {
		if pr.Number == n {
			out, _ := json.Marshal(map[string]string{"state": pr.State})
			fmt.Println(string(out))
			return
		}
	}
	fail("no pull request %d", n)
}

// gitOut runs git in the current directory, as gh stack would, and fails
// loudly when git does.
func gitOut(args ...string) string {
	cmd := exec.Command("git", args...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		fail("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String())
}

// localStackFile is gh stack's tracking file, resolved the way gh stack
// resolves it: through git rev-parse --git-dir from the current directory.
type localStackFile struct {
	SchemaVersion int              `json:"schemaVersion"`
	Repository    string           `json:"repository"`
	Stacks        []map[string]any `json:"stacks"`
}

func stackFilePath() string {
	return filepath.Join(gitOut("rev-parse", "--git-dir"), "gh-stack")
}

// checkoutStack answers gh stack checkout <pr-number | pr-url | stack-number>
// the way the real one does for a stack it does not yet track: it finds the
// stack on GitHub, fetches its branches, writes its local tracking and checks
// out the branch asked for. A stack it already tracks with a different
// composition is refused with exit code 3, which is what the real one does
// when it cannot ask.
func checkoutStack(args []string) {
	if len(args) != 1 {
		fail("stack checkout wants exactly one argument, got %q", strings.Join(args, " "))
	}
	ref := args[0]
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	n, err := strconv.Atoi(ref)
	if err != nil {
		fail("stack checkout with a branch name %q; stk should pass a pull request", args[0])
	}
	s := load()
	byNumber := map[int]pullRequest{}
	for _, pr := range s.PRs {
		byNumber[pr.Number] = pr
	}
	var found *remoteStack
	target := ""
	for i := range s.RemoteStacks {
		rs := &s.RemoteStacks[i]
		if rs.Number == n {
			found, target = rs, byNumber[rs.PRs[len(rs.PRs)-1]].Head
			break
		}
		for _, pr := range rs.PRs {
			if pr == n {
				found, target = rs, byNumber[n].Head
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		fmt.Fprintf(os.Stderr, "✗ no stack found for %s\n", args[0])
		os.Exit(2)
	}
	var branches []string
	for _, pr := range found.PRs {
		branches = append(branches, byNumber[pr].Head)
	}

	// Composition check against the local tracking.
	path := stackFilePath()
	local := localStackFile{SchemaVersion: 1, Stacks: []map[string]any{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &local); err != nil {
			fail("unreadable %s: %v", path, err)
		}
	}
	for _, st := range local.Stacks {
		entries, _ := st["branches"].([]any)
		var names []string
		hit := false
		for _, e := range entries {
			m, _ := e.(map[string]any)
			name, _ := m["branch"].(string)
			names = append(names, name)
			for _, b := range branches {
				if b == name {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		if strings.Join(names, " ") == strings.Join(branches, " ") {
			gitOut("checkout", "-q", target)
			fmt.Fprintln(os.Stderr, "✓ Local stack matches remote — switching to branch")
			return
		}
		fmt.Fprintln(os.Stderr, "✗ local stack composition differs from remote")
		os.Exit(3)
	}

	// Fetch and create the branches, then record the stack.
	prev := found.Base
	var refs []map[string]any
	for i, name := range branches {
		gitOut("fetch", "-q", "origin", name)
		if exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+name).Run() != nil {
			gitOut("branch", "--track", name, "origin/"+name)
		}
		pr := byNumber[found.PRs[i]]
		refs = append(refs, map[string]any{
			"branch":      name,
			"base":        gitOut("rev-parse", "refs/heads/"+prev),
			"pullRequest": map[string]any{"number": pr.Number, "url": pr.URL},
		})
		prev = name
	}
	local.Stacks = append(local.Stacks, map[string]any{
		"number":   found.Number,
		"trunk":    map[string]any{"branch": found.Base, "head": gitOut("rev-parse", "refs/heads/"+found.Base)},
		"branches": refs,
	})
	data, err := json.MarshalIndent(local, "", "  ")
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fail("%v", err)
	}
	gitOut("checkout", "-q", target)
	fmt.Fprintf(os.Stderr, "✓ Checked out stack #%d at %s\n", found.Number, target)
}

func readyPR(args []string) {
	values, switches := flags(args)
	n := prNumber(args, values)
	s := load()
	for i := range s.PRs {
		if s.PRs[i].Number == n {
			s.PRs[i].IsDraft = switches["undo"]
			s.save()
			return
		}
	}
	fail("no pull request %d", n)
}

func closePR(args []string) {
	values, _ := flags(args)
	n := prNumber(args, values)
	s := load()
	for i := range s.PRs {
		if s.PRs[i].Number == n {
			s.PRs[i].State = "CLOSED"
			if c := values["comment"]; c != "" {
				key := strconv.Itoa(n)
				s.Comments[key] = append(s.Comments[key], comment{ID: s.NextComment, Body: c, Login: "tester"})
				s.NextComment++
			}
			s.save()
			return
		}
	}
	fail("no pull request %d", n)
}

// prNumber picks the positional pull request number out of the arguments.
func prNumber(args []string, values map[string]string) int {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		skip := false
		for _, v := range values {
			if v == a {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		n, err := strconv.Atoi(a)
		if err != nil {
			continue
		}
		return n
	}
	fail("no pull request number in: %s", strings.Join(args, " "))
	return 0
}

// api answers the comment endpoints stk uses:
//
//	GET   /repos/<owner>/<name>/issues/<number>/comments
//	POST  /repos/<owner>/<name>/issues/<number>/comments
//	PATCH /repos/<owner>/<name>/issues/comments/<id>
func api(args []string) {
	values, _ := flags(args)
	method := strings.ToUpper(values["method"])
	if method == "" {
		method = "GET"
	}
	path := ""
	for _, a := range args {
		if strings.HasPrefix(a, "/repos/") {
			path = a
		}
	}
	if path == "" {
		fail("no API path in: %s", strings.Join(args, " "))
	}
	body := ""
	for i, a := range args {
		// stk passes the body as: -f body=<text>, or --raw-field body=<text>
		if (a == "-f" || a == "--raw-field" || a == "-F" || a == "--field") && i+1 < len(args) {
			if text, ok := strings.CutPrefix(args[i+1], "body="); ok {
				body = text
			}
		}
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	s := load()
	// One pull request by number, which stk reads for branches that are gone.
	if method == "GET" && len(parts) == 5 && parts[3] == "pulls" {
		n, err := strconv.Atoi(parts[4])
		if err != nil {
			fail("bad pull request number %q", parts[4])
		}
		for _, pr := range s.PRs {
			if pr.Number == n {
				out, _ := json.Marshal(pr)
				fmt.Println(string(out))
				return
			}
		}
		fail("no pull request %d", n)
	}
	// The base of a pull request is changed through REST, because gh pr edit
	// asks for scopes a base change does not need.
	if method == "PATCH" && len(parts) == 5 && parts[3] == "pulls" {
		base := ""
		for i, a := range args {
			if (a == "-f" || a == "--raw-field") && i+1 < len(args) {
				if v, ok := strings.CutPrefix(args[i+1], "base="); ok {
					base = v
				}
			}
		}
		n, err := strconv.Atoi(parts[4])
		if err != nil {
			fail("bad pull request number %q", parts[4])
		}
		for i := range s.PRs {
			if s.PRs[i].Number == n {
				s.PRs[i].Base = base
				s.save()
				out, _ := json.Marshal(s.PRs[i])
				fmt.Println(string(out))
				return
			}
		}
		fail("no pull request %d", n)
	}
	switch {
	case method == "GET" && len(parts) == 6 && parts[3] == "issues" && parts[5] == "comments":
		// One JSON object per line, which is what gh's --jq projection emits.
		for _, c := range s.Comments[parts[4]] {
			out, _ := json.Marshal(c)
			fmt.Println(string(out))
		}
	case method == "POST" && len(parts) == 6 && parts[3] == "issues" && parts[5] == "comments":
		c := comment{ID: s.NextComment, Body: body, Login: "tester"}
		s.NextComment++
		s.Comments[parts[4]] = append(s.Comments[parts[4]], c)
		s.save()
		out, _ := json.Marshal(c)
		fmt.Println(string(out))
	case method == "PATCH" && len(parts) == 5 && parts[3] == "issues" && parts[4] == "comments":
		fail("no comment id in %s", path)
	case method == "PATCH" && len(parts) == 6 && parts[3] == "issues" && parts[4] == "comments":
		id, err := strconv.ParseInt(parts[5], 10, 64)
		if err != nil {
			fail("bad comment id %q", parts[5])
		}
		for pr, list := range s.Comments {
			for i := range list {
				if list[i].ID == id {
					s.Comments[pr][i].Body = body
					s.save()
					out, _ := json.Marshal(s.Comments[pr][i])
					fmt.Println(string(out))
					return
				}
			}
		}
		fail("no comment %d", id)
	default:
		fail("unexpected %s %s", method, path)
	}
}
