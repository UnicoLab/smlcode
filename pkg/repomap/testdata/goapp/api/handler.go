package api

import (
	"errors"

	"github.com/example/goapp/core"
)

// Handler wires HTTP to the core engine.
type Handler struct {
	Engine *core.Engine
}

// NewHandler constructs a Handler around NewEngine.
func NewHandler(name string) *Handler {
	return &Handler{Engine: core.NewEngine(name)}
}

// Serve runs one step through the Engine.
func (h *Handler) Serve(step string) error {
	if h.Engine == nil {
		return errors.New("no engine")
	}
	return h.Engine.Run(step)
}
