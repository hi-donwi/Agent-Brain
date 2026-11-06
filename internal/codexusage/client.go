// Package codexusage reads the documented, read-only usage methods exposed by
// the local Codex App Server JSON-RPC interface.
package codexusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	clientName    = "agent_brain"
	clientTitle   = "Agent-Brain"
	clientVersion = "1"
	maxMessage    = 4 << 20
)

// Snapshot is a point-in-time, in-memory view. It is not persisted by this package.
type Snapshot struct {
	RateLimits     []RateLimitBucket `json:"rate_limits"`
	Usage          *UsageData        `json:"usage,omitempty"`
	UsageStatus    string            `json:"usage_status"`
	UsageErrorCode *int              `json:"usage_error_code,omitempty"`
}

// RateLimitBucket is one Codex rate-limit bucket returned by App Server.
type RateLimitBucket struct {
	LimitID              string     `json:"limit_id"`
	LimitName            *string    `json:"limit_name"`
	Primary              *RateLimit `json:"primary"`
	Secondary            *RateLimit `json:"secondary"`
	RateLimitReachedType *string    `json:"rate_limit_reached_type"`
}

// RateLimit contains only fields the App Server documents for a window.
type RateLimit struct {
	UsedPercent        *float64   `json:"used_percent"`
	WindowDurationMins *int       `json:"window_duration_mins"`
	ResetsAt           *time.Time `json:"resets_at"`
}

// UsageData contains ChatGPT-plan token activity from account/usage/read.
// It is distinct from quota usage in RateLimitBucket.
type UsageData struct {
	Summary           *UsageSummary      `json:"summary"`
	DailyUsageBuckets []DailyUsageBucket `json:"daily_usage_buckets,omitempty"`
}

// UsageSummary contains the documented aggregate token-activity fields.
type UsageSummary struct {
	LifetimeTokens        *int64 `json:"lifetime_tokens"`
	PeakDailyTokens       *int64 `json:"peak_daily_tokens"`
	LongestRunningTurnSec *int64 `json:"longest_running_turn_sec"`
	CurrentStreakDays     *int64 `json:"current_streak_days"`
	LongestStreakDays     *int64 `json:"longest_streak_days"`
}

// DailyUsageBucket contains one optional daily token-activity bucket.
type DailyUsageBucket struct {
	StartDate string `json:"start_date"`
	Tokens    int64  `json:"tokens"`
}

type rpcResponse struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code int `json:"code"`
}

type rateLimitsResponse struct {
	RateLimits          *wireRateLimitBucket           `json:"rateLimits"`
	RateLimitsByLimitID map[string]wireRateLimitBucket `json:"rateLimitsByLimitId"`
}

type wireRateLimitBucket struct {
	LimitID              string         `json:"limitId"`
	LimitName            *string        `json:"limitName"`
	Primary              *wireRateLimit `json:"primary"`
	Secondary            *wireRateLimit `json:"secondary"`
	RateLimitReachedType *string        `json:"rateLimitReachedType"`
}

