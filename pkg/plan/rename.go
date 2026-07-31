package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RenameKind classifies rename work for acceptance + tooling.
type RenameKind string

const (
	RenameNone   RenameKind = ""
	RenameSymbol RenameKind = "symbol"
	RenameFile   RenameKind = "file"
)

// RenameSpec describes a detected rename intent.
type RenameSpec struct {
	Kind      RenameKind
	OldPath   string
	NewPath   string
	OldSymbol string
	NewSymbol string
}

var (
	reRenameSymbol = regexp.MustCompile(`(?i)\brename\s+([A-Za-z_][\w]*)\s+to\s+([A-Za-z_][\w]*)`)
	reRenameFile   = regexp.MustCompile(`(?i)\brename\s+([^\s]+?\.\w+)\s+to\s+([^\s]+?\.\w+)`)
	reMvPath       = regexp.MustCompile(`(?i)\b(?:mv|move)\s+([^\s]+?\.\w+)\s+to\s+([^\s]+?\.\w+)`)
)

// DetectRenameIntent inspects query + task text for rename/move patterns.
func DetectRenameIntent(texts ...string) RenameSpec {
	blob := strings.Join(texts, "\n")
	if strings.TrimSpace(blob) == "" {
		return RenameSpec{}
	}
	if m := reRenameFile.FindStringSubmatch(blob); len(m) == 3 {
		return RenameSpec{
			Kind: RenameFile, OldPath: cleanPath(m[1]), NewPath: cleanPath(m[2]),
		}
	}
	if m := reMvPath.FindStringSubmatch(blob); len(m) == 3 {
		return RenameSpec{
			Kind: RenameFile, OldPath: cleanPath(m[1]), NewPath: cleanPath(m[2]),
		}
	}
	if m := reRenameSymbol.FindStringSubmatch(blob); len(m) == 3 {
		spec := RenameSpec{Kind: RenameSymbol, OldSymbol: m[1], NewSymbol: m[2]}
		// Best-effort path from surrounding text.
		for _, p := range ExtractFilePaths(blob) {
			spec.OldPath = p
			spec.NewPath = p
			break
		}
		return spec
	}
	lower := strings.ToLower(blob)
	if strings.Contains(lower, "rename") {
		// Soft signal — treat as symbol rename if two Ident-like tokens near "rename".
		return RenameSpec{Kind: RenameSymbol}
	}
	return RenameSpec{}
}

// RenameSatisfied reports whether disk state matches a rename acceptance.
// Symbol: old identifier gone (or only in comments) and new present in focus files.
// File: old path absent, new path present.
func RenameSatisfied(root string, spec RenameSpec, focus []string) bool {
	if root == "" || spec.Kind == RenameNone {
		return false
	}
	switch spec.Kind {
	case RenameFile:
		if spec.OldPath == "" || spec.NewPath == "" {
			return false
		}
		oldGone := !FileExists(root, spec.OldPath)
		newPresent := FileExists(root, spec.NewPath)
		return oldGone && newPresent
	case RenameSymbol:
		if spec.OldSymbol == "" || spec.NewSymbol == "" {
			return false
		}
		files := focus
		if len(files) == 0 && spec.OldPath != "" {
			files = []string{spec.OldPath}
		}
		if len(files) == 0 {
			return false
		}
		newOK, oldGone := false, true
		for _, f := range files {
			if !FileExists(root, f) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				continue
			}
			body := string(data)
			if containsIdent(body, spec.NewSymbol) {
				newOK = true
			}
			if containsIdent(body, spec.OldSymbol) {
				oldGone = false
			}
		}
		return newOK && oldGone
	}
	return false
}

// EnrichTaskFilesForRename ensures Files includes old+new paths when known.
func EnrichTaskFilesForRename(t *Task, query string) {
	if t == nil {
		return
	}
	spec := DetectRenameIntent(query, t.Title, t.Description, t.Acceptance, strings.Join(t.Files, " "))
	if spec.Kind == RenameNone {
		return
	}
	seen := map[string]bool{}
	var files []string
	add := func(p string) {
		p = cleanPath(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		files = append(files, p)
	}
	for _, f := range t.Files {
		add(f)
	}
	add(spec.OldPath)
	add(spec.NewPath)
	if len(files) > 0 {
		t.Files = files
	}
	// Encode hint for workers / acceptance.
	if spec.Kind == RenameSymbol && spec.OldSymbol != "" && spec.NewSymbol != "" {
		hint := "RENAME_SYMBOL " + spec.OldSymbol + " -> " + spec.NewSymbol
		if !strings.Contains(t.Acceptance, "RENAME_SYMBOL") {
			if t.Acceptance != "" {
				t.Acceptance += "; "
			}
			t.Acceptance += hint
		}
	}
	if spec.Kind == RenameFile && spec.OldPath != "" && spec.NewPath != "" {
		hint := "RENAME_FILE " + spec.OldPath + " -> " + spec.NewPath
		if !strings.Contains(t.Acceptance, "RENAME_FILE") {
			if t.Acceptance != "" {
				t.Acceptance += "; "
			}
			t.Acceptance += hint
		}
	}
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "`\"'")
	p = filepath.ToSlash(p)
	return p
}

func containsIdent(body, ident string) bool {
	if ident == "" {
		return false
	}
	// Word-boundary-ish check to avoid matching substrings.
	re := regexp.MustCompile(`(?m)(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(ident) + `([^A-Za-z0-9_]|$)`)
	return re.MatchString(body)
}
