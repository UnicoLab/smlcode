package loop

import "testing"

// Measured on a live 30B: one task's reviews ran 40, 40, 40, 40, 20, 40, 0
// across its attempts — never improving, and finally destroying what was there.
// Each of those rounds cost 300-560s of a 45-minute run while other tasks had
// not been attempted once.
func TestAScoreThatDroppedStopsTheCorrections(t *testing.T) {
	for name, scores := range map[string][]int{
		"the measured drop":      {40, 20},
		"a drop after a plateau": {40, 40, 40, 20},
		"destroyed outright":     {40, 0},
		"a small drop":           {81, 80},
	} {
		if !correctionRegressed(scores) {
			t.Errorf("%s: %v was not seen as a regression", name, scores)
		}
	}
}

// The narrow case on purpose. A score that merely fails to improve may still be
// one round from passing, and ending there throws away tasks that were about to
// land — so only an actual DROP counts.
func TestAFlatOrImprovingScoreKeepsGoing(t *testing.T) {
	for name, scores := range map[string][]int{
		"improving":          {20, 40},
		"a plateau":          {40, 40},
		"a long plateau":     {40, 40, 40},
		"recovering":         {40, 20, 60},
		"one attempt only":   {40},
		"nothing judged yet": {},
	} {
		if correctionRegressed(scores) {
			t.Errorf("%s: %v ended the task early", name, scores)
		}
	}
}
