package dryrun

import (
	"fmt"
	"io"
	"strconv"
)

// Status is the outcome of one check.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
	StatusSkipped Status = "skipped"
)

// Check is one probe's outcome.
type Check struct {
	Status Status
	// Path is the config path the check is about ("providers[local]",
	// "httpserver", "skills.dirs[2]") or the flag ("--remote").
	Path    string
	Message string
	// Fix says how to correct a problem; empty for ok and skipped checks.
	Fix string
	// Line and Column locate the path in the config file; 0 when the value
	// is an applied default the file does not carry.
	Line, Column int
}

// Report is the outcome of one dry run.
type Report struct {
	// File is the config file the checks refer to.
	File   string
	Checks []Check
}

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

// Errors counts the checks that fail the dry run.
func (r *Report) Errors() int { return r.count(StatusError) }

// Warnings counts the checks worth a look that do not fail it.
func (r *Report) Warnings() int { return r.count(StatusWarning) }

// OK counts the checks that passed.
func (r *Report) OK() int { return r.count(StatusOK) }

func (r *Report) count(status Status) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == status {
			n++
		}
	}
	return n
}

// Write renders the whole report: one line per check - status, path,
// message - with the place in the config file and the fix indented under a
// problem, and the status line last.
func (r *Report) Write(w io.Writer) {
	for _, c := range r.Checks {
		r.writeCheck(w, c)
	}
	r.writeSummary(w)
}

// WriteProblems renders only what needs attention - the warnings and errors,
// each with its place and fix - and the status line. A run where everything
// passed is that one line.
func (r *Report) WriteProblems(w io.Writer) {
	for _, c := range r.Checks {
		if c.Status == StatusError || c.Status == StatusWarning {
			r.writeCheck(w, c)
		}
	}
	r.writeSummary(w)
}

func (r *Report) writeCheck(w io.Writer, c Check) {
	_, _ = fmt.Fprintf(w, "%-8s %s: %s\n", c.Status, c.Path, c.Message)
	if c.Line > 0 && r.File != "" {
		loc := r.File + ":" + strconv.Itoa(c.Line)
		if c.Column > 0 {
			loc += ":" + strconv.Itoa(c.Column)
		}
		_, _ = fmt.Fprintf(w, "         at %s\n", loc)
	}
	if c.Fix != "" {
		_, _ = fmt.Fprintf(w, "         fix: %s\n", c.Fix)
	}
}

func (r *Report) writeSummary(w io.Writer) {
	_, _ = fmt.Fprintf(w, "dry run: %s, %s, %d ok\n", plural(r.Errors(), "error"), plural(r.Warnings(), "warning"), r.OK())
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
