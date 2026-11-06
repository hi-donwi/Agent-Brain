package codexusage

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReadSnapshotUsesReadOnlyAppServerMethods(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"method":"account/updated","params":{"authMode":"chatgpt"}}`,
		`{"id":2,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":1793700000}},"rateLimitsByLimitId":{"codex":{"limitId":"codex","limitName":null,"primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":1793700000},"secondary":null},"codex_weekly":{"limitId":"codex_weekly","limitName":"Weekly","primary":{"usedPercent":40,"windowDurationMins":10080,"resetsAt":1794300000},"secondary":null}}}}`,
		`{"id":3,"result":{"summary":{"lifetimeTokens":1234,"peakDailyTokens":500,"longestRunningTurnSec":60,"currentStreakDays":2,"longestStreakDays":4},"dailyUsageBuckets":[{"startDate":"2026-11-02","tokens":300}]}}`,
	}, "\n")
	var sent bytes.Buffer
	snapshot, err := ReadSnapshot(strings.NewReader(input), &sent)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if len(snapshot.RateLimits) != 2 {
		t.Fatalf("rate-limit buckets = %d, want 2", len(snapshot.RateLimits))
	}
	if snapshot.RateLimits[0].LimitID != "codex" || snapshot.RateLimits[1].LimitID != "codex_weekly" {
		t.Fatalf("rate-limit buckets are not stably ordered: %+v", snapshot.RateLimits)
	}
	if got := snapshot.RateLimits[1].Primary.ResetsAt; got == nil || !got.Equal(time.Unix(1794300000, 0).UTC()) {
		t.Fatalf("weekly reset = %v, want documented timestamp", got)
	}
	if snapshot.Usage == nil || snapshot.Usage.Summary == nil || snapshot.Usage.Summary.LifetimeTokens == nil || *snapshot.Usage.Summary.LifetimeTokens != 1234 {
		t.Fatalf("usage summary = %+v, want documented summary", snapshot.Usage)
	}
	if len(snapshot.Usage.DailyUsageBuckets) != 1 || snapshot.Usage.DailyUsageBuckets[0].Tokens != 300 {
		t.Fatalf("daily usage = %+v", snapshot.Usage.DailyUsageBuckets)
	}

	written := sent.String()
	for _, method := range []string{"initialize", "initialized", "account/rateLimits/read", "account/usage/read"} {
		if !strings.Contains(written, `"method":"`+method+`"`) {
			t.Errorf("request stream does not contain method %q: %s", method, written)
		}
	}
	for _, method := range []string{"account/rateLimitResetCredit/consume", "account/sendAddCreditsNudgeEmail"} {
		if strings.Contains(written, method) {
			t.Errorf("read-only snapshot sent mutating/nudge method %q", method)
		}
	}
}

func TestReadSnapshotDoesNotInventMissingResetMetadata(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"id":2,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":80,"windowDurationMins":300,"resetsAt":null},"secondary":null}}}`,
		`{"id":3,"result":{"summary":null}}`,
	}, "\n")
	snapshot, err := ReadSnapshot(strings.NewReader(input), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if len(snapshot.RateLimits) != 1 || snapshot.RateLimits[0].Primary == nil {
		t.Fatalf("rate limits = %+v", snapshot.RateLimits)
	}
	if snapshot.RateLimits[0].Primary.ResetsAt != nil {
		t.Fatalf("missing reset metadata was guessed as %v", snapshot.RateLimits[0].Primary.ResetsAt)
	}
	if snapshot.Usage == nil || snapshot.Usage.Summary != nil {
		t.Fatalf("missing summary should remain unavailable, got %+v", snapshot.Usage)
	}
}

func TestReadSnapshotMarksOptionalUsageUnavailableOnRPCError(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"id":2,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":10,"windowDurationMins":300,"resetsAt":1793700000}}}}`,
		`{"id":3,"error":{"code":-32601,"message":"sensitive upstream detail"}}`,
	}, "\n")
	var sent bytes.Buffer
	snapshot, err := ReadSnapshot(strings.NewReader(input), &sent)
	if err != nil {
		t.Fatalf("ReadSnapshot should preserve rate limits when optional usage is unsupported: %v", err)
	}
	if snapshot.Usage != nil || snapshot.UsageErrorCode == nil || *snapshot.UsageErrorCode != -32601 {
		t.Fatalf("optional usage status = %+v, code=%v", snapshot.Usage, snapshot.UsageErrorCode)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("sensitive upstream detail")) {
		t.Fatal("snapshot exposed raw upstream error text")
	}
}

func TestReadSnapshotFailsClosedWhenRateLimitsCannotBeRead(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"id":2,"error":{"code":401,"message":"token value must not leak"}}`,
	}, "\n")
	_, err := ReadSnapshot(strings.NewReader(input), &bytes.Buffer{})
	if err == nil {
		t.Fatal("ReadSnapshot succeeded without rate-limit data")
	}
	if strings.Contains(err.Error(), "token value must not leak") {
		t.Fatal("error exposed raw upstream response")
	}
}
