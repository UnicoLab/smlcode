package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// configureWorkspace initializes a project and points --root at it.
func configureWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Root = root
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	prev := flagRoot
	flagRoot = root
	t.Cleanup(func() { flagRoot = prev })
	return root
}

// modelServer stands in for LM Studio / oMLX: it serves a realistic mixture,
// which is the point — a model server serves whatever it was given, and the
// list is rarely all chat models.
func modelServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	if len(ids) == 0 {
		ids = []string{"nomic-embed-text", "Qwen2.5-Coder-14B-Instruct", "Qwen2.5-1.5B-Instruct"}
	}
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

func runConfigure(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := configureCmd()
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&out)
	err := c.Execute()
	return out.String(), err
}

// The headline: a user with a server running gets a config that works, without
// having to know what to put in it.
func TestConfigureWritesAWorkingConfig(t *testing.T) {
	root := configureWorkspace(t)
	srv := modelServer(t)

	if _, err := runConfigure(t, "--yes", "--endpoint", srv.URL+"/v1"); err != nil {
		t.Fatalf("configure: %v", err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != srv.URL+"/v1" {
		t.Errorf("endpoint = %q, want the server that answered", cfg.Endpoint)
	}
	// Not the embedding model, and not the 1.5B either.
	if cfg.Model != "Qwen2.5-Coder-14B-Instruct" {
		t.Errorf("model = %q, want the coder-tuned model", cfg.Model)
	}
}

// --dry-run is what makes this safe to run on a configured machine.
func TestDryRunWritesNothing(t *testing.T) {
	root := configureWorkspace(t)
	srv := modelServer(t)
	before, err := os.ReadFile(filepath.Join(root, ".slmcode", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runConfigure(t, "--yes", "--dry-run", "--endpoint", srv.URL+"/v1"); err != nil {
		t.Fatalf("configure --dry-run: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(root, ".slmcode", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("--dry-run rewrote the config")
	}
}

// --endpoint is an instruction, not a hint. Without the narrowing, a probe of
// an address that is down falls through to whatever else is running and
// `configure --endpoint X` quietly settles on Y.
func TestAnExplicitEndpointIsNotSecondGuessed(t *testing.T) {
	configureWorkspace(t)
	live := modelServer(t)
	_ = live // running, but NOT the one the user named

	out, err := runConfigure(t, "--yes", "--endpoint", "http://127.0.0.1:9/v1")
	if err == nil {
		t.Fatalf("a dead endpoint the user named must fail rather than fall through:\n%s", out)
	}
	if strings.Contains(out, live.URL) {
		t.Errorf("configure settled on an endpoint the user did not name:\n%s", out)
	}
}

// Pinning a model the server does not serve writes a config whose first run
// fails — the exact situation this command exists to prevent.
func TestPinningAModelTheServerDoesNotServeIsRefused(t *testing.T) {
	root := configureWorkspace(t)
	srv := modelServer(t)

	out, err := runConfigure(t, "--yes", "--endpoint", srv.URL+"/v1", "--model", "gpt-9-ultra")
	if err == nil {
		t.Fatal("pinning an unserved model must be refused")
	}
	if !strings.Contains(err.Error()+out, "Qwen2.5-Coder-14B-Instruct") {
		t.Errorf("the refusal does not say what IS served: %v\n%s", err, out)
	}
	cfg, _ := config.Load(root)
	if cfg.Model == "gpt-9-ultra" {
		t.Error("the unserved model was written anyway")
	}
}

func TestPinningAServedModelOverridesTheRanking(t *testing.T) {
	root := configureWorkspace(t)
	srv := modelServer(t)

	if _, err := runConfigure(t, "--yes", "--endpoint", srv.URL+"/v1",
		"--model", "Qwen2.5-1.5B-Instruct"); err != nil {
		t.Fatalf("configure --model: %v", err)
	}
	cfg, _ := config.Load(root)
	if cfg.Model != "Qwen2.5-1.5B-Instruct" {
		t.Errorf("model = %q, want the pinned one", cfg.Model)
	}
}

// A server with nothing usable on it is a different problem from a dead port,
// and saying "start a server" to somebody whose server is running is how they
// stop trusting the tool.
func TestAServerWithNoCodingModelSaysWhy(t *testing.T) {
	configureWorkspace(t)
	srv := modelServer(t, "nomic-embed-text", "whisper-large-v3")

	out, err := runConfigure(t, "--yes", "--endpoint", srv.URL+"/v1")
	if err == nil {
		t.Fatal("a server with no usable model cannot yield a config")
	}
	blob := err.Error() + out
	if !strings.Contains(blob, "coder-tuned") {
		t.Errorf("the failure does not explain the real problem: %v\n%s", err, out)
	}
}

func TestConfigureJSONIsMachineReadable(t *testing.T) {
	configureWorkspace(t)
	srv := modelServer(t)

	// --json goes to stdout by design (emitJSON), so this asserts the command
	// succeeds and writes; the payload shape is covered in pkg/autoconfig.
	if _, err := runConfigure(t, "--yes", "--json", "--dry-run", "--endpoint", srv.URL+"/v1"); err != nil {
		t.Fatalf("configure --json: %v", err)
	}
}

func TestConfigureRejectsABadTimeout(t *testing.T) {
	configureWorkspace(t)
	if _, err := runConfigure(t, "--yes", "--timeout", "banana"); err == nil {
		t.Error("an unparsable timeout must be refused rather than ignored")
	}
}
