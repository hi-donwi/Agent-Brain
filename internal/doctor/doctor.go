// Package doctor runs lokal's health checks. Each check reports a severity, a
// message, and, when something is wrong, the command or action that fixes it;
// the report decides the process exit code (0 healthy, 1 warnings, 2 failures)
// so scripts and agent hosts can act on it.
package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Severity of one check result.
type Severity string

const (
	OK   Severity = "ok"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

// Outcome is what a check's Run returns.
type Outcome struct {
	Severity Severity
	Message  string
	Fix      string
}

// Check is one named health check.
type Check struct {
	ID    string
	Title string
	Run   func(context.Context) Outcome
}

// Result is a check's outcome, identified.
type Result struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}

// Summary counts results by severity.
type Summary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// Report is the outcome of a doctor run, in check order.
type Report struct {
	Checks  []Result `json:"checks"`
	Summary Summary  `json:"summary"`
}

// Run executes the checks in order.
func Run(ctx context.Context, checks []Check) Report {
	report := Report{Checks: []Result{}}
	for _, c := range checks {
		o := c.Run(ctx)
		report.Checks = append(report.Checks, Result{ID: c.ID, Title: c.Title, Severity: o.Severity, Message: o.Message, Fix: o.Fix})
		switch o.Severity {
		case OK:
			report.Summary.OK++
		case Warn:
			report.Summary.Warn++
		default:
			report.Summary.Fail++
		}
	}
	return report
}

// ExitCode is 2 when any check failed, 1 when any warned, and 0 otherwise.
func (r Report) ExitCode() int {
	switch {
	case r.Summary.Fail > 0:
		return 2
	case r.Summary.Warn > 0:
		return 1
	}
	return 0
}

var badges = map[Severity]string{OK: "[PASS]", Warn: "[WARN]", Fail: "[FAIL]"}

// WriteText prints one line per check, a fix line under each problem, and a
// summary line.
func (r Report) WriteText(w io.Writer) error {
	width := 0
	for _, c := range r.Checks {
		width = max(width, len(c.ID))
	}
	var b strings.Builder
	for _, c := range r.Checks {
		badge, ok := badges[c.Severity]
		if !ok {
			badge = badges[Fail]
		}
		fmt.Fprintf(&b, "%s %-*s  %s\n", badge, width, c.ID, c.Message)
		if c.Fix != "" {
			fmt.Fprintf(&b, "%s fix: %s\n", strings.Repeat(" ", len(badge)+width+2), c.Fix)
		}
	}
	warnings := "warnings"
	if r.Summary.Warn == 1 {
		warnings = "warning"
	}
	fmt.Fprintf(&b, "\n%d passed, %d %s, %d failed\n", r.Summary.OK, r.Summary.Warn, warnings, r.Summary.Fail)
	_, err := io.WriteString(w, b.String())
	return err
}

// HumanBytes formats a size in MiB below 1 GiB and in GiB with one decimal above.
func HumanBytes(n uint64) string {
	const mib, gib = 1 << 20, 1 << 30
	if n < gib {
		return fmt.Sprintf("%d MiB", n/mib)
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/gib)
}
