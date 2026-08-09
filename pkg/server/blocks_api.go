package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	reg, err := blocks.Load(s.h.Config.Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" {
		writeJSON(w, map[string]any{
			"blocks": reg.Catalog(kind),
			"kind":   kind,
		})
		return
	}
	writeJSON(w, reg.View(s.h.Config.ActivePack, s.h.Config.ActivePipeline))
}

func (s *Server) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.PathValue("kind")))
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	reg, err := blocks.Load(s.h.Config.Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	switch kind {
	case blocks.KindPipeline, "pipelines":
		b, ok := reg.GetPipeline(id)
		if !ok {
			http.Error(w, "pipeline not found", 404)
			return
		}
		writeJSON(w, b)
	case blocks.KindAgent, "agents":
		b, ok := reg.GetAgent(id)
		if !ok {
			http.Error(w, "agent block not found", 404)
			return
		}
		writeJSON(w, b)
	case blocks.KindQuality:
		b, ok := reg.GetQuality(id)
		if !ok {
			http.Error(w, "quality block not found", 404)
			return
		}
		writeJSON(w, b)
	case blocks.KindPack, "packs":
		b, ok := reg.GetPack(id)
		if !ok {
			http.Error(w, "pack not found", 404)
			return
		}
		writeJSON(w, b)
	default:
		http.Error(w, "unknown kind (pipeline|agent|quality|pack)", 400)
	}
}

func (s *Server) handleApplyPack(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	var body struct {
		MaterializeAgents bool `json:"materialize_agents"`
		ForceAgents       bool `json:"force_agents"`
	}
	body.MaterializeAgents = true
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	reg, err := blocks.Load(s.h.Config.Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	res, err := blocks.ApplyPack(s.h.Config, reg, id, blocks.ApplyOptions{
		MaterializeAgents: body.MaterializeAgents,
		ForceAgents:       body.ForceAgents,
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.h.Config.Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.h.Orchestrator != nil && res.PipelinePath != "" {
		if cfg, err := pipeline.Load(s.h.Config.SlmDir()); err == nil {
			_ = s.h.Orchestrator.SetPipeline(cfg)
		}
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "applied but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":     true,
		"result": res,
		"config": s.h.Config.Public(),
		"catalog": reg.View(s.h.Config.ActivePack, s.h.Config.ActivePipeline),
	})
}

func (s *Server) handleApplyPipelineBlock(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	reg, err := blocks.Load(s.h.Config.Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	res, err := blocks.ApplyPipelinePreset(s.h.Config, reg, id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.h.Config.Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.h.Orchestrator != nil {
		if cfg, err := pipeline.Load(s.h.Config.SlmDir()); err == nil {
			_ = s.h.Orchestrator.SetPipeline(cfg)
		}
	}
	writeJSON(w, map[string]any{
		"ok":     true,
		"result": res,
		"config": s.h.Config.Public(),
	})
}
