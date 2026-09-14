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

type state struct {
	NextPR      int                  `json:"nextPr"`
	NextComment int64                `json:"nextComment"`
	PRs         []pullRequest        `json:"prs"`
	Comments    map[string][]comment `json:"comments"`
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

func listPRs(args []string) {
	values, _ := flags(args)
	head := values["head"]
	s := load()
	for _, pr := range s.PRs {
		if pr.Head == head {
			out, _ := json.Marshal([]pullRequest{pr})
			fmt.Println(string(out))
			return
		}
	}
	fmt.Println("[]")
}

func createPR(args []string) {
	values, switches := flags(args)
	s := load()
	for _, pr := range s.PRs {
		if pr.Head == values["head"] {
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

func readyPR(args []string) {
	values, switches := flags(args)
	number := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && values["repo"] != a {
			number = a
			break
		}
	}
	if number == "" {
		fail("no pull request number in: %s", strings.Join(args, " "))
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		fail("bad pull request number %q", number)
	}
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
