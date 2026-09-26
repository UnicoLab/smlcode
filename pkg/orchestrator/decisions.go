package orchestrator

import (
	"context"

	"github.com/UnicoLab/slmcode/pkg/laya"
)

// decisionGuidance is deliberately advisory: model scores never alter role IDs,
// file permissions, budgets, review verdicts or deterministic acceptance gates.
func (o *Orchestrator) decisionGuidance(ctx context.Context, role, input string) string {
	if o.cfg == nil || o.cfg.LayaEndpoint == "" || !o.cfg.LayaGuidance {
		return ""
	}
	client, err := laya.New(laya.Options{Provider: o.cfg.LayaProvider, Endpoint: o.cfg.LayaEndpoint,
		Model: o.cfg.LayaModel, APIKey: o.cfg.LayaAPIKey, Timeout: o.cfg.LayaTimeout})
	if err == nil {
		if o.workspace != nil {
			input = o.workspace.RedactSecrets(input)
		}
		var hint string
		hint, err = client.Guidance(ctx, role, input)
		if err == nil {
			return hint
		}
	}
	o.emit("decision", "guidance unavailable; keeping normal agent input: "+err.Error(), "")
	return ""
}
