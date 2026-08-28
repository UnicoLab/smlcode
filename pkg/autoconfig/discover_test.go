package autoconfig

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// fakeServer answers for the endpoints it knows and refuses everything else,
// recording what each probe was sent.
type fakeServer struct {
	mu     sync.Mutex
	serves map[string][]string
	keys   map[string]string
	tried  []string
}

func (f *fakeServer) probe(_ context.Context, c Candidate, apiKey string) ([]string, error) {
	f.mu.Lock()
	f.tried = append(f.tried, c.Endpoint)
	if f.keys == nil {
		f.keys = map[string]string{}
	}
	f.keys[c.Endpoint] = apiKey
	models, ok := f.serves[c.Endpoint]
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("connection refused")
	}
	return models, nil
}

func cfgWith(provider, endpoint, key string) *config.Config {
	c := config.Default("/tmp/x")
	c.Provider = provider
	c.Endpoint = endpoint
	c.APIKey = key
	return c
}

// A user who set an endpoint is telling you where to look. Re-detecting around
// a working configuration is how a tool silently moves somebody's setup out
// from under them.
func TestAWorkingConfigurationIsConfirmedNotReplaced(t *testing.T) {
	srv := &fakeServer{serves: map[string][]string{
		"http://127.0.0.1:9999/v1": {"Qwen2.5-Coder-7B-Instruct"},
		"http://127.0.0.1:1234/v1": {"Qwen2.5-Coder-32B-Instruct"},
		"http://127.0.0.1:11434":   {"llama3.3:70b-instruct"},
	}}
	cfg := cfgWith("omlx", "http://127.0.0.1:9999/v1", "")

	got := Discover(context.Background(), cfg, nil, srv.probe)
	if !got.Found {
		t.Fatal("a live configured endpoint must be found")
	}
	// A bigger model exists elsewhere. It is not the point: the user said where.
	if got.Choice.Endpoint != "http://127.0.0.1:9999/v1" {
		t.Errorf("Endpoint = %q, want the configured one kept", got.Choice.Endpoint)
	}
	if got.Choice.Model != "Qwen2.5-Coder-7B-Instruct" {
		t.Errorf("Model = %q", got.Choice.Model)
	}
}

func TestADeadConfiguredEndpointFallsThroughToWhatIsRunning(t *testing.T) {
	srv := &fakeServer{serves: map[string][]string{
		"http://127.0.0.1:1234/v1": {"nomic-embed-text", "Qwen2.5-Coder-14B-Instruct"},
	}}
	cfg := cfgWith("omlx", "http://127.0.0.1:9999/v1", "")

	got := Discover(context.Background(), cfg, nil, srv.probe)
	if !got.Found {
		t.Fatalf("a running server must be found; findings:\n%s", got.Explain())
	}
	if got.Choice.Provider != "lmstudio" || got.Choice.Endpoint != "http://127.0.0.1:1234/v1" {
		t.Errorf("Choice = %+v, want the server that is actually running", got.Choice)
	}
	// And the embedding model on the same server is not what it picked.
	if got.Choice.Model != "Qwen2.5-Coder-14B-Instruct" {
		t.Errorf("Model = %q, want the coder rather than the embedding model", got.Choice.Model)
	}
}

// A key belongs to the service it was issued for. Attaching the user's OpenAI
// key to a probe of 127.0.0.1:1234 because LM Studio might be listening there
// hands a credential to whatever answers that port.
func TestACredentialNeverTravelsToAnUnconfiguredLocalPort(t *testing.T) {
	srv := &fakeServer{serves: map[string][]string{
		"http://127.0.0.1:1234/v1": {"Qwen2.5-Coder-7B-Instruct"},
	}}
	cfg := cfgWith("openai", "https://api.openai.com/v1", "sk-secret")

	Discover(context.Background(), cfg, nil, srv.probe)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for endpoint, sent := range srv.keys {
		local := strings.Contains(endpoint, "127.0.0.1")
		if local && sent != "" {
			t.Errorf("the API key was sent to the local endpoint %s", endpoint)
		}
		if endpoint == "https://api.openai.com/v1" && sent != "sk-secret" {
			t.Errorf("the configured endpoint was not sent its own key")
		}
	}
}

