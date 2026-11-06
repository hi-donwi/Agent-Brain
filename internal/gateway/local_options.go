package gateway

import (
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Brain/internal/home"
)

func localContextLimit(target ResolvedTarget, cfg *home.Config) int {
	limit := 8192
	if cfg != nil && cfg.Inference.ContextLength > 0 {
		limit = cfg.Inference.ContextLength
	}
	if target.CatalogModel != nil && target.CatalogModel.MaxContext > 0 {
		if cfg == nil || cfg.Inference.ContextLength == 0 {
			return target.CatalogModel.MaxContext
		}
		limit = min(limit, target.CatalogModel.MaxContext)
	}
	return limit
}

func validateLocalChat(req *chatRequest) (json.RawMessage, error) {
	if req.N != nil && *req.N != 1 {
		return nil, fmt.Errorf("local chat only supports n=1")
	}
	if req.Logprobs {
		return nil, fmt.Errorf("local chat does not support logprobs")
	}
	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		return nil, fmt.Errorf("local chat cannot guarantee parallel_tool_calls=false")
	}
	for _, limit := range []*int{req.MaxTokens, req.MaxCompletionTokens} {
		if limit != nil && *limit <= 0 {
			return nil, fmt.Errorf("output token limits must be positive")
		}
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return nil, fmt.Errorf("temperature must be between 0 and 2")
	}
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return nil, fmt.Errorf("top_p must be between 0 and 1")
	}
	var choice string
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		if json.Unmarshal(req.ToolChoice, &choice) != nil || (choice != "auto" && choice != "none") {
			return nil, fmt.Errorf("local chat supports only tool_choice auto or none; required/named selection is not supported")
		}
	}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name   string `json:"name"`
			Strict bool   `json:"strict"`
		} `json:"function"`
	}
	if len(req.Tools) > 0 && json.Unmarshal(req.Tools, &tools) != nil {
		return nil, fmt.Errorf("tools must be an array of function definitions")
	}
	for _, tool := range tools {
		if tool.Type != "function" || tool.Function.Name == "" || tool.Function.Strict {
			return nil, fmt.Errorf("local tools require a named function; strict tool schemas are not supported")
		}
	}
	if choice == "none" || len(tools) == 0 {
		req.Tools = nil
	}
	if len(req.ResponseFormat) == 0 || string(req.ResponseFormat) == "null" {
		return nil, nil
	}
	var format struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(req.ResponseFormat, &format); err != nil {
		return nil, fmt.Errorf("invalid response_format: %w", err)
	}
	switch format.Type {
	case "text":
		return nil, nil
	case "json_object":
		return json.RawMessage(`"json"`), nil
	case "json_schema":
		var schema map[string]any
		if format.JSONSchema.Strict || json.Unmarshal(format.JSONSchema.Schema, &schema) != nil || schema == nil {
			return nil, fmt.Errorf("json_schema requires a schema object; strict OpenAI schema semantics are not supported")
		}
		return format.JSONSchema.Schema, nil
	default:
		return nil, fmt.Errorf("unsupported response_format type %q", format.Type)
	}
}
