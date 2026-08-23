package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	reg, err := blocks.Load(s.cfg().Root)
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
	writeJSON(w, reg.View(s.cfg().ActivePack, s.cfg().ActivePipeline))
}

func (s *Server) handleGetBlock(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.PathValue("kind")))
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	reg, err := blocks.Load(s.cfg().Root)
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

func (s *Server) handleCreateBlock(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	kind := blocks.NormalizeKind(r.PathValue("kind"))
	if kind == "" {
		http.Error(w, "unknown kind (pipeline|agent|quality|pack)", 400)
		return
	}
	block, err := decodeBlockJSON(kind, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id := blockMetaID(block)
	if id == "" {
		http.Error(w, "block id is required", 400)
		return
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if blockExists(reg, kind, id) {
		http.Error(w, fmt.Sprintf("block %q already exists (edit it instead)", id), http.StatusConflict)
		return
	}
	path, err := blocks.Save(s.cfg().Root, block)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.writeBlockSaved(w, block, path)
}

func (s *Server) handleUpdateBlock(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	kind := blocks.NormalizeKind(r.PathValue("kind"))
	if kind == "" {
		http.Error(w, "unknown kind (pipeline|agent|quality|pack)", 400)
		return
	}
	pathID := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	block, err := decodeBlockJSON(kind, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id := blockMetaID(block)
	if id == "" {
		http.Error(w, "block id is required", 400)
		return
	}
	if id != pathID {
		http.Error(w, fmt.Sprintf("body id %q does not match path id %q", id, pathID), 400)
		return
	}
	path, err := blocks.Save(s.cfg().Root, block)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Mirrors ApplyPack materialization: write the agent spec so the runtime
	// picks the override up immediately.
	if kind == blocks.KindAgent {
		if _, err := agents.WriteCustom(s.cfg().AgentsDir(), block.(*blocks.AgentBlock).Spec); err != nil {
			http.Error(w, "block saved but agent materialization failed: "+err.Error(), 500)
			return
		}
	}
	s.writeBlockSaved(w, block, path)
}

func (s *Server) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	kind := blocks.NormalizeKind(r.PathValue("kind"))
	if kind == "" {
		http.Error(w, "unknown kind (pipeline|agent|quality|pack)", 400)
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	found, err := blocks.Delete(s.cfg().Root, kind, id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Drop the materialized agent too (yaml and yml variants).
	if kind == blocks.KindAgent {
		_ = agents.DeleteCustom(s.cfg().AgentsDir(), id)
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "deleted but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "found": found})
}

// writeBlockSaved responds to successful create/update with the saved block,
// its path, and the reloaded catalog (mirrors handleApplyPack).
func (s *Server) writeBlockSaved(w http.ResponseWriter, block any, path string) {
	catalog, err := s.reloadBlocksCatalog()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "saved but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"block":   block,
		"path":    path,
		"config":  s.cfg().Public(),
		"catalog": catalog,
	})
}

// reloadBlocksCatalog rebuilds the registry and returns the public catalog
// view used by the Studio sidebar.
func (s *Server) reloadBlocksCatalog() (any, error) {
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		return nil, err
	}
	return reg.View(s.cfg().ActivePack, s.cfg().ActivePipeline), nil
}

// decodeBlockJSON decodes a JSON request body into a typed block of the given
// kind, normalizes + validates it, and returns the typed pointer. A body kind
// that mismatches the path kind is rejected.
func decodeBlockJSON(kind string, r io.Reader) (any, error) {
	var block any
	switch kind {
	case blocks.KindPipeline:
		var b blocks.PipelineBlock
		if err := json.NewDecoder(r).Decode(&b); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		block = &b
	case blocks.KindAgent:
		var b blocks.AgentBlock
		if err := json.NewDecoder(r).Decode(&b); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		block = &b
	case blocks.KindQuality:
		var b blocks.QualityBlock
		if err := json.NewDecoder(r).Decode(&b); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		block = &b
	case blocks.KindPack:
		var b blocks.PackBlock
		if err := json.NewDecoder(r).Decode(&b); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		block = &b
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	meta := blockMeta(block)
	if meta.Kind != "" && blocks.NormalizeKind(meta.Kind) != kind {
		return nil, fmt.Errorf("body kind %q does not match path kind %q", meta.Kind, kind)
	}
	meta.Kind = kind
	if err := blockValidate(block); err != nil {
		return nil, err
	}
	return block, nil
}

func blockMeta(block any) *blocks.Meta {
	switch b := block.(type) {
	case *blocks.PipelineBlock:
		return &b.Meta
	case *blocks.AgentBlock:
		return &b.Meta
	case *blocks.QualityBlock:
		return &b.Meta
	case *blocks.PackBlock:
		return &b.Meta
	default:
		return nil
	}
}

func blockMetaID(block any) string {
	if m := blockMeta(block); m != nil {
		return strings.ToLower(strings.TrimSpace(m.ID))
	}
	return ""
}

func blockValidate(block any) error {
	switch b := block.(type) {
	case *blocks.PipelineBlock:
		return b.Validate()
	case *blocks.AgentBlock:
		return b.Validate()
	case *blocks.QualityBlock:
		return b.Validate()
	case *blocks.PackBlock:
		return b.Validate()
	default:
		return fmt.Errorf("unsupported block type %T", block)
	}
}

func blockExists(reg *blocks.Registry, kind, id string) bool {
	switch kind {
	case blocks.KindPipeline:
		_, ok := reg.GetPipeline(id)
		return ok
	case blocks.KindAgent:
		_, ok := reg.GetAgent(id)
		return ok
	case blocks.KindQuality:
		_, ok := reg.GetQuality(id)
		return ok
	case blocks.KindPack:
		_, ok := reg.GetPack(id)
		return ok
	default:
		return false
	}
}

func (s *Server) handleApplyPack(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	var body struct {
		MaterializeAgents bool `json:"materialize_agents"`
		ForceAgents       bool `json:"force_agents"`
	}
	body.MaterializeAgents = true
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	res, err := blocks.ApplyPack(s.cfg(), reg, id, blocks.ApplyOptions{
		MaterializeAgents: body.MaterializeAgents,
		ForceAgents:       body.ForceAgents,
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.cfg().Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.orch() != nil && res.PipelinePath != "" {
		if cfg, err := pipeline.Load(s.slmDir()); err == nil {
			_ = s.orch().SetPipeline(cfg)
		}
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "applied but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"result":  res,
		"config":  s.cfg().Public(),
		"catalog": reg.View(s.cfg().ActivePack, s.cfg().ActivePipeline),
	})
}

func (s *Server) handleApplyPipelineBlock(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	res, err := blocks.ApplyPipelinePreset(s.cfg(), reg, id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.cfg().Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if s.orch() != nil {
		if cfg, err := pipeline.Load(s.slmDir()); err == nil {
			_ = s.orch().SetPipeline(cfg)
		}
	}
	writeJSON(w, map[string]any{
		"ok":     true,
		"result": res,
		"config": s.cfg().Public(),
	})
}
