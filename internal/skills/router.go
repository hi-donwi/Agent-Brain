package skills

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/hi-donwi/Agent-Local/internal/store"
)

// Embedder generates vector embeddings for a string.
type Embedder interface {
	Embed(ctx context.Context, model string, text string) ([]float32, error)
}

// RouteResult describes one matched skill and its relevance scores.
type RouteResult struct {
	Summary      SkillSummary `json:"summary"`
	Score        float64      `json:"score"`
	KeywordScore float64      `json:"keyword_score"`
	VectorScore  float64      `json:"vector_score"`
}

// Router ranks and filters candidate skills within a token budget.
type Router struct {
	manager    *Manager
	embedder   Embedder
	embedModel string

	mu       sync.RWMutex
	embCache map[string][]float32 // Key: skillName -> normalized embedding
}

// NewRouter creates a new skill router.
func NewRouter(manager *Manager, embedder Embedder, embedModel string) *Router {
	return &Router{
		manager:    manager,
		embedder:   embedder,
		embedModel: embedModel,
		embCache:   make(map[string][]float32),
	}
}

// Route selects candidate skills for a task description, staying within maxTokens.
// If maxTokens <= 0, a sensible default budget of 1000 tokens is applied.
func (r *Router) Route(ctx context.Context, task string, maxTokens int) ([]RouteResult, error) {
	if maxTokens <= 0 {
		maxTokens = 1000
	}

	skills, err := r.manager.List()
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, nil
	}

	terms := tokenize(task)
	var queryVec []float32
	if r.embedder != nil && len(strings.TrimSpace(task)) > 0 {
		if rawVec, err := r.embedder.Embed(ctx, r.embedModel, task); err == nil && len(rawVec) > 0 {
			queryVec = store.Normalize(rawVec)
		}
	}

	var results []RouteResult
	for _, s := range skills {
		kwScore := r.scoreKeywords(terms, s)
		vecScore := 0.0

		if len(queryVec) > 0 {
			emb, err := r.getSkillEmbedding(ctx, s)
			if err == nil && len(emb) == len(queryVec) {
				dot := store.DotProduct(queryVec, emb)
				if dot > 0 {
					vecScore = float64(dot)
				}
			}
		}

		combined := kwScore
		if len(queryVec) > 0 {
			// Hybrid blend: 40% keyword, 60% semantic similarity
			combined = 0.4*kwScore + 0.6*vecScore
		}

		if combined > 0.01 {
			results = append(results, RouteResult{
				Summary:      s,
				Score:        combined,
				KeywordScore: kwScore,
				VectorScore:  vecScore,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Filter by token budget
	var budgeted []RouteResult
	usedTokens := 0
	for _, res := range results {
		cost := res.Summary.EstimateTokens()
		if usedTokens+cost > maxTokens && len(budgeted) > 0 {
			break
		}
		budgeted = append(budgeted, res)
		usedTokens += cost
	}

	return budgeted, nil
}

func (r *Router) scoreKeywords(terms []string, s SkillSummary) float64 {
	if len(terms) == 0 {
		return 0.0
	}

	nameLower := strings.ToLower(s.Name)
	descLower := strings.ToLower(s.Description)
	var kwLowers []string
	for _, kw := range s.Keywords {
		kwLowers = append(kwLowers, strings.ToLower(kw))
	}

	rawScore := 0.0
	for _, t := range terms {
		if len(t) < 2 {
			continue
		}
		// Exact or prefix match in skill name
		if nameLower == t || strings.Contains(nameLower, t) {
			rawScore += 3.0
		}
		// Match in metadata keywords
		for _, kw := range kwLowers {
			if strings.Contains(kw, t) {
				rawScore += 2.0
				break
			}
		}
		// Match in description
		if strings.Contains(descLower, t) {
			rawScore += 1.0
		}
	}

	// Normalize by term count
	normalized := rawScore / (float64(len(terms)) * 3.0)
	if normalized > 1.0 {
		return 1.0
	}
	return normalized
}

func (r *Router) getSkillEmbedding(ctx context.Context, s SkillSummary) ([]float32, error) {
	r.mu.RLock()
	cached, ok := r.embCache[s.Name]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}

	// Text for embedding combines name and description
	text := s.Name + ": " + s.Description
	raw, err := r.embedder.Embed(ctx, r.embedModel, text)
	if err != nil {
		return nil, err
	}

	norm := store.Normalize(raw)
	r.mu.Lock()
	r.embCache[s.Name] = norm
	r.mu.Unlock()

	return norm, nil
}

func tokenize(s string) []string {
	var words []string
	var current strings.Builder

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}
