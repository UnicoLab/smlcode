// Package core holds the engine.
package core

import (
	"fmt"
	"strings"
)

// MaxWorkers bounds the pool.
const MaxWorkers = 8

var defaultName = "engine"

// Engine drives the pipeline.
type Engine struct {
	Name string
}

// Runner is implemented by anything the engine can drive.
type Runner interface {
	Run(step string) error
}

// NewEngine builds an Engine.
func NewEngine(name string) *Engine {
	if name == "" {
		name = defaultName
	}
	return &Engine{Name: strings.TrimSpace(name)}
}

// Run executes one step.
func (e *Engine) Run(step string) error {
	if step == "" {
		return fmt.Errorf("empty step")
	}
	return nil
}

func (e *Engine) helper() string { return e.Name }
