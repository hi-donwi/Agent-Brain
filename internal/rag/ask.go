package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/store"
)

// AskResponse contains the synthesized answer, citations, and underlying search results.
type AskResponse struct {
	Question  string               `json:"question"`
	Answer    string               `json:"answer"`
	Citations []string             `json:"citations"`
	Results   []store.SearchResult `json:"results,omitempty"`
}

// AskEngine answers natural language questions grounded in local file citations.
type AskEngine struct {
	Searcher *Searcher
	Adapter  backend.Adapter
}

// NewAskEngine creates an AskEngine with searcher and backend adapter.
func NewAskEngine(searcher *Searcher, adapter backend.Adapter) *AskEngine {
	return &AskEngine{
		Searcher: searcher,
		Adapter:  adapter,
	}
}

const ragSystemPrompt = `You are a helpful local assistant. Answer the user's question accurately and strictly based on the provided excerpts from local files.
Every claim must cite its source file and line numbers using [file:line].
If the answer cannot be determined from the excerpts, explicitly say that the information was not found in the indexed files. Do not extrapolate.`

// Ask retrieves relevant file chunks and synthesizes a cited answer.
func (ae *AskEngine) Ask(ctx context.Context, collectionName, question, chatModel string) (*AskResponse, error) {
	results, err := ae.Searcher.Search(ctx, collectionName, question, 5)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return &AskResponse{
			Question:  question,
			Answer:    "Not found in your indexed files.",
			Citations: nil,
			Results:   nil,
		}, nil
	}

	var citations []string
	var contextBuilder strings.Builder

	for _, res := range results {
		ch := res.Chunk
		citation := fmt.Sprintf("%s:%d-%d", ch.Path, ch.StartLine, ch.EndLine)
		citations = append(citations, citation)

		contextBuilder.WriteString(fmt.Sprintf("--- File: %s ---\n", citation))
		contextBuilder.WriteString(ch.Content)
		contextBuilder.WriteString("\n\n")
	}

	if ae.Adapter == nil {
		return &AskResponse{
			Question:  question,
			Answer:    "Backend adapter offline; retrieved matching excerpts below.",
			Citations: citations,
			Results:   results,
		}, nil
	}

	userPrompt := fmt.Sprintf("Context excerpts:\n%s\nQuestion: %s", contextBuilder.String(), question)

	chatResp, err := ae.Adapter.Chat(ctx, backend.ChatRequest{
		Model: chatModel,
		Messages: []backend.ChatMessage{
			{Role: "system", Content: ragSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("model chat generation failed: %w", err)
	}

	answer := strings.TrimSpace(chatResp.Message.Content)
	if answer == "" {
		answer = "Not found in your indexed files."
	}

	return &AskResponse{
		Question:  question,
		Answer:    answer,
		Citations: citations,
		Results:   results,
	}, nil
}
