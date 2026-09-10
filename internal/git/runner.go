// Package git is the only place in stk that talks to the git executable.
//
// Everything is passed to exec.Command as argv; no shell strings are ever
// constructed.
package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Result is the outcome of one git invocation.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// OK reports whether git ran and exited zero.
func (r Result) OK() bool { return r.Err == nil && r.ExitCode == 0 }

// Out is stdout with the trailing newline removed.
func (r Result) Out() string { return strings.TrimRight(r.Stdout, "\n") }

// Lines is stdout split into non-empty lines.
func (r Result) Lines() []string {
	out := r.Out()
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Error turns a non-zero result into an error carrying git's own message.
func (r Result) Error() error {
	if r.OK() {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" && r.Err != nil {
		msg = r.Err.Error()
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", r.ExitCode)
	}
	return fmt.Errorf("git %s: %s", strings.Join(r.Args, " "), msg)
}

// Runner executes git in a fixed working directory.
type Runner struct {
	Dir     string
	Env     []string
	Verbose bool
	DryRun  bool
	Log     io.Writer
}

// NewRunner returns a Runner rooted at dir.
func NewRunner(dir string) *Runner { return &Runner{Dir: dir, Log: os.Stderr} }

// WithDir returns a copy of the runner rooted at another directory. Used to
// operate on a sibling worktree without changing the current one.
func (r *Runner) WithDir(dir string) *Runner {
	c := *r
	c.Dir = dir
	return &c
}

func (r *Runner) logf(format string, args ...any) {
	if !r.Verbose || r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, format+"\n", args...)
}

func (r *Runner) command(args []string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	return cmd
}

func finish(res *Result, err error) {
	if err == nil {
		return
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return
	}
	res.Err = err
	res.ExitCode = -1
}

// Run executes git and captures its output. Read-only queries use this, so it
// ignores DryRun.
func (r *Runner) Run(args ...string) Result {
	res := Result{Args: args}
	var stdout, stderr strings.Builder
	cmd := r.command(args)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	r.logf("+ git %s", strings.Join(args, " "))
	finish(&res, cmd.Run())
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res
}

// RunInput is Run with data fed to git on stdin.
func (r *Runner) RunInput(stdin string, args ...string) Result {
	res := Result{Args: args}
	var stdout, stderr strings.Builder
	cmd := r.command(args)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	r.logf("+ git %s", strings.Join(args, " "))
	finish(&res, cmd.Run())
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res
}

// Mutate is Run for commands that change the repository. Under DryRun it
// reports success without executing anything.
func (r *Runner) Mutate(args ...string) Result {
	if r.DryRun {
		r.logf("(dry-run) git %s", strings.Join(args, " "))
		return Result{Args: args}
	}
	return r.Run(args...)
}

// Capture runs git with stdout and stderr captured but the real stdin
// attached, so credential and signing helpers can still reach the terminal
// while stk keeps control of what is printed.
func (r *Runner) Capture(args ...string) Result {
	res := Result{Args: args}
	if r.DryRun {
		r.logf("(dry-run) git %s", strings.Join(args, " "))
		return res
	}
	var stdout, stderr strings.Builder
	cmd := r.command(args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	r.logf("+ git %s", strings.Join(args, " "))
	finish(&res, cmd.Run())
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res
}

// Interactive runs git with the process's own stdio attached, so git can drive
// an editor or print progress. Used for rebase and passthrough.
func (r *Runner) Interactive(args ...string) Result {
	res := Result{Args: args}
	if r.DryRun {
		r.logf("(dry-run) git %s", strings.Join(args, " "))
		return res
	}
	cmd := r.command(args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	r.logf("+ git %s", strings.Join(args, " "))
	finish(&res, cmd.Run())
	return res
}

// Passthrough forwards an unrecognised stk command to git verbatim and returns
// git's exit code.
func (r *Runner) Passthrough(args ...string) int {
	cmd := r.command(args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "stk: %v\n", err)
		return 128
	}
	return 0
}
