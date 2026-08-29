package blocks

import (
	"os"
	"path/filepath"
	"testing"
)

// write creates a file and every directory above it.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The fixtures below mirror what the real CLIs produce. Both were scaffolded
// for real while writing this (shadcn 4.19.0, untitledui 0.1.64) and the marker
// sets are taken from the result, not from the docs — the docs say Untitled UI
// writes a components.json and `untitledui init --vite` does not.
func TestAssemblerPacksDetectRealScaffolds(t *testing.T) {
	reg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("shadcn", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "components.json", `{"style":"base-nova","aliases":{"ui":"@/components/ui"}}`)
		write(t, root, "package.json",
			`{"dependencies":{"react":"^19.0.0","class-variance-authority":"^0.7.1","tailwind-merge":"^3.0.0"}}`)
		// ENOUGH .tsx files to max out the extension tiebreak (ExtScore 2 ×
		// MaxExtHits 3 = 6). A one-file fixture hid a real defect: `react` also
		// matches this project and collects those 6 points, the assembler packs
		// list no extensions and cannot, so at the original priority react won
		// by two and every real shadcn scaffold was detected as plain react.
		for _, f := range []string{
			"src/App.tsx", "src/main.tsx",
			"src/components/ui/button.tsx", "src/components/ui/card.tsx",
			"src/components/ui/dialog.tsx",
		} {
			write(t, root, f, "export default function X() { return null }\n")
		}
		if q := reg.DetectQuality(root); q == nil || q.ID != "shadcn" {
			t.Fatalf("detected %v, want shadcn", qualityID(q))
		}
	})

	t.Run("untitled ui", func(t *testing.T) {
		root := t.TempDir()
		// No components.json — verified absent from a real `untitledui init`.
		write(t, root, "package.json",
			`{"dependencies":{"react":"^19.2.0","@untitledui/icons":"^0.0.22","react-aria-components":"^1.20.0","tailwind-merge":"^3.6.0"}}`)
		for _, f := range []string{
			"src/App.tsx", "src/main.tsx",
			"src/components/base/buttons/button.tsx",
			"src/components/application/modals/modal.tsx",
			"src/components/application/table/table.tsx",
		} {
			write(t, root, f, "export default function X() { return null }\n")
		}
		if q := reg.DetectQuality(root); q == nil || q.ID != "untitledui" {
			t.Fatalf("detected %v, want untitledui", qualityID(q))
		}
	})

	// The regression that matters most: a plain React app must NOT be claimed
	// by either assembler pack. Detection is additive, so a marker as broad as
	// package.json or a .tsx extension would make these packs outrank `react`
	// on every React project in the world.
	t.Run("plain react is untouched", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"dependencies":{"react":"^19.0.0","react-dom":"^19.0.0"}}`)
		write(t, root, "src/App.tsx", "export default function App() { return null }\n")
		q := reg.DetectQuality(root)
		if q == nil {
			t.Fatal("nothing detected for a plain React app")
		}
		if q.ID == "shadcn" || q.ID == "untitledui" {
			t.Fatalf("plain React app was claimed by %q — an assembler pack must need its own marker", q.ID)
		}
	})

	// A project carrying BOTH dependencies resolves deterministically rather
	// than by map order.
	t.Run("both libraries resolve deterministically", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "components.json", `{}`)
		write(t, root, "package.json",
			`{"dependencies":{"class-variance-authority":"^0.7.1","@untitledui/icons":"^0.0.22"}}`)
		first := reg.DetectQuality(root)
		for i := 0; i < 20; i++ {
			if got := reg.DetectQuality(root); qualityID(got) != qualityID(first) {
				t.Fatalf("detection is unstable: %v then %v", qualityID(first), qualityID(got))
			}
		}
	})
}

func qualityID(q *QualityBlock) string {
	if q == nil {
		return "<nil>"
	}
	return q.ID
}