type wireRateLimit struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int     `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

type usageResponse struct {
	Summary           *wireUsageSummary  `json:"summary"`
	DailyUsageBuckets []DailyUsageBucket `json:"dailyUsageBuckets"`
}

type wireUsageSummary struct {
	LifetimeTokens        *int64 `json:"lifetimeTokens"`
	PeakDailyTokens       *int64 `json:"peakDailyTokens"`
	LongestRunningTurnSec *int64 `json:"longestRunningTurnSec"`
	CurrentStreakDays     *int64 `json:"currentStreakDays"`
	LongestStreakDays     *int64 `json:"longestStreakDays"`
}

type client struct {
	reader *bufio.Scanner
	writer io.Writer
	nextID int
}

// ReadSnapshot initializes an App Server connection and requests rate limits and
// token-activity usage. The transport is expected to be the App Server's local
// newline-delimited JSON stdio transport. It never calls mutation methods.
func ReadSnapshot(input io.Reader, output io.Writer) (Snapshot, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxMessage)
	c := &client{reader: scanner, writer: output}

	var initialized json.RawMessage
	if err := c.request("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    clientName,
			"title":   clientTitle,
			"version": clientVersion,
		},
	}, &initialized); err != nil {
		return Snapshot{}, err
	}
	if _, err := io.WriteString(c.writer, "{\"method\":\"initialized\",\"params\":{}}\n"); err != nil {
		return Snapshot{}, fmt.Errorf("write Codex App Server initialization acknowledgement: %w", err)
	}

	var rates rateLimitsResponse
	if err := c.request("account/rateLimits/read", map[string]any{}, &rates); err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		RateLimits:  normalizeRateLimits(rates),
		UsageStatus: "unavailable",
	}
	var usage usageResponse
	if err := c.request("account/usage/read", map[string]any{}, &usage); err != nil {
		if code, ok := rpcErrorCode(err); ok {
			snapshot.UsageErrorCode = &code
			return snapshot, nil
		}
		return Snapshot{}, err
	}
	snapshot.UsageStatus = "available"
	snapshot.Usage = normalizeUsage(usage)
	return snapshot, nil
}

func (c *client) request(method string, params any, result any) error {
	c.nextID++
	id := c.nextID
	request := struct {
		Method string `json:"method"`
		ID     int    `json:"id"`
		Params any    `json:"params,omitempty"`
	}{Method: method, ID: id, Params: params}
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Codex App Server request %s: %w", method, err)
	}
	if _, err := c.writer.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write Codex App Server request %s: %w", method, err)
	}

	for c.reader.Scan() {
		line := bytes.TrimSpace(c.reader.Bytes())
		if len(line) == 0 {
			continue
		}
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return fmt.Errorf("decode Codex App Server response to %s", method)
		}
		if response.ID == nil || *response.ID != id {
			continue // Notifications and responses to other request IDs are not ours.
		}
		if response.Error != nil {
			return appServerRPCError{method: method, code: response.Error.Code}
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("Codex App Server response to %s omitted result", method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode Codex App Server result for %s", method)
		}
		return nil
	}
	if err := c.reader.Err(); err != nil {
		return fmt.Errorf("read Codex App Server response to %s: %w", method, err)
	}
	return fmt.Errorf("Codex App Server closed before responding to %s", method)
}

type appServerRPCError struct {
	method string
	code   int
}

func (e appServerRPCError) Error() string {
	return fmt.Sprintf("Codex App Server request %s failed (code %d)", e.method, e.code)
}

func rpcErrorCode(err error) (int, bool) {
	e, ok := err.(appServerRPCError)
	return e.code, ok
}

func normalizeRateLimits(response rateLimitsResponse) []RateLimitBucket {
	buckets := make([]RateLimitBucket, 0, len(response.RateLimitsByLimitID)+1)
	if len(response.RateLimitsByLimitID) > 0 {
		keys := make([]string, 0, len(response.RateLimitsByLimitID))
		for key := range response.RateLimitsByLimitID {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			buckets = append(buckets, normalizeRateLimitBucket(key, response.RateLimitsByLimitID[key]))
		}
		return buckets
	}
	if response.RateLimits != nil {
		buckets = append(buckets, normalizeRateLimitBucket("", *response.RateLimits))
	}
	return buckets
}

func normalizeRateLimitBucket(key string, wire wireRateLimitBucket) RateLimitBucket {
	limitID := wire.LimitID
	if limitID == "" {
		limitID = key
	}
	return RateLimitBucket{
		LimitID:              limitID,
		LimitName:            wire.LimitName,
		Primary:              normalizeWindow(wire.Primary),
		Secondary:            normalizeWindow(wire.Secondary),
		RateLimitReachedType: wire.RateLimitReachedType,
	}
}

func normalizeWindow(wire *wireRateLimit) *RateLimit {
	if wire == nil {
		return nil
	}
	var reset *time.Time
	if wire.ResetsAt != nil {
		value := time.Unix(*wire.ResetsAt, 0).UTC()
		reset = &value
	}
	return &RateLimit{
		UsedPercent:        wire.UsedPercent,
		WindowDurationMins: wire.WindowDurationMins,
		ResetsAt:           reset,
	}
}

func normalizeUsage(wire usageResponse) *UsageData {
	var summary *UsageSummary
	if wire.Summary != nil {
		summary = &UsageSummary{
			LifetimeTokens:        wire.Summary.LifetimeTokens,
			PeakDailyTokens:       wire.Summary.PeakDailyTokens,
			LongestRunningTurnSec: wire.Summary.LongestRunningTurnSec,
			CurrentStreakDays:     wire.Summary.CurrentStreakDays,
			LongestStreakDays:     wire.Summary.LongestStreakDays,
		}
	}
	return &UsageData{Summary: summary, DailyUsageBuckets: wire.DailyUsageBuckets}
}
