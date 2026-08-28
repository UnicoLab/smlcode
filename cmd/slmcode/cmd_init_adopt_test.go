package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// `slmcode init` used to end a first run with "no model server answered" and a
// pointer at `slmcode doctor` — two more commands for somebody who has just
// scaffolded a workspace, when a server is often running on a port that is
// simply not the default.

func adoptWorkspace(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Root = root
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func adoptServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	body := `{"object":"list","data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `"}`
	}
	body += `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInitAdoptsAServerItFinds(t *testing.T) {
	// adoptDiscoveredServer reads two globals that other tests in this package
	// set; a stale one here would look like a bug in adoption.
	prev := flagEndpoint
	flagEndpoint = ""
	t.Cleanup(func() { flagEndpoint = prev })
	t.Setenv("SLMCODE_ENDPOINT", "")

	cfg := adoptWorkspace(t)
	srv := adoptServer(t, "nomic-embed-text", "Qwen2.5-Coder-14B-Instruct")
	cfg.Endpoint = srv.URL + "/v1"

	if !adoptDiscoveredServer(context.Background(), cfg) {
		t.Fatalf("a live server must be adopted (endpoint=%q flagEndpoint=%q env=%q)",
			cfg.Endpoint, flagEndpoint, os.Getenv("SLMCODE_ENDPOINT"))
	}
	if cfg.Model != "Qwen2.5-Coder-14B-Instruct" {
		t.Errorf("model = %q, want the coder rather than the embedding model", cfg.Model)
	}
	// And it reaches disk, or the next command sees the old configuration.
	reloaded, err := config.Load(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Endpoint != srv.URL+"/v1" {
		t.Errorf("saved endpoint = %q", reloaded.Endpoint)
	}
}

// `slmcode init --endpoint X` is a statement about where the server is.
// Quietly configuring Y instead would be the tool arguing with the user.
func TestInitStandsDownWhenTheUserPinnedAnEndpoint(t *testing.T) {
	cfg := adoptWorkspace(t)
	srv := adoptServer(t, "Qwen2.5-Coder-14B-Instruct")
	cfg.Endpoint = srv.URL + "/v1"

	prev := flagEndpoint
	flagEndpoint = "http://127.0.0.1:9999/v1"
	t.Cleanup(func() { flagEndpoint = prev })

	if adoptDiscoveredServer(context.Background(), cfg) {
		t.Error("a pinned endpoint must not be replaced by a discovered one")
	}
}

func TestInitStandsDownWhenTheEnvironmentPinnedAnEndpoint(t *testing.T) {
	cfg := adoptWorkspace(t)
	srv := adoptServer(t, "Qwen2.5-Coder-14B-Instruct")
	cfg.Endpoint = srv.URL + "/v1"
	t.Setenv("SLMCODE_ENDPOINT", "http://127.0.0.1:9999/v1")

	if adoptDiscoveredServer(context.Background(), cfg) {
		t.Error("an endpoint pinned in the environment must not be replaced")
	}
}

// A server whose models cannot write code is not something to adopt, and
// saying so beats writing a config whose first run fails.
//
// Asserts that THIS server was not adopted rather than that nothing was: a
// developer running LM Studio while the suite runs has a real second candidate,
// and adopting it would be correct. A test that fails on a machine with a model
// server running is a test nobody trusts.
func TestInitDoesNotAdoptAServerWithNothingUsable(t *testing.T) {
	prev := flagEndpoint
	flagEndpoint = ""
	t.Cleanup(func() { flagEndpoint = prev })
	t.Setenv("SLMCODE_ENDPOINT", "")

	cfg := adoptWorkspace(t)
	srv := adoptServer(t, "nomic-embed-text", "whisper-large-v3")
	cfg.Endpoint = srv.URL + "/v1"

	adoptDiscoveredServer(context.Background(), cfg)
	if cfg.Endpoint == srv.URL+"/v1" && cfg.Model != "" {
		for _, bad := range []string{"nomic-embed-text", "whisper-large-v3"} {
			if cfg.Model == bad {
				t.Errorf("adopted %q, which cannot write code", bad)
			}
		}
	}
}
