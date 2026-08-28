package autoconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseBothListingFormats(t *testing.T) {
	openAI := []byte(`{"object":"list","data":[{"id":"a"},{"id":"b"},{"id":""}]}`)
	if got := ParseModelList(openAI); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("OpenAI listing = %v", got)
	}
	// Ollama answers a different shape, and it is a server developers run.
	ollama := []byte(`{"models":[{"name":"llama3.3:70b","model":"llama3.3:70b"},{"model":"qwen:7b"}]}`)
	if got := ParseModelList(ollama); !reflect.DeepEqual(got, []string{"llama3.3:70b", "qwen:7b"}) {
		t.Errorf("Ollama listing = %v", got)
	}
	for _, bad := range [][]byte{nil, []byte("not json"), []byte(`{"data":[]}`), []byte(`{}`)} {
		if got := ParseModelList(bad); len(got) != 0 {
			t.Errorf("ParseModelList(%q) = %v, want nothing", bad, got)
		}
	}
}

// Ollama does not speak the OpenAI listing route, and a base URL that already
// ends in /v1 must not get a second one.
func TestTheListingURLMatchesTheServer(t *testing.T) {
	for _, tc := range []struct{ provider, endpoint, want string }{
		{"omlx", "http://127.0.0.1:8000/v1", "http://127.0.0.1:8000/v1/models"},
		{"omlx", "http://127.0.0.1:8000/v1/", "http://127.0.0.1:8000/v1/models"},
		{"lmstudio", "http://127.0.0.1:1234", "http://127.0.0.1:1234/v1/models"},
		{"ollama", "http://127.0.0.1:11434", "http://127.0.0.1:11434/api/tags"},
		{"ollama", "http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/api/tags"},
	} {
		got := modelsURL(Candidate{Provider: tc.provider, Endpoint: tc.endpoint})
		if got != tc.want {
			t.Errorf("modelsURL(%s, %s) = %q, want %q", tc.provider, tc.endpoint, got, tc.want)
		}
	}
}

func TestTheProberReadsARealServer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"Qwen2.5-Coder-7B-Instruct"}]}`))
	}))
	defer srv.Close()

	probe := HTTPProber(0)
	got, err := probe(context.Background(), Candidate{Provider: "omlx", Endpoint: srv.URL + "/v1"}, "sk-test")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"Qwen2.5-Coder-7B-Instruct"}) {
		t.Errorf("models = %v", got)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// A rejected key is a different problem from a dead port, and telling somebody
// to start a server they are already running is how they stop trusting the tool.
func TestARejectedKeySaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := HTTPProber(0)(context.Background(), Candidate{Provider: "omlx", Endpoint: srv.URL + "/v1"}, "")
	if err == nil || !strings.Contains(err.Error(), "API key was rejected") {
		t.Errorf("err = %v, want an auth explanation", err)
	}
}

// "dial tcp 127.0.0.1:1234: connect: connection refused" is accurate and tells
// a non-Go user nothing.
func TestATransportFailureIsExplainedInPlainWords(t *testing.T) {
	_, err := HTTPProber(0)(context.Background(),
		Candidate{Provider: "omlx", Endpoint: "http://127.0.0.1:1/v1"}, "")
	if err == nil {
		t.Fatal("a dead port must fail")
	}
	if strings.Contains(err.Error(), "dial tcp") {
		t.Errorf("err = %q, want a plain-words reason", err)
	}
}
