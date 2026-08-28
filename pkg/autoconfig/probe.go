package autoconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// DefaultProbeTimeout bounds one candidate.
//
// Short on purpose. A local server that is running answers a model listing in
// single-digit milliseconds; anything slower than this is a port with something
// else behind it, and every second spent waiting is a second of a first-run
// experience that looks like a hang.
const DefaultProbeTimeout = 3 * time.Second

// HTTPProber lists an endpoint's models over HTTP.
//
// It is the only part of this package that touches the network, which is why
// everything that decides takes a Prober rather than calling this directly.
func HTTPProber(timeout time.Duration) Prober {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, c Candidate, apiKey string) ([]string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(c), nil)
		if err != nil {
			return nil, err
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, transportReason(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP %d — the API key was rejected", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return ParseModelList(body), nil
	}
}

// modelsURL builds the listing URL for a candidate.
//
// Ollama does not speak the OpenAI listing route; everything else does, and a
// base URL that already ends in /v1 must not get a second one.
func modelsURL(c Candidate) string {
	endpoint := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if config.IsOllama(c.Provider) {
		return strings.TrimRight(strings.TrimSuffix(endpoint, "/v1"), "/") + "/api/tags"
	}
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint + "/models"
	}
	return endpoint + "/v1/models"
}

// transportReason turns a dial error into something a user can act on.
//
// "dial tcp 127.0.0.1:1234: connect: connection refused" is accurate and tells
// a non-Go user nothing. The distinction that matters is "nothing is listening"
// versus "something is listening and did not answer in time", because those
// have different fixes.
func transportReason(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("nothing is listening")
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Client.Timeout"):
		return fmt.Errorf("no answer before the timeout")
	case strings.Contains(s, "no such host"):
		return fmt.Errorf("host not found")
	case strings.Contains(s, "certificate"):
		return fmt.Errorf("TLS certificate rejected")
	default:
		return err
	}
}

// ParseModelList reads model ids out of either listing format.
//
// OpenAI-compatible servers answer {"data":[{"id":…}]}; Ollama answers
// {"models":[{"name":…}]}. Both are handled because both are servers a
// developer actually runs.
func ParseModelList(body []byte) []string {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, d := range payload.Data {
		add(d.ID)
	}
	for _, m := range payload.Models {
		if m.Name != "" {
			add(m.Name)
			continue
		}
		add(m.Model)
	}
	return out
}
