package stream_test

import (
	"context"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Verifies GoLangGraph token-stream early-exit (wired by slmcode agents) cancels
// remaining chunks once a complete structured JSON result is formed.
func TestTokenStreamEarlyExitCancelsWaste(t *testing.T) {
	var sent int
	streamFn := func(_ context.Context, _ llm.CompletionRequest, cb llm.StreamCallback) error {
		parts := []string{
			`{"approved":true,"score":9`,
			`,"summary":"ok","issues":[]}`,
			` EXTRA TOKENS THAT SHOULD NOT BE GENERATED`,
		}
		for _, p := range parts {
			sent++
			if err := cb(llm.CompletionResponse{
				Choices: []llm.Choice{{Delta: llm.Message{Content: p}}},
			}); err != nil {
				return err
			}
		}
		return nil
	}
	resp, err := llm.CollectStream(context.Background(), streamFn, llm.CompletionRequest{
		EarlyExit: llm.DefaultEarlyExit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatalf("expected cancel after complete JSON, sent=%d", sent)
	}
	if resp.Choices[0].FinishReason != "early_exit" {
		t.Fatalf("finish=%s", resp.Choices[0].FinishReason)
	}
	if !strings.Contains(resp.Choices[0].Message.Content, `"approved":true`) {
		t.Fatalf("content=%s", resp.Choices[0].Message.Content)
	}
}

func TestTokenStreamToolCallEarlyExit(t *testing.T) {
	var sent int
	streamFn := func(_ context.Context, _ llm.CompletionRequest, cb llm.StreamCallback) error {
		chunks := []llm.ToolCall{
			{Index: 0, ID: "1", Type: "function", Function: llm.FunctionCall{Name: "ws_edit", Arguments: `{"path":"a.go","old_str":"x",`}},
			{Index: 0, Function: llm.FunctionCall{Arguments: `"new_str":"y"}`}},
			{Index: 0, Function: llm.FunctionCall{Arguments: `,"waste":true}`}},
		}
		for _, tc := range chunks {
			sent++
			if err := cb(llm.CompletionResponse{
				Choices: []llm.Choice{{Delta: llm.Message{ToolCalls: []llm.ToolCall{tc}}}},
			}); err != nil {
				return err
			}
		}
		return nil
	}
	resp, err := llm.CollectStream(context.Background(), streamFn, llm.CompletionRequest{
		EarlyExit: llm.DefaultEarlyExit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatalf("sent=%d", sent)
	}
	if !llm.LooksCompleteToolCalls(resp.Choices[0].Message.ToolCalls) {
		t.Fatalf("tools=%+v", resp.Choices[0].Message.ToolCalls)
	}
}