// Probing serially means a laptop with nothing running pays the timeout for
// every well-known address in turn. A first run that appears to hang is one
// people quit.
func TestEveryCandidateIsProbedConcurrently(t *testing.T) {
	cfg := cfgWith("omlx", "", "")
	want := len(Candidates(cfg, nil))
	if want < 2 {
		t.Fatalf("fixture offers %d candidates; this proves nothing", want)
	}

	arrived := make(chan struct{}, want)
	release := make(chan struct{})
	probe := func(_ context.Context, _ Candidate, _ string) ([]string, error) {
		arrived <- struct{}{}
		<-release // hold every prober here until all of them have arrived
		return nil, errors.New("refused")
	}

	done := make(chan struct{})
	go func() {
		Discover(context.Background(), cfg, nil, probe)
		close(done)
	}()

	// Serial probing can never get all of them in flight at once: the second
	// prober is not entered until the first returns, and none return until
	// release is closed.
	for i := 0; i < want; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d candidates were in flight — probing is serial", i, want)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Discover did not return after the probers were released")
	}
}

// A hosted endpoint with no key is a guaranteed 401. Offering it is noise.
func TestAHostedProviderIsOnlyProbedWhenItsKeyIsPresent(t *testing.T) {
	srv := &fakeServer{serves: map[string][]string{}}
	env := func(name string) string {
		if name == "GROQ_API_KEY" {
			return "gsk-test"
		}
		return ""
	}
	Discover(context.Background(), cfgWith("omlx", "", ""), env, srv.probe)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	tried := strings.Join(srv.tried, " ")
	if !strings.Contains(tried, "groq.com") {
		t.Errorf("the provider whose key is set was never tried: %v", srv.tried)
	}
	if strings.Contains(tried, "openai.com") || strings.Contains(tried, "openrouter") {
		t.Errorf("a keyless hosted provider was probed: %v", srv.tried)
	}
}

// The three ways a pass can fail are different problems with different fixes,
// and collapsing them into "no endpoint found" sends somebody to restart a
// server that is already running.
func TestAFailedPassSaysWhichProblemItIs(t *testing.T) {
	nothing := Result{Findings: []Finding{{Err: "connection refused"}}}
	if got := nothing.NothingFound(); !strings.Contains(got, "Start one") {
		t.Errorf("NothingFound = %q, want a start-a-server message", got)
	}

	empty := Result{Findings: []Finding{{Err: "answered, but serves no models"}}}
	if got := empty.NothingFound(); !strings.Contains(got, "Load a model") {
		t.Errorf("NothingFound = %q, want a load-a-model message", got)
	}

	wrongKind := Result{Findings: []Finding{{Models: []string{"nomic-embed-text"}}}}
	if got := wrongKind.NothingFound(); !strings.Contains(got, "coder-tuned") {
		t.Errorf("NothingFound = %q, want a wrong-kind-of-model message", got)
	}
}

func TestDiscoverIsSafeWithNothingWiredUp(t *testing.T) {
	if got := Discover(context.Background(), nil, nil, nil); got.Found {
		t.Error("no prober, no discovery")
	}
	if got := Discover(context.Background(), cfgWith("omlx", "", ""), nil, nil); got.Found {
		t.Error("no prober, no discovery")
	}
}

func TestExplainNamesEveryCandidateAndItsOutcome(t *testing.T) {
	r := Result{Findings: []Finding{
		{Candidate: Candidate{Provider: "omlx", Endpoint: "http://a/v1", Reason: "already configured"},
			Models: []string{"m"}},
		{Candidate: Candidate{Provider: "ollama", Endpoint: "http://b"}, Err: "connection refused"},
	}}
	got := r.Explain()
	for _, want := range []string{"http://a/v1", "already configured", "http://b", "connection refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain is missing %q:\n%s", want, got)
		}
	}
}
