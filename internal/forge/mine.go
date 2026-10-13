package forge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/trace"
)

// Miner clusters traces into recurring operational patterns.
type Miner struct {
	Store   *trace.Store
	MinRuns int
}

// NewMiner initializes a Miner.
func NewMiner(store *trace.Store, minRuns int) *Miner {
	if minRuns <= 0 {
		minRuns = 2
	}
	return &Miner{
		Store:   store,
		MinRuns: minRuns,
	}
}

// Mine analyzes all recorded sessions in the store and returns candidate clusters.
func (m *Miner) Mine(ctx context.Context) ([]TraceCluster, error) {
	if m.Store == nil {
		return nil, fmt.Errorf("trace store is required")
	}

	summaries, err := m.Store.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("list trace sessions: %w", err)
	}

	type sessionData struct {
		id           string
		signature    string
		intent       string
		turnCount    int
		promptTokens int
		evalTokens   int
	}

	clusterMap := make(map[string][]sessionData)
	intentMap := make(map[string]string)
	sigMap := make(map[string]string)

	for _, sum := range summaries {
		if sum.HasErrors || sum.RecordCount == 0 {
			continue
		}

		records, err := m.Store.ReadSession(sum.SessionID)
		if err != nil || len(records) == 0 {
			continue
		}

		sig, intent := extractSignatureAndIntent(records)
		if sig == "" && intent == "" {
			continue
		}

		// Key by signature if available, otherwise by intent
		key := sig
		if key == "" {
			key = intent
		}

		sd := sessionData{
			id:           sum.SessionID,
			signature:    sig,
			intent:       intent,
			turnCount:    len(records),
			promptTokens: sum.TotalPromptToks,
			evalTokens:   sum.TotalEvalToks,
		}

		clusterMap[key] = append(clusterMap[key], sd)
		if intentMap[key] == "" && intent != "" {
			intentMap[key] = intent
		}
		if sigMap[key] == "" && sig != "" {
			sigMap[key] = sig
		}
	}

	var clusters []TraceCluster

	for key, sessions := range clusterMap {
		if len(sessions) < m.MinRuns {
			continue
		}

		totalTurns := 0
		totalPrompt := 0
		totalEval := 0
		var sessionIDs []string

		for _, s := range sessions {
			totalTurns += s.turnCount
			totalPrompt += s.promptTokens
			totalEval += s.evalTokens
			sessionIDs = append(sessionIDs, s.id)
		}

		count := len(sessions)
		intent := intentMap[key]
		if intent == "" {
			intent = key
		}

		suggestedName := slugify(intent)
		if suggestedName == "" {
			suggestedName = "skill-" + hashShort(key)
		}

		clusterID := "cl-" + hashShort(key)

		clusters = append(clusters, TraceCluster{
			ID:              clusterID,
			Signature:       sigMap[key],
			Intent:          intent,
			SuggestedName:   suggestedName,
			SessionIDs:      sessionIDs,
			TurnCount:       totalTurns,
			SuccessfulRuns:  count,
			AvgPromptTokens: totalPrompt / count,
			AvgEvalTokens:   totalEval / count,
		})
	}

	// Sort by number of successful runs descending
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].SuccessfulRuns != clusters[j].SuccessfulRuns {
			return clusters[i].SuccessfulRuns > clusters[j].SuccessfulRuns
		}
		return clusters[i].ID < clusters[j].ID
	})

	return clusters, nil
}

func extractSignatureAndIntent(records []trace.Record) (string, string) {
	var toolSequence []string
	var firstPrompt string

	for _, r := range records {
		for _, tc := range r.ToolCalls {
			if tc.Name != "" {
				toolSequence = append(toolSequence, tc.Name)
			}
		}
		if firstPrompt == "" {
			for _, m := range r.Messages {
				if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
					firstPrompt = strings.TrimSpace(m.Content)
					break
				}
			}
		}
	}

	var signature string
	if len(toolSequence) > 0 {
		signature = strings.Join(toolSequence, " -> ")
	}

	intent := extractIntentSummary(firstPrompt)
	return signature, intent
}

var nonAlphaNumRegex = regexp.MustCompile(`[^a-zA-Z0-9\s-]+`)

func extractIntentSummary(prompt string) string {
	cleaned := nonAlphaNumRegex.ReplaceAllString(prompt, " ")
	words := strings.Fields(strings.ToLower(cleaned))
	if len(words) == 0 {
		return ""
	}

	// Filter common stop words
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "in": true, "on": true, "at": true,
		"to": true, "for": true, "with": true, "by": true, "from": true, "of": true,
		"and": true, "or": true, "how": true, "can": true, "you": true, "i": true,
		"please": true, "what": true, "is": true, "do": true, "it": true,
	}

	var meaningful []string
	for _, w := range words {
		if !stopWords[w] && len(w) > 2 {
			meaningful = append(meaningful, w)
		}
		if len(meaningful) >= 4 {
			break
		}
	}

	if len(meaningful) == 0 {
		if len(words) > 3 {
			return strings.Join(words[:3], " ")
		}
		return strings.Join(words, " ")
	}

	return strings.Join(meaningful, " ")
}

func slugify(s string) string {
	cleaned := nonAlphaNumRegex.ReplaceAllString(s, " ")
	words := strings.Fields(strings.ToLower(cleaned))
	if len(words) == 0 {
		return ""
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, "-")
}

func hashShort(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}
