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

// stdin is shared by every prompt: a reader buffers past the newline it was
// asked for, so a fresh one per question would swallow the answer to the next.
var stdin = bufio.NewReader(os.Stdin)

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
	line, err := ask(fmt.Sprintf("%s %s", question, suffix))
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
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

// ReadLine asks an open question on stderr and returns the trimmed answer.
//
// An empty answer, or end of input, is treated as a cancellation rather than
// as a value: stk never acts on a name the user did not type.
func ReadLine(question string) (string, error) {
	answer, err := ask(question)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return "", ErrCancelled
	}
	return answer, nil
}

// ReadLineDefault asks an open question, offering def when the user answers
// with an empty line.
//
// Unlike ReadLine an empty answer is a value, because the default is one stk
// has already shown and the user is accepting it.
func ReadLineDefault(question, def string) (string, error) {
	prompt := question
	if def != "" {
		prompt = fmt.Sprintf("%s [%s]", question, def)
	}
	answer, err := ask(prompt)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// ReadParagraph collects a multi-line answer, ending at a blank line or at the
// end of input.
//
// The default is printed rather than pre-typed, since a terminal cannot offer
// several lines for editing; answering nothing at all accepts it.
func ReadParagraph(question, def string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s\n", question)
	for _, line := range strings.Split(def, "\n") {
		if line != "" {
			fmt.Fprintf(os.Stderr, "    %s\n", line)
		}
	}
	var lines []string
	for {
		fmt.Fprint(os.Stderr, "> ")
		line, err := stdin.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		text := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(text) != "" {
			lines = append(lines, text)
		}
		if errors.Is(err, io.EOF) || strings.TrimSpace(text) == "" {
			break
		}
	}
	if len(lines) == 0 {
		return def, nil
	}
	return strings.Join(lines, "\n"), nil
}

// ask writes a question to stderr and reads one line of the answer.
func ask(question string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s ", question)
	line, err := stdin.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
