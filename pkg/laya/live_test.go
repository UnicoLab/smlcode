package laya

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveDecisionServer is an opt-in deployment smoke, not an accuracy benchmark.
func TestLiveDecisionServer(t *testing.T) {
	endpoint := os.Getenv("SLMCODE_TEST_DECISION_ENDPOINT")
	if endpoint == "" {
		t.Skip("live decision server not configured (SLMCODE_TEST_DECISION_ENDPOINT)")
	}
	client, err := New(Options{Endpoint: endpoint, Provider: os.Getenv("SLMCODE_TEST_DECISION_PROVIDER"), Model: os.Getenv("SLMCODE_TEST_DECISION_MODEL"), APIKey: os.Getenv("SLMCODE_TEST_DECISION_API_KEY"), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	score, err := client.Probability(ctx, "Task: fix JSON parser errors. Candidate: JSON parser error handling and regression tests.", "Is the candidate useful for the task?")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("valid decision score: %.4f (not an accuracy assertion)", score)
}
