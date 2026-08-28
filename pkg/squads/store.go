package squads

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// PlanFile is the on-disk name of the squad plan inside .slmcode/.
const PlanFile = "squads.json"

// Save writes the plan and its rendered contract into slmDir.
//
// Both, always, and atomically. The JSON is what a resumed run reloads; the
// Markdown is what the agents read. Writing one without the other produces the
// worst failure this package can have — squads executing against a contract
// that describes a different plan.
func Save(slmDir string, p Plan) error {
	if slmDir == "" {
		return fmt.Errorf("squads: no .slmcode directory")
	}
	if err := os.MkdirAll(slmDir, 0o750); err != nil {
		return fmt.Errorf("squads: %w", err)
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("squads: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(slmDir, PlanFile), append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("squads: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(slmDir, ContractFile), []byte(RenderContract(p)), 0o644); err != nil {
		return fmt.Errorf("squads: %w", err)
	}
	return nil
}

// Load reads a saved plan. The bool is false when none has been written.
func Load(slmDir string) (Plan, bool, error) {
	if slmDir == "" {
		return Plan{}, false, nil
	}
	body, err := os.ReadFile(filepath.Join(slmDir, PlanFile))
	if err != nil {
		if os.IsNotExist(err) {
			return Plan{}, false, nil
		}
		return Plan{}, false, fmt.Errorf("squads: %w", err)
	}
	var p Plan
	if err := json.Unmarshal(body, &p); err != nil {
		return Plan{}, false, fmt.Errorf("squads: %s is not readable: %w", PlanFile, err)
	}
	p.Normalize()
	return p, true, nil
}

// Clear removes a saved plan and its contract.
func Clear(slmDir string) {
	if slmDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(slmDir, PlanFile))
	_ = os.Remove(filepath.Join(slmDir, ContractFile))
}
