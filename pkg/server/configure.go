package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/autoconfig"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// ── Auto-configuration from the Studio ───────────────────────────────────
//
// The same job as `slmcode configure`, reachable from the browser. It has to
// exist on both surfaces for the same reason the CLI command does: a user whose
// endpoint is not answering is looking at a Studio that cannot run anything,
// and telling them to go and find a terminal is the point at which they stop.
//
// Split in two on purpose. GET looks and reports; POST writes. A single
// endpoint that did both would mean the only way to SEE what auto-configuration
// would do is to let it happen.

// handleConfigureScan probes for a model server and reports what it found,
// changing nothing.
func (s *Server) handleConfigureScan(w http.ResponseWriter, r *http.Request) {
	res, cfg := s.discover(r)
	writeJSON(w, configureView(res, cfg, false))
}

// handleConfigureApply probes and writes the result.
func (s *Server) handleConfigureApply(w http.ResponseWriter, r *http.Request) {
	// A run reads the model and endpoint it was started with. Rewriting them
	// underneath it would leave half the run on one model and half on another,
	// which is the same reason PUT /api/config refuses.
	if s.rejectMutationWhileRunning(w) {
		return
	}
	res, probeCfg := s.discover(r)
	if !res.Found {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(configureView(res, probeCfg, false))
		return
	}

	choice := res.Choice
	// An explicit model must be one the server actually serves: writing a
	// config whose first run fails is the situation this exists to prevent.
	if want := strings.TrimSpace(requestedModel(r)); want != "" {
		ok := false
		for _, f := range res.Findings {
			if f.Live() && f.Endpoint == choice.Endpoint {
				for _, name := range f.Models {
					if strings.EqualFold(name, want) {
						choice.Model, choice.Why, ok = name, "chosen in the Studio", true
					}
				}
			}
		}
		if !ok {
			http.Error(w, choice.Endpoint+" does not serve "+want, http.StatusUnprocessableEntity)
			return
		}
	}

	var saveErr error
	s.withConfigWrite(func(c *config.Config) {
		c.Provider = choice.Provider
		c.Endpoint = choice.Endpoint
		c.Model = choice.Model
		// A detected provider is not a stack choice; a stale highlight would
		// show a stack the config no longer matches.
		c.ActiveStack = ""
		c.Normalize()
		saveErr = c.Save()
	})
	if saveErr != nil {
		http.Error(w, saveErr.Error(), http.StatusInternalServerError)
		return
	}
	// The running orchestrator holds the old endpoint. Rebuild it, exactly as
	// PUT /api/config does, or the Studio would report a configuration the
	// harness is not using.
	orch, err := orchestrator.New(s.cfg())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.setOrch(orch)
	writeJSON(w, configureView(res, s.cfg(), true))
}

// discover runs one probe pass for a request.
func (s *Server) discover(r *http.Request) (autoconfig.Result, *config.Config) {
	cfg := s.cfg()
	probeCfg := *cfg
	if ep := strings.TrimSpace(r.URL.Query().Get("endpoint")); ep != "" {
		probeCfg.Endpoint = ep
	}
	timeout := autoconfig.DefaultProbeTimeout
	ctx, cancel := context.WithTimeout(r.Context(), timeout+7*time.Second)
	defer cancel()
	res := autoconfig.Discover(ctx, &probeCfg, os.Getenv, autoconfig.HTTPProber(timeout))
	if ep := strings.TrimSpace(r.URL.Query().Get("endpoint")); ep != "" {
		res = narrowTo(res, ep)
	}
	return res, cfg
}

// narrowTo keeps only the endpoint the caller named, so an explicit choice
// cannot fall through to whatever else happens to be running.
func narrowTo(res autoconfig.Result, endpoint string) autoconfig.Result {
	want := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	var kept []autoconfig.Finding
	for _, f := range res.Findings {
		if strings.TrimRight(f.Endpoint, "/") == want {
			kept = append(kept, f)
		}
	}
	out := autoconfig.Result{Findings: kept}
	out.Choice, out.Found = autoconfig.Choose(kept)
	return out
}

// requestedModel reads an optional model override from the body.
func requestedModel(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	var body struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.Model
}

// configureView is the shape both endpoints return.
func configureView(res autoconfig.Result, cfg *config.Config, applied bool) map[string]interface{} {
	tried := make([]map[string]interface{}, 0, len(res.Findings))
	for _, f := range res.Findings {
		tried = append(tried, map[string]interface{}{
			"provider": f.Provider, "endpoint": f.Endpoint, "reason": f.Reason,
			"models": f.Models, "live": f.Live(), "error": f.Err,
			"latency_ms": f.Latency.Milliseconds(),
		})
	}
	out := map[string]interface{}{
		"ok":      res.Found,
		"tried":   tried,
		"applied": applied,
	}
	if res.Found {
		out["choice"] = res.Choice
	} else {
		out["reason"] = res.NothingFound()
	}
	if cfg != nil {
		out["current"] = map[string]interface{}{
			"provider": cfg.Provider, "endpoint": cfg.Endpoint, "model": cfg.Model,
		}
	}
	return out
}
