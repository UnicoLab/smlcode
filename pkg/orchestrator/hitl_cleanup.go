package orchestrator

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/hitl"
)

func clearPendingHITL(slmDir string) {
	if strings.TrimSpace(slmDir) == "" {
		return
	}
	hitl.ClearAll(slmDir)
}
