// Package output renders stk results as text or JSON.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Status symbols shared by every command that reports branch state.
const (
	SymOK       = "✓"
	SymSkipped  = "⊘"
	SymFailed   = "✗"
	SymCurrent  = "←"
	SymRestack  = "!"
	SymProblem  = "?"
	SymWorktree = "@"
	SymDirty    = "*"
	SymAhead    = "↑"
	SymBehind   = "↓"
)

// Printer writes human-readable output, honouring --quiet.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	Quiet bool
}

// New returns a Printer writing to the process's streams.
func New() *Printer { return &Printer{Out: os.Stdout, Err: os.Stderr} }

// Printf writes a line to stdout unless --quiet is set.
func (p *Printer) Printf(format string, args ...any) {
	if p.Quiet {
		return
	}
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Raw writes to stdout regardless of --quiet. Used for data the caller asked
// for explicitly, such as a branch name or JSON.
func (p *Printer) Raw(format string, args ...any) {
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Warnf writes a line to stderr. Warnings are not suppressed by --quiet.
func (p *Printer) Warnf(format string, args ...any) {
	fmt.Fprintf(p.Err, format+"\n", args...)
}

// OK reports a successful step.
func (p *Printer) OK(format string, args ...any) {
	p.Printf(Green(SymOK)+" "+format, args...)
}

// Skip reports a step that was deliberately not performed. It is dimmed:
// nothing happened, so it should not compete with the steps that did.
func (p *Printer) Skip(format string, args ...any) {
	p.Printf(Dim(SymSkipped)+" "+format, args...)
}

// Fail reports a failed step on stderr.
func (p *Printer) Fail(format string, args ...any) {
	p.Warnf(Red(SymFailed)+" "+format, args...)
}

// Dry reports the action a command would have taken. The format describes the
// action alone ("would push %s"); the marker is added here so every dry run
// looks the same.
func (p *Printer) Dry(format string, args ...any) {
	p.Printf(Dim("(dry-run)")+" "+format, args...)
}

// JSON writes an indented JSON document to stdout.
func (p *Printer) JSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
