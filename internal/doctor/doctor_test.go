package doctor

import (
	"bytes"
	"context"
	"testing"
)

func fixed(sev Severity, msg, fix string) func(context.Context) Outcome {
	return func(context.Context) Outcome { return Outcome{Severity: sev, Message: msg, Fix: fix} }
}

func TestRunKeepsOrderAndCountsSeverities(t *testing.T) {
	report := Run(context.Background(), []Check{
		{ID: "a", Title: "First", Run: fixed(OK, "fine", "")},
		{ID: "b", Title: "Second", Run: fixed(Warn, "meh", "do x")},
		{ID: "c", Title: "Third", Run: fixed(OK, "fine", "")},
	})

	if len(report.Checks) != 3 || report.Checks[0].ID != "a" || report.Checks[1].ID != "b" || report.Checks[2].ID != "c" {
		t.Fatalf("checks out of order: %+v", report.Checks)
	}
	if report.Checks[1] != (Result{ID: "b", Title: "Second", Severity: Warn, Message: "meh", Fix: "do x"}) {
		t.Fatalf("result = %+v", report.Checks[1])
	}
	if report.Summary != (Summary{OK: 2, Warn: 1, Fail: 0}) {
		t.Fatalf("summary = %+v", report.Summary)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		sevs []Severity
		want int
	}{
		{nil, 0},
		{[]Severity{OK, OK}, 0},
		{[]Severity{OK, Warn}, 1},
		{[]Severity{Warn, Fail, OK}, 2},
	}
	for _, c := range cases {
		var checks []Check
		for _, s := range c.sevs {
			checks = append(checks, Check{ID: "x", Run: fixed(s, "", "")})
		}
		if got := Run(context.Background(), checks).ExitCode(); got != c.want {
			t.Errorf("ExitCode(%v) = %d, want %d", c.sevs, got, c.want)
		}
	}
}

func TestWriteText(t *testing.T) {
	report := Run(context.Background(), []Check{
		{ID: "hardware", Run: fixed(OK, "8 logical CPUs", "")},
		{ID: "disk", Run: fixed(Fail, "100 MiB free", "free disk space")},
	})
	var out bytes.Buffer

	if err := report.WriteText(&out); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	want := "[PASS] hardware  8 logical CPUs\n" +
		"[FAIL] disk      100 MiB free\n" +
		"                 fix: free disk space\n" +
		"\n" +
		"1 passed, 0 warnings, 1 failed\n"
	if out.String() != want {
		t.Fatalf("WriteText() =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:                      "0 MiB",
		512 * 1024 * 1024:      "512 MiB",
		1536 * 1024 * 1024:     "1.5 GiB",
		8 * 1024 * 1024 * 1024: "8.0 GiB",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
