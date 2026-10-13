package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/doctor"
)

func checksWith(sevs ...doctor.Severity) func() []doctor.Check {
	return func() []doctor.Check {
		var cs []doctor.Check
		for i, s := range sevs {
			o := doctor.Outcome{Severity: s, Message: "msg " + string(s)}
			if s != doctor.OK {
				o.Fix = "fix it"
			}
			cs = append(cs, doctor.Check{
				ID:    string(rune('a' + i)),
				Title: "check",
				Run:   func(context.Context) doctor.Outcome { return o },
			})
		}
		return cs
	}
}

func runDoctor(t *testing.T, checks func() []doctor.Check, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newDoctorCmd(checks)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDoctorHealthyExitsZero(t *testing.T) {
	out, err := runDoctor(t, checksWith(doctor.OK, doctor.OK))
	if err != nil {
		t.Fatalf("doctor with all checks passing returned %v", err)
	}
	if !strings.Contains(out, "[PASS] a") || !strings.Contains(out, "2 passed, 0 warnings, 0 failed") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestDoctorExitCodesFollowTheWorstResult(t *testing.T) {
	cases := []struct {
		sevs []doctor.Severity
		code int
	}{
		{[]doctor.Severity{doctor.OK, doctor.Warn}, 1},
		{[]doctor.Severity{doctor.Warn, doctor.Fail}, 2},
	}
	for _, c := range cases {
		_, err := runDoctor(t, checksWith(c.sevs...))
		var exit *ExitCodeError
		if !errors.As(err, &exit) || exit.Code != c.code {
			t.Errorf("severities %v: err = %v, want exit code %d", c.sevs, err, c.code)
		}
	}
}

func TestDoctorJSON(t *testing.T) {
	out, err := runDoctor(t, checksWith(doctor.OK, doctor.Fail), "--json")
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}

	var got struct {
		Checks []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Fix      string `json:"fix"`
		} `json:"checks"`
		Summary  map[string]int `json:"summary"`
		ExitCode *int           `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if len(got.Checks) != 2 || got.Checks[1].Severity != "fail" || got.Checks[1].Fix != "fix it" {
		t.Fatalf("checks = %+v", got.Checks)
	}
	if got.Summary["ok"] != 1 || got.Summary["fail"] != 1 || got.ExitCode == nil || *got.ExitCode != 2 {
		t.Fatalf("summary = %v, exit_code = %v", got.Summary, got.ExitCode)
	}
}

func TestRootHasDoctor(t *testing.T) {
	cmd, _, err := NewRootCmd().Find([]string{"doctor"})
	if err != nil || cmd.Name() != "doctor" {
		t.Fatalf("lokal doctor is not wired into the root command: %v", err)
	}
}

func TestExitStatus(t *testing.T) {
	if code, msg := exitStatus(nil); code != 0 || msg != "" {
		t.Errorf("nil: got %d %q", code, msg)
	}
	if code, msg := exitStatus(&ExitCodeError{Code: 2}); code != 2 || msg != "" {
		t.Errorf("exit code error: got %d %q, want 2 and no message (the report was printed)", code, msg)
	}
	if code, msg := exitStatus(errors.New("boom")); code != 1 || msg != "lokal: boom" {
		t.Errorf("plain error: got %d %q", code, msg)
	}
}
