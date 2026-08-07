package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// RegisterFindModelsTool adds specialist tool find_models (auth-gated catalog search).
func RegisterFindModelsTool(reg *tools.ToolRegistry, cfg *config.Config) error {
	if reg == nil || cfg == nil {
		return nil
	}
	tool := tools.NewGenericTool(
		"find_models",
		"Search selectable LLM model ids for the active provider (auth-aware). "+
			"Use when choosing or verifying a model. Returns JSON catalog with matches + auth status.",
		func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			q, _ := args["query"].(string)
			limit := 32
			switch v := args["limit"].(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			case string:
				if n, err := strconv.Atoi(v); err == nil {
					limit = n
				}
			}
			cat := Find(ctx, cfg, q, limit)
			b, err := json.MarshalIndent(cat, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Substring filter (optional)",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max matches (default 32)",
				},
			},
		},
	)
	if err := reg.RegisterTool(tool); err != nil {
		// Already registered is fine on rebuild.
		if !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("find_models: %w", err)
		}
	}
	return nil
}
