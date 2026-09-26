package laya

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatResponse(content, reason string) string {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": reason,
		"message": map[string]any{"content": content}}}})
	return string(raw)
}

func TestOpenAIProtocolAndFailures(t *testing.T) {
	for _, tc := range []struct {
		content, reason string
		valid           bool
	}{
		{`{"probability":0}`, "stop", true}, {`{"probability":1}`, "stop", true},
		{`{"probability":0.9}`, "length", false}, {`{"probability":0.9}`, "tool_calls", false},
		{`{}`, "stop", false}, {`{"probability":null}`, "stop", false},
		{`{"probability":-0.1}`, "stop", false}, {`{"probability":1.1}`, "stop", false},
		{`{"probability":"0.9"}`, "stop", false}, {"```json\n{}\n```", "stop", false},
		{`{"probability":0.9} {}`, "stop", false},
	} {
		t.Run(tc.content+tc.reason, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("bad request path/auth")
				}
				var req struct {
					Model          string
					Messages       []struct{ Role, Content string }
					MaxTokens      int               `json:"max_tokens"`
					ResponseFormat map[string]string `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Model != "Org/CaseSensitive" || len(req.Messages) != 2 || req.Messages[1].Content != "data" || req.MaxTokens != 64 || req.ResponseFormat["type"] != "json_object" {
					t.Errorf("bad payload: %+v", req)
				}
				_, _ = fmt.Fprint(w, chatResponse(tc.content, tc.reason))
			}))
			defer srv.Close()
			client, err := New(Options{Provider: "openai", Endpoint: srv.URL + "/custom/v1/", Model: "Org/CaseSensitive", APIKey: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Probability(context.Background(), "data", "relevant?")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	for _, body := range []string{`{}`, `{"choices":[]}`, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"probability\":0.9}","refusal":"denied"}}]}`, strings.Repeat(" ", MaxResponseBytes+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
		client, err := New(Options{Provider: "openai", Endpoint: srv.URL, Model: "served"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Probability(context.Background(), "data", "q")
		srv.Close()
		if err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	for _, opts := range []Options{{Provider: "typo", Endpoint: "http://localhost", Model: "m"}, {Provider: "openai", Endpoint: "http://localhost"}} {
		if _, err := New(opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}
