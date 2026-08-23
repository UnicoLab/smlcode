package repomap

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Kind classifies an extracted symbol.
const (
	KindPackage   = "package"
	KindFunc      = "func"
	KindMethod    = "method"
	KindType      = "type"
	KindClass     = "class"
	KindConst     = "const"
	KindVar       = "var"
	KindInterface = "interface"
)

// Symbol is one top-level declaration extracted from a source file.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"sig"`
	Line      int    `json:"line"`
	Exported  bool   `json:"exported"`
	Receiver  string `json:"recv,omitempty"`
}

// File is the extracted view of a single source file.
type File struct {
	Path     string   `json:"path"` // repo-relative, slash separated
	Lang     string   `json:"lang"`
	Package  string   `json:"pkg,omitempty"`
	Imports  []string `json:"imports,omitempty"`
	Symbols  []Symbol `json:"symbols,omitempty"`
	Refs     []string `json:"refs,omitempty"` // identifiers referenced but not defined here
	Size     int64    `json:"size"`
	ModTime  int64    `json:"mtime"`
	Rank     float64  `json:"-"`
	Language string   `json:"-"` // alias kept for readability in callers
}

// LangForPath maps a file extension to an extractor language id.
func LangForPath(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	default:
		return ""
	}
}

var (
	// Go
	goPackageRe = regexp.MustCompile(`^package\s+([A-Za-z_][A-Za-z0-9_]*)`)
	goFuncRe    = regexp.MustCompile(`^func\s+(?:\(([^)]*)\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\(`)
	goTypeRe    = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\s+(\w+)?`)
	goConstRe   = regexp.MustCompile(`^(const|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s`)
	goImportRe  = regexp.MustCompile(`^\s*(?:[A-Za-z_.][A-Za-z0-9_]*\s+)?"([^"]+)"`)

	// Python
	pyDefRe    = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyClassRe  = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[:(]`)
	pyImportRe = regexp.MustCompile(`^\s*(?:from\s+([A-Za-z_][A-Za-z0-9_.]*)\s+import|import\s+([A-Za-z_][A-Za-z0-9_.]*))`)

	// JS / TS
	jsFuncRe   = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>]*>)?\s*\(`)
	jsClassRe  = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	jsConstFn  = regexp.MustCompile(`^\s*(export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
	tsTypeRe   = regexp.MustCompile(`^\s*(export\s+)?(?:declare\s+)?(interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	jsImportRe = regexp.MustCompile(`(?:^\s*import\b[^'"]*['"]([^'"]+)['"]|require\(\s*['"]([^'"]+)['"]\s*\))`)

	// Rust
	rustFnRe   = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]*"\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustTypeRe = regexp.MustCompile(`^\s*(pub(?:\([^)]*\))?\s+)?(struct|enum|trait|type|impl)\s+(?:<[^>]*>\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	rustUseRe  = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z_][A-Za-z0-9_:]*)`)
	rustModRe  = regexp.MustCompile(`^\s*(?:pub\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)`)

	// Java
	javaPkgRe   = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	javaImpRe   = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_][A-Za-z0-9_.*]*)\s*;`)
	javaTypeRe  = regexp.MustCompile(`^\s*(?:public\s+|protected\s+|private\s+|abstract\s+|final\s+|static\s+)*(class|interface|enum|record)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	javaMethRe  = regexp.MustCompile(`^\s+(?:public|protected|private)\s+(?:static\s+|final\s+|synchronized\s+|abstract\s+|native\s+)*[A-Za-z_<>\[\],.\s?]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	identifierR = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
)

// ExtractSource parses one source blob. rel is used only for the language
// decision and the recorded path; it never touches the filesystem.
func ExtractSource(rel, src string) File {
	rel = filepath.ToSlash(rel)
	lang := LangForPath(rel)
	f := File{Path: rel, Lang: lang, Language: lang}
	if lang == "" {
		return f
	}
	lines := strings.Split(src, "\n")
	switch lang {
	case "go":
		extractGo(&f, lines)
	case "python":
		extractPython(&f, lines)
	case "javascript", "typescript":
		extractJS(&f, lines)
	case "rust":
		extractRust(&f, lines)
	case "java":
		extractJava(&f, lines)
	}
	f.Refs = collectRefs(lines, f)
	return f
}

func trimSig(line string) string {
	line = strings.TrimRight(strings.TrimSpace(line), " \t{")
	line = strings.TrimSuffix(line, "{")
	return strings.TrimSpace(line)
}

func isExportedGo(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

func extractGo(f *File, lines []string) {
	inImport := false
	for i, raw := range lines {
		line := raw
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if m := goPackageRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			continue
		}
		if strings.HasPrefix(trimmed, "import (") {
			inImport = true
			continue
		}
		if inImport {
			if trimmed == ")" {
				inImport = false
				continue
			}
			if m := goImportRe.FindStringSubmatch(line); m != nil {
				f.Imports = append(f.Imports, m[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			if m := goImportRe.FindStringSubmatch(strings.TrimPrefix(trimmed, "import ")); m != nil {
				f.Imports = append(f.Imports, m[1])
			}
			continue
		}
		if m := goFuncRe.FindStringSubmatch(line); m != nil {
			recv := strings.TrimSpace(m[1])
			kind := KindFunc
			if recv != "" {
				kind = KindMethod
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[2]), Receiver: recvType(recv),
			})
			continue
		}
		if m := goTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindType
			if strings.Contains(line, " interface") {
				kind = KindInterface
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[1], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[1]),
			})
			continue
		}
		if m := goConstRe.FindStringSubmatch(line); m != nil {
			kind := KindConst
			if m[1] == "var" {
				kind = KindVar
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: isExportedGo(m[2]),
			})
		}
	}
}

func recvType(recv string) string {
	recv = strings.TrimSpace(recv)
	if recv == "" {
		return ""
	}
	fields := strings.Fields(recv)
	t := fields[len(fields)-1]
	t = strings.TrimLeft(t, "*")
	if i := strings.IndexByte(t, '['); i > 0 {
		t = t[:i]
	}
	return t
}

func extractPython(f *File, lines []string) {
	for i, line := range lines {
		if m := pyImportRe.FindStringSubmatch(line); m != nil {
			mod := m[1]
			if mod == "" {
				mod = m[2]
			}
			if mod != "" {
				f.Imports = append(f.Imports, mod)
			}
			continue
		}
		if m := pyClassRe.FindStringSubmatch(line); m != nil {
			if len(m[1]) > 0 {
				continue // nested class: skip
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: KindClass, Signature: trimSig(strings.TrimSuffix(strings.TrimSpace(line), ":")),
				Line: i + 1, Exported: !strings.HasPrefix(m[2], "_"),
			})
			continue
		}
		if m := pyDefRe.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			kind := KindFunc
			if indent > 0 {
				kind = KindMethod
			}
			if indent > 4 {
				continue // nested helper
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(strings.TrimSuffix(strings.TrimSpace(line), ":")),
				Line: i + 1, Exported: !strings.HasPrefix(m[2], "_"),
			})
		}
	}
}

func extractJS(f *File, lines []string) {
	for i, line := range lines {
		if m := jsImportRe.FindStringSubmatch(line); m != nil {
			mod := m[1]
			if mod == "" {
				mod = m[2]
			}
			if mod != "" {
				f.Imports = append(f.Imports, mod)
			}
		}
		switch {
		case jsClassRe.MatchString(line):
			m := jsClassRe.FindStringSubmatch(line)
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: KindClass, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case jsFuncRe.MatchString(line):
			m := jsFuncRe.FindStringSubmatch(line)
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case tsTypeRe.MatchString(line):
			m := tsTypeRe.FindStringSubmatch(line)
			kind := KindType
			if m[2] == "interface" {
				kind = KindInterface
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[3], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		case jsConstFn.MatchString(line):
			m := jsConstFn.FindStringSubmatch(line)
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: m[1] != "",
			})
		}
	}
}

func extractRust(f *File, lines []string) {
	for i, line := range lines {
		if m := rustUseRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			continue
		}
		if m := rustModRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, "crate::"+m[1])
		}
		if m := rustFnRe.FindStringSubmatch(line); m != nil {
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: KindFunc, Signature: trimSig(line),
				Line: i + 1, Exported: strings.TrimSpace(m[1]) != "",
			})
			continue
		}
		if m := rustTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindType
			switch m[2] {
			case "trait":
				kind = KindInterface
			case "struct", "enum":
				kind = KindType
			case "impl":
				kind = KindClass
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[3], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.TrimSpace(m[1]) != "" || m[2] == "impl",
			})
		}
	}
}

func extractJava(f *File, lines []string) {
	for i, line := range lines {
		if m := javaPkgRe.FindStringSubmatch(line); m != nil {
			f.Package = m[1]
			continue
		}
		if m := javaImpRe.FindStringSubmatch(line); m != nil {
			f.Imports = append(f.Imports, m[1])
			continue
		}
		if m := javaTypeRe.FindStringSubmatch(line); m != nil {
			kind := KindClass
			if m[1] == "interface" {
				kind = KindInterface
			}
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[2], Kind: kind, Signature: trimSig(line),
				Line: i + 1, Exported: strings.Contains(line, "public"),
			})
			continue
		}
		if m := javaMethRe.FindStringSubmatch(line); m != nil {
			f.Symbols = append(f.Symbols, Symbol{
				Name: m[1], Kind: KindMethod, Signature: trimSig(line),
				Line: i + 1, Exported: strings.Contains(line, "public"),
			})
		}
	}
}

// collectRefs gathers identifiers used in the file that it does not itself
// define. These become the graph edges: file A references symbol S, file B
// defines S ⇒ edge A→B.
func collectRefs(lines []string, f File) []string {
	defined := map[string]bool{}
	for _, s := range f.Symbols {
		defined[s.Name] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") ||
			strings.HasPrefix(t, "*") {
			continue
		}
		for _, id := range identifierR.FindAllString(line, -1) {
			if defined[id] || seen[id] || isCommonWord(id) {
				continue
			}
			seen[id] = true
			out = append(out, id)
			if len(out) >= 400 {
				return out
			}
		}
	}
	return out
}

var commonWords = map[string]bool{
	"func": true, "package": true, "import": true, "return": true, "string": true,
	"error": true, "nil": true, "true": true, "false": true, "int": true, "bool": true,
	"var": true, "const": true, "type": true, "struct": true, "interface": true,
	"map": true, "range": true, "for": true, "if": true, "else": true, "switch": true,
	"case": true, "default": true, "break": true, "continue": true, "defer": true,
	"len": true, "make": true, "append": true, "self": true, "this": true, "def": true,
	"class": true, "from": true, "and": true, "not": true, "None": true, "let": true,
	"fmt": true, "pub": true, "use": true, "mod": true, "impl": true, "fn": true,
	"public": true, "private": true, "static": true, "void": true, "new": true,
	"const_": true, "float64": true, "float32": true, "int64": true, "byte": true,
	"the": true, "with": true, "that": true, "this_": true, "value": true,
}

func isCommonWord(id string) bool { return commonWords[id] }
