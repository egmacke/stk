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
	p.Printf(SymOK+" "+format, args...)
}

// Skip reports a step that was deliberately not performed.
func (p *Printer) Skip(format string, args ...any) {
	p.Printf(SymSkipped+" "+format, args...)
}

// Fail reports a failed step on stderr.
func (p *Printer) Fail(format string, args ...any) {
	p.Warnf(SymFailed+" "+format, args...)
}

// JSON writes an indented JSON document to stdout.
func (p *Printer) JSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
