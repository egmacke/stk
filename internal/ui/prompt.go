package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// ErrCancelled is returned when the user dismisses an interactive prompt.
var ErrCancelled = errors.New("cancelled")

// IsTerminal reports whether the process is attached to an interactive
// terminal on both stdin and stdout.
func IsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// Confirm asks a yes/no question on stderr and reads the answer from stdin.
func Confirm(question string, defaultYes bool) (bool, error) {
	suffix := "(y/N)"
	if defaultYes {
		suffix = "(Y/n)"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, suffix)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, nil
	}
}
