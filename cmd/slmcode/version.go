package main

// Build-time metadata (overridden via -ldflags).
//
//	go build -ldflags "-X main.Version=0.5.0 -X main.SourceRoot=/path -X main.GitCommit=abc -X main.BuildTime=…"
var (
	Version    = "0.6.0"
	SourceRoot = "" // absolute path to the slmcode checkout used to build this binary
	GitCommit  = "unknown"
	BuildTime  = "unknown"
)
