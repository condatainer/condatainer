// Package utils holds console output, file and permission helpers, downloads, tmp
// directory selection and the file lock shared by the rest of the tool.
package utils

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"golang.org/x/term"
)

// DebugMode controls whether PrintDebug output is visible.
var DebugMode = false

// QuietMode controls whether verbose messages are suppressed (errors/warnings still shown)
var QuietMode = false

// YesMode controls whether to automatically answer yes to all prompts
var YesMode = false

// projectPrefix is the standard tag for all logs.
const projectPrefix = "CNT"

// ---------------------------------------------------------
// 1. Private Color Definitions
//    (We hide these so we don't use raw colors in logic)
// ---------------------------------------------------------

var (
	red      = color.New(color.FgRed).SprintFunc()
	green    = color.New(color.FgGreen).SprintFunc()
	yellow   = color.New(color.FgYellow).SprintFunc()
	blueBold = color.New(color.FgBlue, color.Bold).SprintFunc()
	magenta  = color.New(color.FgMagenta).SprintFunc()
	cyan     = color.New(color.FgCyan).SprintFunc()
	gray     = color.New(color.FgHiBlack).SprintFunc()
	bold     = color.New(color.Bold).SprintFunc()
)

// ---------------------------------------------------------
// 2. Semantic Styles (The "Style..." API)
//    Use these for formatting specific types of data.
// ---------------------------------------------------------

// StyleError formats critical failure messages (Red).
func StyleError(msg string) string { return red(msg) }

// StyleSuccess formats success messages (Green).
func StyleSuccess(msg string) string { return green(msg) }

// StyleWarning formats non-critical warnings (Yellow).
func StyleWarning(msg string) string { return yellow(msg) }

// StyleDebug formats the debug-line prefix (Gray).
func StyleDebug(msg string) string { return gray(msg) }

// StyleDim formats secondary text a reader can skip (Gray).
func StyleDim(msg string) string { return gray(msg) }

// StyleTitle formats section headings (Cyan).
func StyleTitle(title string) string { return cyan(title) }

// StyleName formats names, identifiers, or keys (Yellow).
func StyleName(name string) string { return yellow(name) }

// ---------------------------------------------------------
// 3. Log Printers
//    High-level functions that print entire lines with tags.
// ---------------------------------------------------------

// PrintMessage prints a standard info message. → stderr
// Output: [CNT] Message...
func PrintMessage(format string, a ...any) {
	if QuietMode {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "[%s] %s\n", projectPrefix, msg)
}

// PrintSuccess prints a success message with a Green prefix. → stderr
// Output: [CNT✓] Operation complete.
func PrintSuccess(format string, a ...any) {
	if QuietMode {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s\n", StyleSuccess("["+projectPrefix+"✓]"), msg)
}

// PrintError prints an error message with a Red prefix. → stderr
// Output: [CNT✗] Something failed.
func PrintError(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s\n", StyleError("["+projectPrefix+"✗]"), msg)
}

// PrintWarning prints a warning with a Yellow prefix. → stderr
// Output: [CNT!] Disk is almost full.
func PrintWarning(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s\n", StyleWarning("["+projectPrefix+"!]"), msg)
}

// PrintHint prints a helpful hint with a Cyan prefix. → stderr
// Output: [CNT▶] Try running with --force.
func PrintHint(format string, a ...any) {
	if QuietMode {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s\n", cyan("["+projectPrefix+"▶]"), msg)
}

// PrintNote prints a note with a Magenta prefix. → stderr
// Output: [CNT◇] This might take a while.
func PrintNote(format string, a ...any) {
	if QuietMode {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s\n", magenta("["+projectPrefix+"◇]"), msg)
}

// PrintDebug prints a debug message with a Gray prefix (only if DebugMode is true). → stderr
// Output: [CNT…] Executing: rm -rf /tmp/foo
func PrintDebug(format string, a ...any) {
	if DebugMode {
		msg := fmt.Sprintf(format, a...)
		fmt.Fprintf(os.Stderr, "%s %s\n", StyleDebug("["+projectPrefix+"…]"), msg)
	}
}

// ---------------------------------------------------------
// 4. Terminal Detection
// ---------------------------------------------------------

// IsInteractiveShell checks if stdout is connected to a TTY (interactive terminal).
// Returns true if the program is running in an interactive shell, false otherwise.
func IsInteractiveShell() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// IsStdinPiped returns true if stdin is a pipe or redirected (not a TTY).
func IsStdinPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// ShouldAnswerYes checks if we should automatically answer yes to prompts
// Returns true if --yes flag is set, false otherwise
func ShouldAnswerYes() bool {
	return YesMode
}

// stdinReader buffers os.Stdin for the life of the process. It has to be shared:
// a fresh bufio.Reader per call reads ahead into its buffer and then discards it,
// so every line after the first is lost whenever stdin is a pipe rather than a
// TTY — which is how a scheduler job replays answers.
var (
	stdinReader     *bufio.Reader
	stdinReaderOnce sync.Once
)

// ReadLineContext reads a trimmed line from stdin, preserving case.
// Returns context.Canceled if ctx is done before input arrives.
func ReadLineContext(ctx context.Context) (string, error) {
	stdinReaderOnce.Do(func() { stdinReader = bufio.NewReader(os.Stdin) })
	ch := make(chan string, 1)
	go func() {
		s, _ := stdinReader.ReadString('\n')
		ch <- strings.TrimSpace(s)
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case s := <-ch:
		return s, nil
	}
}

// Confirm writes prompt to w and reports whether the answer is y or yes. When
// reading stops, as on Ctrl-C, it ends the prompt's line and answers no.
func Confirm(ctx context.Context, w io.Writer, prompt string) bool {
	fmt.Fprint(w, prompt)
	choice, err := ReadLineContext(ctx)
	if err != nil {
		fmt.Fprintln(w)
		return false
	}
	return choice == "y" || choice == "yes"
}
